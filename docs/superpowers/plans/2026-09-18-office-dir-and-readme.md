# Каталог `office/` и витрина README — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** убрать из корня репозитория девять `.go`-файлов и семь записей
содержимого офиса, собрав их в каталоге `office/`, и превратить README из
справочника на 649 строк в витрину на ~170 строк, вынеся руководство в
`docs/guide/`.

**Architecture:** `go:embed` не поднимается выше каталога своего пакета, но
вниз умеет. Поэтому содержимое офиса (`roles/`, `skills/`, `hooks/`,
`sbx-kits/`, четыре YAML) и пакет, который его встраивает, переезжают в один
каталог `office/`; ограждения релиза становятся отдельным пакетом
`office/validators/`. `OFFICE_CONFIG_ROOT` начинает означать «каталог офиса»,
и клонов­ский `office/` совпадает по раскладке с распакованным
`${OFFICE_HOME}/office/<версия>/` — специальное правило со срезанием префикса
`bootstrap/` исчезает.

**Tech Stack:** Go 1.26 (`go:embed`, build tags, implicit `_GOOS_GOARCH.go`
constraints), POSIX sh (обёртки `bin/*`, `scripts/*.sh`), GoReleaser v2.18.2,
GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-18-office-dir-and-readme-design.md`

## Global Constraints

- Язык документов и комментариев — русский; идентификаторы, термины и ключи
  конфигов — английские (`CLAUDE.md`).
- Сообщения коммитов — английские, conventional commits, с трейлером
  `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- Ни одного изменения в том, что видит агент или трекер: формат маркеров,
  протокол трекера, контракты ролей, имена полей (`config_sha`, `config:`)
  остаются как есть.
