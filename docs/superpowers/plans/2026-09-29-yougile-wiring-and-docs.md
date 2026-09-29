---
change: yougile-wiring-and-docs
design-doc: docs/superpowers/specs/2026-09-29-yougile-wiring-and-docs-design.md
base-ref: 6225f8427be08f163e7f211efe1d32c42f1ce0a6
---

# yougile-wiring-and-docs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a project declare `tracker: yougile` in `projects.local.yaml`. The runner (`tick`/`loop`/`reap`/`ls`/`complete-splits`) and `runner doctor` then open it from `${OFFICE_HOME}/tracker-yougile.yaml`, and the docs explain how to provision it.

**Architecture:** A strict loader `yougile.LoadConfig` reads a new connection file with exactly one project, keyed by the `projects.local.yaml` key, and refuses `ru.yougile.com`. `yougile.Config` gains `Key`: the runner addresses the project by that key, and the API keeps using `ProjectID`. `cmd/runner/office.go` gains `openYouGile`. It follows `openMock`'s shape: one shared account, the same tracker for every role, and `Accounts` = `Whoami()` email plus `also_agents`. `newOffices` dispatches to it only when a project names `yougile`, so the file is listed and opened only then. `doctor` gets a four-finding YouGile stage after the JIRA stage. To make that possible, the JIRA stage is first extracted into a function, so its early returns no longer end the whole report.

**Tech Stack:** Go 1.26.6 (`go.mod`), `gopkg.in/yaml.v3` (already required), standard library (`net/http/httptest`, `encoding/json`). No new dependencies. POSIX `sh` for `scripts/doc-recipe-test.sh`.

**Spec:** Design Doc `docs/superpowers/specs/2026-09-29-yougile-wiring-and-docs-design.md` is the source of HOW; follow it exactly. Delta spec: `docs/openspec/changes/yougile-wiring-and-docs/specs/runner-multi-tracker/spec.md`. Task boundaries: `docs/openspec/changes/yougile-wiring-and-docs/tasks.md`. Executors read the Design Doc and this plan together.

Run every command from the worktree root `/Users/aleksejkolesnikov/IdeaProjects/virtual-office/.worktrees/yougile-wiring-and-docs`.

## How this plan maps to `tasks.md`

| `tasks.md` item | Plan task | Ticked in |
|---|---|---|
| 1.4 `Config.Key` | Task 1 | Task 1 |
| 1.2 schema, strict `LoadConfig`, sample, `runner init` places it | Tasks 2, 3 | Task 3 |
| 6.3 refuse `ru.yougile.com` | Task 2 | Task 2 |
| 1.1 `"yougile"` in `trackers` | Task 4 | Task 4 |
| 1.3 opened only when used | Task 4 | Task 4 |
| 2.1 `openYouGile` | Task 4 | Task 4 |
| 2.2 per-office machinery needs no change | Task 4 (checked by the three-tracker test) | Task 4 |
| 5.1 config/wiring unit tests | Task 4 | Task 4 |
| 6.1 office email in `Accounts` | Task 4 | Task 4 |
| 6.2 `Logf` → runner log | Task 4 | Task 4 |
| 4.3 doctor stage | Task 5 | Task 5 |
| 4.1 reference doc + guides + configuration | Task 6 | Task 6 |
| 6.4 docs: Blocked → column outside the graph, not archive | Task 6 | Task 6 |
| 6.5 docs: `runner ls` shows archived cards | Task 6 | Task 6 |
| 4.2 README + ONBOARDING | Task 7 | Task 7 |
| 3.1, 3.2 account + project provisioned by the owner | Task 8 (manual checkpoint) | Task 8 |

Each task's commit ticks its `tasks.md` boxes (`- [ ]` → `- [x]`) **in the same commit**.

## Global Constraints

- Module `github.com/kao73/virtual-office`, Go `1.26.6`. **No new dependencies**; `go.mod`/`go.sum` stay untouched.
- Code comments, error texts and user-facing output are **Russian**, in the tone of the surrounding code. Identifiers, YAML keys and check-ids are English.
- Connection file name: `tracker-yougile.yaml` (`yougile.TrackerFile`). Sample: `office/tracker-yougile.example.yaml` (`yougile.ExampleFile`).
- File keys, verbatim: `base_url`, `api_key_env`, `also_agents`, `projects.<key>.project_id`, `projects.<key>.columns`, `projects.<key>.create_status`.
- `api_key_env` holds the **name** of the variable, never the key. The key never appears in errors, logs or doctor output.
- `base_url` with host `ru.yougile.com` is **refused** by `LoadConfig`. The error says attachments would not download and names `https://yougile.com`.
- Exactly **one** YouGile project per runner. With two, the error text contains `один проект YouGile на раннер`. This holds whether the second project is in `tracker-yougile.yaml` or in `projects.local.yaml`.
- `tracker-yougile.yaml` is opened, and listed in the `конфигурация:` header, **only** when some project declares `tracker: yougile`.
- Doctor check-ids, verbatim: `config:tracker-yougile.yaml`, `skip:yougile`, `cred:<api_key_env>`, `yougile:open`, `skip:yougile-checks`, `yougile:account`. Doctor makes only GET requests to YouGile.
- Every role shares one YouGile tracker/account. Per-role YouGile accounts are not built.
- Test commands: `go test ./...`, `go vet ./...`; the docs harness `sh scripts/doc-recipe-test.sh`.
- Commit messages: `feat(yougile-wiring-and-docs): …` / `docs(yougile-wiring-and-docs): …`, ending with the attribution line `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

### One deliberate departure from the Design Doc (§4 step 6)

The Design Doc sets `tr.Logf` to print `"yougile: "+f`. Every message the adapter logs already starts with `yougile: ` (`status.go` `collect`, `comment.go` `FindByMarker`; asserted by `status_test.go:293` and `comment_test.go:51`). With the extra prefix every line would read `yougile: yougile: …`. The runner therefore prints `format+"\n"` as is, and Task 4's test pins that the line appears **once** with a single `yougile: ` prefix. Record this in the Build notes and the verify report.

## Review Focus

- **A failed JIRA stage must not hide the YouGile stage in `doctor`.** Today every JIRA failure `return concludeExit(...)`s out of `doctorCommand`. A machine with a broken `tracker.yaml` and a healthy YouGile project should still get all four YouGile findings. Task 5 extracts the JIRA stage and tests this exact mix.
- **A workflow status without a column.** The shipped graph has eight statuses, including human-only `Backlog`/`Done`. Someone who maps only the role statuses would see Open succeed, and later `List` would fail on `ErrUnmappedColumn`. The runner must refuse at start-up and name the missing statuses. Tested in Task 4 (runner) and Task 5 (doctor `config:` finding).
- **Key mismatch between the two files** (`projects.local.yaml` says `SHOP`, `tracker-yougile.yaml` says `shop`). Expect a refusal naming both keys, not an opaque `ErrNoProject` on the first tick. Tested in Task 4.
- **`ru.yougile.com` written with a trailing slash, a path or in upper case** (`https://RU.yougile.com/`). It must still be refused, because the check compares the parsed host case-insensitively. Tested in Task 2.
- **The office's own chat messages read as human replies.** Spec scenario "The office's own account is not a human". Task 4 feeds a comment authored by the `Whoami` email through `tracker.HumanAnswers` with the opened `accounts` and expects no answer.

---

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/tracker/yougile/yougile.go` (modify) | `Config.Key`, defaulting in `Open`, `checkProject` against `Key`; package/field doc refresh | 1 |
| `internal/tracker/yougile/task.go` (modify) | `toTask` reports `Key` as `Task.Project` | 1 |
| `internal/tracker/yougile/yougile_test.go` (modify) | Key tests | 1 |
| `internal/tracker/yougile/config.go` (create) | `TrackerFile`, `ExampleFile`, `FileConfig`, `ProjectConfig`, `LoadConfig`, `ProjectKey`, `Tracker(key)` | 2 |
| `internal/tracker/yougile/config_test.go` (create) | loader table tests, shipped sample loads | 2, 3 |
| `office/tracker-yougile.example.yaml` (create) | commented sample | 3 |
| `office/payload.go`, `office/payload_test.go` (modify) | embed the sample | 3 |
| `cmd/runner/init.go`, `cmd/runner/init_test.go` (modify) | `runner init` places the sample | 3 |
| `scripts/doc-recipe-test.sh` (modify) | harness checks the sample is placed | 3 |
| `internal/tracker/config.go`, `internal/tracker/config_test.go` (modify) | `trackers` gains `yougile` | 4 |
| `office/projects.local.example.yaml` (modify) | `tracker` comment names `yougile` | 4 |
| `cmd/runner/office.go` (modify) | `openYouGile`, `youGileFile`, `youGileColumns`, dispatch | 4 |
| `cmd/runner/office_test.go` (modify) | fake YouGile server, wiring tests | 4 |
| `cmd/runner/doctor.go`, `cmd/runner/doctor_test.go` (modify) | `doctorJira` extraction, `doctorYouGile` stage | 5 |
| `docs/reference/yougile-requirements.md` (create) | what the office requires from YouGile | 6 |
| `docs/guide/project-setup.md`, `docs/guide/machine-setup.md`, `docs/reference/configuration.md`, `docs/guide/operations.md` (modify) | YouGile steps | 6 |
| `docs/openspec/changes/yougile-wiring-and-docs/design.md` (modify) | drop the stale `docs/notes/yougile-setup.md` mentions (Design Doc §8) | 6 |
| `README.md`, `docs/ONBOARDING.md` (modify) | `yougile` next to `jira`/`mock` | 7 |

---

### Task 1: `yougile.Config.Key` — the runner's project name (tasks.md 1.4)

**Files:**
- Modify: `internal/tracker/yougile/yougile.go` (`Config`, `Open`, `checkProject`, package doc, `Logf` doc)
- Modify: `internal/tracker/yougile/task.go:74` (`toTask`)
- Test: `internal/tracker/yougile/yougile_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `yougile.Config.Key string`. Empty means `ProjectID`. It is what `checkProject` accepts and what `Task.Project`/`TaskRef.Project` carry. API paths keep `ProjectID`.

- [x] **Step 1: Write the failing tests** (append to `yougile_test.go`, after `TestCheckProjectRejectsForeignProject`)

