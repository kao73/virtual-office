# Go-пакеты под internal/, точки входа в cmd/ — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Перенести 11 Go-пакетов корня репозитория (`adapters`, `backends`,
`budget`, `forge`, `guard`, `ledger`, `pipeline`, `runagent`, `runner`,
`tracker`, `workspace`) под `internal/`, а три точки входа
(`runner/cmd/{runner,run-agent,validate-result}`) — в `cmd/` верхнего уровня,
без изменения поведения системы.

**Architecture:** Чисто структурный перенос: `git mv` пакетов, массовая правка
import-путей, точечная правка нескольких строковых литералов и
относительных `..`-путей в тестах, которые зависят от глубины каталога
пакета от корня репозитория. Поведение не меняется — гарантия этого:
`go build`, `go vet`, `go test ./...` зелёные до и после.

**Tech Stack:** Go 1.26 (модуль `github.com/kao73/virtual-office`), git,
POSIX shell (`bin/runner`, `bin/run-agent`).

**Spec:** `docs/superpowers/specs/2026-08-27-golang-root-restructure-design.md`

## Global Constraints

- Module path `github.com/kao73/virtual-office` не меняется — `go.mod` не
  трогаем.
- Имена пакетов не меняются — меняется только import-путь (добавляется
  `internal/`).
- Не группировать пакеты внутри `internal/` по слоям (`internal/core/`,
  `internal/integrations/` и т.п.) — вне рамок.
- Не разбивать пакет `runner` на более мелкие — вне рамок.
- Не трогать `docs/notes/`, `docs/STAGE-*.md`, `docs/comet/`,
  `docs/openspec/`, `docs/superpowers/specs/` и `docs/superpowers/plans/`
  (кроме самой спеки и этого плана), а также историческую строку
  `docs/DESIGN.md:163` — это датированные записи, не текущее состояние.
- Ветка `golang-root-restructure` уже создана от `master` (обычная git-ветка,
  без Comet Classic) — работаем на ней.
- Каждая задача — отдельный коммит; хуки не обходить (`--no-verify` не
  использовать).

---

## Task 1: Перенести Go-пакеты под internal/, точки входа в cmd/, поправить импорты и относительные пути

**Files:**
- Move: `adapters/`, `backends/`, `budget/`, `forge/`, `guard/`, `ledger/`,
  `pipeline/`, `runagent/`, `runner/`, `tracker/`, `workspace/` →
  `internal/<имя>/` (сохраняя поддиректории: `adapters/claude`,
  `backends/{sbx,local}`, `tracker/{mock,jira}`)
- Move: `internal/runner/cmd/{runner,run-agent,validate-result}` →
  `cmd/{runner,run-agent,validate-result}`
- Modify: все `*.go` файлы репозитория (импорт-путь `github.com/kao73/virtual-office/<pkg>` → `.../internal/<pkg>`)
- Modify: `internal/runner/validator.go` (константа `validatorPkg`)
- Modify: `internal/runner/role_test.go`, `internal/runner/validator_test.go`,
  `internal/pipeline/pipeline_test.go`, `internal/tracker/boundary_test.go`,
  `internal/tracker/config_test.go`, `internal/tracker/jira/jira_test.go`,
  `internal/adapters/claude/hook_test.go`, `cmd/run-agent/main_test.go`
  (относительные `..`-пути к корню репозитория — глубина каталога меняется)

**Interfaces:**
- Consumes: ничего (первая задача).
- Produces: дерево `internal/<pkg>` + `cmd/{runner,run-agent,validate-result}`,
  на которое опираются Task 2 (build-цели в `bin/`) и Task 3 (пути в доках).
  Контракт для них — `go build ./... && go vet ./... && go test ./...`
  зелёные на выходе из этой задачи.

Это рефакторинг без нового поведения, поэтому цикл «тест сначала» здесь не
red/green на новом тесте, а «весь набор тестов зелёный до и после» — шаги
ниже задают его явно, а не оставляют результатом go test как сюрприз.

- [x] **Шаг 1: Зафиксировать базовую линию**

```sh
go build ./... && go vet ./... && go test ./...
```

