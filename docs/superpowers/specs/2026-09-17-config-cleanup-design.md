---
comet_change: config-cleanup
role: technical-design
canonical_spec: openspec
---

# config-cleanup — технический дизайн

Источник требований — дельта-спеки изменения
`docs/openspec/changes/config-cleanup/specs/` (`config-boundary`,
`runner-multi-tracker`, MODIFIED `role-sandbox-permissions`). Здесь — как
это сделать; что и зачем — в `proposal.md` и `design.md` того же изменения.

## Зачем и что меняется в решении §2.5

§2.5 DESIGN.md проводил границу «репозиторий описывает офис, `${OFFICE_HOME}`
— инстанс» и честно оставлял долг: список проектов всё ещё поставлялся
в репозитории, и чужой клон первым же прогоном получал `-dirty`. Владелец
провёл границу жёстче: **репозиторий — это фреймворк, инстанс настраивается
только в `${OFFICE_HOME}`.** Из этого следуют три вещи, и все три — в этом
изменении:

1. `projects.yaml` исчезает. Проект описывается одной записью
   в `projects.local.yaml`.
2. То, что в `projects.yaml` не было проектами — `defaults.tools.deny`
   (агент не пушит, не переключает ветки, не переписывает историю, не делает
   `rm -rf`) и `defaults.network` (реестры пакетов), — это правила
   **ролей**, а не инстанса. Они переезжают в `roles/_base/base.yaml`,
   машиночитаемую половину `roles/_base/base.md` («действуют всегда, роль
   может добавить, но не отменить»).
3. Флаг `--tracker` исчезает: какой трекер у проекта, уже сказано в его
   записи, и раннер обслуживает все названные.

`--backend` не трогается — решение владельца.

## Объём

Внутри: `roles/_base/base.yaml` и его слияние в `LoadRole`; загрузчик
проектов по одному файлу; сторож от оставшегося `projects.yaml`;
`cmd/runner` — офис на трекер; условное открытие `tracker.yaml`; образец
`tracker.example.yaml`; `add_link_type` в `jira-setup.sh`; DESIGN §2.5;
те фрагменты README/ONBOARDING, что стали бы ложью; миграция этой машины.

Снаружи: вывод `default_branch` из `origin/HEAD`; умолчание `--backend`;
дедупликация `role.yaml`; `CONFIG.md`, `projects.local.example.yaml`,
`bootstrap/` — следующее изменение.

## Базовые правила ролей: `roles/_base/base.yaml`

Формат — тот же, что у блоков `network`/`tools` в `role.yaml`, потому что
это и есть «роль, от которой наследуют все»:

```yaml
# Правила, общие для всех ролей. Действуют всегда, поверх любой роли:
# роль может добавить, но не отменить (base.md). Слияние — объединение.
network:
  allow:
    - registry-1.docker.io
    # … Docker Hub, GitHub, ghcr, GitHub Releases, npm, PyPI, Go — с теми же
    # комментариями-свидетельствами, что стояли в projects.yaml
tools:
  deny:
    - "Bash(git *push*)"
    # … те же строки, что были в defaults.tools.deny
```

Что **не** переезжает: хосты, найденные для одного проекта (Playwright,
MCR, bun, `ports.ubuntu.com`, `deb.nodesource.com`) — они уходят в запись
EXP в машинном файле (см. «Миграция»). Комментарий в `base.yaml` говорит
об этом одной строкой и ссылается на `git log -- projects.yaml`, где
свидетельства остаются.

### `internal/runner/role.go`

```go
// BaseDir — каталог правил, общих для всех ролей; BaseRulesFile — их
// машиночитаемая половина рядом с base.md.
const (
	BaseDir       = "_base"
	BaseRulesFile = "base.yaml"
)

// baseRules — то, что наследует каждая роль. Форма совпадает с role.yaml,
// а не с projects.local.yaml: это роль, от которой наследуют все.
type baseRules struct {
	Network Network `yaml:"network"`
	Tools   Tools   `yaml:"tools"`
}
```

`LoadRole` после `validate`:

- `dir := filepath.Join(configRoot, RolesDir, BaseDir)`; каталога нет —
  слоя нет, роль возвращается как есть (временные роли в тестах);
- каталог есть, файла нет — `fmt.Errorf("%s: базовые правила ролей не
  найдены: каталог есть, файла нет", path)` — файл обязателен ровно там,
  где обязателен `base.md`;
- строгий разбор (`KnownFields(true)`), затем
  `r.Network.Allow = Union(base.Network.Allow, r.Network.Allow)` и то же
  для `Tools.Allow`/`Tools.Deny`.