```go
// Раннер зовёт проект ключом из projects.local.yaml, а не UUID YouGile:
// Key — то, что принимает checkProject и что несут Task.Project и TaskRef.Project.
// В API по-прежнему уходит ProjectID.
func TestKeyNamesTheProjectForTheRunner(t *testing.T) {
	fake := newFake(t)
	cfg := testConfig(serve(t, fake))
	cfg.Key = "SHOP"
	tr, err := Open(cfg)
	if err != nil {
		t.Fatalf("трекер не открыт: %v", err)
	}
	tr.Now = func() time.Time { return now }

	if err := tr.checkProject("SHOP"); err != nil {
		t.Errorf("свой ключ отвергнут: %v", err)
	}
	if err := tr.checkProject(testProject); !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("UUID проекта вместо ключа дал %v, ожидался ErrNoProject", err)
	}
	refs, err := tr.ListReady("SHOP", "Ready")
	if err != nil || len(refs) != 1 || refs[0].Project != "SHOP" {
		t.Errorf("ListReady(SHOP) = %+v, %v; ожидался один ref проекта SHOP", refs, err)
	}
	task, err := tr.Get(testKey)
	if err != nil || task.Project != "SHOP" {
		t.Errorf("Get: Project=%q, %v; ожидался SHOP", task.Project, err)
	}
	if fake.count("GET /api-v2/projects/"+testProject) != 1 {
		t.Errorf("в API ушёл не ProjectID: %v", fake.requests)
	}
}

// Пустой Key — это ProjectID: прежние вызовы и live_test.go не меняются.
func TestKeyDefaultsToProjectID(t *testing.T) {
	tr, _ := fixture(t)
	if err := tr.checkProject(testProject); err != nil {
		t.Errorf("ProjectID без Key отвергнут: %v", err)
	}
	task, err := tr.Get(testKey)
	if err != nil || task.Project != testProject {
		t.Errorf("Get: Project=%q, %v; ожидался %q", task.Project, err, testProject)
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tracker/yougile/ -run 'TestKey' -v`
Expected: compile FAIL, `cfg.Key undefined (type Config has no field or method Key)`.

- [x] **Step 3: Implement**

In `yougile.go`, add the field to `Config`, right after `ProjectID`:

```go
	// Key — имя проекта у раннера: ключ из projects.local.yaml (SHOP), а не
	// UUID YouGile. Его принимает checkProject и его несут Task.Project и
	// TaskRef.Project — рабочие папки, ветки и реестр видят тот же ключ, что
	// у jira и mock. В API уходит ProjectID. Пусто — ProjectID.
	Key string
```

In `Open`, right after the `cfg.ProjectID == ""` check:

```go
	if cfg.Key == "" {
		cfg.Key = cfg.ProjectID
	}
```

Replace `checkProject`:

```go
// checkProject — Tracker обслуживает ровно один проект YouGile, и раннер
// зовёт его ключом Key.
func (t *Tracker) checkProject(project string) error {
	if project != t.cfg.Key {
		return fmt.Errorf("%w: %q (этот трекер обслуживает проект %q, в YouGile — %q)",
			tracker.ErrNoProject, project, t.cfg.Key, t.cfg.ProjectID)
	}
	return nil
}
```

In `task.go` `toTask`, change `Project: t.cfg.ProjectID` to `Project: t.cfg.Key`.

Doc refresh in `yougile.go`:
- The `Config` comment: replace "Загрузчика из файла пока нет — его добавит yougile-wiring-and-docs; ключ API приходит из окружения на стороне вызывающего и сюда попадает уже значением." with "Из файла её собирает FileConfig.Tracker (config.go); ключ API приходит из окружения и сюда попадает уже значением."
- The `Logf` comment: replace "yougile-wiring-and-docs направит его в лог раннера" with "раннер направляет его в свой вывод (cmd/runner/office.go, openYouGile)".

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tracker/yougile/ -v -run 'TestKey|TestCheckProject'` then `go test ./internal/tracker/yougile/`
Expected: PASS (the whole package; `live_test.go` skips without credentials).

- [x] **Step 5: Tick and commit**

In `tasks.md`, tick 1.4.

```bash
git add internal/tracker/yougile/yougile.go internal/tracker/yougile/task.go internal/tracker/yougile/yougile_test.go docs/openspec/changes/yougile-wiring-and-docs/tasks.md
git commit -m "feat(yougile-wiring-and-docs): Config.Key names the project for the runner

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Strict `tracker-yougile.yaml` loader (tasks.md 1.2 part, 6.3)

**Files:**
- Create: `internal/tracker/yougile/config.go`
- Test: `internal/tracker/yougile/config_test.go`

**Interfaces:**
- Consumes: `yougile.Config` with `Key` (Task 1).
- Produces:
  - `const TrackerFile = "tracker-yougile.yaml"`, `const ExampleFile = "tracker-yougile.example.yaml"`
  - `type FileConfig struct { BaseURL string; APIKeyEnv string; AlsoAgents []string; Projects map[string]ProjectConfig }` (yaml keys `base_url`, `api_key_env`, `also_agents`, `projects`)
  - `type ProjectConfig struct { ProjectID string; Columns map[string]string; CreateStatus string }` (yaml `project_id`, `columns`, `create_status`)
  - `func LoadConfig(path string) (FileConfig, error)`
  - `func (FileConfig) ProjectKey() string`: the key of the single project (valid after `LoadConfig`).
  - `func (FileConfig) Tracker(key string) (Config, error)`

- [x] **Step 1: Write the failing tests** (`config_test.go`)

```go
package yougile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validFile — tracker-yougile.yaml, который загрузчик обязан принять.
const validFile = `base_url: https://yougile.com
api_key_env: YOUGILE_API_KEY
also_agents: [bot@example.com]
projects:
  SHOP:
    project_id: proj-1
    columns:
      Ready: col-ready
      InProgress: col-work
    create_status: Ready
`

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), TrackerFile)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("%s не записан: %v", path, err)
	}
	return path
}

func TestLoadConfigReadsValidFile(t *testing.T) {
	fc, err := LoadConfig(writeFile(t, validFile))
	if err != nil {
		t.Fatalf("годный файл отвергнут: %v", err)
	}
	if fc.BaseURL != "https://yougile.com" || fc.APIKeyEnv != "YOUGILE_API_KEY" ||
		len(fc.AlsoAgents) != 1 || fc.AlsoAgents[0] != "bot@example.com" {
		t.Errorf("файл доехал не целиком: %+v", fc)
	}
	if fc.ProjectKey() != "SHOP" {
		t.Errorf("ProjectKey = %q, ожидался SHOP", fc.ProjectKey())
	}
	p := fc.Projects["SHOP"]
	if p.ProjectID != "proj-1" || p.Columns["InProgress"] != "col-work" || p.CreateStatus != "Ready" {
		t.Errorf("проект доехал не целиком: %+v", p)
	}
}

// Нет файла — отказ называет образец и `runner init`: сам файл не пишут,
// его копируют из образца.
func TestLoadConfigMissingFilePointsAtSample(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), TrackerFile))
	if err == nil {
		t.Fatal("отсутствующий файл принят")
	}
	for _, want := range []string{TrackerFile, ExampleFile, "runner init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("отказ не назвал %q: %v", want, err)
		}
	}
}

func TestLoadConfigRejectsUnknownField(t *testing.T) {
	_, err := LoadConfig(writeFile(t, validFile+"sticker_id: s-1\n"))
	if err == nil || !strings.Contains(err.Error(), "sticker_id") {
		t.Errorf("неизвестное поле дало %v", err)
	}
}

// Пустой документ — не «не разобран: EOF», а перечень недостающего одним отказом.
func TestLoadConfigEmptyFileJoinsAllErrors(t *testing.T) {
	_, err := LoadConfig(writeFile(t, ""))
	if err == nil {
		t.Fatal("пустой файл принят")
	}
	for _, want := range []string{"нарушает контракт", "base_url", "api_key_env", "projects"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в отказе нет %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "EOF") {
		t.Errorf("пустой файл отвергнут как неразобранный: %v", err)
	}
}

func TestLoadConfigRejectsBrokenFile(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"нет base_url", strings.Replace(validFile, "base_url: https://yougile.com\n", "", 1), "base_url"},
		{"ru.", strings.Replace(validFile, "https://yougile.com", "https://ru.yougile.com", 1), "https://yougile.com"},
		{"ru. со слешем и в верхнем регистре", strings.Replace(validFile, "https://yougile.com", "https://RU.yougile.com/", 1), "вложения"},
		{"нет api_key_env", strings.Replace(validFile, "api_key_env: YOUGILE_API_KEY\n", "", 1), "api_key_env"},
		{"нет проектов", strings.Split(validFile, "projects:")[0], "projects"},
		{"два проекта", validFile + "  BLOG:\n    project_id: proj-2\n    columns: {Ready: c}\n", "один проект YouGile на раннер"},
		{"нет project_id", strings.Replace(validFile, "    project_id: proj-1\n", "", 1), "projects.SHOP.project_id"},
		{"пустые колонки", strings.Replace(validFile,
			"    columns:\n      Ready: col-ready\n      InProgress: col-work\n", "    columns: {}\n", 1), "projects.SHOP.columns"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadConfig(writeFile(t, tc.body))
			if err == nil {
				t.Fatal("битый файл принят")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("в отказе нет %q: %v", tc.want, err)
			}
		})
	}
}

// Отказ ru. говорит, почему: вложения не скачаются.
func TestLoadConfigRuHostExplainsAttachments(t *testing.T) {
	_, err := LoadConfig(writeFile(t, strings.Replace(validFile, "https://yougile.com", "https://ru.yougile.com", 1)))
	if err == nil || !strings.Contains(err.Error(), "вложения не скачаются") {
		t.Errorf("отказ ru. не объяснил причину: %v", err)
	}
}

func TestFileConfigTrackerBuildsAdapterConfig(t *testing.T) {
	fc, err := LoadConfig(writeFile(t, validFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOUGILE_API_KEY", "секрет")
	cfg, err := fc.Tracker("SHOP")
	if err != nil {
		t.Fatalf("Tracker: %v", err)
	}
	if cfg.Key != "SHOP" || cfg.ProjectID != "proj-1" || cfg.APIKey != "секрет" ||
		cfg.BaseURL != "https://yougile.com" || cfg.CreateStatus != "Ready" || cfg.ColumnIDs["Ready"] != "col-ready" {
		t.Errorf("Config собран не так: %+v", cfg)
	}
	// Карта колонок — копия: правка Config не должна тронуть FileConfig.
	cfg.ColumnIDs["Ready"] = "испорчено"
	if fc.Projects["SHOP"].Columns["Ready"] != "col-ready" {
		t.Error("ColumnIDs делит карту с FileConfig")
	}
}

// Пустая переменная — отказ с её именем, но без значения.
func TestFileConfigTrackerRejectsEmptyKey(t *testing.T) {
	fc, err := LoadConfig(writeFile(t, validFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOUGILE_API_KEY", "")
	if _, err := fc.Tracker("SHOP"); err == nil || !strings.Contains(err.Error(), "YOUGILE_API_KEY") {
		t.Errorf("пустой ключ дал %v", err)
	}
}

func TestFileConfigTrackerRejectsUnknownKey(t *testing.T) {
	fc, err := LoadConfig(writeFile(t, validFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOUGILE_API_KEY", "секрет")
	if _, err := fc.Tracker("BLOG"); err == nil || !strings.Contains(err.Error(), "BLOG") {
		t.Errorf("чужой ключ дал %v", err)
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tracker/yougile/ -run 'TestLoadConfig|TestFileConfig' -v`
Expected: compile FAIL, `undefined: LoadConfig`, `undefined: TrackerFile`.