Ожидается: всё проходит (`ok` по каждому пакету). Если какой-то тест уже
падает или пропущен на `master` — запомните это сейчас, чтобы не приписать
рефакторингу чужую поломку.

- [x] **Шаг 2: Создать целевые каталоги**

```sh
mkdir -p internal cmd
```

- [x] **Шаг 3: Перенести пакеты (git mv, сохраняет историю файлов)**

```sh
git mv adapters internal/adapters
git mv backends internal/backends
git mv budget internal/budget
git mv forge internal/forge
git mv guard internal/guard
git mv ledger internal/ledger
git mv pipeline internal/pipeline
git mv runagent internal/runagent
git mv runner internal/runner
git mv tracker internal/tracker
git mv workspace internal/workspace
```

- [x] **Шаг 4: Вынести точки входа из internal/runner/cmd/ в cmd/ верхнего уровня**

```sh
git mv internal/runner/cmd/runner cmd/runner
git mv internal/runner/cmd/run-agent cmd/run-agent
git mv internal/runner/cmd/validate-result cmd/validate-result
rmdir internal/runner/cmd
```

Проверка: `find internal/runner -maxdepth 1 -type d` не должен показывать
`cmd`.

- [x] **Шаг 5: Массовая правка import-путей**

```sh
grep -rl 'github\.com/kao73/virtual-office/' --include='*.go' . | \
  xargs sed -i '' -E 's#(github\.com/kao73/virtual-office/)(adapters|backends|budget|forge|guard|ledger|pipeline|runagent|runner|tracker|workspace)#\1internal/\2#g'
```

Регулярка целится ровно в 11 известных имён пакетов — не в произвольный
хвост пути после module root, — чтобы правка была предсказуемой и её можно
было прочитать в diff'е, а не гадать, что именно она задела.

- [x] **Шаг 6: Поправить хардкод пути сборки ограждения**

Файл: `internal/runner/validator.go`, строка с `validatorPkg`.

Было:
```go
const validatorPkg = "./runner/cmd/validate-result"
```

Стало:
```go
const validatorPkg = "./cmd/validate-result"
```

Это не импорт, а строка, которую `EnsureValidator` передаёт в `go build`
при сборке бинарника ограждения для песочницы — правка импортов (шаг 5) её
не касается.

- [x] **Шаг 7: Поправить относительные `..`-пути в тестах, зависящие от глубины пакета**

Перенос `runner` → `internal/runner`, `pipeline` → `internal/pipeline`,
`tracker` → `internal/tracker` и т.д. добавляет один уровень вложенности —
тесты, которые находят корень репозитория через `filepath.Abs("..")` или
`LoadRole("..", …)`, после переноса будут смотреть на `internal/`, а не на
настоящий корень. У `cmd/run-agent` (был `runner/cmd/run-agent`, стал
`cmd/run-agent`) — обратный эффект: каталог стал на уровень МЕЛЬЧЕ.

`internal/runner/role_test.go`:
```go
// строка 254, было:
role, err := LoadRole("..", name)
// стало:
role, err := LoadRole(filepath.Join("..", ".."), name)
```
```go
// строка 277, было:
role, err := LoadRole("..", "reviewer")
// стало:
role, err := LoadRole(filepath.Join("..", ".."), "reviewer")
```
```go
// строка 308, было:
entries, err := os.ReadDir(filepath.Join("..", RolesDir))
// стало:
entries, err := os.ReadDir(filepath.Join("..", "..", RolesDir))
```

`internal/runner/validator_test.go`, функция `configRoot`:
```go
// было:
root, err := filepath.Abs("..")
// стало:
root, err := filepath.Abs(filepath.Join("..", ".."))
```

`internal/pipeline/pipeline_test.go`, функция `repoRoot`:
```go
// было:
root, err := filepath.Abs("..")
// стало:
root, err := filepath.Abs(filepath.Join("..", ".."))
```

`internal/tracker/boundary_test.go`, `TestRepoCarriesNoMachineValues`:
```go
// было:
root := ".."
// стало:
root := filepath.Join("..", "..")
```

`internal/tracker/config_test.go` — три одинаковых вхождения (строки 15,
846, 878), во всех трёх заменить одинаково (`replace_all`, если правите
через Edit):
```go
// было:
root := filepath.Join("..")
// стало:
root := filepath.Join("..", "..")
```