`Union` — сегодняшний `tracker.unionStrings`, переехавший в `runner` как
экспортируемая функция: `tracker` уже импортирует `runner`, обратное
направление невозможно, а две копии одного `sort+compact` — лишнее.
`tracker.MergeProjectRules` и `LoadProjects` зовут `runner.Union`.

Следствие для `cmd/run-agent`: базовые правила приходят через `LoadRole`,
поэтому прогон без `--project` впервые получает запреты на `git push` и
прочее. С `--project` поверх них по-прежнему ложатся правила проекта.

### Тест поставки

`internal/runner/role_test.go`: (1) роль во временном `roles/` без `_base`
загружается без слоя; (2) с `_base/base.yaml` — `Tools.Deny` роли содержит
и базовые, и свои строки, повторы схлопнуты; (3) `_base/` есть, `base.yaml`
нет — отказ с путём. Плюс тест на **поставляемый** файл в
`internal/tracker/boundary_test.go` — там, где уже сторожат существование
`workflow.yaml`: `roles/_base/base.yaml` обязан существовать и содержать
`Bash(git *push*)`. Гарантия, что раннер, а не агент, владеет origin, не
должна исчезать молча.

## Загрузчик проектов: `internal/tracker/config.go`

Уходят: `ProjectsFile`, `officeProject`, `checkOfficeHalf`, `machineKeys`,
`extractDefaultsOffice`, `extractDefaultsMachine`, проверки парности
ключей, «офис без единого проекта».

Остаются и меняются:

```go
// ProjectsLocalFile — единственный файл проектов; живёт в ${OFFICE_HOME}.
const ProjectsLocalFile = "projects.local.yaml"

// DefaultBranchPrefix — префикс веток задач, если проект не назвал свой.
const DefaultBranchPrefix = "agent/"

// machineProject — запись проекта. Всё, что у проекта есть, — здесь.
type machineProject struct {
	RepoURL       string    `yaml:"repo_url"`
	DefaultBranch string    `yaml:"default_branch"`
	BranchPrefix  string    `yaml:"branch_prefix"`
	WorktreeRoot  string    `yaml:"worktree_root"`
	Tracker       string    `yaml:"tracker"`
	Forge         string    `yaml:"forge"`
	AutoMerge     AutoMerge `yaml:"auto_merge"`
	Rules         `yaml:",inline"`
}

func LoadProjects(machinePath string) (Projects, error)
```

Порядок внутри `LoadProjects`:

1. `os.Stat`: нет файла — сегодняшний отказ «не заведён…», текст
   дополнен `default_branch` в списке обязательных ключей и `auto_merge`
   среди необязательных.
2. `decodeLoose` в `map[string]map[string]any` ради `defaults`:
   `checkDefaults(raw["defaults"])` — множество ключей обязано быть
   подмножеством `{network, tools}`; лишние перечисляются в отказе
   `«defaults: ключ auto_merge не положен — это свойство проекта, а не
   умолчаний; здесь только network и tools»`. Один allow-list вместо двух
   deny-списков: новое поле в `machineProject` не сможет снова проскочить.
3. `decodeStrict` в `map[string]machineProject`; `defaults` вынимается
   в `Rules` (как сейчас `extractDefaultsMachine`, но без своей проверки —
   она уже сделана).
4. Ни одного проекта — отказ `«%s не называет ни одного проекта: офису
   нечего вести»` (текст сегодняшний, файл другой).
5. На каждую запись: `RepoURL`, `Tracker`, `DefaultBranch` обязательны
   (отказ называет ключ, проект, файл); `BranchPrefix == ""` →
   `DefaultBranchPrefix`; `WorktreeRoot` абсолютный; `Tracker` из
   `trackers`; `AutoMerge.Enabled` требует `Forge`; `Network`/`Tools` =
   `runner.Union(defaults, own)`.

Добавляется:

```go
// TrackersInUse — трекеры, названные проектами, по алфавиту. Порядок
// устойчив намеренно: по нему раннер обходит офисы.
func (p Projects) TrackersInUse() []string

// OfficeProjectsFile — имя файла, которого в репозитории больше нет.
// Остался от прежней раскладки — раннер о нём скажет, а не промолчит.
const OfficeProjectsFile = "projects.yaml"

// RefuseLeftoverOfficeFile — отказ, если под корнем конфигурации лежит
// projects.yaml. Проекты живут в projects.local.yaml, общие правила —
// в roles/_base/base.yaml; файл, который раньше носил и то и другое,
// не читается, и молча пройти мимо него значило бы запустить офис на
// половине конфигурации.
func RefuseLeftoverOfficeFile(configRoot string) error
```