- Ветка `comet/office-dir-and-readme` от `master` (PR #11 уже смёржен).
- Полный набор проверок репозитория: `go build ./...`, `go vet ./...`,
  `gofmt -l .` (обязан быть пуст), `go test ./...`,
  `sh scripts/install-test.sh`, `sh scripts/check-host-is-target.sh`.
- Релизные проверки: `sh scripts/build-validators.sh && go test -tags release ./office/...`;
  после них удалять `office/validators/validate-result-*`.
- Модуль — `github.com/kao73/virtual-office`; после переезда корневого пакета
  нет, пакет поставки — `github.com/kao73/virtual-office/office`.

---

# Этап A — переезд

### Task 1: Содержимое офиса и его пакет переезжают в `office/`

Механический переезд: `git mv`, правка директив `go:embed`, путей импорта,
ldflags, обёрток, скриптов и тестов, которые считают корнем офиса корень
репозитория. Поведение не меняется нигде, кроме одного места: распаковка
перестаёт срезать префикс `bootstrap/`, потому что срезать больше нечего.

**Files:**
- Move: `roles/`, `skills/`, `hooks/`, `workflow.yaml`, `budgets.yaml`,
  `tracker.example.yaml`, `projects.local.example.yaml` → `office/`
- Move: `bootstrap/sbx-kits/` → `office/sbx-kits/`
- Move: `payload.go`, `payload_test.go` → `office/`
- Move: `validators_dev.go`, `validators_darwin_arm64.go`,
  `validators_linux_amd64.go`, `validators_linux_arm64.go`,
  `validators_test.go`, `validators_release_test.go`,
  `payload/validators/README.md` → `office/validators/`
- Move: `release_config_test.go` → `internal/release/config_test.go`
- Modify: `office/payload.go`, `office/validators/*.go`,
  `internal/office/unpack.go`, `internal/office/unpack_test.go`,
  `internal/office/unpack_umask_test.go`, `internal/runner/office.go`,
  `internal/runner/validator.go`, `internal/runner/office_test.go`,
  `internal/runner/validator_test.go`, `internal/runner/role_test.go`,
  `internal/tracker/boundary_test.go`, `internal/tracker/config_test.go`,
  `internal/tracker/jira/jira_test.go`, `internal/pipeline/pipeline_test.go`,
  `internal/adapters/claude/hook_test.go`, `cmd/runner/init.go`,
  `cmd/runner/init_test.go`, `cmd/runner/office_test.go`,
  `bin/runner`, `bin/run-agent`, `bin/eval-roles`,
  `scripts/build-validators.sh`, `.gitignore`, `.goreleaser.yaml`

**Interfaces:**
- Consumes: ничего (первая задача).
- Produces: пакет `github.com/kao73/virtual-office/office` с `Payload embed.FS`
  и `Version string`; пакет `github.com/kao73/virtual-office/office/validators`
  с `Validators embed.FS`. Константа `runner.ConfigRootEnv` (`OFFICE_CONFIG_ROOT`)
  теперь означает каталог офиса, а не корень клона.

- [ ] **Step 1: Записать исходные числа**

```bash
cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office
go test -count=1 ./... 2>&1 | tail -25
go test -count=1 -json ./... 2>/dev/null | grep '"Action":"pass"' | grep -c '"Test":'
```

Ожидается: все пакеты `ok`, число тестов запомнить (на `master` — 1166).
Переезд сам по себе не создаёт и не удаляет ни одного теста; расхождение по
итогу задачи значит, что тест потерялся при `git mv`.

- [ ] **Step 2: Перенести файлы**

```bash
mkdir -p office/validators
git mv roles skills hooks office/
git mv workflow.yaml budgets.yaml tracker.example.yaml projects.local.example.yaml office/
git mv bootstrap/sbx-kits office/sbx-kits
git mv payload.go payload_test.go office/
git mv validators_dev.go validators_darwin_arm64.go validators_linux_amd64.go \
       validators_linux_arm64.go validators_test.go validators_release_test.go \
       office/validators/
git mv payload/validators/README.md office/validators/README.md
rmdir payload/validators payload
mkdir -p internal/release
git mv release_config_test.go internal/release/config_test.go
```

`bootstrap/` остаётся: в нём живёт `bootstrap/jira/` — рецепт полигона, он не
часть офиса.

- [ ] **Step 3: Поправить директивы embed в `office/payload.go`**

Пути становятся относительными к `office/`, префикс `bootstrap/` исчезает:

```go
//go:embed all:roles all:skills all:hooks all:sbx-kits
//go:embed workflow.yaml budgets.yaml tracker.example.yaml projects.local.example.yaml
var Payload embed.FS
```

В доке пакета заменить объяснение «лежит в корне модуля потому, что embed не
умеет подниматься выше каталога пакета, а все эти пути видны только отсюда» на
то, что верно теперь: пакет лежит рядом с содержимым, которое встраивает, и
каталог `office/` — это и есть офис; `go:embed` вниз умеет, вверх нет.

- [ ] **Step 4: Поправить `office/validators/*.go`**

Пакет переименовывается, пути embed теряют каталог (файлы лежат рядом):

```go
//go:build release

package validators

import "embed"

// Validators — ограждения под платформы, которые нужны этому раннеру: хост
// darwin/arm64 и его песочница sbx, linux/arm64. Файлы кладёт
// scripts/build-validators.sh; без них релизная сборка не компилируется —
// нарочно: неполный релиз не должен собираться.
//
//go:embed validate-result-darwin-arm64 validate-result-linux-arm64
var Validators embed.FS
```

Аналогично `linux_amd64` (`//go:embed validate-result-linux-amd64`),
`linux_arm64` (`//go:embed validate-result-linux-arm64`) и `validators_dev.go`
(`package validators`, без директивы). В `validators_test.go` и
`validators_release_test.go` — `package validators` и пути без каталога:

```go
raw, err := fs.ReadFile(Validators, "validate-result-"+runtime.GOOS+"-"+runtime.GOARCH)
```

Имена файлов не переименовывать: суффикс `_<os>_<arch>.go` даёт неявное
ограничение сборки, на котором держится «нет файла под цель — релиз не
компилируется», а префикс `validators_` нужен глобу в тесте конфигурации.

- [ ] **Step 5: Поправить импорты и константу каталога ограждений**

`internal/runner/office.go`:

```go
payload "github.com/kao73/virtual-office/office"
```

`internal/runner/validator.go` — два импорта вместо одного, и `validatorsDir`
исчезает, потому что файлы лежат в корне своего embed-дерева:

```go
validators "github.com/kao73/virtual-office/office/validators"
```

```go
func validatorsOrDefault() fs.FS { return orDefault(validatorsFS, validators.Validators) }
```

```go
raw, err := fs.ReadFile(validatorsOrDefault(), name)
```

Удалить `const validatorsDir = "payload/validators"` и его использование;
в `internal/runner/validator_test.go` хелпер `fakeValidators` кладёт ключи без
каталога:

```go
m[validatorName(p)] = &fstest.MapFile{Data: []byte("#!/bin/sh\necho " + p.String() + "\nexit 2\n")}
```

`cmd/runner/init.go`, `cmd/runner/init_test.go`, `cmd/runner/office_test.go`,
`internal/runner/office_test.go` — заменить
`payload "github.com/kao73/virtual-office"` на
`payload "github.com/kao73/virtual-office/office"`.

- [ ] **Step 6: Убрать срезание префикса из распаковки**

`internal/office/unpack.go`: удалить `const bootstrapPrefix` вместе с
комментарием и упростить строку пути:

```go
out := filepath.Join(dst, filepath.FromSlash(path))
```

В доке `Unpack` убрать фразу про перенос `bootstrap/` на уровень выше, оставив
причину своего обхода вместо `os.CopyFS` (тот не умеет прав). В
`internal/office/unpack_umask_test.go` — `want[filepath.FromSlash(path)] = mode`,
и удалить ставший ненужным импорт `strings`, если он больше не используется.
В фикстурах `internal/office/unpack_test.go` и `internal/runner/office_test.go`
ключи `"bootstrap/sbx-kits/..."` заменить на `"sbx-kits/..."`.

- [ ] **Step 7: Поправить тесты, считающие корнем офиса корень репозитория**

| Файл | Было | Стало |
|---|---|---|
| `internal/runner/role_test.go:369,390,405,535` | `filepath.Join("..", "..")` | `filepath.Join("..", "..", "office")` |
| `internal/runner/validator_test.go:18` | `filepath.Abs(filepath.Join("..", ".."))` | без изменений — это корень **модуля** для `go build ./cmd/validate-result` |
| `internal/tracker/boundary_test.go:25,111` | `filepath.Join("..", "..")` | `filepath.Join("..", "..", "office")` |
| `internal/tracker/config_test.go:16,920` | `filepath.Join("..", "..")` | `filepath.Join("..", "..", "office")` |
| `internal/tracker/jira/jira_test.go:1363` | `filepath.Join("..", "..", "..")` | `filepath.Join("..", "..", "..", "office")` |
| `internal/pipeline/pipeline_test.go:272` | хелпер `repoRoot(t)` — на деле корень офиса: кормит `LoadWorkflow` и `Office{Root:…, Source: SourceClone}` | `filepath.Abs(filepath.Join("..", "..", "office"))`, а сам хелпер переименовать в `officeRoot` |
| `internal/adapters/claude/hook_test.go:36` | `Office{Root: filepath.Join("..","..","..")}` | корень модуля, `Source: SourceClone` — **без изменений**: `buildValidator` зовёт `go build` из этого каталога |

Различие важно: `Office.Root` в режиме клона служит двум вещам — оттуда
читаются роли (`LoadRole`) и оттуда же собирается ограждение (`go build`).
После переезда это разные каталоги, и в задаче 2 (`eval-roles`) они разводятся
по-настоящему; здесь достаточно, чтобы каждый тест указывал на тот каталог,
который он на самом деле проверяет. Если тест валится — смотреть, что именно
он читает: `roles/` → `office/`, `./cmd/...` → корень модуля.

- [ ] **Step 8: Поправить обёртки `bin/*`**

Обёртки делают две вещи с одним путём: собирают бинарник (нужен корень
модуля) и сообщают офис (нужен `office/`). После переезда это разные пути.
`bin/runner`:

```sh
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
OFFICE_CONFIG_ROOT="$repo/office"
OFFICE_INVOCATION_DIR=$PWD
export OFFICE_CONFIG_ROOT OFFICE_INVOCATION_DIR

bin="${OFFICE_HOME:-$HOME/.office}/bin"
mkdir -p "$bin"

cd "$repo"
go build -o "$bin/runner-dev" ./cmd/runner
exec "$bin/runner-dev" "$@"
```

Так же `bin/run-agent` (`run-agent-dev`) и `bin/eval-roles` (`eval-roles`).
В комментарии каждой обёртки заменить «корень конфиг-репозитория» на «каталог
офиса в клоне».

- [ ] **Step 9: Поправить скрипты, gitignore и GoReleaser**

`scripts/build-validators.sh` — выход в новое место:

```sh
  GOOS=$os GOARCH=$arch CGO_ENABLED=0 go build -trimpath \
    -o "office/validators/validate-result-$os-$arch" ./cmd/validate-result
  echo "office/validators/validate-result-$os-$arch"
```

`.gitignore` — заменить строки про `payload/validators`:

```
# Кросс-собранные ограждения релиза: их кладёт scripts/build-validators.sh
office/validators/validate-result-*
```

`.goreleaser.yaml` — путь ldflags:

```yaml
    ldflags: &build_ldflags ["-s -w -X github.com/kao73/virtual-office/office.Version=v{{ .Version }}"]
```

- [ ] **Step 10: Поправить переехавший тест релизной конфигурации**

`internal/release/config_test.go`: `package release`, все чтения от корня
репозитория, глоб ищет файлы в новом месте.

```go
// read — файл релизной обвязки от корня репозитория.
func read(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", name))
	if err != nil {
		t.Fatalf("%s не прочитан: %v", name, err)
	}
	return string(raw)
}
```

Оба глоба `filepath.Glob("validators_*_*.go")` →
`filepath.Glob(filepath.Join("..", "..", "office", "validators", "validators_*_*.go"))`,
и имя файла для сообщений брать через `filepath.Base(name)`. Регулярка embed
теряет каталог:

```go
embed := regexp.MustCompile(`validate-result-([a-z0-9]+)-([a-z0-9]+)`)
```

- [ ] **Step 11: Прогнать проверки**

```bash
gofmt -l . ; go build ./... && go vet ./... && go test -count=1 ./... 2>&1 | tail -25
```

Ожидается: `gofmt` пуст, все пакеты `ok`, число тестов то же, что в шаге 1.

- [ ] **Step 12: Проверить релизный путь и установку**

```bash
sh scripts/build-validators.sh && go test -count=1 -tags release ./office/...
rm -f office/validators/validate-result-*
sh scripts/install-test.sh
sh scripts/check-host-is-target.sh
git status --short   # обязано быть пусто, кроме переезда
```

- [ ] **Step 13: Проверить клон живьём**

```bash
./bin/runner version
```

Ожидается: строка `офис: <клон>/office (клон, OFFICE_CONFIG_ROOT)`.

- [ ] **Step 14: Убедиться, что история файлов не потеряна**

```bash
git log --follow --oneline -3 -- office/roles/implementer/role.yaml
```

Ожидается: коммиты старше этого переезда.

- [ ] **Step 15: Коммит**

```bash
git add -A
git commit -m "$(cat <<'EOF'
refactor: the office is a directory, and the package that embeds it lives there

go:embed cannot climb above its package directory, so the payload package
had to sit at the repo root next to roles/, skills/, hooks/ and the four
YAML files — nine .go files in the root of a repository whose root is the
first thing anyone reads. Content and package move into office/ together;
the release checkers become their own package office/validators/.

OFFICE_CONFIG_ROOT now means what its name says — the office directory,
not the clone's root — so a clone's office/ and an unpacked
${OFFICE_HOME}/office/<version>/ have the same layout, and the rule that
stripped the bootstrap/ prefix during unpack is gone.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `eval-roles` разводит корень репозитория и каталог офиса

Единственное место переезда, где меняется логика. Сейчас одна величина
`repoRoot` служит трём целям: корпус `evals/`, `cmd.Dir` для
`go build ./cmd/run-agent` и `OFFICE_CONFIG_ROOT` для агента. После задачи 1
третья цель — другой каталог, и без правки команда без флагов напечатает
«0 cases found» и выйдет с нулём: корпус будет искаться в `office/evals`.

**Files:**
- Modify: `cmd/eval-roles/main.go:39-44,78,85,92-111`,
  `cmd/eval-roles/invoke.go:20-62`, `cmd/eval-roles/main_test.go`,
  `cmd/eval-roles/invoke_test.go`, `bin/eval-roles`

**Interfaces:**
- Consumes: `runner.ResolveOffice(runner.Resolve{}) (runner.Office, error)` и
  `runner.Office{Root, Identity, Source}` из задачи 1.
- Produces: ничего для последующих задач.

- [ ] **Step 1: Написать падающий тест на молчаливый ноль**

`cmd/eval-roles/main_test.go`:

```go
// Корпус кейсов и каталог офиса — разные места, и пустой корпус обязан быть
// отказом, а не зелёным нулём: запуск не из корня репозитория иначе молча
// сообщал бы «кейсов нет», хотя они есть.
func TestRunRefusesWithoutEvalsDir(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, errOut bytes.Buffer
	code, err := run(nil, &out, &errOut)
	if err == nil {
		t.Fatalf("отказа нет, код %d, вывод %q", code, out.String())
	}
	if !strings.Contains(err.Error(), "evals") {
		t.Errorf("отказ не называет корпус: %v", err)
	}
}
```

- [ ] **Step 2: Прогнать тест — обязан упасть**

```bash
go test ./cmd/eval-roles/ -run TestRunRefusesWithoutEvalsDir -v
```

Ожидается: FAIL — сегодня проверяется наличие `roles/`, а не `evals/`, и в
пустом каталоге отказ звучит про корень офиса.

- [ ] **Step 3: Развести две величины**

`cmd/eval-roles/main.go` — вместо `officeRoot()`:

```go
// repoRoot — корень репозитория: оттуда берётся корпус кейсов и оттуда же
// собирается run-agent. Сторож на evals/ обязателен: без него запуск не из
// корня находит пустое множество и зеленеет «0 cases found», как будто
// кейсов действительно нет.
func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(filepath.Join(wd, "evals")); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%q не похож на корень репозитория: нет evals/", wd)
	}
	return wd, nil
}
```

И в `run`:

```go
	repo, err := repoRoot()
	if err != nil {
		return 0, err
	}
	office, err := runner.ResolveOffice(runner.Resolve{Unpack: true})
	if err != nil {
		return 0, err
	}

	dirs, err := discoverCases(filepath.Join(repo, "evals"), *roleFlag, *caseFlag)