- [x] **Step 3: Implement `config.go`**

```go
package yougile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// TrackerFile — подключение к YouGile; живёт в ${OFFICE_HOME}, а не
// в репозитории, и открывается, только если проект назвал tracker: yougile.
// Свой файл, а не раздел tracker.yaml: тот — плоская схема JIRA, и делить
// его значило бы сломать каждую настроенную JIRA-машину.
const TrackerFile = "tracker-yougile.yaml"

// ExampleFile — образец TrackerFile с объяснениями. Лежит в репозитории
// и в поставке; `runner init` кладёт его в ${OFFICE_HOME}. Раннер его
// не читает никогда.
const ExampleFile = "tracker-yougile.example.yaml"

// refusedHost — хост, с которым вложения не скачиваются: /user-data/…
// отвечает 302 на prod-user-data.yougile.com, а файловый клиент идёт только
// на хост BaseURL и его поддомены (newFileClient).
const refusedHost = "ru.yougile.com"

// FileConfig — TrackerFile как он лежит на диске.
type FileConfig struct {
	BaseURL string `yaml:"base_url"`
	// APIKeyEnv — имя переменной окружения с ключом API; сам ключ в файл
	// не попадает никогда, как secret_env у tracker.yaml.
	APIKeyEnv string `yaml:"api_key_env"`
	// AlsoAgents — email'ы чужой автоматизации: их комментарии тоже не слова
	// человека. Учётку самого офиса сюда не пишут — раннер узнаёт её у
	// /users/me.
	AlsoAgents []string `yaml:"also_agents"`
	// Projects — по ключу из projects.local.yaml. Карта, а не одна запись,
	// чтобы второй проект однажды не менял форму файла; сегодня он ровно один.
	Projects map[string]ProjectConfig `yaml:"projects"`
}

// ProjectConfig — один проект YouGile: его id и колонка на каждый статус графа.
type ProjectConfig struct {
	ProjectID    string            `yaml:"project_id"`
	Columns      map[string]string `yaml:"columns"`
	CreateStatus string            `yaml:"create_status"`
}

// LoadConfig читает TrackerFile. Разбор строгий, как у tracker.yaml:
// неизвестное поле — ошибка, а не молча забытая настройка. Все нарушения
// идут одним отказом: починив одно, узнавать о втором следующим запуском —
// лишний круг.
func LoadConfig(path string) (FileConfig, error) {
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return FileConfig{}, fmt.Errorf("%s не заведён: подключение к YouGile — свойство инстанса, "+
			"а не офиса. Сделайте файл из образца %s: его кладёт рядом `runner init` "+
			"(в клоне он лежит в office/%s)", path, ExampleFile, ExampleFile)
	case err != nil:
		return FileConfig{}, fmt.Errorf("%s не прочитан: %w", path, err)
	}

	var fc FileConfig
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	// Пустой документ — io.EOF; о нём скажет проверка ниже, назвав недостающее.
	if err := dec.Decode(&fc); err != nil && !errors.Is(err, io.EOF) {
		return FileConfig{}, fmt.Errorf("%s не разобран: %w", path, err)
	}
	if err := errors.Join(fc.validate()...); err != nil {
		return FileConfig{}, fmt.Errorf("%s нарушает контракт: %w", path, err)
	}
	return fc, nil
}

func (fc FileConfig) validate() []error {
	var errs []error
	if fc.BaseURL == "" {
		errs = append(errs, errors.New("base_url не задан: без адреса YouGile идти некуда (пример: https://yougile.com)"))
	} else if u, err := url.Parse(fc.BaseURL); err == nil && strings.EqualFold(u.Hostname(), refusedHost) {
		errs = append(errs, fmt.Errorf("base_url=%q: с %s вложения не скачаются — /user-data/ перенаправляет "+
			"на prod-user-data.yougile.com, а это не поддомен %s; укажите https://yougile.com",
			fc.BaseURL, refusedHost, refusedHost))
	}
	if fc.APIKeyEnv == "" {
		errs = append(errs, errors.New("api_key_env не задан: в файле — имя переменной окружения с ключом API, не сам ключ"))
	}
	switch len(fc.Projects) {
	case 0:
		errs = append(errs, errors.New("projects пуст: опишите проект YouGile под его ключом из projects.local.yaml"))
	case 1:
		for key, p := range fc.Projects {
			if p.ProjectID == "" {
				errs = append(errs, fmt.Errorf("projects.%s.project_id не задан", key))
			}
			if len(p.Columns) == 0 {
				errs = append(errs, fmt.Errorf("projects.%s.columns пуст: статус графа — это колонка, "+
					"раннеру нужна карта статус → id колонки", key))
			}
		}
	default:
		errs = append(errs, fmt.Errorf("projects описывает %d проекта (%s): поддерживается один проект YouGile на раннер",
			len(fc.Projects), strings.Join(slices.Sorted(maps.Keys(fc.Projects)), ", ")))
	}
	return errs
}

// ProjectKey — ключ единственного проекта. Осмыслен после LoadConfig: тот
// не пропускает файл, где проектов не ровно один.
func (fc FileConfig) ProjectKey() string {
	for key := range fc.Projects {
		return key
	}
	return ""
}

// Tracker собирает Config адаптера для проекта key; ключ API берётся из
// переменной api_key_env. Пустая переменная — отказ с её именем: Open сказал
// бы только «ключ API не задан», не сказав, где его ждали.
func (fc FileConfig) Tracker(key string) (Config, error) {
	p, ok := fc.Projects[key]
	if !ok {
		return Config{}, fmt.Errorf("%s не описывает проект %q", TrackerFile, key)
	}
	apiKey := os.Getenv(fc.APIKeyEnv)
	if apiKey == "" {
		return Config{}, fmt.Errorf("переменная %s пуста: в ней ждут ключ API YouGile (api_key_env в %s)",
			fc.APIKeyEnv, TrackerFile)
	}
	return Config{
		Key:          key,
		BaseURL:      fc.BaseURL,
		APIKey:       apiKey,
		ProjectID:    p.ProjectID,
		ColumnIDs:    maps.Clone(p.Columns),
		CreateStatus: p.CreateStatus,
	}, nil
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tracker/yougile/ -run 'TestLoadConfig|TestFileConfig' -v` then `go test ./internal/tracker/yougile/ && go vet ./internal/tracker/yougile/`
Expected: PASS.

- [x] **Step 5: Tick and commit**

In `tasks.md`, tick 6.3. Leave 1.2 open; Task 3 finishes it.

```bash
git add internal/tracker/yougile/config.go internal/tracker/yougile/config_test.go docs/openspec/changes/yougile-wiring-and-docs/tasks.md
git commit -m "feat(yougile-wiring-and-docs): strict tracker-yougile.yaml loader, ru. refused

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Shipped sample, embedded and placed by `runner init` (tasks.md 1.2)

**Files:**
- Create: `office/tracker-yougile.example.yaml`
- Modify: `office/payload.go` (embed directive, package doc "оба образца" → "образцы"), `office/payload_test.go:16` (`payloadFiles`)
- Modify: `cmd/runner/init.go` (`samples`, `initCommand` doc "два образца" → "три образца")
- Modify: `cmd/runner/init_test.go` (`TestInitLaysOutFreshHome`, `TestInitLeavesConfiguredHomeAlone`)
- Modify: `cmd/runner/office_test.go` (`TestOfficesUnpackPayloadWithoutConfigRoot` file list)
- Modify: `scripts/doc-recipe-test.sh` (step 1)
- Test: `internal/tracker/yougile/config_test.go` (shipped sample loads)

**Interfaces:**
- Consumes: `yougile.LoadConfig`, `yougile.ExampleFile`, `yougile.TrackerFile` (Task 2).
- Produces: `payload.Payload` contains `tracker-yougile.example.yaml`; `runner init` writes `${OFFICE_HOME}/tracker-yougile.example.yaml` and prints a `cp … tracker-yougile.yaml` hint.

- [x] **Step 1: Write the failing tests**

Append to `internal/tracker/yougile/config_test.go`:

```go
// Поставляемый образец обязан загружаться как есть: иначе первый же
// человек, скопировавший его, получит отказ не про свои значения, а про
// опечатку в образце. Покрытие графа тут не проверить — его сверяет раннер.
func TestShippedSampleLoads(t *testing.T) {
	fc, err := LoadConfig(filepath.Join("..", "..", "..", "office", ExampleFile))
	if err != nil {
		t.Fatalf("образец %s не загружается: %v", ExampleFile, err)
	}
	for _, status := range []string{"Backlog", "Analysis", "Ready", "InProgress", "Review", "Approved", "Done", "Blocked"} {
		if fc.Projects[fc.ProjectKey()].Columns[status] == "" {
			t.Errorf("в образце нет колонки статуса %s", status)
		}
	}
	if fc.BaseURL != "https://yougile.com" {
		t.Errorf("образец советует base_url %q, а годится только https://yougile.com", fc.BaseURL)
	}
}
```

In `office/payload_test.go` line 16, add `"tracker-yougile.example.yaml"` to `payloadFiles`.

In `cmd/runner/init_test.go`:
- import `"github.com/kao73/virtual-office/internal/tracker/yougile"`;
- in `TestInitLaysOutFreshHome`: `wantFiles := []string{tracker.ProjectsLocalExampleFile, jira.ExampleFile, yougile.ExampleFile}`; `strings.Count(printed, "создан")` expects `6`, and the message becomes `"ожидалось шесть (три образца и три задания)"`; the name loop also checks `yougile.TrackerFile`; the doc comment above the test says "с тремя образцами конфигурации".
- in `TestInitLeavesConfiguredHomeAlone`: replace the trailing "единственный отсутствовавший образец" block with a loop over `jira.ExampleFile, yougile.ExampleFile` that asserts each now exists.

In `cmd/runner/office_test.go` `TestOfficesUnpackPayloadWithoutConfigRoot`, add `"tracker-yougile.example.yaml"` next to `"tracker.example.yaml"`.

- [x] **Step 2: Run tests to verify they fail**

Run two commands:
`go test ./internal/tracker/yougile/ -run TestShippedSampleLoads -v` → FAIL `no such file or directory`.
`go test ./office/ ./cmd/runner/ -run 'TestPayload|TestInit|TestOfficesUnpack' -v` → FAIL (`tracker-yougile.example.yaml не в Payload`, `в хозяйстве … ожидались ровно …`).

- [x] **Step 3: Implement**

`office/tracker-yougile.example.yaml`:

```yaml
# Подключение офиса к YouGile: ${OFFICE_HOME}/tracker-yougile.yaml.
#
# Нужен только проектам с tracker: yougile в projects.local.yaml; без них раннер
# этот файл не открывает и не упоминает. Образец копируется целиком:
#     cp "${OFFICE_HOME:-$HOME/.office}/tracker-yougile.example.yaml" \
#        "${OFFICE_HOME:-$HOME/.office}/tracker-yougile.yaml"
# После копии правятся ключ проекта, project_id и восемь id колонок. Как их
# узнать и что офис требует от YouGile — docs/reference/yougile-requirements.md.
#
# Секретов здесь нет и не будет: ключ API живёт в окружении, в файле — только
# имя переменной.