Сторож зовётся в `cmd/runner/offices()` и в `cmd/run-agent` перед
`LoadProjects` — в обоих местах, где корень конфигурации известен.

Сообщения, называвшие `projects.yaml`: `Projects.Get`
(«не описан в projects.local.yaml»), `tracker.SkipUnknownProject`,
`cmd/runner/board.go` — правятся на один файл. `boundary_test.go` убирает
`ProjectsFile` из `named`.

## `cmd/runner`: офис на трекер

### `offices()` — конструктор

Сигнатура и место те же, что у `office()` (`cmd/runner/office.go`):
`offices(fs *flag.FlagSet, args []string, out io.Writer) (*offices, error)`.
Флаг `--tracker` не объявляется. Порядок:

1. `configRoot`, `runner.Home()`, `tracker.RefuseLeftoverOfficeFile(configRoot)`.
2. `workflow` ← `sources.office(configRoot, WorkflowFile)`.
3. `projects` ← `tracker.LoadProjects(sources.machine(home, ProjectsLocalFile))`.
4. Для каждого `name` из `projects.TrackersInUse()`:
   - `mock` — `mock.Default()`, учётки как сегодня;
   - `jira` — `jira.LoadConfig(sources.machine(home, jira.TrackerFile))`,
     `Open`, `CheckAccount`, `OpenAs` на каждую роль, `AgentAccounts()` —
     ровно сегодняшний блок `case "jira"`.
   Отказ любого трекера — отказ всей команды (строгая сборка, D5).
5. `forgesOf(projects)` по **всем** проектам, один раз.
6. `workspace.Default()`, `ledger.Default()`, `budget.Load(...)`,
   `runner.ConfigSHA`, `runagent.SandboxesOf(*backend)` — один раз.
7. На каждый трекер — `pipeline.Office{Tracker, Trackers, Accounts,
   Projects: projects.For(name), …общие поля}`; обёртка `namedOffice`.

Строки источников печатаются по ходу, как сегодня; `tracker.yaml`
выходит после `projects.local.yaml` и только при `jira` в списке.
`TestConfigSourcesPrintImmediately` остаётся верным.

### Тип `offices` — `cmd/runner/offices.go`

```go
// namedOffice — офис и имя его трекера: у pipeline.Office имени нет,
// а обход и заголовки в выводе — забота этой обёртки, не пайплайна.
type namedOffice struct {
	name string
	*pipeline.Office
}

// offices — все офисы этого раннера, по одному на трекер, в порядке
// TrackersInUse. Пайплайн о множественности не знает: он получает один
// офис и работает в нём, как и раньше.
type offices struct {
	list []namedOffice
	out  io.Writer
}

// each обходит офисы по порядку. Заголовок «== трекер jira ==» печатается
// только когда офисов больше одного: при одном вывод совпадает с прежним
// байт в байт. Ошибка одного офиса не останавливает остальных — все
// собираются errors.Join и уходят наверх разом (D5).
func (all *offices) each(ctx context.Context, fn func(namedOffice) error) error

// byProject — офис, которому принадлежит проект. Для worktree rm и ls
// --project: у задачи есть проект, у проекта — трекер, у трекера — офис.
func (all *offices) byProject(key string) (namedOffice, error)

// loop — драйвер цикла, переехавший из pipeline.Office.Loop: на каждой
// итерации по каждому офису reap → tick → complete-splits, ошибки в лог,
// затем пауза или остановка по сигналу. ctx проверяется и **между
// офисами**: сигнал, пришедший во время прогона в jira, останавливает
// цикл после этого прогона, не дожидаясь mock.
func (all *offices) loop(ctx context.Context, every time.Duration, role string) error
```

`each` при `ctx.Err() != nil` перед очередным офисом возвращает
накопленное — этим и держится «стоп между прогонами» на уровне офиса.

### Команды

| Команда | Было | Стало |
|---|---|---|
| `tick` | `o.TickAll` / `o.Tick(role)` | `all.each(…)`; при `--role` строка `«%s: работы нет»` печатается в каждом офисе, под его заголовком |
| `loop` | `o.Loop(ctx, every, role)` | `all.loop(ctx, every, role)`; `Office.Loop`, `tickOnce` удаляются из `pipeline.go` |
| `reap`, `complete-splits` | `o.Reap` / `o.CompleteSplits` | через `each` |
| `ls` | один `printBoard` | источники один раз (их печатает `offices()`), затем `each` с `printBoard` на офис; `--project` → `byProject` |
| `worktree rm KEY` | `o.Tracker.Get(key)` | `byProject(entry.Project)`, дальше как сегодня |

