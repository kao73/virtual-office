---
comet_change: sandbox-network-and-permissions
role: technical-design
canonical_spec: openspec
archived-with: 2026-08-27-sandbox-network-and-permissions
status: final
---

# Слоистое разрешение network/tools для прогона роли — технический дизайн

Контекст, цели и высокоуровневые решения — в `proposal.md` и `design.md`
изменения (`docs/openspec/changes/sandbox-network-and-permissions/`) и
в спеке `specs/role-sandbox-permissions/spec.md`. Здесь — как это
реализуется: типы, точки интеграции, тестовая стратегия.

## Схема данных

`tracker/config.go` получает общий тип правил:

```go
// Rules — сетевой и инструментальный слой, который может назвать любой
// уровень (repo-wide умолчания, проект, машина). Роль (уровень 4) продолжает
// использовать свои существующие Network/Tools из runner.Role — с ней этот
// тип не смешивается, слияние с ролью происходит отдельным шагом (см. ниже).
type Rules struct {
    Network []string `yaml:"network"`
    Tools   Tools    `yaml:"tools"` // тип Tools уже существует (runner.Tools: Allow, Deny []string)
}
```

`officeProject` и `machineProject` получают встроенные `Rules` (embedding,
чтобы не плодить новый уровень вложенности в YAML):

```go
type officeProject struct {
    DefaultBranch string `yaml:"default_branch"`
    BranchPrefix  string `yaml:"branch_prefix"`
    Rules         `yaml:",inline"`
}

type machineProject struct {
    RepoURL      string `yaml:"repo_url"`
    WorktreeRoot string `yaml:"worktree_root"`
    Tracker      string `yaml:"tracker"`
    Forge        string `yaml:"forge"`
    Rules        `yaml:",inline"`
}
```

`Project` (итоговый, публичный тип) получает те же поля `Network`/`Tools`
— уже смёрженные, не office/machine-специфичные.

## Зарезервированный ключ `defaults`

```go
const reservedRulesKey = "defaults"

// extractDefaults вынимает ключ "defaults" из карты до основного цикла
// валидации проектов и проверяет, что под ним не спрятаны машинные/офисные
// поля, которым там не место (сигнал, что кто-то перепутал defaults
// с реальным проектом).
func extractDefaultsOffice(m map[string]officeProject) (Rules, error) {
    d, ok := m[reservedRulesKey]
    if !ok {
        return Rules{}, nil
    }
    delete(m, reservedRulesKey)
    if d.DefaultBranch != "" || d.BranchPrefix != "" {
        return Rules{}, fmt.Errorf(
            "%s: %q — зарезервированное имя для repo-wide умолчаний network/tools, "+
                "default_branch/branch_prefix ему не положены (это не проект)",
            ProjectsFile, reservedRulesKey)
    }
    return d.Rules, nil
}
// extractDefaultsMachine — зеркально, проверяет RepoURL/WorktreeRoot/Tracker/Forge пусты.
```

`LoadProjects` вызывает обе функции сразу после `decodeStrict`, до
существующего цикла проверки парности ключей office/machine — тот цикл
после этого видит только настоящие проекты и не меняется по существу.

Оба вызова диагностируются отдельно: `defaults` может отсутствовать в
`projects.yaml`, в `projects.local.yaml`, в обоих или быть в обоих —
это не ошибка ни в одном случае (repo-wide/машинный слой просто пуст).

## Слияние (union)

Одна функция на оба списка-строки, используется и для `Network`, и для
каждого из `Tools.Allow`/`Tools.Deny` по отдельности:

```go
func unionStrings(layers ...[]string) []string {
    var all []string
    for _, l := range layers {
        all = append(all, l...)
    }
    slices.Sort(all)
    return slices.Compact(all)
}
```

(Тот же приём, что уже применяет `adapters/claude/adapter.go:240-244`
в текущем `networkAllow` — не новый паттерн, а его обобщение на большее
число слоёв.)

Слои 1–3 (repo-wide `defaults`, проект, машинный `defaults`, машинный
проект) сливаются внутри `LoadProjects` в `Project.Network`/`Project.Tools`:

```go
project.Network = unionStrings(officeDefaults.Network, half.Network, machineDefaults.Network, local.Network)
project.Tools.Allow = unionStrings(officeDefaults.Tools.Allow, half.Tools.Allow, machineDefaults.Tools.Allow, local.Tools.Allow)
project.Tools.Deny  = unionStrings(officeDefaults.Tools.Deny,  half.Tools.Deny,  machineDefaults.Tools.Deny,  local.Tools.Deny)
```

Слой 4 (роль) сливается отдельной функцией, ближе к месту запуска роли
(не в `tracker/config.go` — роль там не видна):

```go
// runner/role.go (или соседний файл того же пакета)
func MergeProjectRules(role Role, project tracker.Project) Role {
    role.Network.Allow = unionStrings(project.Network, role.Network.Allow)
    role.Tools.Allow = unionStrings(project.Tools.Allow, role.Tools.Allow)
    role.Tools.Deny = unionStrings(project.Tools.Deny, role.Tools.Deny)
    return role
}
```

Возвращает новое значение `Role` (копия — `Role` уже передаётся по
значению везде в существующем коде), дальше по коду ничего не отличает
«роль после слияния» от «роль как есть» — `adapters/claude/adapter.go`
не меняется в части типов, только получает уже смёрженную роль.

## Точки интеграции

`adapter.Build(role runner.Role, ...)` не знает о `Project` и не должен —
слияние происходит ДО вызова `Build`, не внутри адаптера. `networkAllow(role)`
в адаптере не меняется по сигнатуре: `role.Network.Allow` к моменту вызова
уже несёт объединённый результат всех четырёх слоёв.