# Корень хоста, всегда https://yougile.com. ru.yougile.com раннер отвергает:
# с ним вложения не скачаются (файлы отдаёт prod-user-data.yougile.com, а это
# не поддомен ru.yougile.com). /api-v2 не дописывайте — адаптер добавит сам.
base_url: https://yougile.com

# Имя переменной окружения с ключом API учётки офиса — отдельной, не
# человеческой: комментарий от учётки офиса человеком не считается, и ваш
# собственный ответ под той же учёткой офис бы не услышал.
api_key_env: YOUGILE_API_KEY

# Email'ы чужой автоматизации: её сообщения в чате задачи тоже не слова человека.
# Учётку самого офиса сюда не пишут — раннер узнаёт её у YouGile сам.
also_agents: []

# Проект — ровно один на раннер, под тем же ключом, что в projects.local.yaml.
projects:
  PROJ:
    # id проекта YouGile (GET /api-v2/projects).
    project_id: 00000000-0000-0000-0000-000000000000
    # Статус графа (office/workflow.yaml) → id колонки на доске проекта. Статус
    # задачи в YouGile — её колонка; колонка нужна каждому статусу графа, даже
    # тем, куда задачи кладёт только человек (Backlog, Done). Колонки заводит
    # человек, раннер их только сверяет.
    columns:
      Backlog: 00000000-0000-0000-0000-000000000001
      Analysis: 00000000-0000-0000-0000-000000000002
      Ready: 00000000-0000-0000-0000-000000000003
      InProgress: 00000000-0000-0000-0000-000000000004
      Review: 00000000-0000-0000-0000-000000000005
      Approved: 00000000-0000-0000-0000-000000000006
      Done: 00000000-0000-0000-0000-000000000007
      Blocked: 00000000-0000-0000-0000-000000000008
    # Куда ложатся задачи, которые офис заводит сам (дети разбиения).
    create_status: Backlog
```

`office/payload.go`: second embed line becomes

```go
//go:embed workflow.yaml budgets.yaml tracker.example.yaml tracker-yougile.example.yaml projects.local.example.yaml
```

and the package doc "оба образца" → "образцы конфигурации".

`cmd/runner/init.go`: import `"github.com/kao73/virtual-office/internal/tracker/yougile"`, and append to `samples`:

```go
	{yougile.ExampleFile, yougile.TrackerFile, "ключ проекта, project_id и id колонок; нужен только проектам с tracker: yougile"},
```

Update the `initCommand` doc: "два образца конфигурации" → "три образца конфигурации", and the list of working files → "projects.local.yaml, tracker.yaml, tracker-yougile.yaml, budgets.yaml".

`scripts/doc-recipe-test.sh` step 1: comment "пять файлов" → "шесть файлов"; add `tracker-yougile.example.yaml \` after `tracker.example.yaml \` in the loop; after the existing `grep -q 'tracker.yaml'` line add:

```sh
grep -q 'tracker-yougile.yaml' "$work/out-init" || fail "init не сказал, куда копировать образец YouGile"
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tracker/yougile/ ./office/ ./cmd/runner/` then `sh scripts/doc-recipe-test.sh`
Expected: PASS; the harness prints `ok: runner init` and runs to the end.

- [x] **Step 5: Tick and commit**

In `tasks.md`, tick 1.2.

```bash
git add office/tracker-yougile.example.yaml office/payload.go office/payload_test.go cmd/runner/init.go cmd/runner/init_test.go cmd/runner/office_test.go scripts/doc-recipe-test.sh internal/tracker/yougile/config_test.go docs/openspec/changes/yougile-wiring-and-docs/tasks.md
git commit -m "feat(yougile-wiring-and-docs): ship tracker-yougile sample, runner init places it

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Wire YouGile into the runner (tasks.md 1.1, 1.3, 2.1, 2.2, 5.1, 6.1, 6.2)

**Files:**
- Modify: `internal/tracker/config.go:485` (`trackers`)
- Modify: `internal/tracker/config_test.go` (accepts `yougile`)
- Modify: `office/projects.local.example.yaml:18` (comment only)
- Modify: `cmd/runner/office.go` (`newOffices` switch, doc comment; new `youGileFile`, `youGileColumns`, `openYouGile`)
- Test: `cmd/runner/office_test.go`

**Interfaces:**
- Consumes: `yougile.LoadConfig`, `FileConfig.ProjectKey`, `FileConfig.Tracker`, `FileConfig.AlsoAgents`, `yougile.TrackerFile` (Task 2); `yougile.Open`, `(*yougile.Tracker).Whoami`, `.Logf` (existing); `tracker.Projects.For/Keys`, `tracker.Workflow.Statuses/Order`, `tracker.HumanAnswers`.
- Produces (used by Task 5):
  - `func youGileFile(path string, declared tracker.Projects) (yougile.FileConfig, string, error)`: load plus the project-key match. `declared` is `projects.For("yougile")`.
  - `func youGileColumns(fc yougile.FileConfig, key string, workflow tracker.Workflow) error`: every graph status has a column.
  - `func openYouGile(path string, workflow tracker.Workflow, declared tracker.Projects, out io.Writer) (opened, error)`
  - Test helpers in `office_test.go`: `youGileOpts`, `youGileServer(t, opts) (url string, requests func() []string)`, `trackerYouGileYAML(baseURL, key string, columns map[string]string) string`, `youGileColumnIDs`, `youGileProject`, `youGileFixture(t, projectsLocal string, opts youGileOpts) (home string, requests func() []string)`.

- [ ] **Step 1: Write the failing test for the tracker list**

Append to `internal/tracker/config_test.go`:

```go
// yougile — третий трекер: проект с ним принимается, и список для подсказок
// его называет.
func TestLoadProjectsAcceptsYouGile(t *testing.T) {
	projects, err := load(t, strings.Replace(validMachine, "tracker: mock", "tracker: yougile", 1))
	if err != nil {
		t.Fatalf("проект с tracker: yougile отвергнут: %v", err)
	}
	if got := projects.For("yougile").Keys(); !slices.Equal(got, []string{"OFF"}) {
		t.Errorf("For(yougile) = %v, ожидался [OFF]", got)
	}
	if !slices.Contains(Trackers(), "yougile") {
		t.Errorf("Trackers() = %v, yougile не назван", Trackers())
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tracker/ -run TestLoadProjectsAcceptsYouGile -v`
Expected: FAIL `tracker="yougile", ожидается один из [mock jira]`.

- [ ] **Step 3: Write the failing runner tests** (append to `cmd/runner/office_test.go`; add imports `encoding/json`, `maps`, `slices`, `sync`, `github.com/kao73/virtual-office/internal/tracker/yougile`)

