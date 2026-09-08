---
comet_change: pr-auto-merge
role: technical-design
canonical_spec: openspec
---

# pr-auto-merge — технический дизайн

## Зачем и что меняется в решении §2.8

`docs/DESIGN.md` §2.8 сейчас говорит однозначно: «Сливает человек. Авто-слияния
нет и на этом этапе не будет». Решение сознательно подтверждалось ещё раз
9 дней назад, при работе над `role-comet-native-workflow` (Task 23): пользователь
явно отложил «ревьюера, который ведёт несколько циклов ревью вместе с внешним CI
перед тем, как человек сольёт» — теми же словами: «я бы не хотел сейчас заниматься
GitHub Actions и подробной реализацией ревьювера... важно, чтобы наша роль в
будущем имела технические возможности это сделать», и зафиксировано отдельно:
«human still merges — confirmed explicitly with the user; DESIGN.md §2.8's
"человек — единственная точка" principle stands unchanged».

Что изменилось не техническая новизна риска, а стоимость статус-кво: пока
задача в `Approved` ждёт клика человека, весь конвейер по факту стоит —
конвейер требует наблюдения человека на неопределённый срок, что противоречит
изначальной идее «человек участвует в фиксированных точках, а не в каждом шаге»
(README.md). Это изменение размыкает точку `Approved → Done` **там, где владелец
явно согласился её разомкнуть**, оставляя её как есть везде, где не согласился.

Решение §2.8 переписывается на: по умолчанию сливает человек; на явно
опомошенном проекте — сливает офис, детерминированным кодом, без новой роли.

## Объём

**В этом изменении:**
- `auto_merge.enabled` / `auto_merge.target_branch` — конфигурация на проект,
  обе оси одним изменением.
- Механический гейт v1 — только `Approved` (уже истинно к моменту PR-прохода)
  плюс актуальность базы (см. «Гейт слияния» ниже). Ожидания сверх этого нет —
  вето-окна не будет: как только гейт пройден, офис жмёт merge на первом же
  тике.
- `Forge.Merge()` — новый метод, реализация для GitHub.
- Расширение триггера возврата к implementer'у: не только текстовый конфликт,
  но и «база продвинулась вперёд с момента ветвления задачи» — действует
  **везде**, не только на auto-merge-проектах (осознанный выбор, см. ниже).

**Вне объёма, осознанно:**
- Статус-чеки CI / внешний review-бот как гейт слияния — отложено тем же
  решением, что и раньше (Task 23); эта тема его не пересматривает.
- Автоматическое продвижение интеграционной ветки в `main` — вручную,
  человеком, вне офиса.
- Вето-окно («смержим через N часов, если никто не возразил») — отклонено
  явным решением в этом брейншторме.
- Автосоздание отсутствующей `target_branch` — офис не изобретает
  долгоживущие ветки, отказывает громко.
- Выбор `merge_method` (squash/rebase) конфигом — хардкод `merge` (merge
  commit) в v1, см. ниже.

## Конфигурация

```yaml
# ${OFFICE_HOME}/projects.local.yaml
OFF:
  repo_url: ...
  tracker: jira
  forge: github
  auto_merge:
    enabled: true
    target_branch: office-integration   # пусто — значит default_branch
```

Место — `projects.local.yaml`, рядом с `forge`, не `projects.yaml`. Рассуждение:
`forge` уже живёт на машинной половине именно потому, что «открывать ли
настоящий PR против настоящего remote» — решение конкретного инстанса, а не
портируемое свойство офиса; доверие авто-мержу — того же рода решение,
завязанное на то же самое `repo_url`.

### `internal/tracker/config.go`

```go
// AutoMerge — доверие конкретного инстанса конкретному проекту: мержить ли
// самим и куда. Решение машины, не офиса — тот же класс, что Forge.
type AutoMerge struct {
    Enabled      bool   `yaml:"enabled"`
    TargetBranch string `yaml:"target_branch"`
}

type Project struct {
    ...
    Forge     string    `yaml:"forge"`
    AutoMerge AutoMerge `yaml:"auto_merge"`
    ...
}

type machineProject struct {
    ...
    Forge     string    `yaml:"forge"`
    AutoMerge AutoMerge `yaml:"auto_merge"`
    Rules     `yaml:",inline"`
}
```