Два места, где сегодня грузится `Role`, ведут себя по-разному относительно
момента, когда известен проект:

**`pipeline/pipeline.go` (`Office.tickRole`, реальный конвейер).**
`LoadRole` (строка ~176) вызывается ДО того, как `claim()` резолвит
`task.ref.Project` (~190/325) — проект задачи известен только после
захвата задачи. `MergeProjectRules` вызывается ПОСЛЕ `claim()`, когда
`project` уже есть в `claimed`/аналогичной структуре — не сразу после
`LoadRole`. Итоговая, уже смёрженная `Role` передаётся дальше в
`pipeline/agent.go` → `runagent.Options`.

**`runner/cmd/run-agent/main.go` (ручной/debug CLI).**
Сейчас не знает о проектах вовсе (`-role`/`-workdir`/`-backend`/`-task`/
`-base`, без `-project`). Получает новый необязательный флаг `-project`:
если задан — грузит `tracker.LoadProjects` (те же `projects.yaml`/
`projects.local.yaml`, что и `pipeline.go`), резолвит `Projects.Get(*project)`
и применяет `MergeProjectRules`; если не задан — поведение как сегодня,
роль без слоёв 1–3 (явный debug-режим «роль в изоляции», не тихий пробел).

Мотивация: ручное воспроизведение бага через `run-agent` не должно
расходиться с тем, что видит реальный конвейер — иначе повторится ситуация
исходной находки (403 в живом прогоне не воспроизводился одинаково без
понимания, откуда на самом деле берётся сеть).

## Изменения в `role.yaml`

`tools.allow` всех трёх ролей — широкие правила (`Bash(*)` и подобные)
вместо перечисления подкоманд. `network.allow` (`pypi.org`,
`files.pythonhosted.org`) убирается — покрывается `defaults.network`
после эмпирической проверки PyPI (`tasks.md`, 2.4). Роль-специфичные
`tools.deny` (например, запрет `add`/`commit`/`restore` у `reviewer`)
остаются в `role.yaml`.

## Формулировки `defaults.tools.deny`

Эмпирически проверено 2026-08-27 (throwaway git-репозиторий + локальный
bare remote, `claude -p --permission-mode dontAsk`; полный протокол —
`.comet/handoff/brainstorm-summary.md` этого изменения):

- `Bash(git push*)` — не ловит `git -c core.pager=cat push` (известно
  с `stage-1-retro.md`).
- `Bash(*git push*)` — тоже не ловит: во флаговой форме подряд идущей
  подстроки «git push» физически нет. Паттерн синтаксически рабочий —
  ловит обычный `git push origin master`.
- `Bash(git *push*)` — звёздочка МЕЖДУ «git» и «push» — ловит флаговую
  форму. Подтверждено как deny поверх широкого `Bash(*)` allow — именно
  предлагаемая для ролей конфигурация.

Семь существующих строк переносятся в эту форму:
`Bash(git *push*)`, `Bash(git *remote*)`, `Bash(git *checkout*)`,
`Bash(git *switch*)`, `Bash(git *branch*)`, `Bash(git *worktree*)`,
`Bash(git *config*)` — плюс новые опасные команды за пределами git
(`rm -rf` и т.п., формулировки — по факту при реализации задачи 3.3,
тем же приёмом: звёздочка на месте, где может встрять флаг, не только
по краям).

Побочно замечено (не решение этого изменения, для сведения): составная
команда через `;` (`git push ...; echo ...`) была отклонена отдельным
механизмом харнесса Claude Code, не связанным с glob-паттернами
allow/deny.

## Тестовая стратегия

**Юнит-тесты `tracker/config.go`:**
- `defaults` отсутствует в `projects.yaml`, задан в `projects.local.yaml`
  (и наоборот) — не ошибка, слой с той стороны просто пуст
- `defaults` задан в обоих файлах — оба вклада учтены
- `defaults.office` содержит `default_branch` — явная ошибка с понятным
  текстом (аналогично `defaults.machine` с `repo_url`/`tracker`)
- проект без собственных `network`/`tools` наследует только `defaults`
- специфика одного проекта не видна другому (два проекта, разные `network`)
- проект без `defaults` вообще ведёт себя как сегодня (регресс)

**Юнит-тесты слияния роли:** `MergeProjectRules` — объединение без потерь
(deny роли не может исчезнуть при более широком project-level allow),
дедуп повторов между слоями.

**Интеграционная/живая проверка (`tasks.md`, группа 6):** прогон роли
на `sbx` для `EXP` (реальные unit-тесты, БД, при применимости
`docker compose`) и для проекта без собственной специфики (`VO`/`OFFICE`)
— на итоговых `defaults.tools.deny`-формулировках, не только в изоляции
throwaway-репозитория этого брейнштормінга.

## Риски / граничные случаи

- Слияние `Rules` через `yaml:",inline"` — YAML-декодер должен принимать
  вложенные поля `network`/`tools` на том же уровне, что и `repo_url`
  и т.п.; если `gopkg.in/yaml.v3` с `KnownFields(true)` конфликтует
  с `inline` для встроенных структур (учитывая, что раньше в проекте
  `inline` не использовался) — проверить на первом же тесте; запасной
  вариант — явные поля `Network`/`Tools` без embedding, без изменения
  итогового API.
- `pipeline.go`: перенос точки слияния с «сразу после `LoadRole`» на
  «после `claim()`» — риск случайно передать в `runagent`/`agent.go`
  немёрженную роль, если по пути есть другой путь передачи роли в обход
  правки; покрыть тестом на уровне `pipeline` (роль на выходе `tickRole`
  содержит смёрженные значения).
- `run-agent -project` — опечатка в имени проекта не должна тихо
  проигнорироваться: `Projects.Get` уже возвращает содержательную ошибку
  («проект не описан...») — используется как есть.