```go
const youGileProject = "SHOP:\n  repo_url: https://example.test/s.git\n  tracker: yougile\n  default_branch: master\n"

// youGileColumnIDs — колонка на каждый статус поставляемого графа.
var youGileColumnIDs = map[string]string{
	"Backlog": "col-backlog", "Analysis": "col-analysis", "Ready": "col-ready", "InProgress": "col-work",
	"Review": "col-review", "Approved": "col-approved", "Done": "col-done", "Blocked": "col-blocked",
}

// youGileOpts — чем тестовый YouGile отличается от исправного.
type youGileOpts struct {
	noProject bool // GET /projects/{id} отвечает 404
	meStatus  int  // не 0 — /users/me отвечает этим кодом
}

// youGileServer — минимальный YouGile для сборки офиса и доктора: проект
// proj-1, одна доска с колонкой на каждый статус графа и /users/me. Любой
// другой запрос — ошибка теста: ни сборка, ни доктор ничего сверх этого
// звать не должны. Отдаёт адрес и журнал запросов «МЕТОД путь».
func youGileServer(t *testing.T, opts youGileOpts) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var requests []string
	page := func(items []map[string]any) map[string]any {
		return map[string]any{"paging": map[string]any{"next": false}, "content": items}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		var body any
		switch r.URL.Path {
		case "/api-v2/users/me":
			if opts.meStatus != 0 {
				http.Error(w, `{"message":"нет"}`, opts.meStatus)
				return
			}
			body = map[string]any{"id": "u-office", "email": "office@example.com"}
		case "/api-v2/projects/proj-1":
			if opts.noProject {
				http.NotFound(w, r)
				return
			}
			body = map[string]any{"id": "proj-1", "title": "Shop"}
		case "/api-v2/boards":
			body = page([]map[string]any{{"id": "board-1", "projectId": "proj-1"}})
		case "/api-v2/columns":
			var cols []map[string]any
			for _, id := range slices.Sorted(maps.Values(youGileColumnIDs)) {
				cols = append(cols, map[string]any{"id": id, "boardId": "board-1"})
			}
			body = page(cols)
		default:
			t.Errorf("YouGile: неожиданный запрос %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	return server.URL, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(requests)
	}
}

// trackerYouGileYAML — tracker-yougile.yaml, смотрящий на тестовый сервер.
func trackerYouGileYAML(baseURL, key string, columns map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "base_url: %s\napi_key_env: YOUGILE_API_KEY\nalso_agents: [bot@example.com]\n", baseURL)
	fmt.Fprintf(&b, "projects:\n  %s:\n    project_id: proj-1\n    create_status: Backlog\n    columns:\n", key)
	for _, status := range slices.Sorted(maps.Keys(columns)) {
		fmt.Fprintf(&b, "      %s: %s\n", status, columns[status])
	}
	return b.String()
}

// youGileFixture — хозяйство с данными проектами и tracker-yougile.yaml на
// тестовый YouGile под ключом SHOP; ключ API — в окружении.
func youGileFixture(t *testing.T, projectsLocal string, opts youGileOpts) (string, func() []string) {
	t.Helper()
	url, requests := youGileServer(t, opts)
	_, home := fixtureRunner(t, projectsLocal)
	writeYouGileFile(t, home, trackerYouGileYAML(url, "SHOP", youGileColumnIDs))
	t.Setenv("YOUGILE_API_KEY", "секрет")
	return home, requests
}

func writeYouGileFile(t *testing.T, home, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, yougile.TrackerFile), []byte(body), 0o644); err != nil {
		t.Fatalf("%s не записан: %v", yougile.TrackerFile, err)
	}
}

// Машина на mock и jira не знает о YouGile: файла нет, строки о нём нет.
func TestOfficesWithoutYouGileNeverTouchItsFile(t *testing.T) {
	jiraFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/myself") {
			fmt.Fprint(w, `{"name":"office"}`)
			return
		}
		http.NotFound(w, r)
	}, "секрет")
	var out bytes.Buffer
	if _, err := newOffices(flags("tick"), nil, &out); err != nil {
		t.Fatalf("офисы не собраны: %v", err)
	}
	if strings.Contains(out.String(), yougile.TrackerFile) {
		t.Errorf("%s упомянут там, где не открывался:\n%s", yougile.TrackerFile, out.String())
	}
}

// Проект yougile без файла — отказ с именем файла, и файл назван в раскладке.
func TestOfficesYouGileProjectWithoutFileIsRefused(t *testing.T) {
	fixtureRunner(t, mockProject+youGileProject)
	var out bytes.Buffer
	all, err := newOffices(flags("tick"), nil, &out)
	if err == nil || !strings.Contains(err.Error(), yougile.TrackerFile) {
		t.Fatalf("отказ не назвал %s: %v", yougile.TrackerFile, err)
	}
	if all != nil {
		t.Error("при отказе yougile собран офис mock")
	}
	if !strings.Contains(out.String(), yougile.TrackerFile) {
		t.Errorf("%s не назван в раскладке:\n%s", yougile.TrackerFile, out.String())
	}
}

// Все три трекера разом, без флага: три офиса по алфавиту, каждый со своими
// проектами, хозяйство общее. Прочей машинерии офиса (доска, reap, loop)
// правки не нужны — она видит yougile тем же namedOffice (tasks.md 2.2).
func TestOfficesServeAllThreeTrackers(t *testing.T) {
	jiraFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/myself") {
			fmt.Fprint(w, `{"name":"office"}`)
			return
		}
		http.NotFound(w, r)
	}, "секрет")
	url, _ := youGileServer(t, youGileOpts{})
	home := os.Getenv(runner.HomeEnv)
	if err := os.WriteFile(filepath.Join(home, tracker.ProjectsLocalFile), []byte(mockProject+jiraProject+youGileProject), 0o644); err != nil {
		t.Fatal(err)
	}
	writeYouGileFile(t, home, trackerYouGileYAML(url, "SHOP", youGileColumnIDs))
	t.Setenv("YOUGILE_API_KEY", "секрет")

	var out bytes.Buffer
	all, err := newOffices(flags("tick"), nil, &out)
	if err != nil {
		t.Fatalf("офисы не собраны: %v", err)
	}
	var names []string
	for _, o := range all.list {
		names = append(names, o.name)
	}
	if !slices.Equal(names, []string{"jira", "mock", "yougile"}) {
		t.Fatalf("офисы %v, ожидались jira, mock, yougile", names)
	}
	if got := all.list[2].Projects.Keys(); !slices.Equal(got, []string{"SHOP"}) {
		t.Errorf("офис yougile видит %v, ожидался только SHOP", got)
	}
	if all.list[2].Workspaces != all.list[0].Workspaces {
		t.Error("хозяйство машины не общее для офисов")
	}
	if !strings.Contains(out.String(), yougile.TrackerFile) {
		t.Errorf("%s не назван в раскладке:\n%s", yougile.TrackerFile, out.String())
	}
}

// openYouGile: учётка офиса и also_agents — агентские, лог адаптера — в вывод
// раннера, каждая роль — тот же трекер.
func TestOpenYouGileAccountsLogAndRoles(t *testing.T) {
	home, requests := youGileFixture(t, youGileProject, youGileOpts{})
	workflow, err := tracker.LoadWorkflow(filepath.Join("..", "..", "office", tracker.WorkflowFile))
	if err != nil {
		t.Fatal(err)
	}
	projects, err := tracker.LoadProjects(filepath.Join(home, tracker.ProjectsLocalFile))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	o, err := openYouGile(filepath.Join(home, yougile.TrackerFile), workflow, projects.For("yougile"), &out)
	if err != nil {
		t.Fatalf("openYouGile: %v", err)
	}
	if !slices.Equal(o.accounts, []string{"office@example.com", "bot@example.com"}) {
		t.Errorf("accounts = %v, ожидались office@example.com и bot@example.com", o.accounts)
	}
	for _, role := range workflow.Order() {
		if o.byRole[role] != o.tasks {
			t.Errorf("роль %s получила не общий трекер", role)
		}
	}
	// Лог адаптера — в вывод раннера, с одним префиксом: свой «yougile: »
	// у сообщений адаптера уже есть.
	o.tasks.(*yougile.Tracker).Logf("yougile: задача %s пропущена: %v", "t-1", "битые данные")
	if !strings.Contains(out.String(), "yougile: задача t-1 пропущена: битые данные\n") ||
		strings.Contains(out.String(), "yougile: yougile:") {
		t.Errorf("лог адаптера не дошёл до вывода раннера как есть:\n%s", out.String())
	}
	// Доктор и сборка только читают.
	for _, r := range requests() {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("сборка офиса отправила %s", r)
		}
	}
}

// Сообщение под учёткой офиса — не ответ человека (спека, «The office's own
// account is not a human»).
func TestOpenYouGileOfficeAccountIsNotHuman(t *testing.T) {
	home, _ := youGileFixture(t, youGileProject, youGileOpts{})
	workflow, err := tracker.LoadWorkflow(filepath.Join("..", "..", "office", tracker.WorkflowFile))
	if err != nil {
		t.Fatal(err)
	}
	projects, err := tracker.LoadProjects(filepath.Join(home, tracker.ProjectsLocalFile))
	if err != nil {
		t.Fatal(err)
	}
	o, err := openYouGile(filepath.Join(home, yougile.TrackerFile), workflow, projects.For("yougile"), io.Discard)
	if err != nil {
		t.Fatalf("openYouGile: %v", err)
	}
	// Отчёт с вопросом — тем же кодом, что печатает раннер (как asked()
	// в internal/tracker/questions_test.go).
	body := tracker.ReportBody(
		tracker.Marker{RunID: "run-1", Role: "analyst", Outcome: "needs_human", Next: "human", ConfigSHA: "5bc6a3b0"},
		runner.Result{Outcome: runner.OutcomeNeedsHuman, Summary: "Нужен выбор.", NextOwner: "human",
			Questions: []runner.Question{{ID: "Q1", Text: "Какой провайдер?", Options: []runner.Option{
				{ID: "a", Label: "Stripe"}, {ID: "b", Label: "PayPal"}}}}},
		"", runner.Usage{})
	report := tracker.Comment{Author: "office@example.com", Body: body}
	reply := tracker.Comment{Author: "office@example.com", Body: "Q1: a"}
	if got := tracker.HumanAnswers([]tracker.Comment{report, reply}, "analyst", o.accounts); got != nil {
		t.Errorf("запись учётки офиса принята за ответ человека: %+v", got)
	}
	human := tracker.Comment{Author: "human@example.com", Body: "Q1: a"}
	if got := tracker.HumanAnswers([]tracker.Comment{report, human}, "analyst", o.accounts); len(got) == 0 {
		t.Error("ответ человека не распознан — проверка выше ничего не доказывает")
	}
}

func TestOpenYouGileRefusals(t *testing.T) {
	workflow, err := tracker.LoadWorkflow(filepath.Join("..", "..", "office", tracker.WorkflowFile))
	if err != nil {
		t.Fatal(err)
	}
	partial := maps.Clone(youGileColumnIDs)
	delete(partial, "Backlog")
	delete(partial, "Done")
	cases := []struct {
		name, projectsLocal, key string
		columns                  map[string]string
		want                     []string
	}{
		{"ключи расходятся", youGileProject, "shop", youGileColumnIDs, []string{"SHOP", "shop"}},
		{"два yougile-проекта", youGileProject + strings.Replace(youGileProject, "SHOP", "BLOG", 1), "SHOP", youGileColumnIDs,
			[]string{"один проект YouGile на раннер", "BLOG", "SHOP"}},
		{"статусу графа нет колонки", youGileProject, "SHOP", partial, []string{"Backlog", "Done"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			url, requests := youGileServer(t, youGileOpts{})
			_, home := fixtureRunner(t, tc.projectsLocal)
			writeYouGileFile(t, home, trackerYouGileYAML(url, tc.key, tc.columns))
			t.Setenv("YOUGILE_API_KEY", "секрет")
			projects, err := tracker.LoadProjects(filepath.Join(home, tracker.ProjectsLocalFile))
			if err != nil {
				t.Fatal(err)
			}
			_, err = openYouGile(filepath.Join(home, yougile.TrackerFile), workflow, projects.For("yougile"), io.Discard)
			if err == nil {
				t.Fatal("отказа нет")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("отказ не назвал %q: %v", want, err)
				}
			}
			// Все три отказа — по локальным файлам, до сети.
			if got := requests(); len(got) != 0 {
				t.Errorf("до отказа ушли запросы: %v", got)
			}
		})
	}
}

// Отвергнутый ключ — отказ всей команды, как у jira.
func TestOfficesRejectedYouGileKeyRefusesWholeCommand(t *testing.T) {
	youGileFixture(t, mockProject+youGileProject, youGileOpts{meStatus: http.StatusUnauthorized})
	all, err := newOffices(flags("tick"), nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "учётка офиса") {
		t.Fatalf("отказ не про учётку офиса: %v", err)
	}
	if all != nil {
		t.Error("при отвергнутом ключе собран офис mock")
	}
}
```