`internal/tracker/jira/jira_test.go`, строка 697:
```go
// было:
root := filepath.Join("..", "..")
// стало:
root := filepath.Join("..", "..", "..")
```

`internal/adapters/claude/hook_test.go`:
```go
// строка 36, было:
path, err := runner.EnsureValidator(filepath.Join("..", ".."), runner.HostPlatform())
// стало:
path, err := runner.EnsureValidator(filepath.Join("..", "..", ".."), runner.HostPlatform())
```
```go
// строки 46, 146, 175 — одинаковая строка трижды, было:
shipped, err := os.ReadFile(filepath.Join("..", "..", "hooks", "require-result.sh"))
// стало (во всех трёх местах):
shipped, err := os.ReadFile(filepath.Join("..", "..", "..", "hooks", "require-result.sh"))
```

`cmd/run-agent/main_test.go`, строка 31 (единственный файл с обратным
сдвигом — каталог стал мельче на один уровень):
```go
// было:
root, err := filepath.Abs(filepath.Join("..", "..", ".."))
// стало:
root, err := filepath.Abs(filepath.Join("..", ".."))
```

- [x] **Шаг 8: Собрать и проверить статически**

```sh
go build ./... && go vet ./...
```

Ожидается: без ошибок. Если `go build` жалуется на неизвестный import-путь —
значит шаг 5 пропустил файл; найдите его через `grep -rn 'virtual-office/'
--include='*.go' .` и добавьте `internal/` вручную.

- [x] **Шаг 9: Прогнать полный набор тестов**

```sh
go test ./...
```

Ожидается: тот же результат, что в шаге 1 (все пакеты `ok`, включая
`internal/runner` — там живут тесты `EnsureValidator`, которые реально
пересобирают `cmd/validate-result` и упадут, если шаг 6 или 7 сделаны не
до конца).

- [x] **Шаг 10: Проверить, что старых import-путей не осталось**

```sh
grep -rn 'virtual-office/\(adapters\|backends\|budget\|forge\|guard\|ledger\|pipeline\|runagent\|runner\|tracker\|workspace\)' --include='*.go' . | grep -v 'virtual-office/internal/'
```

Ожидается: пустой вывод.

- [x] **Шаг 11: Закоммитить**

```sh
git add -A
git commit -m "$(cat <<'EOF'
refactor: перенести Go-пакеты под internal/, точки входа в cmd/

Одиннадцать Go-пакетов лежали прямо в корне репозитория вперемешку
с не-Go контентом офиса; runner/cmd/* — внутри пакета runner без
содержательной причины. internal/ закрывает импорт извне на уровне
компилятора, cmd/ верхнего уровня — идиоматичное место для точек
входа, зависящих от разных внутренних пакетов. Поведение не менялось:
go build/vet/test зелёные до и после.
EOF
)"
```

---

## Task 2: Обновить build-цели в bin/runner и bin/run-agent

**Files:**
- Modify: `bin/runner`
- Modify: `bin/run-agent`

**Interfaces:**
- Consumes: `cmd/runner`, `cmd/run-agent` из Task 1.
- Produces: рабочие CLI-обёртки `./bin/runner`, `./bin/run-agent`,
  собирающие бинарники из новых путей — на них рассчитывает README (раздел
  «От нуля до первой задачи») и `docs/ONBOARDING.md`.

- [x] **Шаг 1: Поправить build-цель в bin/runner**

Было:
```sh
go build -o "$bin/runner" ./runner/cmd/runner
```

Стало:
```sh
go build -o "$bin/runner" ./cmd/runner
```

- [x] **Шаг 2: Проверить, что runner собирается и исполняется с новой цели**

```sh
./bin/runner
echo "exit: $?"
```

Ожидается: код выхода `2`, в stderr — `runner: нужна подкоманда` и текст
usage. Это доказывает, что бинарник собрался именно из `./cmd/runner` и
дошёл до реальной логики, а не упал на «package not found».

- [x] **Шаг 3: Поправить build-цель в bin/run-agent**

Было:
```sh
go build -o "$bin/run-agent" ./runner/cmd/run-agent
```