`machineKeys` пополняется `"auto_merge"` — тем же приёмом, что уже ловит
`forge`, забредший в файл офиса, и объясняет, куда он переехал.

Сборка (`LoadProjects`, где сегодня `Forge: local.Forge`) добавляет
`AutoMerge: local.AutoMerge`. Валидация — рядом с существующими проверками
`RepoURL`/`DefaultBranch`/`Tracker`:

```go
if project.AutoMerge.Enabled && project.Forge == "" {
    errs = append(errs, fmt.Errorf(
        "%s: auto_merge.enabled=true, но forge не задан — мержить через API "+
            "некуда (%s)", key, machinePath))
}
```

### `PRBranch()` — ветка форка и цели PR-прохода

```go
// PRBranch — ветка, от которой форкаются задачи, и цель PR-прохода:
// target_branch авто-мержа, а без него — default_branch. Не default_branch
// впрямую: иначе задача B (depends_on A) форкалась бы от main и не видела бы
// уже влитую в интеграционную ветку работу A — гейт зависимостей
// (claim(), internal/pipeline/deps.go) молча переставал бы что-либо значить.
func (p Project) PRBranch() string {
    if p.AutoMerge.TargetBranch != "" {
        return p.AutoMerge.TargetBranch
    }
    return p.DefaultBranch
}
```

Подставляется вместо голого `project.DefaultBranch` в трёх точках:

| Файл | Было | Смысл |
|---|---|---|
| `internal/workspace/workspace.go:402` | `worktree add -b ws.Branch ws.Dir origin/<DefaultBranch>` | от какой ветки форкается задача |
| `internal/pipeline/pipeline.go:483` | `BaseBranch: "origin/" + c.project.DefaultBranch` | база, которую видит агент (reviewer диффует и мержит relative к ней) |
| `internal/pipeline/prpass.go` (`openPR`, `followPR`) | `MergeCheck(..., project.DefaultBranch)`, `OpenPR(..., project.DefaultBranch, ...)` | слияемость и цель pull request |

`internal/workspace/workspace.go:329` (`git init --bare -b project.DefaultBranch`
при первом заведении bare-клона) не трогается — там `DefaultBranch` в
буквальном git-смысле, к маршруту задач не относится.

### Требование к бутстрапу

Если `target_branch` задан, а такой ветки на remote ещё нет — офис её не
создаёт. Первое обращение просто падает штатной git-ошибкой («invalid
reference») тем же путём, каким сегодня падает опечатка в `default_branch`, —
специальной проверки при загрузке конфига не заводим (сеть на этом шаге может
быть недоступна, а последующий сбой и так информативен). Ветку заводит
человек, прежде чем включать `auto_merge.enabled` — это осознанный выбор:
офис не изобретает долгоживущие ветки, которыми потом распоряжается человек.

## Гейт слияния

Гейт v1 не требует ожидания: `Approved` уже истинно на входе в очередь
PR-прохода. Единственное, что реально нужно проверить перед мержем, — что
контекст, в котором работали implementer и reviewer, не устарел.

### `BaseAdvanced` — новая проверка рядом с `MergeCheck`

`MergeCheck` (`internal/workspace/merge.go`) отвечает только про текстовый
конфликт. Нужна вторая, более узкая проверка: продвинулась ли база с
момента, когда ветка задачи была срублена, — даже без единого конфликтного
маркера код, который видели implementer и reviewer, мог устареть по смыслу.

```go
// internal/workspace/merge.go

// BaseAdvanced отвечает, обогнала ли база ветку задачи — есть ли в base
// коммиты, которых ветка задачи ещё не содержит. Отдельно от MergeCheck:
// это не про текстовый конфликт, а про то, устарел ли контекст, в котором
// implementer писал, а reviewer смотрел diff, — база могла уйти вперёд и
// без единого маркера конфликта.
func (m *Manager) BaseAdvanced(repo, branch, base string) (bool, error) {
    cmd := exec.Command("git", "-C", repo, "merge-base", "--is-ancestor",
        "origin/"+base, "origin/"+branch)
    cmd.Env = gitEnv()
    switch err := cmd.Run(); {
    case err == nil:
        return false, nil // base уже целиком в предках ветки задачи
    case isExitCode(err, 1):
        return true, nil
    default:
        return false, fmt.Errorf("продвижение %s относительно %s не проверено: %w",
            base, branch, err)
    }
}
```