- [ ] **Step 4: Run to verify they fail**

Run: `go test ./cmd/runner/ -run 'YouGile' -v`
Expected: compile FAIL `undefined: openYouGile`.

- [ ] **Step 5: Implement**

`internal/tracker/config.go:485`:

```go
var trackers = []string{"mock", "jira", "yougile"}
```

`office/projects.local.example.yaml:18`, comment only:

```yaml
  # Где живут задачи проекта: jira (нужен tracker.yaml), yougile (нужен
  # tracker-yougile.yaml) или mock (файловый трекер).
```

`cmd/runner/office.go`: import `"github.com/kao73/virtual-office/internal/tracker/yougile"`. Change the `newOffices` doc sentence "(tracker.yaml открывается только если среди них jira)" to "(tracker.yaml — только если среди них jira, tracker-yougile.yaml — только если yougile)". Add a case to the switch, after `jira`:

```go
		case "yougile":
			o, err = openYouGile(sources.machine(home, yougile.TrackerFile), workflow, projects.For("yougile"), out)
```

Then, after `openJira`:

```go
// youGileFile читает tracker-yougile.yaml и сверяет его с projects.local.yaml:
// проект YouGile один на раннер, и ключ у него в обоих файлах один. declared —
// проекты с tracker: yougile. Отказ называет обе стороны: «нет такого проекта»
// на первом же тике не сказал бы, какой из двух файлов править.
func youGileFile(path string, declared tracker.Projects) (yougile.FileConfig, string, error) {
	fc, err := yougile.LoadConfig(path)
	if err != nil {
		return yougile.FileConfig{}, "", err
	}
	key := fc.ProjectKey()
	keys := declared.Keys()
	switch {
	case len(keys) > 1:
		return yougile.FileConfig{}, "", fmt.Errorf("%s называет %d проекта с tracker: yougile (%s): поддерживается один проект YouGile на раннер",
			tracker.ProjectsLocalFile, len(keys), strings.Join(keys, ", "))
	case len(keys) == 1 && keys[0] != key:
		return yougile.FileConfig{}, "", fmt.Errorf("%s описывает проект %q, а %s называет проект с tracker: yougile %q — ключи обязаны совпадать",
			path, key, tracker.ProjectsLocalFile, keys[0])
	}
	return fc, key, nil
}

// youGileColumns — у каждого статуса графа есть колонка. Статус задачи
// в YouGile — её колонка, и без колонки для Backlog или Done задачу в таком
// статусе не прочтёт ни List, ни доска. Сверка локальная: сами колонки на
// сервере проверяет Open.
func youGileColumns(fc yougile.FileConfig, key string, workflow tracker.Workflow) error {
	columns := fc.Projects[key].Columns
	var missing []string
	for _, status := range workflow.Statuses {
		if columns[status] == "" {
			missing = append(missing, status)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s: у статусов графа %s нет колонки в projects.%s.columns",
			yougile.TrackerFile, strings.Join(missing, ", "), key)
	}
	return nil
}

// openYouGile — YouGile по tracker-yougile.yaml. Учётка одна на всех: роли
// различаются маркером комментария, а не автором, — форма openMock, а не
// openJira. Её email (Whoami) — агентский: без него записки офиса в чате
// задачи сходили бы за ответы человека. Лог адаптера — в вывод раннера;
// префикс «yougile: » у его сообщений уже есть.
func openYouGile(path string, workflow tracker.Workflow, declared tracker.Projects, out io.Writer) (opened, error) {
	fc, key, err := youGileFile(path, declared)
	if err != nil {
		return opened{}, err
	}
	if err := youGileColumns(fc, key, workflow); err != nil {
		return opened{}, err
	}
	cfg, err := fc.Tracker(key)
	if err != nil {
		return opened{}, err
	}
	office, err := yougile.Open(cfg)
	if err != nil {
		return opened{}, err
	}
	email, err := office.Whoami()
	if err != nil {
		return opened{}, fmt.Errorf("учётка офиса: %w", err)
	}
	office.Logf = func(format string, args ...any) { fmt.Fprintf(out, format+"\n", args...) }

	o := opened{tasks: office, byRole: map[string]tracker.Tracker{}, accounts: append([]string{email}, fc.AlsoAgents...)}
	for _, role := range workflow.Order() {
		o.byRole[role] = office
	}
	return o, nil
}
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `go test ./internal/tracker/ ./cmd/runner/ -v -run 'YouGile|TestOffices'` then `go test ./... && go vet ./...`
Expected: PASS. If `TestOfficesServeAllThreeTrackers` shows the per-office machinery needs a change beyond the dispatch (it should not), stop and report. Tasks.md 2.2 asserts that it doesn't.

- [ ] **Step 7: Tick and commit**

In `tasks.md`, tick 1.1, 1.3, 2.1, 2.2, 5.1, 6.1, 6.2.

```bash
git add internal/tracker/config.go internal/tracker/config_test.go office/projects.local.example.yaml cmd/runner/office.go cmd/runner/office_test.go docs/openspec/changes/yougile-wiring-and-docs/tasks.md
git commit -m "feat(yougile-wiring-and-docs): runner opens tracker: yougile projects

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Doctor YouGile stage (tasks.md 4.3)

**Files:**
- Modify: `cmd/runner/doctor.go` (header comment; extract `doctorJira`; add `loadOfficeWorkflow`, `doctorYouGile`)
- Test: `cmd/runner/doctor_test.go`

**Interfaces:**
- Consumes: `youGileFile`, `youGileColumns`, `youGileServer`, `youGileOpts`, `youGileFixture`, `writeYouGileFile`, `trackerYouGileYAML`, `youGileColumnIDs`, `youGileProject` (Task 4); `yougile.Open`, `Whoami`, `FileConfig.APIKeyEnv`, `FileConfig.Tracker`.
- Produces:
  - `func doctorJira(report func(finding), home string, projects tracker.Projects, office runner.Office, officeErr error)`
  - `func doctorYouGile(report func(finding), home string, projects tracker.Projects, office runner.Office, officeErr error)`
  - `func loadOfficeWorkflow(o runner.Office, resolveErr error) (tracker.Workflow, error)`

- [ ] **Step 1: Write the failing tests** (append to `doctor_test.go`; add import `github.com/kao73/virtual-office/internal/tracker/yougile`)

```go
func TestDoctorYouGileFullSuccess(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	_, requests := youGileFixture(t, mockProject+youGileProject, youGileOpts{})

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("доктор отказал на счастливом пути: %v\n%s", err, out.String())
	}
	printed := out.String()
	for _, want := range []string{
		findingPrefix("ok", "config:tracker-yougile.yaml"), findingPrefix("ok", "cred:YOUGILE_API_KEY"),
		findingPrefix("ok", "yougile:open"), findingPrefix("ok", "yougile:account"),
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("нет строки %q:\n%s", want, printed)
		}
	}
	if msg, _ := findingMessage(t, printed, "ok", "yougile:account"); !strings.Contains(msg, "office@example.com") {
		t.Errorf("yougile:account не назвал учётку: %q", msg)
	}
	if strings.Contains(printed, "секрет") {
		t.Errorf("ключ API попал в отчёт:\n%s", printed)
	}
	for _, r := range requests() {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("доктор отправил %s — YouGile доктору только читать", r)
		}
	}
}

// Нет yougile-проектов — стадии нет вовсе.
func TestDoctorWithoutYouGileProjectsSkipsStage(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	fixtureRunner(t, mockProject)
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("доктор отказал: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "yougile") {
		t.Errorf("mock-only офис упомянул YouGile:\n%s", out.String())
	}
}

func TestDoctorYouGileMissingFileSkipsDependentChecks(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	fixtureRunner(t, mockProject+youGileProject)
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err == nil {
		t.Fatal("отсутствующий tracker-yougile.yaml должен быть fatal")
	}
	printed := out.String()
	for _, want := range []string{findingPrefix("fail", "config:tracker-yougile.yaml"), findingPrefix("warn", "skip:yougile")} {
		if !strings.Contains(printed, want) {
			t.Errorf("нет строки %q:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "cred:YOUGILE") || strings.Contains(printed, "yougile:") {
		t.Errorf("проверки, зависящие от файла, запустились:\n%s", printed)
	}
}

// Покрытие графа колонками — под config:, до сети.
func TestDoctorYouGileMissingColumnFailsConfig(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	url, requests := youGileServer(t, youGileOpts{})
	_, home := fixtureRunner(t, youGileProject)
	partial := maps.Clone(youGileColumnIDs)
	delete(partial, "Blocked")
	writeYouGileFile(t, home, trackerYouGileYAML(url, "SHOP", partial))
	t.Setenv("YOUGILE_API_KEY", "секрет")

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err == nil {
		t.Fatal("статус без колонки должен быть fatal")
	}
	msg, ok := findingMessage(t, out.String(), "fail", "config:tracker-yougile.yaml")
	if !ok || !strings.Contains(msg, "Blocked") {
		t.Errorf("config: не назвал Blocked: %q\n%s", msg, out.String())
	}
	if !strings.Contains(out.String(), findingPrefix("warn", "skip:yougile")) {
		t.Errorf("нет skip:yougile:\n%s", out.String())
	}
	if len(requests()) != 0 {
		t.Errorf("до отказа ушли запросы: %v", requests())
	}
}

func TestDoctorYouGileEmptyKeySkipsServerChecks(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	youGileFixture(t, youGileProject, youGileOpts{})
	t.Setenv("YOUGILE_API_KEY", "")
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err == nil {
		t.Fatal("пустой ключ должен быть fatal")
	}
	printed := out.String()
	for _, want := range []string{
		findingPrefix("ok", "config:tracker-yougile.yaml"),
		findingPrefix("fail", "cred:YOUGILE_API_KEY"), findingPrefix("warn", "skip:yougile-checks"),
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("нет строки %q:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "yougile:open") || strings.Contains(printed, "yougile:account") {
		t.Errorf("серверные проверки запустились без ключа:\n%s", printed)
	}
}

func TestDoctorYouGileOpenFailureSkipsAccount(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	youGileFixture(t, youGileProject, youGileOpts{noProject: true})
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err == nil {
		t.Fatal("несуществующий проект должен быть fatal")
	}
	printed := out.String()
	for _, want := range []string{findingPrefix("fail", "yougile:open"), findingPrefix("warn", "skip:yougile-checks")} {
		if !strings.Contains(printed, want) {
			t.Errorf("нет строки %q:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "yougile:account") {
		t.Errorf("yougile:account запустился после отказа Open:\n%s", printed)
	}
}

func TestDoctorYouGileRejectedKeyFailsAccount(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	youGileFixture(t, youGileProject, youGileOpts{meStatus: http.StatusUnauthorized})
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err == nil {
		t.Fatal("отвергнутый ключ должен быть fatal")
	}
	if !strings.Contains(out.String(), findingPrefix("fail", "yougile:account")) {
		t.Errorf("нет fail yougile:account:\n%s", out.String())
	}
}

// Сломанный tracker.yaml не прячет YouGile: стадии независимы.
func TestDoctorBrokenJiraDoesNotHideYouGile(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	home, _ := youGileFixture(t, mockProject+jiraProject+youGileProject, youGileOpts{})
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte("это: не: tracker.yaml: {{{"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err == nil {
		t.Fatal("сломанный tracker.yaml должен быть fatal")
	}
	printed := out.String()
	for _, want := range []string{
		findingPrefix("fail", "config:tracker.yaml"), findingPrefix("warn", "skip:jira"),
		findingPrefix("ok", "yougile:open"), findingPrefix("ok", "yougile:account"),
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("нет строки %q:\n%s", want, printed)
		}
	}
}
```