Код возврата `tick`: ненулевой, если `each` вернул ошибку; текст —
`errors.Join`, по строке на офис с его именем в префиксе
(`jira: поиск задач не удался: …`).

## `tracker.yaml`: только при `jira`

Список трекеров известен после загрузки проектов, поэтому файл
открывается в шаге 4 конструктора и только для `jira`. На машине, где все
проекты `mock`, файла может не быть, и в раскладке конфигурации строки
о нём нет — как сегодня под `--tracker mock`. Проект `jira` без файла —
сегодняшний отказ `jira.LoadConfig` с именем файла.

## `scripts/jira-setup.sh`: тип связи

По образцу `add_field` (найти по имени → создать → напечатать):

```sh
# --- тип связи «зависит от» ---------------------------------------------------
# Его читает LinkDependsOn (depends_on_link в tracker.yaml). Направление,
# в котором этот сервер рисует outward/inward, компенсирует код раннера
# (internal/tracker/jira, LinkDependsOn), а не имена здесь.
add_link_type() {
	local name=$1 outward=$2 inward=$3
	if api "$url/rest/api/2/issueLinkType" | python3 -c '…name == sys.argv[1]…' "$name"; then
		echo "  тип связи $name уже есть"; return
	fi
	api -X POST -d "{\"name\":\"$name\",\"outward\":\"$outward\",\"inward\":\"$inward\"}" \
		"$url/rest/api/2/issueLinkType" >/dev/null
	echo "  тип связи $name заведён"
}
echo "тип связи:"
add_link_type Depends 'depends on' 'is depended on by'
```

Итоговая сводка получает строку `depends_on_link: Depends` после четырёх
полей. Имена — константы скрипта: они же стоят в образце, а переименовать
их — значит править обоих.

## `tracker.example.yaml`: заголовок под копию

Первый абзац переписывается как заголовок рабочего файла:

```yaml
# Подключение офиса к JIRA: ${OFFICE_HOME}/tracker.yaml.
#
# Образец лежит в репозитории как tracker.example.yaml и копируется сюда
# целиком:
#     mkdir -p "${OFFICE_HOME:-$HOME/.office}"
#     cp tracker.example.yaml "${OFFICE_HOME:-$HOME/.office}/tracker.yaml"
# После копии правятся ровно пять значений: base_url и четыре customfield_*
# из вывода scripts/jira-setup.sh. Остальное совпадёт где угодно.
```

Абзац про «раннер этот файл не читает» уходит. `accounts.roles`,
`also_agents`, `issue_type` — закомментированные блоки, у каждого одна
строка: «раскомментируй, если …». `depends_on_link: Depends` активен —
тип заводит скрипт.

## `docs/DESIGN.md` §2.5 — правка текста

Формулировка границы: «**репозиторий — это фреймворк, инстанс настраивается
в `${OFFICE_HOME}`**». Списки:

- В репо: роли (`role.md`, `role.yaml`) и их общие правила
  (`roles/_base/base.md`, `roles/_base/base.yaml`), скиллы, адаптеры,
  раннер, bootstrap, документация, граф переходов, дефолтные бюджеты,
  образец `tracker.example.yaml`. **Проектов в репозитории нет.**
- В `${OFFICE_HOME}`: `projects.local.yaml` — каждый проект целиком (где
  репозиторий, ветка по умолчанию, трекер, forge, auto_merge, добавки
  к правилам), `tracker.yaml`, перекрытие бюджетов.

Абзацы «Проекты собираются из двух половин…» и «Долг закрыт не до конца»
удаляются; вместо них — «Раннер обслуживает все трекеры, названные
проектами; флага выбора трекера нет» и «Слои разрешений: базовый
(`roles/_base/base.yaml`) → машинный (`defaults`) → проектный (запись) →
ролевой». Абзац про печать источников остаётся.

## README и ONBOARDING — только то, что стало бы ложью

README: шаг 3 быстрого старта удаляется, шаг 4 получает `default_branch`;
`./bin/runner ls --tracker jira` и `tick --tracker jira` → без флага;
«Где что лежит»: строка `projects.yaml` уходит, `roles/` упоминает
`_base/base.yaml`; абзац про `-dirty` в конце раздела — переписан: списка
проектов в репозитории больше нет. ONBOARDING: Б5 — без правки
`projects.yaml`, `default_branch` в машинной половине, `Depends` заводит
скрипт (Б2); Б7 — команды без `--tracker`; симптом «`config:…-dirty`» —
причина «ваш проект в закоммиченном projects.yaml» больше не существует,
абзац сокращается до общего случая.

## Миграция этой машины