### Расширенный триггер возврата — везде, не только на auto-merge

И в `openPR`, и в `followPR` (`prpass.go`) `merge.Conflict || advanced`
уводит задачу в `prConflict`, а не только `merge.Conflict`, как сегодня.
**Действует для всех проектов**, не только с `auto_merge.enabled` —
осознанный выбор в пользу единообразия: одна ветка кода вместо двух режимов,
и human-merge-проекты от этого не теряют ничего — implementer и так лучше
подготовленного PR не хуже человека, а сама ре-проверка возникает не чаще,
чем база реально двигается. Явно принимается, что для активной очереди на
human-merge-проекте это учащает возвраты к implementer'у относительно
сегодняшнего поведения.

`prConflict` получает дополнительный параметр, чтобы не соврать в тексте —
`merge.Conflict` может быть `false`, пока `advanced` — `true`, и текст
«ветка не сливается» тогда был бы неточным (тот же принцип, что уже
использован для `prSkipped`: не называть событие тем, чем оно не является):

```go
func (o *Office) prConflict(task tracker.Task, project tracker.Project, url string, textConflict bool) error {
    ...
    reason := "Ветка %s не сливается с %s."
    if !textConflict {
        reason = "База %s продвинулась вперёд с тех пор, как ветка %s была создана — " +
            "конфликта нет, но контекст мог устареть."
    }
    ...
}
```

### `roles/implementer/role.md` — правки

Строки 121–133 («Если ветка не сливается») чинятся в двух местах:

1. `git merge origin/<ветка по умолчанию>` → ссылка на реально переданную
   базу из контекста (`BaseBranch`, теперь равную `PRBranch()`), а не
   хардкод «ветки по умолчанию» — сегодня это работает только потому, что
   `default_branch` и есть единственная база; с `target_branch` это стало бы
   активной ошибкой (implementer чинил бы не ту ветку).
2. Триггер расширяется с «ветка не сливается» на «база продвинулась вперёд» —
   процедура та же (`git merge`, разрешить если есть что разрешать, прогнать
   тесты, `done → reviewer`), но без предположения, что обязательно будут
   маркеры конфликта.

## Механика слияния

### `Forge.Merge()`

```go
// internal/forge/forge.go
type Forge interface {
    OpenPR(project, branch, base, title, body string) (string, error)
    PRState(url string) (State, error)
    // Merge сливает pull request. ErrRefused — окончательный отказ forge
    // (не мержится, права нет, PR не найден); прочая ошибка — сбой связи,
    // задачу за неё двигать нельзя.
    Merge(url string) error
}
```

### GitHub

```go
// internal/forge/github.go

// mergeMethod — merge commit, не squash и не rebase: сохраняет всю историю
// ветки задачи как есть, ничего не сжимает. Не вынесено в конфиг — YAGNI,
// пока не спросили.
const mergeMethod = "merge"

func (g *GitHub) Merge(url string) error {
    repo, number, err := parsePRURL(url)
    if err != nil {
        return err
    }
    payload, err := json.Marshal(map[string]string{"merge_method": mergeMethod})
    if err != nil {
        return err
    }
    path := fmt.Sprintf("/repos/%s/pulls/%d/merge", repo, number)
    return g.do(http.MethodPut, path, payload, nil)
}
```

`g.do` уже классифицирует 4xx как `ErrRefused`, а сбой связи/5xx — как
обычную ошибку (`github.go:149-154`) — переиспользуется без изменений,
новой классификации не нужно.

### `followPR` — где подключается merge

```go
func (o *Office) followPR(task tracker.Task, url string) error {
    ...
    switch state {
    case forge.Merged: return o.prMerged(task, url)
    case forge.Closed:  return o.prAnomaly(...)
    }

    merge, err := o.Workspaces.MergeCheck(repo, project.Branch(task.Key), project.PRBranch())
    if err != nil { o.logf(...); return nil }
    advanced, err := o.Workspaces.BaseAdvanced(repo, project.Branch(task.Key), project.PRBranch())
    if err != nil { o.logf(...); return nil }

    if merge.Conflict || advanced {
        return o.prConflict(task, project, url, merge.Conflict)
    }
    if !project.AutoMerge.Enabled {
        return nil // как сегодня: чисто, ждём человека
    }
    return o.attemptMerge(task, project, url)
}
```