Also add `maps` to the imports if not present, and add an assertion to `TestDoctorMockOnlyOfficeSkipsJiraEntirelyWithoutTrackerFile`: `strings.Contains(printed, "yougile")` must be false.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./cmd/runner/ -run 'TestDoctor.*YouGile' -v`
Expected: FAIL. No `config:tracker-yougile.yaml` finding; `TestDoctorYouGileMissingFileSkipsDependentChecks` fails with "должен быть fatal".

- [ ] **Step 3: Implement**

In `doctor.go`:

1. Header comment. After `jira:workflow:<проект>:<роль>, skip:workflow:<проект>` append `, config:tracker-yougile.yaml, skip:yougile, cred:<api_key_env>, yougile:open, skip:yougile-checks, yougile:account`. In the first line's stage list, add `tracker-yougile.yaml, живой YouGile` after `живая JIRA`. Import `yougile`.

2. Extract the JIRA stage. Move everything in `doctorCommand` from `jiraProjects := projects.For("jira")` to the end of the `for _, key := range jiraProjects.Keys()` loop, verbatim, into:

```go
// doctorJira — стадия 3: tracker.yaml и живая JIRA, только когда хоть один
// проект назвал jira. Отдельной функцией, чтобы её отказы кончали только её
// саму: стадия YouGile идёт следом и должна отчитаться и при сломанной JIRA.
func doctorJira(report func(finding), home string, projects tracker.Projects, office runner.Office, officeErr error) {
	jiraProjects := projects.For("jira")
	if len(jiraProjects) == 0 {
		return
	}
	// … moved body, with every `return concludeExit(out, findings)` → `return` …
}
```

`doctorCommand` then ends with:

```go
	doctorJira(report, home, projects, office, officeErr)
	doctorYouGile(report, home, projects, office, officeErr)
	return concludeExit(out, findings)