```

Ниже по функции: `resolveRunAgentBin(repo, binDir)` и
`evaluateCase(runAgentBin, repo, office, c, stderr, *keepFailedFlag, *cloneFlag)`.

- [ ] **Step 4: Передать офис агенту явно**

`cmd/eval-roles/invoke.go` — `runRoleAgent` получает офис отдельным
параметром, а корень модуля остаётся рабочим каталогом сборки:

```go
func runRoleAgent(binPath, repoRoot string, office runner.Office, role, workdir, taskPath, taskKey string, clone bool) (runner.Result, int, error) {
```

```go
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), runner.ConfigRootEnv+"="+office.Root)
```

`cmd/eval-roles/run.go` — `evaluateCase` принимает офис и пробрасывает его в
`runRoleAgent`:

```go
func evaluateCase(runAgentBin, repoRoot string, office runner.Office, c Case, stderr io.Writer, keepFailed, clone bool) (outcome CaseOutcome) {
```

```go
	result, _, err := runRoleAgent(runAgentBin, repoRoot, office, c.Role, fixtureDir, taskPath, taskKey, clone)
```

- [ ] **Step 5: Прогнать тесты пакета**

```bash
go test -count=1 ./cmd/eval-roles/ -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Ожидается: новый тест PASS, остальные PASS. Тесты, выставлявшие
`OFFICE_CONFIG_ROOT` на корень репозитория, правятся на `<корень>/office`.

- [ ] **Step 6: Проверить, что сторож умеет падать**

Временно вернуть в `repoRoot()` проверку `roles/` вместо `evals/`, прогнать
`TestRunRefusesWithoutEvalsDir` — обязан упасть; вернуть `evals/` правкой той
же строки (не `git checkout`), убедиться `git diff` и прогнать снова.

- [ ] **Step 7: Живая проверка**

```bash
./bin/eval-roles --role reviewer 2>&1 | head -5
```

Ожидается: кейсы находятся (не «0 cases found»); прогон платный — прервать
после строки об обнаруженных кейсах.

- [ ] **Step 8: Коммит**

```bash
git add cmd/eval-roles bin/eval-roles
git commit -m "$(cat <<'EOF'
fix(eval-roles): the corpus root and the office directory are two things

One value served three purposes: the evals/ corpus, the module root for
`go build ./cmd/run-agent`, and OFFICE_CONFIG_ROOT for the agent. They
coincided only while the office was the repository root. After the move
the corpus would be looked up in office/evals, found missing, and a run
without filters would print "0 cases found" and exit zero — the silent
green the guard in officeRoot() existed to prevent.

The office now comes from runner.ResolveOffice like everywhere else, the
corpus and the build stay at the repository root, and the guard moved to
the thing it actually protects: evals/.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Тест: раскладка клона и поставки совпадает

Свойство, ради которого затевался переезд, должно быть проверяемым, а не
обещанным в коммите.

**Files:**
- Modify: `office/payload_test.go` (в импорты добавляется `strings`; `bytes`,
  `io/fs`, `os`, `path/filepath`, `testing` уже есть)

**Interfaces:**
- Consumes: `office.Payload` из задачи 1.
- Produces: ничего.

- [ ] **Step 1: Написать тест**

```go
// Поставка и каталог офиса в клоне — одно дерево: путь внутри embed равен
// пути внутри office/. Это и есть смысл переезда: распаковка больше ничего
// не переименовывает, а значит «роль лежит там же, где лежала» — свойство,
// а не обещание. Go-файлы пакета и ограждения в поставку не входят.
func TestPayloadMirrorsOfficeDirectory(t *testing.T) {
	skip := func(path string) bool {
		return strings.HasSuffix(path, ".go") ||
			path == "validators" || strings.HasPrefix(path, "validators/")
	}

	inPayload := map[string]bool{}
	err := fs.WalkDir(Payload, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		inPayload[path] = true
		if _, err := os.Stat(filepath.FromSlash(path)); err != nil {
			t.Errorf("%s в поставке, но не на диске под office/: %v", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("поставка не обойдена: %v", err)
	}

	err = filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(path)
		if skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !inPayload[rel] {
			t.Errorf("%s лежит в office/, но не едет в поставке", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("каталог офиса не обойдён: %v", err)
	}
}
```

- [ ] **Step 2: Прогнать — обязан пройти**

```bash
go test -count=1 ./office/ -run TestPayloadMirrorsOfficeDirectory -v
```

- [ ] **Step 3: Проверить, что тест умеет падать**

Временно убрать `all:hooks` из директивы `//go:embed` в `office/payload.go`,
прогнать тест — обязан сообщить, что `hooks/require-result.sh` лежит в
`office/`, но не едет в поставке. Вернуть директиву правкой той же строки,
убедиться `git diff` пуст, прогнать снова.

- [ ] **Step 4: Коммит**

```bash
git add office/payload_test.go
git commit -m "$(cat <<'EOF'
test(office): the payload and the clone's office are the same tree

The move's whole point is that a path inside the payload equals a path
inside office/, so unpacking renames nothing. That was a claim in a
commit message; now it fails the suite when it stops being true — in
both directions, so a file added to office/ without an embed directive is
caught as well.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: Документация, где путь стал неверным

Только пути; переписывание текстов — этап B.

**Files:**
- Modify: `docs/DESIGN.md:45-59`, `docs/notes/install.md`,
  `office/sbx-kits/README.md`, `office/validators/README.md`,
  `internal/backends/sbx/sbx.go:34-35`, `CLAUDE.md` (если ссылается на пути)

- [ ] **Step 1: Найти все упоминания старых путей**

```bash
grep -rn 'bootstrap/sbx-kits\|payload/validators\|`payload.go`\|virtual-office\.Version' \
  --include='*.md' --include='*.go' --include='*.sh' --include='*.yaml' . \
  | grep -v 'docs/comet/archive\|docs/openspec/changes/archive\|docs/superpowers/plans'
```

- [ ] **Step 2: Поправить найденное**

В `docs/DESIGN.md` §2.5 — `OFFICE_CONFIG_ROOT` указывает на каталог офиса
(`<клон>/office`). В `docs/notes/install.md` — состав релиза и таблица веток:
`office/payload.go`, `office/validators/`, ldflags-путь
`github.com/kao73/virtual-office/office.Version`. В `office/sbx-kits/README.md`
— путь к скрипту в клоне (`office/sbx-kits/bake-comet-template.sh`).
В `office/validators/README.md` — новое место и новая строка `.gitignore`.
В `internal/backends/sbx/sbx.go` — комментарий со ссылкой на кит.

- [ ] **Step 3: Проверить ссылки**

```bash
grep -rhno '\](\(docs/[^)]*\|office/[^)]*\))' --include='*.md' . \
  | sed 's/.*](\(.*\))/\1/' | sort -u | while read -r p; do
    [ -e "$p" ] || echo "БИТАЯ ССЫЛКА: $p"
  done
```

- [ ] **Step 4: Коммит**

```bash
git add -A
git commit -m "$(cat <<'EOF'
docs: paths follow the office into its own directory

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

# Этап B — витрина

Общее правило для всех задач этапа: **перенос не копирование**. Каждый абзац
сверяется с кодом; устаревшее правится или выбрасывается, а не переносится
«на всякий случай». Источник — нынешний README (нумерация строк ниже — по
состоянию на `master` после задачи 4).

### Task 5: `docs/guide/quickstart.md`

**Files:**
- Create: `docs/guide/quickstart.md`
- Source: `README.md:72-152` («От нуля до первой задачи»), `README.md:186-208`
  («То же самое против JIRA»)

- [ ] **Step 1: Перенести текст и сверить с кодом**

Заголовки документа: «Установка», «Первый проект», «Первая задача»,
«Что происходит за цикл», «То же самое против JIRA», «Путь из клона».

Проверить построчно: имена команд (`runner init`, `runner tick`, `runner ls`,
`runner mock add`), путь установки (релиз, а не клон), пути
`${OFFICE_HOME}/projects.local.yaml` и `${OFFICE_HOME}/mock`, ключи записи
проекта (`repo_url`, `default_branch`, `tracker`, `branch_prefix`, `forge`) —
против `internal/tracker/config.go` и `cmd/runner/main.go`. Путь из клона
(`./bin/runner`) оставить отдельным абзацем: он теперь собирает `runner-dev`.

- [ ] **Step 2: Проверить, что сценарий выполним**

Пройти его руками на файловом трекере до шага «посмотреть, что вышло»
(прогон агента платный — остановиться перед ним и отметить это в документе,
как сделано в `docs/notes/install.md`).

- [ ] **Step 3: Коммит**

```bash
git add docs/guide/quickstart.md
git commit -m "docs(guide): quickstart — from install to the first task

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 6: `docs/guide/roles-and-flow.md`

**Files:**
- Create: `docs/guide/roles-and-flow.md`
- Source: `README.md:283-353`, `README.md:354-371`, `README.md:372-395`

- [ ] **Step 1: Перенести и сверить**

Заголовки документа: «Путь задачи», «analyst», «implementer», «reviewer»,
«Возвраты и круги», «Статус — не колонка», «Каталог изменения».

Схему пути задачи сверить с `office/workflow.yaml` (статусы, роли, переходы по
исходам) и `docs/contracts/tracker-protocol.md` (маркеры, кто что пишет).
Раздел «Статус — не колонка» сверить с тем, как раннер читает статусы JIRA
(`internal/tracker/jira/jira.go`). «Каталог изменения» — с тем, что роли
кладут в ветку (`office/roles/*/role.md`).

- [ ] **Step 2: Коммит**

```bash
git add docs/guide/roles-and-flow.md
git commit -m "docs(guide): three roles and the path a task walks

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 7: `docs/guide/operations.md`

**Files:**
- Create: `docs/guide/operations.md`
- Source: `README.md:209-282` («По расписанию»), `README.md:489-548`
  («Ручной запуск агента»), `README.md:590-649` (часть про `${OFFICE_HOME}`)

- [ ] **Step 1: Перенести и сверить**

Заголовки документа: «По расписанию» (launchd, systemd), «Ручной запуск
агента», «Хозяйство `${OFFICE_HOME}`», «Когда что-то пошло не так».

Задания launchd и systemd сверить с тем, что раннер не демон
(`cmd/runner/main.go`), и с именами установленных бинарников после #11:
в расписании зовётся `${OFFICE_HOME}/bin/runner` (релиз), а не обёртка.
Раздел про ручной запуск — с флагами `run-agent` (`cmd/run-agent/main.go`).
Дерево `${OFFICE_HOME}` — с тем, что создаёт `runner init` и распаковка
(`office/<версия>/`, `bin/`, `runs/`, `worktrees/`, `repos/`, `ledger.jsonl`).

- [ ] **Step 2: Коммит**

```bash
git add docs/guide/operations.md
git commit -m "docs(guide): running the office — schedule, manual runs, the home directory

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 8: `docs/guide/development.md`

**Files:**
- Create: `docs/guide/development.md`
- Source: `README.md:574-589` («Проверки»), `README.md:549-573`
  («Golden-кейсы»), `README.md:590-649` (карта репозитория)

- [ ] **Step 1: Перенести и сверить**

Заголовки документа: «Проверки», «Golden-кейсы ролей», «Релиз и снапшот»,
«Где что лежит».

Список проверок дополнить тем, что появилось в #11 и здесь:
`sh scripts/install-test.sh`, `sh scripts/check-host-is-target.sh`,
`sh scripts/build-validators.sh && go test -tags release ./office/...`,
`sh scripts/release-snapshot.sh`. Карту репозитория написать в новой
раскладке — с `office/` и `office/validators/`, без `payload/`. Раздел про
`eval-roles` сверить с задачей 2 (корпус — от корня репозитория).

- [ ] **Step 2: Проверить каждую названную команду**

Прогнать все команды из раздела «Проверки»; ни одна не должна падать и ни
одна не должна оставлять за собой файлов (`git status --short` пуст).

- [ ] **Step 3: Коммит**

```bash
git add docs/guide/development.md
git commit -m "docs(guide): checks, golden cases, and the repository map

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 9: `docs/DESIGN.md` принимает «Как это устроено» и «Вопросы»

**Files:**
- Modify: `docs/DESIGN.md`
- Source: `README.md:396-488`, `README.md:153-185`

- [ ] **Step 1: Перенести без дублей**

`README.md:396-488` пересекается с §2 DESIGN (решения принимает код,
`workflow.yaml` как источник переходов, изоляция, учёт). Переносить только то,
чего в DESIGN нет; совпадающее — выбросить, а не повторить. Раздел «Вопросы»
(Q1–Q4) — это записанные решения, а не вопросы: они уходят в §2 отдельными
пунктами с формулировкой «решение и почему».

- [ ] **Step 2: Коммит**

```bash
git add docs/DESIGN.md
git commit -m "docs(design): absorb the README's internals section, minus the duplicates

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

### Task 10: README — витрина

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Написать новый README по раскладке**

| Блок | Строк | Что внутри |
|---|---|---|
| Шапка | 8 | `# virtual-office`, одна строка: «Команда AI-агентов, ведущая разработку проекта-клиента через его трекер. Человек участвует в фиксированных точках, а не в каждом шаге.», честная строка о статусе (этап 5 сдан, механика проверена на живом проекте) |
| Схема | 15 | ASCII-схема пути задачи из `README.md:283-300`, сжатая до одного экрана |
| Установка | 10 | `curl -fsSL …/install.sh \| sh`, `runner init`, `runner version`; строка про PATH |
| Первая задача | 25 | Файловый трекер: `runner mock add`, `runner tick`, `runner ls`; ссылка на `docs/guide/quickstart.md` |
| Возможности | 12 | Таблица: роли → `docs/guide/roles-and-flow.md`, расписание и ручной запуск → `operations.md`, трекеры → `docs/contracts/tracker-protocol.md`, песочница → `docs/contracts/role-sandbox-permissions.md`, бюджеты → `docs/notes/budgets.md` |
| Как устроено | 10 | Абзац: решения принимает код, граф в `workflow.yaml`, офис едет в бинарнике; ссылка на `docs/DESIGN.md` |
| Требования | 10 | `git`, `sbx`, `claude`; Go — только для сборки из исходников; darwin/arm64, linux/amd64, linux/arm64 |
| Ограничения | 12 | Чего система не делает: не демон, не сливает PR без конфига, роли не устанавливаются на машину, полигон ≠ клиентская доска |
| Документация | 15 | Ссылки с одной строкой о каждом разделе: guide, DESIGN, contracts, ONBOARDING, notes |
| Статус и лицензия | 5 | |

- [ ] **Step 2: Проверить каждую команду из README**

Выполнить всё, что в нём написано как команда (кроме платного прогона).

- [ ] **Step 3: Проверить ссылки и объём**

```bash
wc -l README.md   # ожидается ~170, не больше 200
grep -o '\](\([^)]*\))' README.md | sed 's/.*](\(.*\))/\1/' \
  | grep -v '^http' | while read -r p; do [ -e "$p" ] || echo "БИТАЯ: $p"; done
```

- [ ] **Step 4: Коммит**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs: README is a front page, not a manual

649 lines held the pitch, a tutorial, the role reference, the internals,
the manual-run instructions, the check list and the repository map,
because there was nowhere else to put a user guide. There is now
(docs/guide/), so the README answers the two questions a front page owes
a reader — what is this, and how do I try it — in about 170 lines.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Финальная проверка ветки

- [ ] `go build ./... && go vet ./... && gofmt -l .` — пусто
- [ ] `go test -count=1 ./...` — ноль упавших; число тестов = исходное + 2
      (задачи 2 и 3)
- [ ] `sh scripts/build-validators.sh && go test -count=1 -tags release ./office/...`,
      затем `rm -f office/validators/validate-result-*`
- [ ] `sh scripts/install-test.sh` под `sh`, `/bin/dash`, `bash`
- [ ] `sh scripts/release-snapshot.sh` — гейт личности отработал, маркер на
      месте; `rm -rf dist office/validators/validate-result-*`
- [ ] `./bin/runner version` из клона называет `<клон>/office`
- [ ] `git status --short` пуст
- [ ] Ни одной битой ссылки в `README.md` и `docs/**`
- [ ] `ls *.go 2>/dev/null` — пусто: в корне не осталось Go-файлов