Стало:
```sh
go build -o "$bin/run-agent" ./cmd/run-agent
```

- [x] **Шаг 4: Проверить, что run-agent собирается и исполняется с новой цели**

```sh
./bin/run-agent
echo "exit: $?"
```

Ожидается: код выхода `2`, в stderr — `run-agent: нужны --role и --workdir`.

- [x] **Шаг 5: Закоммитить**

```sh
git add bin/runner bin/run-agent
git commit -m "$(cat <<'EOF'
refactor: обновить build-цели bin/runner и bin/run-agent под cmd/

Точки входа переехали в cmd/ на предыдущем шаге; обёртки должны
собирать бинарники из новых путей.
EOF
)"
```

---

## Task 3: Обновить пути internal/ и cmd/ в README и контрактах

**Files:**
- Modify: `README.md` (раздел «Где что лежит»)
- Modify: `docs/DESIGN.md` (строка 55)
- Modify: `docs/contracts/role-sandbox-permissions.md` (строки 88, 90, 97,
  101, 140)
- Modify: `docs/contracts/tracker-protocol.md` (строки 7, 313, 780)

**Interfaces:**
- Consumes: итоговую структуру `internal/` + `cmd/` из Task 1 — описывает
  её словами, кода не меняет.
- Produces: README и контракты без устаревших путей; ничего в коде на них
  не завязано, но это единственный способ узнать «где что лежит», не читая
  git log.

Здесь нет теста в обычном смысле — проверка сделана grep'ом в шаге 5: он
подтверждает, что во ВСЕХ живых доках (не архивных) путь `tracker/config.go`
и подобные заменены, а не пропущены.

- [x] **Шаг 1: Обновить README.md, раздел «Где что лежит»**

Найдите блок между заголовком `## Где что лежит` и следующим заголовком
(тройной code fence со списком директорий). Замените содержимое fence на:

```
roles/                 спецификации ролей: промпт, машиночитаемый контракт, шаблоны артефактов
skills/                кастомные скиллы, подключаются ролью поимённо
hooks/                 скрипты ограждений на событии Stop
cmd/                   точки входа: runner, run-agent, validate-result
internal/guard/        сами ограждения: проверки прогона, общие для хука и раннера
internal/adapters/     перевод роли в вызов конкретного агента
internal/backends/     где выполняется агент: local, sbx
internal/runner/       контракт обмена, загрузка роли, сборка входа, архив, команды
internal/runagent/     один прогон агента: роль плюс рабочая папка → результат
internal/tracker/      контракт трекера: задача, аренда, правило владения, комментарии
internal/tracker/mock/ файловый трекер: отладка конвейера без JIRA
internal/forge/        pull request: интерфейс и GitHub через REST
internal/tracker/jira/ трекер поверх JIRA Server REST API v2
internal/workspace/    bare-клоны, worktree задач, публикация веток
internal/ledger/       реестр прогонов: строка на прогон, локально для машины
internal/budget/       пределы расхода поверх реестра
internal/pipeline/     конвейер: захват задачи, прогон, разбор исхода, переходы
bootstrap/             обвязка машины: задания планировщика, локальная JIRA в контейнере
scripts/               smoke-тест, настройка полигона JIRA: статусы, workflow, доски, очередь
docs/                  замысел, контракты, внедрение, рабочие заметки
workflow.yaml          граф состояний: статусы, роли, PR-проход, переходы по исходам
projects.yaml          проекты-клиенты офиса: имена и неизменные свойства веток
tracker.example.yaml   образец подключения к JIRA; рабочий файл — в ${OFFICE_HOME}
budgets.yaml           дефолтные пределы расхода; необязателен, перекрывается накладкой машины
```

Порядок пунктов сохранён как в оригинале (только `cmd/` добавлен после
`hooks/`, а Go-пакеты получили префикс `internal/`).

- [x] **Шаг 2: Обновить docs/DESIGN.md, строка 55**

Было:
```
- Роль — агент-нейтральная спецификация + адаптер (`adapters/claude`, позже `adapters/codex` и т.д.), который превращает её в флаги конкретного CLI.
```