`openPR` не трогается сверх подстановки `PRBranch()` и добавления той же
`advanced`-проверки рядом с существующей `merge.Conflict` веткой — мерж не
пытаемся на этом же тике: задача остаётся в `Approved`, следующий тик
`PRPass` подхватит её через уже существующий путь `advancePR → followPR`,
который увидит `EventPROpened` и посчитает гейт заново, уже свежим fetch'ом.
Это не оптимизация ради простоты, а согласованность с тем, что gate v1 и так
почти всегда истинен сразу — специальный код на «слить немедленно при
открытии» экономил бы один тик ценой второго пути с той же логикой.

### Отказ API при локально чистом состоянии

Если `MergeCheck`/`BaseAdvanced` не увидели проблемы, а `Merge()` всё равно
вернул `ErrRefused` (например, branch protection с требованием, о котором
офис не знает) — это не работа для implementer'а: локально мержить нечего,
он честно отчитается `done`, и цикл повторится вслепую. Заводится отдельный
ограниченный счётчик по образцу `max_push_failures`/`max_lease_expiries` —
новый маркер `tracker.EventMergeRefused = "merge-refused"` (рядом с
`EventPushFailed`/`EventLeaseExpired`, `internal/tracker/marker.go:44,53`),
считается по маркерам в переписке, не полем трекера:

```yaml
# workflow.yaml, limits:
max_merge_refusals: 3   # столько раз подряд forge может отказать в мерже
                         # при локально чистом состоянии, прежде чем позвать человека
```

Исчерпание — эскалация тем же путём, что `prAnomaly` сегодня.

## Терминальный статус

`pr.merged` в `workflow.yaml` не меняется — авто-мерж ведёт в тот же `Done`,
что и сегодняшний human-merge. Работа офиса над задачей закончена, когда код
слит куда договорились; продвижение интеграционной ветки в `main` — уже не
его забота. Меняется только текст записи `prMerged` — называет реальную
ветку (`office-integration`, а не подразумевает `main`), не более.

## `docs/DESIGN.md` §2.8 — правка текста

Раздел переписывается с «сливает человек, авто-слияния нет и не будет» на:
по умолчанию сливает человек; на проекте с явным `auto_merge.enabled` —
сливает офис детерминированным кодом (то же расширение `PRPass`, без роли),
гейт — `Approved` плюс актуальность базы, вето-окна нет. Остальные пункты
§2.8 (forge — интерфейс рядом с трекером, конфликт — работа, не провал,
рабочая папка живёт до слияния) не меняются по существу.

## Тестирование

- `internal/workspace`: юнит-тест `BaseAdvanced` — база не продвинулась
  (ancestor), продвинулась (не ancestor), несуществующая ветка/база
  (текущее поведение `MergeCheck` для сравнения).
- `internal/tracker/config_test.go`: `PRBranch()` — пустой `target_branch`
  падает на `DefaultBranch`, заданный — побеждает; `auto_merge.enabled` без
  `forge` — ошибка загрузки; ключ `auto_merge` в файле офиса — отвергается
  тем же путём, что и `forge` сегодня.
- `internal/forge/github_test.go`: `Merge()` — 200 успех, 405/409 →
  `ErrRefused`, сетевой сбой/5xx → обычная ошибка (по образцу существующих
  тестов `OpenPR`/`PRState` на fake-сервере).
- `internal/pipeline/pipeline_test.go` (или `prpass_test.go`): `followPR` —
  auto-merge включён, гейт чист → вызывает `Merge`, уводит в `pr.merged`;
  конфликт **или** `advanced` (независимо от `auto_merge.enabled`) →
  `prConflict` с верным текстом; auto-merge выключен, гейт чист → задача
  остаётся на месте (сегодняшнее поведение не сломано); отказ `Merge()`
  при чистом состоянии → счётчик `max_merge_refusals`, эскалация по
  исчерпании.
- Живая проверка: сценарий по образцу существующих live-прогонов PR-прохода
  (`docs/notes/stage-5-live-backlog.md`) — задача на реальном GitHub-репо
  с `auto_merge.enabled` и `target_branch`, доходит до `Done` без участия
  человека; отдельно — задача с искусственно устаревшей базой, чтобы увидеть
  расширенный триггер `advanced` вживую, не только в юнит-тесте.
- `go build ./... && go vet ./... && go test ./...` — как обычно.