```

3. Workflow loader shared with `loadWorkingStatuses`:

```go
// loadOfficeWorkflow — граф офиса (office.Root, не ${OFFICE_HOME}); отказ
// резолва офиса отдаётся как есть.
func loadOfficeWorkflow(o runner.Office, resolveErr error) (tracker.Workflow, error) {
	if resolveErr != nil {
		return tracker.Workflow{}, resolveErr
	}
	return tracker.LoadWorkflow(filepath.Join(o.Root, tracker.WorkflowFile))
}
```

Rewrite the head of `loadWorkingStatuses` to `workflow, err := loadOfficeWorkflow(o, resolveErr); if err != nil { return nil, err }`, keeping the rest.

4. The YouGile stage:

```go
// doctorYouGile — стадия 4: tracker-yougile.yaml и живой YouGile, только
// когда хоть один проект назвал yougile. Порядок и каскад — Design Doc
// yougile-wiring-and-docs, §5: файл → переменная с ключом → Open (проект
// и колонки) → Whoami. В YouGile доктор только читает: Open и Whoami — GET.
func doctorYouGile(report func(finding), home string, projects tracker.Projects, office runner.Office, officeErr error) {
	declared := projects.For("yougile")
	if len(declared) == 0 {
		return
	}
	configID := "config:" + yougile.TrackerFile
	skipAll := func() {
		report(finding{"skip:yougile", "warn", "cred/YouGile-проверки пропущены: " + yougile.TrackerFile + " не загрузился"})
	}
	fc, key, err := youGileFile(filepath.Join(home, yougile.TrackerFile), declared)
	if err != nil {
		report(finding{configID, "fail", err.Error()})
		skipAll()
		return
	}
	// Покрытие графа колонками — тоже по локальным файлам, поэтому здесь.
	// Граф не прочитан (офис не резолвится или ещё не распакован) — не беда
	// файла, а непроверенное: warn, и дальше.
	switch workflow, wfErr := loadOfficeWorkflow(office, officeErr); {
	case wfErr != nil:
		report(finding{configID, "warn", fmt.Sprintf("проект %s; покрытие статусов графа колонками не проверено: %v", key, wfErr)})
	default:
		if err := youGileColumns(fc, key, workflow); err != nil {
			report(finding{configID, "fail", err.Error()})
			skipAll()
			return
		}
		report(finding{configID, "ok", "проект " + key + ", колонка на каждый статус графа"})
	}

	skipChecks := func(why string) {
		report(finding{"skip:yougile-checks", "warn", "yougile:open/account пропущены: " + why})
	}
	credID := "cred:" + fc.APIKeyEnv
	if os.Getenv(fc.APIKeyEnv) == "" {
		report(finding{credID, "fail", "не задана"})
		skipChecks("нет ключа API")
		return
	}
	report(finding{credID, "ok", "задана"})

	cfg, err := fc.Tracker(key)
	if err == nil {
		var trk *yougile.Tracker
		if trk, err = yougile.Open(cfg); err == nil {
			report(finding{"yougile:open", "ok", "проект и колонки на месте"})
			if email, err := trk.Whoami(); err != nil {
				report(finding{"yougile:account", "fail", err.Error()})
			} else {
				report(finding{"yougile:account", "ok", "учётка офиса: " + email})
			}
			return
		}
	}
	report(finding{"yougile:open", "fail", err.Error()})
	skipChecks("трекер не открыт")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/runner/ -run 'TestDoctor' -v` then `go test ./... && go vet ./...`
Expected: PASS, including every pre-existing JIRA doctor test, which proves the extraction kept behavior.

- [ ] **Step 5: Tick and commit**

In `tasks.md`, tick 4.3.

```bash
git add cmd/runner/doctor.go cmd/runner/doctor_test.go docs/openspec/changes/yougile-wiring-and-docs/tasks.md
git commit -m "feat(yougile-wiring-and-docs): doctor checks tracker-yougile.yaml, key, project, account

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Reference doc and guide pages (tasks.md 4.1, 6.4, 6.5)

**Files:**
- Create: `docs/reference/yougile-requirements.md`
- Modify: `docs/guide/project-setup.md` ("Запись о проекте", "Подключение к трекеру", the sources-header listing and the note after it)
- Modify: `docs/guide/machine-setup.md` ("Креды" → "Трекер")
- Modify: `docs/reference/configuration.md` ("Какой файл читается откуда", env table, "Ключи: где искать")
- Modify: `docs/guide/operations.md:216` (home tree)
- Modify: `docs/openspec/changes/yougile-wiring-and-docs/design.md` (stale doc name)

**Interfaces:**
- Consumes: final names from Tasks 2–5: file keys, `yougile.TrackerFile`, the doctor check-ids, and the refusal texts.
- Produces: `docs/reference/yougile-requirements.md` with anchors `#учётка`, `#проект-и-колонки`, `#base_url`, `#как-снять-задачу-из-blocked`, `#runner-ls-и-архивные-карточки`, which Task 7 links to.

This is a docs task. The "test" is the reviewer checklist in Step 3 plus `sh scripts/doc-recipe-test.sh`. Use `documentation-writer` (Diátaxis: this is **reference**, mirroring `jira-requirements.md`: a requirement plus a way to check it, nothing else). Russian prose. Pass it through `humanizer` before committing.

- [ ] **Step 1: Write `docs/reference/yougile-requirements.md`**

Structure (headings verbatim, content as described; every `curl` uses `-H "Authorization: Bearer $YOUGILE_API_KEY"` and never prints the key):

```markdown
# Что офис требует от вашего YouGile

<1 paragraph: document for whoever sets up the company in YouGile; each
section is a requirement and a way to check it; no setup script exists — all
by hand in the UI, ids read with curl. Paired with jira-requirements.md.>

## base_url
Requirement: `https://yougile.com`, never `https://ru.yougile.com` — the
runner refuses it (attachments: /user-data/ → 302 to
prod-user-data.yougile.com, not a subdomain of ru.yougile.com). No `/api-v2`.
Check: `curl -fsS -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $YOUGILE_API_KEY" https://yougile.com/api-v2/users/me` → 200.

## Учётка
Requirement: a dedicated non-human user in the company, with its own API key.
A human's login must be a different account: a reply written under the office
account is not heard (docs/contracts/tracker-protocol.md, «Кто человек»). One
account serves every role; roles are told apart by the comment marker. The
runner learns the account's email from /users/me; bots go into `also_agents`.
How to get a key: POST /api-v2/auth/companies → companyId, then
POST /api-v2/auth/keys with login/password/companyId (as that user).
Key goes into the variable named by `api_key_env` (default YOUGILE_API_KEY) —
machine-setup.md «Креды».
Check: the /users/me call above returns the office user's email, not yours.

## Проект и колонки
Requirement: one project per runner; the office user is a member. On a board
of that project, one column per graph status — Backlog, Analysis, Ready,
InProgress, Review, Approved, Done, Blocked (office/workflow.yaml). Titles are
free; the runner maps by id. Extra columns are allowed and are "outside the
graph".
How to read ids:
  curl … 'https://yougile.com/api-v2/projects?limit=100'           → project_id
  curl … 'https://yougile.com/api-v2/boards?projectId=<project_id>' → board id
  curl … 'https://yougile.com/api-v2/columns?boardId=<board id>'    → column ids
(with `| jq '.content[] | {id, title}'` as an optional pretty-print).
Check: `runner doctor` → ok config:tracker-yougile.yaml, ok yougile:open.

## Как снять задачу из Blocked
Move the card to a column outside the graph. Do not archive it: an archived
split parent still gets its children completed by `runner complete-splits`
(accepted residual of the adapter). (tasks.md 6.4)

## runner ls и архивные карточки
`runner ls` also shows archived cards, in their column's status: `List`
includes them so that an archived dependency in a terminal column does not
block its dependents forever. Deleted cards are not shown. (tasks.md 6.5)

## Лимит запросов
YouGile allows 50 requests per minute per company; a 429 surfaces as an error
and the next tick retries. Keep the loop period at the default 2 minutes or more.

## Проверка целиком
`runner doctor` findings: config:tracker-yougile.yaml, cred:<api_key_env>,
yougile:open, yougile:account — what each one checks and what it skips when it fails.
```

- [ ] **Step 2: Update the guides**

- `docs/guide/project-setup.md`, "Запись о проекте": "подключение к JIRA" → "подключение к JIRA или YouGile".
- `docs/guide/project-setup.md`, "Подключение к трекеру": first line → "Только для проекта на JIRA или YouGile — …". Keep the JIRA part under a `### JIRA` subheading, and add `### YouGile`:

  ```sh
  cp "${OFFICE_HOME:-$HOME/.office}/tracker-yougile.example.yaml" \
     "${OFFICE_HOME:-$HOME/.office}/tracker-yougile.yaml"
  ```
  with checklist items: the project key equals the one in `projects.local.yaml`; `project_id` and eight column ids come from [«Что офис требует от вашего YouGile», «Проект и колонки»](../reference/yougile-requirements.md#проект-и-колонки); `base_url` stays `https://yougile.com`; one YouGile project per runner; the key lives only in the environment under the `api_key_env` name ([«Подготовка машины», «Креды»](machine-setup.md#креды)).
- `docs/guide/project-setup.md`, the `runner ls` sample block: add the line `  tracker-yougile.yaml   …/.office/tracker-yougile.yaml (машина, есть)` after `tracker.yaml`. In the checklist note, extend "Если ни один проект не назвал `jira`, строки `tracker.yaml` не будет вовсе" with the same statement for `yougile`/`tracker-yougile.yaml`.
- `docs/guide/machine-setup.md`, "Креды", tracker block: heading text → "если проект ведётся на JIRA или YouGile". Add `export YOUGILE_API_KEY='...'` in a separate `sh` block after JIRA's, with one checklist item: the name matches `api_key_env` in `tracker-yougile.yaml`, and the key belongs to the office's non-human user ([link](../reference/yougile-requirements.md#учётка)).
- `docs/reference/configuration.md`: in "корень хозяйства" add `tracker-yougile.yaml`; in the env table add a row `имя из api_key_env | значение — среда, читает cmd/runner (yougile.FileConfig.Tracker) | ключ API YouGile: в tracker-yougile.yaml только имя переменной`; in "Ключи: где искать" add "подключение к YouGile — `office/tracker-yougile.example.yaml`, он же `${OFFICE_HOME}/tracker-yougile.example.yaml`".
- `docs/guide/operations.md:216` tree: add `├── tracker-yougile.yaml            подключение к YouGile: проект, колонки, имя переменной с ключом` right after the `tracker.yaml` line (align with its neighbors).
- `docs/openspec/changes/yougile-wiring-and-docs/design.md`: the Goals bullet and the second Risk still name `docs/notes/yougile-setup.md`. Replace both with `docs/reference/yougile-requirements.md` (Design Doc §8 says the open-phase artifacts are corrected).

- [ ] **Step 3: Check**

Run: `grep -rn "yougile-setup.md" docs/ --include=*.md | grep -v superpowers/specs` → expect no hits.
Run: `grep -n "ru.yougile.com" docs/reference/yougile-requirements.md` → only in the refusal explanation.
Run: `sh scripts/doc-recipe-test.sh` → passes.
Reviewer checklist: each section is a requirement plus a check; no instruction prints the key; the column list matches `office/workflow.yaml` `statuses`; the check-ids match `doctor.go`'s header; 6.4 and 6.5 each have their own section; every relative link resolves (`ls` each target).

- [ ] **Step 4: Tick and commit**

In `tasks.md`, tick 4.1, 6.4, 6.5.

```bash
git add docs/reference/yougile-requirements.md docs/guide/project-setup.md docs/guide/machine-setup.md docs/reference/configuration.md docs/guide/operations.md docs/openspec/changes/yougile-wiring-and-docs/design.md docs/openspec/changes/yougile-wiring-and-docs/tasks.md
git commit -m "docs(yougile-wiring-and-docs): YouGile requirements reference and setup steps

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: README and onboarding name `yougile` (tasks.md 4.2)

**Files:**
- Modify: `README.md` (intro line 9 stays factual about what was validated; line 36 `runner doctor` comment; "Возможности" where trackers are listed)
- Modify: `docs/ONBOARDING.md` (steps 5–6)

**Interfaces:**
- Consumes: anchors from Task 6.

- [ ] **Step 1: Edit**

- `README.md:36`: `# диагностика без побочных эффектов: инструменты, креды, JIRA, YouGile, песочница, старые снапшоты`.
- `README.md`: wherever the supported trackers are enumerated (search `grep -n "JIRA\|mock" README.md`), add YouGile as a third option with a link to `docs/reference/yougile-requirements.md`. Do **not** change the sentence about what was validated live: YouGile's live run belongs to `yougile-live-validation`.
- `docs/ONBOARDING.md` step 5: "Инстанс трекера отвечает требованиям офиса — [JIRA](reference/jira-requirements.md) или [YouGile](reference/yougile-requirements.md)". Keep the local-JIRA bootstrap sentence and the `mock` sentence. Step 6: add "для YouGile — одна учётка офиса, не ваша: [«Учётка»](reference/yougile-requirements.md#учётка)".

- [ ] **Step 2: Check**

Run: `grep -n -i yougile README.md docs/ONBOARDING.md` → the new mentions are present; every link target exists.
Run: `sh scripts/doc-recipe-test.sh` → passes. The README recipe is unchanged.

- [ ] **Step 3: Tick and commit**

In `tasks.md`, tick 4.2.

```bash
git add README.md docs/ONBOARDING.md docs/openspec/changes/yougile-wiring-and-docs/tasks.md
git commit -m "docs(yougile-wiring-and-docs): name yougile next to jira and mock

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: MANUAL CHECKPOINT — owner provisions the office account and project (tasks.md 3.1, 3.2)

**Not code. The executor does not do this step.** The executor stops here and hands off to the human owner.

**Files:** none changed by the executor, except ticking `tasks.md` after the owner confirms.

- [ ] **Step 1: Hand off to the owner** with this message (Russian, in chat):
  > Нужны 3.1 и 3.2: по `docs/reference/yougile-requirements.md` заведите в YouGile отдельную не-человеческую учётку офиса с ключом API (раздел «Учётка») и проект с колонкой на каждый из восьми статусов графа (раздел «Проект и колонки»). Потом `tracker-yougile.yaml` по образцу, `export YOUGILE_API_KEY=…` и `runner doctor --backend local`. Напишите, где документ оказался неточен или неполон.
- [ ] **Step 2: Wait for the owner's confirmation.** Every gap they report is a docs defect: fix it in `docs/reference/yougile-requirements.md` (and the guides if affected) and commit it as `docs(yougile-wiring-and-docs): fix requirements after provisioning`.
- [ ] **Step 3: Record** in the change's Build notes: `doctor` output from the owner's machine with the key redacted, and the owner's email for the office account. That email is not a secret, but do not commit it if the owner objects. Do **not** run `tick`/`loop` against it: live runs belong to `yougile-live-validation`.
- [ ] **Step 4: Tick 3.1, 3.2 and commit** only after the owner confirms:

```bash
git add docs/openspec/changes/yougile-wiring-and-docs/tasks.md
git commit -m "docs(yougile-wiring-and-docs): office account and project provisioned by the owner

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Full verification

- [ ] **Step 1:** `go vet ./... && go test ./...` → all PASS.
- [ ] **Step 2:** `sh scripts/doc-recipe-test.sh` → runs to the end.
- [ ] **Step 3:** `comet classic openspec -- validate yougile-wiring-and-docs --strict` (from the worktree root, the same invocation earlier verify reports used) → valid.
- [ ] **Step 4:** Check that `tasks.md` has every box ticked (`grep -c '\- \[ \]' docs/openspec/changes/yougile-wiring-and-docs/tasks.md` → `0`). 3.1/3.2 are allowed to stay open only while Task 8 is still waiting on the owner; say so explicitly in the handoff.
- [ ] **Step 5:** Confirm the Logf departure (Global Constraints) is written in the Build notes, so Verify does not flag it as drift.