Стало:
```
- Роль — агент-нейтральная спецификация + адаптер (`internal/adapters/claude`, позже `internal/adapters/codex` и т.д.), который превращает её в флаги конкретного CLI.
```

Строку 163 (`Предложить целевую структуру (roles/, plugins/, adapters/,
runner/, bootstrap/, docs/)...`) **не трогать** — это исторический план MVP
этапа 1, а не описание текущего состояния.

- [x] **Шаг 3: Обновить docs/contracts/role-sandbox-permissions.md**

Строка 88, было:
```
- `tracker.LoadProjects` (`tracker/config.go`) — сливает уровни 1–3 в
```
стало:
```
- `tracker.LoadProjects` (`internal/tracker/config.go`) — сливает уровни 1–3 в
```

Строка 90, было:
```
- `tracker.MergeProjectRules` (`tracker/rules.go`) — сливает уровень 4
```
стало:
```
- `tracker.MergeProjectRules` (`internal/tracker/rules.go`) — сливает уровень 4
```

Строка 97, было:
```
- `adapters/claude/adapter.go` не меняется в части логики: `Build` и
```
стало:
```
- `internal/adapters/claude/adapter.go` не меняется в части логики: `Build` и
```

Строка 101, было:
```
- `backends/sbx/sbx.go` не меняется вовсе: он применяет `l.NetworkAllow`
```
стало:
```
- `internal/backends/sbx/sbx.go` не меняется вовсе: он применяет `l.NetworkAllow`
```

Строка 140, было:
```
  (`adapters/claude/adapter.go`, `toolNames`), и это настоящая техническая
```
стало:
```
  (`internal/adapters/claude/adapter.go`, `toolNames`), и это настоящая техническая
```

Строки 30, 37–38 (упоминания `tracker`/`forge`/`runner` как имён пакетов
или ключей конфига, не файловых путей) **не трогать** — имя пакета не
меняется, меняется только import-путь.

- [x] **Шаг 4: Обновить docs/contracts/tracker-protocol.md**

Строка 7, было:
```
Код контракта — пакет `tracker/`. Общая часть (правило владения, маркеры, разбор графа)
```
стало:
```
Код контракта — пакет `internal/tracker/`. Общая часть (правило владения, маркеры, разбор графа)
```

Строка 313, было:
```
раннера. `tracker/jira` переводит markdown в wiki-разметку Server; `tracker/mock` пишет
```
стало:
```
раннера. `internal/tracker/jira` переводит markdown в wiki-разметку Server; `internal/tracker/mock` пишет
```

Строка 780, было:
```
`tracker/mock` — реализация на файлах: ею отлаживают конвейер, не поднимая JIRA.
```
стало:
```
`internal/tracker/mock` — реализация на файлах: ею отлаживают конвейер, не поднимая JIRA.
```

Строки 815, 820, 828, 835 (`tracker`/`forge` как ключи `projects.local.yaml`)
**не трогать** — это имена конфигурационных полей, не пути.

- [x] **Шаг 5: Проверить, что живые доки не содержат устаревших путей**

```sh
grep -n 'tracker/config\.go\|tracker/rules\.go\|adapters/claude/adapter\.go\|backends/sbx/sbx\.go' \
  docs/contracts/role-sandbox-permissions.md | grep -v 'internal/'
grep -n '`tracker/`\|`tracker/jira`\|`tracker/mock`' docs/contracts/tracker-protocol.md | grep -v 'internal/'
grep -n 'adapters/claude`, позже `adapters/codex' docs/DESIGN.md | grep -v 'internal/'
```

Ожидается: пустой вывод от всех трёх команд.

- [x] **Шаг 6: Закоммитить**

```sh
git add README.md docs/DESIGN.md docs/contracts/role-sandbox-permissions.md docs/contracts/tracker-protocol.md
git commit -m "$(cat <<'EOF'
docs: обновить пути internal/ и cmd/ в README и контрактах

README «Где что лежит», DESIGN.md и живые контракты описывали пути
до переноса Go-пакетов под internal/ и точек входа под cmd/.
Исторические доки (docs/notes, docs/STAGE-*.md, docs/comet,
docs/openspec, прежние specs/plans) намеренно не тронуты.
EOF
)"
```