`~/.office/projects.local.yaml` до сборки:

```yaml
OFFICE:
  repo_url: /Users/aleksejkolesnikov/IdeaProjects/office-polygons/client.git
  default_branch: master
  tracker: mock

VO:
  repo_url: /Users/aleksejkolesnikov/IdeaProjects/office-polygons/client.git
  default_branch: master
  tracker: jira

EXP:
  repo_url: https://github.com/kao73/expense-tracker.git
  default_branch: main
  tracker: jira
  forge: github
  auto_merge:
    enabled: true
    target_branch: office-integration
  # Хосты, нужные только этому проекту (Playwright e2e в песочнице) —
  # раньше лежали в projects.yaml: defaults, свидетельства в git log.
  network:
    - mcr.microsoft.com
    - "*.data.mcr.microsoft.com"
    - bun.sh
    - cdn.playwright.dev
    - playwright.download.prss.microsoft.com
    - ports.ubuntu.com
    - deb.nodesource.com
```

Откат: `git revert`, убрать `default_branch` и блок `network` у EXP,
вернуть `projects.yaml` — старый строгий разбор новых ключей не примет.

## Тестирование

**`internal/runner`** — `role_test.go`: три случая базового слоя (выше);
`Union` — таблица на пустые, повторы, порядок.

**`internal/tracker`** — `config_test.go` таблицей: минимальная запись →
`Branch("KEY-1") == "agent/KEY-1"`, `PRBranch() == default_branch`; явный
`branch_prefix`; нет `default_branch` → отказ с ключом, проектом, файлом;
неизвестный ключ в записи → отказ строгого разбора с именем ключа; файл
без проектов → отказ; `defaults` с `auto_merge`/`default_branch` → отказ
по имени; `defaults.network` попадает в каждый проект; `TrackersInUse`
отсортирован и без повторов; `RefuseLeftoverOfficeFile` — есть файл →
отказ с обоими адресатами, нет → nil. `boundary_test.go` — без
`projects.yaml`, с проверкой `roles/_base/base.yaml`.

**`cmd/runner`** — `offices_test.go`: два `namedOffice` над
`mock.New(tmpA)`, `mock.New(tmpB)`: `each` печатает заголовки только при
двух; при одном — не печатает; ошибка первого не мешает второму, обе
в `errors.Join`; `ctx` отменён до второго — второй не вызван; `byProject`
находит и отказывает. `office_test.go`: временные `OFFICE_CONFIG_ROOT`
(копия `workflow.yaml`, пустой `roles/`) и `OFFICE_HOME`: только `mock` —
без `tracker.yaml` стартует, строки о нём нет; проект `jira` без файла —
отказ с именем файла; `jira` с `httptest`, отвечающим 401 на `/myself`, —
отказ до любой работы; `--tracker x` — ошибка разбора флагов. `board_test.go`
— заголовки при двух трекерах, прежний вывод при одном. `worktree_test.go`
— `rm` находит офис по проекту.

**`internal/pipeline`** — удаление `Loop`/`tickOnce` и правка тестов, что
строили `Projects` руками (`pipeline_test.go`, `prpass_test.go`); поведение
не меняется, новых тестов нет.

**Живьём, без токенов** (отчёт — `docs/superpowers/reports/…-verify.md`):
`./bin/runner ls` — оба трекера, без флага; `./bin/run-agent --role analyst
--dry-run` — `config_sha` без `-dirty`, в правилах `Bash(git *push*)`;
`scripts/jira-setup.sh` дважды на полигоне — «тип связи Depends заведён»,
затем «уже есть», `GET /issueLinkType` показывает один. Платных прогонов
нет: пайплайн не менялся.

## Граничные случаи

- **`roles/_base/` без `base.yaml`** — отказ загрузки любой роли, не пустой
  слой: половина базы (промпт) без другой половины (правила) — это
  сломанная поставка.
- **`defaults` пустой или `defaults: {}`** — законно, слоя нет.
- **Один проект, трекер `mock`, `tracker.yaml` лежит** — файл не читается
  и не упоминается; лишний файл не ошибка.
- **Два проекта на одном bare-репозитории под разными трекерами**
  (OFFICE и VO сегодня) — офисы разные, клон один: `Workspaces` общий,
  `sweepWorktrees` каждого офиса фильтрует папки своими проектами, как
  сегодня под двумя запусками с разными `--tracker`.
- **Сигнал во время прогона** — как сегодня: прогон дорабатывает,
  `each` не заходит в следующий офис, `loop` выходит.
- **`tick --role` при офисе, у которого роли нет в графе** — граф один на
  всех офисов, случая нет.
