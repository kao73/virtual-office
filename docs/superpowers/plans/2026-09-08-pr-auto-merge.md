---
change: pr-auto-merge
design-doc: docs/superpowers/specs/2026-09-08-pr-auto-merge-design.md
base-ref: 3e3f9ec7766d38698d0ab210c82843c57658a809
---

# pr-auto-merge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an explicitly opted-in project have the office merge its own approved pull requests — deterministic code, no new role — while every other project keeps today's human-merge behavior unchanged.

**Architecture:** Four small, ordered layers, each depending on the one before it: (1) a new `auto_merge` config block and a `Project.PRBranch()` helper that resolves the branch task branches fork from and the PR pass targets; (2) a new `Manager.BaseAdvanced` check next to `MergeCheck`, and `PRBranch()` threaded into the two places that currently hardcode `DefaultBranch` for task-branch forking and agent context; (3) a `Forge.Merge()` method with a GitHub implementation; (4) the PR pass (`internal/pipeline/prpass.go`) wired to call `BaseAdvanced` alongside `MergeCheck` everywhere (for every project, not just auto-merge ones) and to attempt `Merge()` when the gate holds and `auto_merge.enabled` is true. A `roles/implementer/role.md` wording fix and three documentation updates round it out, and a live smoke test closes the loop.

**Tech Stack:** Go (stdlib `net/http`, `os/exec` for git plumbing), `gopkg.in/yaml.v3` for config, table-driven `testing` with `httptest` for the GitHub fake server — all existing patterns in this codebase, no new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-08-pr-auto-merge-design.md` (Russian; this plan translates and operationalizes it). Task boundaries mirror `docs/openspec/changes/pr-auto-merge/tasks.md` — task numbers below (1–7) are the same groups, and each step below cites its `tasks.md` item (e.g. "tasks.md 1.1").

## Global Constraints

- **No new role.** Auto-merge is deterministic code inside the existing PR pass (`internal/pipeline/prpass.go`), never an agent.
- **No vote/veto window.** Once the gate (`Approved` + clean `MergeCheck` + `BaseAdvanced` false) holds, the office merges on that same tick — no "wait N hours" logic anywhere.
- **`merge_method` is hardcoded to `"merge"`** (a merge commit) in `internal/forge/github.go`. Not configurable — YAGNI until asked.
- **The office never creates `auto_merge.target_branch`.** If it's set and missing on the remote, the first git operation against it fails with an ordinary git error (same as a typo in `default_branch` today) — no special-cased check at config-load time (network may be unavailable then anyway).
- **`auto_merge` lives in `projects.local.yaml`** (the machine half), next to `forge` — never in `projects.yaml` (the office/repo half). The existing `machineKeys` mechanism must reject it there with a message naming where it belongs, exactly like it already does for `forge`.
- **The widened conflict-return trigger (`merge.Conflict || advanced`) applies to every project**, not only `auto_merge.enabled` ones — this is a deliberate, described behavior change for human-merge projects too (they'll see more implementer round-trips when their base branch is busy).
- **Comments and user-facing record text you add must be in Russian**, matching every existing file in this codebase (`internal/tracker`, `internal/workspace`, `internal/forge`, `internal/pipeline`, `roles/`, `docs/`). This plan document is in English per the Comet artifact language setting, but the code and docs it produces are not.
- **After every task:** `go build ./... && go vet ./... && go test ./...` must be clean before moving to the next task. This is task 7.3 in `tasks.md`, but treat it as a running invariant, not a one-time final step.
- **Note on file naming:** `tasks.md` 3.3 says the GitHub forge tests live in `internal/forge/github_test.go`. That file does not exist — the real, existing test file is `internal/forge/forge_test.go` (it already contains `TestGitHubOpensPullRequest`, `TestGitHubPRState`, etc.). Add the new `Merge()` tests there, not to a new file.

---

## Task 1: Config — `AutoMerge` struct and `Project.PRBranch()`

Corresponds to `tasks.md` §1 (1.1–1.4).

**Files:**
- Modify: `internal/tracker/config.go`
- Test: `internal/tracker/config_test.go`

**Interfaces:**
- Produces: `tracker.AutoMerge{Enabled bool, TargetBranch string}`; `tracker.Project.PRBranch() string`; `tracker.Project.AutoMerge AutoMerge` field. Later tasks (2, 4) call `project.PRBranch()` and read `project.AutoMerge.Enabled`.

### Background

`internal/tracker/config.go` currently has:

```go
type Project struct {
	RepoURL       string `yaml:"repo_url"`
	DefaultBranch string `yaml:"default_branch"`
	BranchPrefix  string `yaml:"branch_prefix"`
	WorktreeRoot string `yaml:"worktree_root"`
	Tracker string `yaml:"tracker"`
	Forge string `yaml:"forge"`
	Network []string
	Tools runner.Tools
}
```
(lines 461–480), a `machineProject` struct (lines 494–500) with `RepoURL/WorktreeRoot/Tracker/Forge/Rules`, `machineKeys = []string{"repo_url", "worktree_root", "tracker", "forge"}` (line 506), and `LoadProjects` (line 616) which assembles `Project{...}` from `office`+`machine` halves around line 675 and validates `RepoURL/DefaultBranch/BranchPrefix/WorktreeRoot/Tracker` around lines 690–707.

- [x] **Step 1: Write the failing tests**

Append to `internal/tracker/config_test.go` (after `TestUnionStringsDedupsAndSorts`, near the other `Projects`-level tests):

```go
// PRBranch — ветка, от которой форкаются задачи и куда метит PR-проход:
// target_branch авто-мержа, если задан, иначе default_branch.
func TestPRBranch(t *testing.T) {
	p := Project{DefaultBranch: "master"}
	if got := p.PRBranch(); got != "master" {
		t.Errorf("PRBranch() = %q без target_branch, ожидался default_branch %q", got, "master")
	}
	p.AutoMerge.TargetBranch = "office-integration"
	if got := p.PRBranch(); got != "office-integration" {
		t.Errorf("PRBranch() = %q, ожидался target_branch %q", got, "office-integration")
	}
}

// auto_merge доезжает из машинной половины и склеивается в Project так же,
// как forge — тем же путём LoadProjects.
func TestLoadProjectsCarriesAutoMerge(t *testing.T) {
	machine := validMachine + "  forge: github\n  auto_merge:\n    enabled: true\n    target_branch: office-integration\n"
	projects, err := loadHalves(t, validOffice, machine)
	if err != nil {
		t.Fatalf("проекты не загружены: %v", err)
	}
	p, err := projects.Get("OFF")
	if err != nil {
		t.Fatalf("проект OFF не найден: %v", err)
	}
	if !p.AutoMerge.Enabled {
		t.Error("auto_merge.enabled не доехал из машинной половины")
	}
	if got := p.PRBranch(); got != "office-integration" {
		t.Errorf("PRBranch() = %q, ожидался office-integration", got)
	}
}
```

Add one row to the existing `TestLoadProjectsRejectsIncomplete` table (in `internal/tracker/config_test.go`, the `cases := []struct{...}{...}` slice):

```go
		{"auto_merge без forge", validOffice, validMachine + "  auto_merge:\n    enabled: true\n", "forge"},
```

No new test is needed for "auto_merge key in the office file is rejected the same way forge is" — `TestLoadProjectsRejectsMachineKeysInOfficeFile` already iterates `machineKeys`, so adding `"auto_merge"` to that slice (Step 3 below) makes it exercise this automatically.

- [x] **Step 2: Run tests to verify they fail**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go test ./internal/tracker/... -run 'TestPRBranch|TestLoadProjectsCarriesAutoMerge|TestLoadProjectsRejectsIncomplete|TestLoadProjectsRejectsMachineKeysInOfficeFile' -v`
Expected: FAIL — `p.AutoMerge` / `p.PRBranch` don't exist yet (compile error), and the incomplete-case row won't fail with "forge" without the new validation.

- [x] **Step 3: Implement**

In `internal/tracker/config.go`, add the `AutoMerge` struct immediately before the `Project` struct (before line 459):

```go
// AutoMerge — доверие конкретного инстанса конкретному проекту: мержить ли
// самим и куда. Решение машины, не офиса — тот же класс, что Forge.
type AutoMerge struct {
	Enabled      bool   `yaml:"enabled"`
	TargetBranch string `yaml:"target_branch"`
}
```

Add the field to `Project`, right after `Forge`:

```go
	// Forge — куда открывать pull request. Пусто — forge у проекта нет:
	// PR-проход вырождается, но маршрут остаётся тем же (см. workflow.yaml: pr).
	Forge string `yaml:"forge"`
	// AutoMerge — сливает ли офис pull request сам, и в какую ветку. Пусто
	// (Enabled: false) — сегодняшнее поведение: сливает человек.
	AutoMerge AutoMerge `yaml:"auto_merge"`
```

Add the field to `machineProject`, right after `Forge`:

```go
	machineProject struct {
		RepoURL      string `yaml:"repo_url"`
		WorktreeRoot string `yaml:"worktree_root"`
		Tracker      string `yaml:"tracker"`
		Forge        string `yaml:"forge"`
		AutoMerge    AutoMerge `yaml:"auto_merge"`
		Rules        `yaml:",inline"`
	}
```

Add `"auto_merge"` to `machineKeys`:

```go
var machineKeys = []string{"repo_url", "worktree_root", "tracker", "forge", "auto_merge"}
```

Add `PRBranch()` right after `Branch()` (line 512):

```go
// Branch — ветка задачи.
func (p Project) Branch(key string) string { return p.BranchPrefix + key }

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

In `LoadProjects`, add `AutoMerge: local.AutoMerge,` to the `Project{...}` literal (around line 675–687):

```go
		projects[key] = Project{
			RepoURL:       local.RepoURL,
			DefaultBranch: half.DefaultBranch,
			BranchPrefix:  half.BranchPrefix,
			WorktreeRoot:  local.WorktreeRoot,
			Tracker:       local.Tracker,
			Forge:         local.Forge,
			AutoMerge:     local.AutoMerge,
			Network:       unionStrings(officeDefaults.Network, half.Network, machineDefaults.Network, local.Network),
			Tools: runner.Tools{
				Allow: unionStrings(officeDefaults.Tools.Allow, half.Tools.Allow, machineDefaults.Tools.Allow, local.Tools.Allow),
				Deny:  unionStrings(officeDefaults.Tools.Deny, half.Tools.Deny, machineDefaults.Tools.Deny, local.Tools.Deny),
			},
		}
```

Add validation in the second `for key, project := range projects` loop (around lines 690–707), after the `tracker` check:

```go
		if !slices.Contains(trackers, project.Tracker) {
			errs = append(errs, fmt.Errorf("%s: tracker=%q, ожидается один из %v (%s)",
				key, project.Tracker, trackers, machinePath))
		}
		if project.AutoMerge.Enabled && project.Forge == "" {
			errs = append(errs, fmt.Errorf(
				"%s: auto_merge.enabled=true, но forge не задан — мержить через API "+
					"некуда (%s)", key, machinePath))
		}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go test ./internal/tracker/... -v`
Expected: PASS, including all pre-existing tests in this package (`TestLoadProjects*`, `TestLoadWorkflow*`, etc.) — nothing above changes `Workflow`/`Limits` yet, so no other test should move.

- [x] **Step 5: Commit**

```bash
git add internal/tracker/config.go internal/tracker/config_test.go
git commit -m "feat(config): add auto_merge project config and Project.PRBranch()"
```

---

## Task 2: Workspace — `BaseAdvanced` and `PRBranch()` threading

Corresponds to `tasks.md` §2 (2.1–2.4). Depends on Task 1 (`Project.PRBranch()`).

**Files:**
- Modify: `internal/workspace/merge.go`
- Modify: `internal/workspace/workspace.go:402` (the `addWorktree` fork point — actual line may have shifted slightly; search for `git(ws.Repo, "worktree", "add", "--quiet", "-b", ws.Branch, ws.Dir, "origin/"+project.DefaultBranch)`)
- Modify: `internal/pipeline/pipeline.go:483` (search for `BaseBranch:  "origin/" + c.project.DefaultBranch`)
- Test: `internal/workspace/merge_test.go`

**Interfaces:**
- Consumes: `tracker.Project.PRBranch() string` (Task 1).
- Produces: `Manager.BaseAdvanced(repo, branch, base string) (bool, error)`. Task 4 calls this from `internal/pipeline/prpass.go` as `o.Workspaces.BaseAdvanced(...)`.

### Background

`internal/workspace/merge.go` already has `MergeCheck` using the same `git merge-tree --write-tree` / `isExitCode` / `gitEnv` helpers that live in `workspace.go`. `BaseAdvanced` is a sibling function in the same file, reusing those same package-level helpers (no new imports needed — `merge.go` already imports `fmt`, `os/exec`, `strconv`, `strings`).

`internal/workspace/workspace.go`'s `addWorktree` (around line 402) has three branches (`local`, `remote`, `default`); only the `default` branch (no local or remote ref for the task branch yet) forks from `"origin/"+project.DefaultBranch` — that's the one line to change.

`internal/pipeline/pipeline.go`'s `work` function builds the agent's `runner.Input` with `BaseBranch: "origin/" + c.project.DefaultBranch` (around line 483) — this is what appears in the agent's context as "Базовая ветка: ..." (see `internal/runner/input.go` `composeContext`, which is also what Task 5's `roles/implementer/role.md` rewrite will point implementers at).

- [x] **Step 1: Write the failing tests**

Append to `internal/workspace/merge_test.go` (it already has `setup(t)`, `commit(t, ...)`, `pushDefault(t, project, name, body)`, `m.Push(ws)`, `m.Repo(...)` helpers used below):

```go
// База не продвинулась — ветка задачи только что срублена и уже содержит все
// коммиты базы. Обычное дневное состояние: гейту здесь нечего заметить.
func TestBaseAdvancedFalseWhenBaseIsAncestor(t *testing.T) {
	m, project, task := setup(t)
	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	commit(t, ws.Dir, "новое.txt", "работа\n", "работа автора")
	if _, err := m.Push(ws); err != nil {
		t.Fatalf("ветка не опубликована: %v", err)
	}

	advanced, err := m.BaseAdvanced(ws.Repo, ws.Branch, project.DefaultBranch)
	if err != nil {
		t.Fatalf("продвижение не проверено: %v", err)
	}
	if advanced {
		t.Error("свежесрубленная ветка признана отставшей от базы")
	}
}

// База продвинулась вперёд с тех пор, как ветка задачи была срублена — даже
// без единого конфликтного маркера контекст, в котором работали implementer
// и reviewer, мог устареть по смыслу.
func TestBaseAdvancedTrueWhenBaseMovedOn(t *testing.T) {
	m, project, task := setup(t)
	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	commit(t, ws.Dir, "своё.txt", "работа\n", "работа автора")
	if _, err := m.Push(ws); err != nil {
		t.Fatalf("ветка не опубликована: %v", err)
	}

	pushDefault(t, project, "чужое.txt", "правка в базе\n")
	if _, err := m.Repo(task.Project, project); err != nil {
		t.Fatalf("клон не освежён: %v", err)
	}

	advanced, err := m.BaseAdvanced(ws.Repo, ws.Branch, project.DefaultBranch)
	if err != nil {
		t.Fatalf("продвижение не проверено: %v", err)
	}
	if !advanced {
		t.Error("продвинувшаяся база не замечена")
	}
}

// Несуществующая ветка или база — ошибка, как и у MergeCheck: сравнивать
// нечего, и это беда обвязки, а не ответ о задаче.
func TestBaseAdvancedMissingRef(t *testing.T) {
	m, project, task := setup(t)
	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}

	if _, err := m.BaseAdvanced(ws.Repo, "нет-такой-ветки", project.DefaultBranch); err == nil {
		t.Error("несуществующая ветка задачи принята без ошибки")
	}
	if _, err := m.BaseAdvanced(ws.Repo, ws.Branch, "нет-такой-базы"); err == nil {
		t.Error("несуществующая база принята без ошибки")
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go test ./internal/workspace/... -run TestBaseAdvanced -v`
Expected: FAIL (compile error — `m.BaseAdvanced` undefined).

- [x] **Step 3: Implement `BaseAdvanced`**

Append to `internal/workspace/merge.go`, after `MergeCheck`:

```go
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
		return false, nil // база уже целиком в предках ветки задачи
	case isExitCode(err, 1):
		return true, nil
	default:
		return false, fmt.Errorf("продвижение %s относительно %s не проверено: %w",
			base, branch, err)
	}
}
```

- [x] **Step 4: Run tests to verify `BaseAdvanced` passes**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go test ./internal/workspace/... -run TestBaseAdvanced -v`
Expected: PASS, all three cases.

- [x] **Step 5: Thread `PRBranch()` into `addWorktree` (`workspace.go`)**

In `internal/workspace/workspace.go`, in `addWorktree`, change:

```go
	default:
		_, err = git(ws.Repo, "worktree", "add", "--quiet", "-b", ws.Branch, ws.Dir, "origin/"+project.DefaultBranch)
	}
```
to:
```go
	default:
		_, err = git(ws.Repo, "worktree", "add", "--quiet", "-b", ws.Branch, ws.Dir, "origin/"+project.PRBranch())
	}
```

Do **not** touch the bare-clone init path (`git init --bare -b project.DefaultBranch`, around `workspace.go:329`) — that's the literal git default branch of a from-scratch bare clone, unrelated to task routing (per the design doc's explicit note).

- [x] **Step 6: Thread `PRBranch()` into the agent's `BaseBranch` (`pipeline.go`)**

In `internal/pipeline/pipeline.go`, in `work`, change:

```go
	input := runner.Input{
		Task:        taskBody(task),
		Branch:      ws.Branch,
		BaseBranch:  "origin/" + c.project.DefaultBranch,
		Context:     contextBody(task, roleName, o.Accounts, o.Workflow.Limits.MaxAttempts),
		Attachments: attachments,
	}
```
to:
```go
	input := runner.Input{
		Task:        taskBody(task),
		Branch:      ws.Branch,
		BaseBranch:  "origin/" + c.project.PRBranch(),
		Context:     contextBody(task, roleName, o.Accounts, o.Workflow.Limits.MaxAttempts),
		Attachments: attachments,
	}
```

- [x] **Step 7: Run the full test suite for both packages**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go test ./internal/workspace/... ./internal/pipeline/... -v 2>&1 | tail -80`
Expected: PASS. Every existing test builds `tracker.Project` with `DefaultBranch` set and no `AutoMerge`, so `PRBranch()` falls back to `DefaultBranch` and behavior is unchanged — this step is a regression check, not new coverage.

- [x] **Step 8: Commit**

```bash
git add internal/workspace/merge.go internal/workspace/merge_test.go internal/workspace/workspace.go internal/pipeline/pipeline.go
git commit -m "feat(workspace): add BaseAdvanced and thread Project.PRBranch() into task forking"
```

---

## Task 3: Forge — `Merge()`

Corresponds to `tasks.md` §3 (3.1–3.3). Independent of Tasks 1–2 (can be done in parallel with them, but is ordered here per `tasks.md`).

**Files:**
- Modify: `internal/forge/forge.go`
- Modify: `internal/forge/github.go`
- Test: `internal/forge/forge_test.go` (**not** `github_test.go` — see Global Constraints note above; that file doesn't exist in this repo)

**Interfaces:**
- Produces: `Forge.Merge(url string) error` on the interface; `(*GitHub).Merge(url string) error`. Task 4 calls this as `impl.Merge(url)` where `impl` comes from `o.Forges[project.Forge]` (type `forge.Forge`).

### Background

`internal/forge/forge.go` defines the `Forge` interface (`OpenPR`, `PRState`) and `ErrRefused`. `internal/forge/github.go`'s `do()` helper already classifies HTTP responses: 4xx → wraps `ErrRefused`, other errors (network, 5xx) → plain error (lines 149–154 of `github.go`). `Merge` reuses `do()` unchanged — no new classification logic.

The only two implementers of `Forge` in the whole repo are `*GitHub` (production) and `fakeForge` (test-only, in `internal/pipeline/prpass_test.go` — updated in Task 4).

- [x] **Step 1: Write the failing tests**

Append to `internal/forge/forge_test.go`. Add `"fmt"` to its import block (`encoding/json`, `errors`, `net/http`, `net/http/httptest`, `strings`, `testing`) for the sub-test names below.

```go
// Merge sливает pull request через PUT .../pulls/{number}/merge, с зашитым
// merge_method: "merge" — сохраняет историю ветки задачи как есть.
func TestGitHubMerge(t *testing.T) {
	var gotMethod, gotPath string
	var gotPayload map[string]string
	g := github(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotPayload)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"merged":true}`))
	})

	if err := g.Merge("https://github.com/kao73/expense-tracker/pull/3"); err != nil {
		t.Fatalf("слияние не выполнено: %v", err)
	}
	if gotMethod != http.MethodPut || gotPath != "/repos/kao73/expense-tracker/pulls/3/merge" {
		t.Errorf("запрос ушёл не туда: %s %s", gotMethod, gotPath)
	}
	if gotPayload["merge_method"] != "merge" {
		t.Errorf("merge_method = %q, ожидалось merge", gotPayload["merge_method"])
	}
}

// 405/409 — GitHub отказывается сливать (не мержится, не прошли required
// checks и т.п.): это окончательный ответ, а не сбой связи.
func TestGitHubMergeRefusal(t *testing.T) {
	for _, code := range []int{http.StatusMethodNotAllowed, http.StatusConflict} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			g := github(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"message":"отказ"}`))
			})
			err := g.Merge("https://github.com/kao73/expense-tracker/pull/3")
			if !errors.Is(err, ErrRefused) {
				t.Errorf("код %d не распознан как отказ: %v", code, err)
			}
		})
	}
}

// Сбой связи/5xx — задачу за него двигать нельзя, следующий проход
// попробует снова.
func TestGitHubMergeTransportFailure(t *testing.T) {
	g := github(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	err := g.Merge("https://github.com/kao73/expense-tracker/pull/3")
	if err == nil {
		t.Fatal("сбой сервера прошёл незамеченным")
	}
	if errors.Is(err, ErrRefused) {
		t.Errorf("сбой связи принят за отказ: %v", err)
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go test ./internal/forge/... -run TestGitHubMerge -v`
Expected: FAIL (compile error — `g.Merge` undefined).

- [x] **Step 3: Implement**

In `internal/forge/forge.go`, update the package doc comment (it currently claims auto-merge doesn't exist and isn't planned, which is now false) and the interface:

```go
// Package forge — где живут pull request проекта.
//
// Офис открывает PR, следит за ним и убирает за собой; сливает по умолчанию
// человек, а на проекте с явным auto_merge.enabled — офис сам, детерминированным
// кодом, тем же интерфейсом Merge.
//
// Слияемость ветки forge не спрашивают. Она считается локально, по bare-клону
// (`git merge-tree`), и это не оптимизация: так проверка не зависит ни от сети,
// ни от реализации forge, а второй forge обходится дешевле — интерфейс у него
// из трёх методов.
package forge
```

```go
// Forge — то немногое, что офису нужно от хостинга репозиториев.
//
// Проект называется ключом трекера: какой репозиторий за ним стоит, forge
// выясняет сам — из того же repo_url, которым пользуется раннер. Второго места
// для этого факта нет намеренно: два места однажды разойдутся.
type Forge interface {
	// OpenPR открывает pull request ветки задачи в ветку по умолчанию
	// и возвращает его адрес.
	OpenPR(project, branch, base, title, body string) (string, error)
	// PRState отвечает, что стало с pull request по его адресу.
	PRState(url string) (State, error)
	// Merge сливает pull request. ErrRefused — окончательный отказ forge
	// (не мержится, права нет, PR не найден); прочая ошибка — сбой связи,
	// задачу за неё двигать нельзя.
	Merge(url string) error
}
```

In `internal/forge/github.go`, add near the top (after the `API` const):

```go
// mergeMethod — merge commit, не squash и не rebase: сохраняет всю историю
// ветки задачи как есть, ничего не сжимает. Не вынесено в конфиг — YAGNI,
// пока не спросили.
const mergeMethod = "merge"
```

Add the method after `PRState`:

```go
// Merge сливает pull request.
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

`g.do` already classifies 4xx as `ErrRefused` and network/5xx as a plain error (lines 149–154) — reused unchanged.

- [x] **Step 4: Run tests to verify they pass**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go test ./internal/forge/... -v`
Expected: PASS, including all pre-existing `TestGitHub*`/`TestParse*` tests (unaffected by this change) and `var _ Forge = (*GitHub)(nil)` continuing to compile.

- [x] **Step 5: Commit**

```bash
git add internal/forge/forge.go internal/forge/github.go internal/forge/forge_test.go
git commit -m "feat(forge): add Merge() to the Forge interface and GitHub implementation"
```

---

## Task 4: PR pass — gate, widened staleness trigger, merge

Corresponds to `tasks.md` §4 (4.1–4.6). Depends on Tasks 1 (`PRBranch()`, `AutoMerge`), 2 (`BaseAdvanced`), 3 (`Forge.Merge()`). This is the task where all the pieces meet.

**Files:**
- Modify: `internal/pipeline/prpass.go`
- Modify: `internal/tracker/config.go` (adds `Limits.MaxMergeRefusals` — same file as Task 1, different struct)
- Modify: `internal/tracker/config_test.go` (workflow YAML fixtures need the new limit)
- Modify: `internal/tracker/marker.go` (adds `EventMergeRefused` + `MergeRefusals` helper)
- Modify: `workflow.yaml` (the real, shipped graph — add `limits.max_merge_refusals`, update the `pr:` block's comment)
- Test: `internal/pipeline/prpass_test.go`

**Interfaces:**
- Consumes: `project.PRBranch()` (Task 1), `o.Workspaces.BaseAdvanced(repo, branch, base) (bool, error)` (Task 2), `impl.Merge(url) error` where `impl forge.Forge` (Task 3), `tracker.MergeRefusals(comments, role) int` (this task, mirrors `tracker.PushFailures`), `o.Workflow.Limits.MaxMergeRefusals int` (this task).
- Produces: `o.prConflict(task, project, url string, textConflict bool) error` (signature change — was 3 args), `o.prMerged(task, project, url) error` (signature change — was 2 args, now takes `project` so it can name the real merge branch), `o.attemptMerge(task, project, url) error`, `o.mergeRefused(task, project, url, mergeErr) error`.

### Background

Read `internal/pipeline/prpass.go` in full before starting — it's ~490 lines and this task touches `openPR`, `followPR`, `prConflict`, and adds two new functions. The design doc (`docs/superpowers/specs/2026-09-08-pr-auto-merge-design.md`, section "Механика слияния") has the reference pseudocode; this plan gives the exact diffs.

Today's `openPR` (lines 77–167) does, in relevant part:

```go
	merge, err := o.Workspaces.MergeCheck(repo, project.Branch(task.Key), project.DefaultBranch)
	if err != nil {
		o.logf("%s: слияние не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	switch {
	case merge.Empty():
		return o.prAnomaly(task, fmt.Sprintf(
			"Сливать нечего: в ветке %s нет коммитов, которых не было бы в %s. "+
				"Открывать pull request не из чего. Работа либо не дошла до ветки, либо уже в основной. "+
				"Это аномалия, а не провал прогона: попытка не потрачена.",
			project.Branch(task.Key), project.DefaultBranch))
	case merge.Conflict:
		return o.prConflict(task, project, "")
	}
	...
	url, err := impl.OpenPR(task.Project, project.Branch(task.Key), project.DefaultBranch, title, body)
```

Today's `followPR` (lines 170–207) does, in relevant part:

```go
	merge, err := o.Workspaces.MergeCheck(repo, project.Branch(task.Key), project.DefaultBranch)
	if err != nil {
		o.logf("%s: слияние не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	if merge.Conflict {
		return o.prConflict(task, project, url)
	}
	return nil
```

Today's `prConflict` (lines 214–239) takes `(task, project, url)` and always says "не сливается".

Today's `prMerged` (lines 265–280) takes `(task, url)` — no `project`, so it can't name the merge branch.

- [x] **Step 1: Write the failing tests**

The existing `fakeForge` in `internal/pipeline/prpass_test.go` (around line 25) implements only `OpenPR`/`PRState`. It must implement `Merge` too, or the package won't compile once `forge.Forge` grows a third method. Update it:

```go
type fakeForge struct {
	url      string      // адрес, который вернёт OpenPR
	state    forge.State // что ответить про открытый PR
	openErr  error
	stateErr error
	mergeErr error // что вернёт Merge; nil — успех

	opened []openedPR
	asked  []string
	merged []string // адреса, по которым звали Merge
}
```

```go
func (f *fakeForge) Merge(url string) error {
	f.merged = append(f.merged, url)
	return f.mergeErr
}
```

Append these tests after `TestPRPassRefusalAsksHuman`:

```go
// auto_merge.enabled: гейт чист → офис сам мержит и уводит задачу в Done,
// без единого касания человека.
func TestPRPassAutoMergeMergesCleanGate(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/20", state: forge.Open})
	project := o.Projects["OFF"]
	project.AutoMerge = tracker.AutoMerge{Enabled: true}
	o.Projects["OFF"] = project
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t) // pull request открыт
	o.pass(t) // followPR видит чистый гейт и мержит сам

	if len(f.merged) != 1 {
		t.Fatalf("Merge вызван %d раз, ожидался один", len(f.merged))
	}
	task := o.get(t, "OFF-1")
	if task.Status != "Done" {
		t.Fatalf("статус %q, ожидался Done", task.Status)
	}
	if task.HumanFlag {
		t.Error("auto-merge зовёт человека")
	}
	if !tracker.HasEvent(task.Comments, tracker.EventMerged) {
		t.Error("в переписке нет записи о слиянии")
	}
}

// auto_merge выключен — сегодняшнее поведение не сломано: на чистом гейте
// задача остаётся на месте, Merge не вызывается.
func TestPRPassHumanMergeUnaffectedByGate(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/21", state: forge.Open})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)
	o.pass(t)

	if len(f.merged) != 0 {
		t.Errorf("Merge вызван без auto_merge: %v", f.merged)
	}
	if task := o.get(t, "OFF-1"); task.Status != "Approved" {
		t.Errorf("статус %q, ожидался Approved", task.Status)
	}
}

// База продвинулась вперёд без единого текстового конфликта — тот же возврат
// к разработчику, что и при конфликте, и на human-merge проекте тоже:
// расширенный триггер действует везде, не только на auto-merge.
func TestPRPassAdvancedBaseTriggersReturnWithoutConflict(t *testing.T) {
	o := newOffice(t)
	o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/22", state: forge.Open})
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")
	o.pass(t) // pull request открыт

	// Правка в базе, которая НЕ пересекается с рабочим деревом ветки задачи —
	// без единого маркера конфликта.
	pushDefault(t, o.origin, "отдельный-файл.txt", "новое в базе\n")

	o.pass(t)

	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Fatalf("статус %q, ожидался Ready", task.Status)
	}
	if !tracker.HasEvent(task.Comments, tracker.EventMergeConflict) {
		t.Error("расширенный триггер не сработал")
	}
	if body := lastComment(t, task).Body; !strings.Contains(body, "продвинулась вперёд") {
		t.Errorf("текст не назвал причину — продвижение базы, а не конфликт:\n%s", body)
	}
}

// Отказ forge при локально чистом состоянии — не работа implementer'а:
// счётчик копится по маркерам, эскалация — по достижении предела
// (max_merge_refusals: 3 в workflow.yaml).
func TestPRPassMergeRefusalEscalatesAtLimit(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/23", state: forge.Open, mergeErr: forge.ErrRefused})
	project := o.Projects["OFF"]
	project.AutoMerge = tracker.AutoMerge{Enabled: true}
	o.Projects["OFF"] = project
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")
	o.pass(t) // pull request открыт

	o.pass(t) // отказ 1
	if task := o.get(t, "OFF-1"); task.Status != "Approved" || task.HumanFlag {
		t.Fatalf("после первого отказа: статус %q, human %v — рано звать человека", task.Status, task.HumanFlag)
	}
	o.pass(t) // отказ 2
	if task := o.get(t, "OFF-1"); task.Status != "Approved" || task.HumanFlag {
		t.Fatalf("после второго отказа: статус %q, human %v — предел ещё не достигнут", task.Status, task.HumanFlag)
	}
	o.pass(t) // отказ 3 — предел
	task := o.get(t, "OFF-1")
	if task.Status != "Blocked" || !task.HumanFlag {
		t.Fatalf("после третьего отказа: статус %q, human %v — ожидалась эскалация", task.Status, task.HumanFlag)
	}
	if len(f.merged) != 3 {
		t.Errorf("Merge вызван %d раз, ожидалось три", len(f.merged))
	}
}
```

Also add a config-level test case to `internal/tracker/config_test.go`'s `TestLoadWorkflowRejectsBrokenGraph` table (mirroring the existing `"предел неудачных пушей не задан"` row) — this needs `validWorkflow` to already contain `max_merge_refusals: 3` (see Step 3 below):

```go
		{
			name: "предел отказов мержа не задан",
			yaml: strings.Replace(validWorkflow, "max_merge_refusals: 3", "max_merge_refusals: 0", 1),
			want: "max_merge_refusals",
		},
```

- [x] **Step 2: Run tests to verify they fail**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go build ./... 2>&1 | head -40`
Expected: compile failures — `fakeForge` doesn't implement `forge.Forge` (missing `Merge`) is fixed by the test file edit above, but `tracker.AutoMerge`/`o.Workspaces.BaseAdvanced`/`impl.Merge` referenced by the new prpass_test.go tests should already compile after Tasks 1–3; what won't compile/pass yet is `tracker.MergeRefusals`, `o.Workflow.Limits.MaxMergeRefusals`, `o.attemptMerge`, `prConflict`'s new 4th argument, and `prMerged`'s new `project` argument — all added in Step 3.

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go test ./internal/tracker/... -run TestLoadWorkflowRejectsBrokenGraph -v`
Expected: FAIL — `max_merge_refusals` isn't in `validWorkflow` yet.

- [x] **Step 3: Implement — `Limits.MaxMergeRefusals` and `EventMergeRefused`**

In `internal/tracker/config.go`, add the field to `Limits` (after `MaxIdleRuns`, before `LeaseMarginSec`):

```go
	// MaxIdleRuns — сколько прогонов роли подряд может не дойти до результата,
	// прежде чем задачу отдадут человеку. Считает записи двух видов одним
	// счётчиком: «не начинал» и «не успел». Общий он потому, что следствие
	// у них одно, а два раздельных счётчика чередование обошло бы.
	MaxIdleRuns    int `yaml:"max_idle_runs"`
	// MaxMergeRefusals — сколько раз подряд forge может отказать в мерже
	// при локально чистом состоянии (auto_merge.enabled), прежде чем задачу
	// отдадут человеку. Отдельный предел по той же причине, что у push
	// failures: это не работа implementer'а — локально мержить нечего.
	MaxMergeRefusals int `yaml:"max_merge_refusals"`
	LeaseMarginSec int `yaml:"lease_margin_sec"`
```

Add validation in `Workflow.validate()`, after the `MaxIdleRuns` check:

```go
	if w.Limits.MaxIdleRuns <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_idle_runs=%d: ожидается положительное число", w.Limits.MaxIdleRuns))
	}
	if w.Limits.MaxMergeRefusals <= 0 {
		errs = append(errs, fmt.Errorf("limits.max_merge_refusals=%d: ожидается положительное число", w.Limits.MaxMergeRefusals))
	}
```

In `internal/tracker/config_test.go`, add `  max_merge_refusals: 3\n` to the `limits:` block of all three workflow YAML fixtures — `validWorkflow`, `twoRoleWorkflow`, and `prWorkflow` — otherwise every test that loads them will now fail validation. For example, `validWorkflow`'s `limits:` block becomes:

```
limits:
  max_attempts: 3
  max_lease_expiries: 3
  max_push_failures: 3
  max_idle_runs: 3
  max_merge_refusals: 3
  lease_margin_sec: 300
```
(same addition, in-place, for `twoRoleWorkflow` and `prWorkflow`).

In `internal/tracker/marker.go`, add the event constant next to `EventMergeConflict`:

```go
	// EventMergeConflict — ветка задачи не сливается с веткой по умолчанию.
	// Это работа, а не провал: попытка не тратится. В семейство состояния
	// не входит — открытый PR конфликт не закрывает.
	EventMergeConflict = "merge-conflict"
	// EventMergeRefused — forge отказал в слиянии pull request'а при
	// локально чистом состоянии (auto_merge.enabled). Это не работа
	// implementer'а — локально мержить нечего.
	EventMergeRefused = "merge-refused"
```

Add the counting helper next to `PushFailures`:

```go
// MergeRefusals — сколько раз подряд forge отказывал в мерже при локально
// чистом состоянии. Считается тем же правилом, что PushFailures/LeaseExpiries:
// это беда стороннего сервиса (или его правила, о котором офис не знает),
// а не провал агента.
func MergeRefusals(comments []Comment, role string) int {
	return eventStreak(comments, role, EventMergeRefused)
}
```

In `workflow.yaml` (repo root), add the limit and update the now-inaccurate `pr:` block comment:

```yaml
# По умолчанию сливает человек. На проекте с явным auto_merge.enabled
# (projects.local.yaml) — сливает офис сам, детерминированным кодом, тем же
# проходом: гейт — Approved плюс актуальная база, без роли и без окна ожидания.
pr:
  role: office
  from: Approved            # разбор прошёл — пора открывать PR
  merged: Done              # человек слил
  conflict: Ready           # ветка не сливается: это работа, а не провал
  closed: Blocked           # PR закрыли без слияния — это разговор с человеком

limits:
  max_attempts: 3           # исчерпав попытки, задача уходит в Blocked к человеку
  max_return_rounds: 3      # отчётов одной роли одному и тому же владельцу подряд
  lease_margin_sec: 300     # аренда = таймаут роли + этот запас

  max_lease_expiries: 3
  max_push_failures: 3
  max_idle_runs: 3

  # Сколько раз подряд forge может отказать в мерже (auto_merge.enabled) при
  # локально чистом состоянии, прежде чем задачу отдадут человеку. Считает
  # маркеры event:merge-refused.
  max_merge_refusals: 3
```
(insert the `max_merge_refusals` block and its comment after the existing `max_idle_runs` block; leave the surrounding comments on `max_lease_expiries`/`max_push_failures`/`max_idle_runs` as they already are — only the new block and the `pr:` header comment change).

- [x] **Step 4: Run the config-layer tests**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go test ./internal/tracker/... -v 2>&1 | tail -60`
Expected: PASS, including `TestShippedConfigIsValid` (which loads the real `workflow.yaml`) and the new `"предел отказов мержа не задан"` case.

- [x] **Step 5: Implement — `prpass.go`**

**Implementation note (task review round 1 finding, fixed):** the plan's `mergeRefused` snippet below escalates via `return o.prAnomaly(...)`, which records `Event: tracker.EventPRClosed`. This was found to be a defect during task review: the PR isn't actually closed at that point (only refused-to-merge), and `EventPRClosed` is one of exactly two members of `prEvents` that `advancePR` routes on — writing it there corrupts routing (a human's later fix would route through `openPR` instead of `followPR`, opening a second PR on an already-open branch, dead-ending in a permanent Blocked loop). **What was actually built instead:** the escalation branch records a new, dedicated `tracker.EventMergeRefusalsExhausted` marker (kept outside `prEvents`, mirroring how `pushFailed` escalates via `EventPushFailuresExhausted` rather than reusing an unrelated state-family event), then moves the task to `o.Workflow.PR.Closed` and sets the human flag directly — without ever calling `prAnomaly` from this path. See `internal/pipeline/prpass.go`'s actual `mergeRefused` and `internal/tracker/marker.go`'s `EventMergeRefusalsExhausted` for the shipped code; the snippet below is the plan's original (superseded) version, kept for historical context.

Replace `openPR`'s merge-check block (the `merge, err := ...` through the `switch { case merge.Empty(): ...; case merge.Conflict: ... }`) with:

```go
	merge, err := o.Workspaces.MergeCheck(repo, project.Branch(task.Key), project.PRBranch())
	if err != nil {
		// Слияемость не посчитана — это беда обвязки, а не ответ о задаче.
		// Двигать задачу нельзя: следующий проход попробует снова.
		o.logf("%s: слияние не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	if merge.Empty() {
		return o.prAnomaly(task, fmt.Sprintf(
			"Сливать нечего: в ветке %s нет коммитов, которых не было бы в %s. "+
				"Открывать pull request не из чего. Работа либо не дошла до ветки, либо уже в основной. "+
				"Это аномалия, а не провал прогона: попытка не потрачена.",
			project.Branch(task.Key), project.PRBranch()))
	}
	advanced, err := o.Workspaces.BaseAdvanced(repo, project.Branch(task.Key), project.PRBranch())
	if err != nil {
		o.logf("%s: продвижение базы не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	if merge.Conflict || advanced {
		return o.prConflict(task, project, "", merge.Conflict)
	}
```

Further down in `openPR`, change the `OpenPR` call:

```go
	url, err := impl.OpenPR(task.Project, project.Branch(task.Key), project.PRBranch(), title, body)
```

Replace `followPR`'s tail (from `// PR открыт. Ветка по умолчанию...` to the end of the function) with:

```go
	// PR открыт. База с тех пор могла уехать вперёд — текстовым конфликтом
	// или без него.
	merge, err := o.Workspaces.MergeCheck(repo, project.Branch(task.Key), project.PRBranch())
	if err != nil {
		o.logf("%s: слияние не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	advanced, err := o.Workspaces.BaseAdvanced(repo, project.Branch(task.Key), project.PRBranch())
	if err != nil {
		o.logf("%s: продвижение базы не проверено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
	if merge.Conflict || advanced {
		return o.prConflict(task, project, url, merge.Conflict)
	}
	if !project.AutoMerge.Enabled {
		return nil // как сегодня: гейт чист, ждём человека
	}
	return o.attemptMerge(task, project, url)
```

And update the one line above it that still calls `prMerged` with the old signature:

```go
	switch state {
	case forge.Merged:
		return o.prMerged(task, project, url)
```

Replace `prConflict` entirely:

```go
// prConflict возвращает задачу в работу: и текстовый конфликт, и продвинувшаяся
// без конфликта база — это работа, а не провал.
//
// Попытка не тратится и pull request не закрывается: задача вернётся сюда после
// разбора, и тот же PR подхватит её — в переписке последней остаётся запись
// об открытии.
func (o *Office) prConflict(task tracker.Task, project tracker.Project, url string, textConflict bool) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	to := o.Workflow.PR.Conflict
	by := tracker.BySystem()

	kind := "конфликт"
	reason := fmt.Sprintf("Ветка %s не сливается с %s.", project.Branch(task.Key), project.PRBranch())
	if !textConflict {
		kind = "продвижение базы"
		reason = fmt.Sprintf("База %s продвинулась вперёд с тех пор, как ветка %s была создана — "+
			"конфликта нет, но контекст мог устареть.", project.PRBranch(), project.Branch(task.Key))
	}

	// Про открытый pull request говорится, только если он есть. Проблема бывает
	// найдена и до открытия — тогда обещать, что «PR подхватит задачу», значит
	// врать: подхватывать нечему. Поймано живой проверкой на GitHub.
	fate := "Pull request откроется, когда работа вернётся сюда."
	if url != "" {
		fate = fmt.Sprintf("Pull request %s остаётся открытым и подхватит задачу, когда она вернётся.", url)
	}
	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMergeConflict, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("%s Задача возвращается в %s: слить базу и разрешить, если есть что, — "+
		"это работа, а не провал, и счётчик попыток не тронут. База уже принесена "+
		"в клон, сеть для слияния не нужна. %s",
		reason, to, fate)); err != nil {
		return err
	}
	o.logf("%s: %s, задача возвращается в %s", task.Key, kind, to)
	return o.move(task, by, to)
}
```

Replace `prMerged` entirely:

```go
// prMerged закрывает жизнь задачи: работа слита в ветку, куда метил PR-проход.
func (o *Office) prMerged(task tracker.Task, project tracker.Project, url string) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	to := o.Workflow.PR.Merged
	by := tracker.BySystem()
	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMerged, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Pull request %s слит в %s. Задача уходит в %s, рабочая папка больше не нужна "+
		"и будет убрана.", url, project.PRBranch(), to)); err != nil {
		return err
	}
	o.logf("%s: pull request слит, задача уходит в %s", task.Key, to)
	return o.move(task, by, to)
}
```

Add two new functions after `prMerged` (before `prAnomaly`):

```go
// attemptMerge сливает pull request сам, когда гейт чист и проект явно
// доверил офису слияние (auto_merge.enabled). Никакого окна ожидания сверх
// гейта нет: как только он пройден, попытка идёт на этом же тике.
func (o *Office) attemptMerge(task tracker.Task, project tracker.Project, url string) error {
	impl, known := o.Forges[project.Forge]
	if !known {
		o.logf("%s: forge %q не собран, задача остаётся на месте", task.Key, project.Forge)
		return nil
	}
	switch err := impl.Merge(url); {
	case err == nil:
		return o.prMerged(task, project, url)
	case errors.Is(err, forge.ErrRefused):
		return o.mergeRefused(task, project, url, err)
	default:
		// Сбой связи. Локально всё ещё чисто — следующий тик попробует снова.
		o.logf("%s: слияние не выполнено, задача остаётся на месте: %v", task.Key, err)
		return nil
	}
}

// mergeRefused разбирается с окончательным отказом forge мержить pull
// request, который локально выглядит чистым (гейт прошёл MergeCheck и
// BaseAdvanced).
//
// Это не работа implementer'а: локально мержить нечего, он честно отчитается
// done, и цикл повторится вслепую. Счётчик — по образцу max_push_failures:
// считается по маркерам в переписке, не полем трекера.
func (o *Office) mergeRefused(task tracker.Task, project tracker.Project, url string, mergeErr error) error {
	runID, err := runner.NewRunID()
	if err != nil {
		return err
	}
	by := tracker.BySystem()
	refusals := tracker.MergeRefusals(task.Comments, o.Workflow.PR.Role) + 1

	if err := o.record(task.Key, by, tracker.Marker{
		RunID: runID, Role: o.Workflow.PR.Role, Event: tracker.EventMergeRefused, ConfigSHA: o.ConfigSHA,
	}, fmt.Sprintf("Forge отказал в слиянии pull request %s, хотя локально гейт чист (нет конфликта, "+
		"база %s не продвинулась): %v. Разбираться с этим — не работа implementer'а: смотреть надо "+
		"на правило forge, о котором офис не знает (например, branch protection).",
		url, project.PRBranch(), mergeErr)); err != nil {
		return err
	}

	if refusals >= o.Workflow.Limits.MaxMergeRefusals {
		o.logf("%s: forge отказывает в мерже подряд %d раз, задача уходит к человеку", task.Key, refusals)
		return o.prAnomaly(task, fmt.Sprintf(
			"Forge отказывает в слиянии %d раз подряд (limits.max_merge_refusals) при локально чистом "+
				"состоянии. Pull request: %s", refusals, url))
	}
	o.logf("%s: forge отказал в слиянии, задача остаётся в очереди прохода: %v", task.Key, mergeErr)
	return nil
}
```

`errors` and `forge` are already imported in `prpass.go` (see its import block: `context`, `errors`, `fmt`, `path/filepath`, `regexp`, `strings`, plus `internal/forge`, `internal/runner`, `internal/tracker`, `internal/workspace`) — no new imports needed.

- [x] **Step 6: Run the pipeline tests**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go test ./internal/pipeline/... -v 2>&1 | tail -150`
Expected: PASS for every test — the four new ones from Step 1, and every pre-existing `TestPRPass*` (they all use human-merge projects with `AutoMerge` zero-valued, so `attemptMerge` is never reached and behavior is identical to before). Pay particular attention to `TestPRPassConflictReturnsWork` and `TestPRPassReusesPullRequestAfterConflict` — they must still pass with `prConflict`'s new 4th argument (`merge.Conflict` is `true` in both, so `textConflict` is `true` and the wording matches the old text closely enough that the existing substring assertions (`tracker.HasEvent(..., EventMergeConflict)`, `strings.Contains(body, f.url)`) still hold.

- [x] **Step 7: Full regression across the whole module**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go build ./... && go vet ./... && go test ./...`
Expected: clean. This is the first point where the whole feature compiles together end-to-end.

- [x] **Step 8: Commit**

```bash
git add internal/pipeline/prpass.go internal/pipeline/prpass_test.go internal/tracker/config.go internal/tracker/config_test.go internal/tracker/marker.go workflow.yaml
git commit -m "feat(pipeline): wire auto-merge gate, widened staleness trigger, and merge-refusal escalation into the PR pass"
```

Follow-up fix commit (task review round 1): `b84d19c` — "fix(pipeline): stop lying pr-closed on merge-refusal escalation; add safety-gate negative test" (see Step 5's implementation note above).

---

## Task 5: `roles/implementer/role.md`

Corresponds to `tasks.md` §5 (5.1–5.2). No code dependency on Tasks 1–4, but conceptually follows them (it documents the widened trigger and the now-variable base branch that Task 2 introduced).

**Files:**
- Modify: `roles/implementer/role.md`

**Interfaces:** None (prose only — no tests; this file is a prompt, not code, and the codebase has no automated check for role prompt wording beyond `TestShippedConfigIsValid`'s structural check on `workflow.yaml`, which this task doesn't touch).

### Background

Lines 121–133 of `roles/implementer/role.md` today read:

```markdown
### Если ветка не сливается

Переписка в контексте может сказать, что ветка задачи не сливается с веткой
по умолчанию. Это не провал прогона и не упрёк: пока работа ждала слияния, ветка
по умолчанию ушла вперёд.

Тогда работа такая: слей ветку по умолчанию в свою (`git merge origin/<ветка
по умолчанию>` — она уже принесена в репозиторий, сеть не нужна), разреши конфликт,
прогони тесты и заверши `done` с `next_owner: reviewer`. Ревьюер разберёт слияние
как обычную работу.

Не переписывай историю ветки: `rebase` и `reset` тебе не выданы, и это намеренно
— история ветки общая, её уже видели и ревьюер, и человек.
```

This is wrong on two counts after Tasks 2 and 4: (1) it hardcodes "ветка по умолчанию" as the merge target, but the real base is whatever `internal/runner/input.go`'s `composeContext` printed as `"- Базовая ветка: %s\n"` (from `BaseBranch`, now `"origin/" + project.PRBranch()` per Task 2) — on an auto-merge project with `target_branch` set, that's the integration branch, not the repo's real default branch; (2) it only describes the text-conflict case, but the trigger (Task 4) now also fires when the base simply advanced with no conflict at all.

- [x] **Step 1: Edit the file**

Replace lines 121–133 with:

```markdown
### Если база продвинулась вперёд

Переписка в контексте может сказать, что база задачи (строка «Базовая ветка:»
в контексте запуска) продвинулась вперёд с тех пор, как ветка задачи была
создана — с текстовым конфликтом или без него. Это не провал прогона и не
упрёк: пока работа ждала разбора и слияния, база ушла вперёд.

Тогда работа такая: слей базовую ветку из контекста в свою (`git merge
origin/<базовая ветка>` — она уже принесена в репозиторий, сеть не нужна),
разреши конфликт, если он есть, прогони тесты и заверши `done` с
`next_owner: reviewer`. Ревьюер разберёт слияние как обычную работу — даже
если разрешать было нечего, сам факт, что база продвинулась, стоит того,
чтобы пройти по коду ещё раз.

Не переписывай историю ветки: `rebase` и `reset` тебе не выданы, и это намеренно
— история ветки общая, её уже видели и ревьюер, и человек.
```

Do not rename the `## Выход` heading or anything after it — only this one subsection changes.

- [x] **Step 2: Sanity-check the file still reads coherently**

Run: `sed -n '100,135p' /Users/aleksejkolesnikov/IdeaProjects/virtual-office/roles/implementer/role.md`
Confirm: the preceding "### Если прошлый прогон был усечён" section and the following "## Выход" section are untouched, and the new section flows from them.

- [x] **Step 3: Commit**

```bash
git add roles/implementer/role.md
git commit -m "docs(implementer): stop hardcoding the default branch in the merge-conflict procedure; widen its trigger"
```

---

## Task 6: Documentation

Corresponds to `tasks.md` §6 (6.1–6.3). Best done after Task 4 lands (so the prose matches shipped behavior), but has no compile dependency on any other task.

**Files:**
- Modify: `docs/DESIGN.md` (§2.8)
- Modify: `README.md` (the PR-pass section)
- Modify: `docs/ONBOARDING.md` (the `projects.local.yaml` walkthrough)

**Interfaces:** None (prose only).

- [x] **Step 1: `docs/DESIGN.md` §2.8**

The section header and three of its bullets need to change. Current header (line 103):

```markdown
### 2.8 Pull request: офис открывает, человек сливает
```
becomes:
```markdown
### 2.8 Pull request: офис открывает, сливает человек или сам офис — по конфигу
```

Current Forge bullet:
```markdown
- **Forge — интерфейс рядом с трекером**, а не часть его: `OpenPR(project, branch, base, title, body) → url` и `PRState(url) → open | merged | closed`. Первая реализация — GitHub через REST, токен из окружения. Проект, у которого forge не задан, живёт по тому же графу: проход вырождается, пишется `event:pr-skipped`, задача уходит в терминальный статус.
```
becomes:
```markdown
- **Forge — интерфейс рядом с трекером**, а не часть его: `OpenPR(project, branch, base, title, body) → url`, `PRState(url) → open | merged | closed` и `Merge(url) → error`. Первая реализация — GitHub через REST, токен из окружения. Проект, у которого forge не задан, живёт по тому же графу: проход вырождается, пишется `event:pr-skipped`, задача уходит в терминальный статус.
```

Current "Сливает человек" bullet:
```markdown
- **Сливает человек.** Авто-слияния нет и на этом этапе не будет: PR читает человек, и это единственная точка, где он обязан быть.
```
becomes:
```markdown
- **Слияние — по умолчанию человек, на явном opt-in — офис.** Проект, не заведший `auto_merge.enabled` в `projects.local.yaml`, живёт по-старому: PR читает и сливает человек. Проект, заведший его, доверяет слияние офису — тем же PR-проходом, без новой роли, как только гейт (следующий пункт) пройден; вето-окна нет. Целевая ветка слияния — `auto_merge.target_branch`, если задана, иначе ветка по умолчанию; она же теперь база, от которой форкаются задачи проекта (`Project.PRBranch()`), — иначе гейт зависимостей (`depends_on`) молча переставал бы что-то значить для проекта с изолированной интеграционной веткой.
```

Current "Конфликт — работа" bullet:
```markdown
- **Конфликт — работа, а не провал.** Задача возвращается разработчику, попытка не тратится, и тот же pull request ждёт её возвращения; второй не открывается.
```
becomes:
```markdown
- **Конфликт — работа, а не провал, и триггер шире одного текстового конфликта.** Задача возвращается разработчику, попытка не тратится, и тот же pull request ждёт её возвращения; второй не открывается. Тот же возврат — на любом проекте, не только auto-merge — срабатывает и когда база просто продвинулась вперёд с момента, когда ветка задачи была срублена, даже без единого конфликтного маркера: контекст, в котором работали implementer и reviewer, мог устареть по смыслу.
```

Leave the other bullets ("Разбор — не конец задачи", "Конфликт слияния считается локально", "Маршрут прохода — в графе", "Состояние pull request выводится из переписки", "Рабочую папку убирает системный проход") untouched.

- [x] **Step 2: `README.md`**

Replace the block from `Дальше работает системный проход` through the paragraph ending `...потому что слияния не было.` (around lines 285–303) with:

```markdown
Дальше работает системный проход — он безролевой, агента у него нет:

- открывает pull request веткой задачи, телом берёт `brief.md` из ветки
  и последний отчёт из тикета, ссылку кладёт комментарием;
- **по умолчанию сливает человек.** На проекте, заведшем `auto_merge.enabled`
  в `projects.local.yaml`, сливает сам офис — тем же проходом,
  детерминированным кодом, без новой роли, — как только гейт чист. Вето-окна
  нет: гейт прошёл — слияние на этом же тике;
- гейт — `Approved` (уже истинно на входе) плюс актуальность базы: ни
  текстового конфликта, ни того, что база продвинулась вперёд с тех пор,
  как ветка задачи была срублена;
- слияние (человеком или офисом) уводит задачу в `Done` — единственный
  терминальный статус;
- закрытый без слияния PR — разговор с человеком: задача уходит в `Blocked`,
  и ответ возвращает её в `Approved`, где PR открывается заново;
- **конфликт слияния — это работа, а не провал**, и это касается не только
  текстового конфликта. Задача возвращается разработчику в `Ready`, счётчик
  попыток не тратится, а тот же PR ждёт её возвращения — на любом проекте,
  не только auto-merge: то же самое происходит и тогда, когда база просто
  продвинулась вперёд без единого конфликтного маркера. Текстовый конфликт
  считается локально, `git merge-tree` по bare-клону; продвижение базы —
  тем же bare-клоном, `git merge-base --is-ancestor`. Ни сети, ни forge для
  этого не нужно.

Куда именно ведёт каждый из этих исходов, записано в `workflow.yaml` блоком `pr`
— как и весь остальной маршрут. У проекта без forge (например, у полигона
на локальном bare-репозитории) проход вырождается: задача уходит в `Done` сразу,
записью `pr-skipped`, потому что слияния не было.
```

- [x] **Step 3: `docs/ONBOARDING.md`**

**Addendum (spec-incremental, found during Task 4 implementation and its fix round):** a fourth file also needed updating — `docs/contracts/tracker-protocol.md` — to document the two new events introduced by Task 4's fix (`event:merge-refused`, `event:merge-refusals-exhausted`) and `limits.max_merge_refusals`. This file wasn't in the plan's original scope for Task 6; see tasks.md item 6.4 and the implementation commit for the added table rows + prose.

In the `projects.local.yaml` walkthrough (around line 414–421), current:

```yaml
OFF:
  repo_url: /tmp/client.git
  tracker: jira          # чей проект: mock или jira
  # forge: github        # если PR нужны
  # worktree_root — не задан, значит ${OFFICE_HOME}/worktrees/<проект>
```
becomes:
```yaml
OFF:
  repo_url: /tmp/client.git
  tracker: jira          # чей проект: mock или jira
  # forge: github        # если PR нужны
  # auto_merge:          # опционально: сливать PR самим, без человека
  #   enabled: true
  #   target_branch: office-integration   # пусто — значит default_branch;
  #                                       # эту ветку заводит человек заранее
  # worktree_root — не задан, значит ${OFFICE_HOME}/worktrees/<проект>
```

- [x] **Step 4: Proofread**

Run: `grep -n "Сливает человек\|Авто-слияния" /Users/aleksejkolesnikov/IdeaProjects/virtual-office/docs/DESIGN.md /Users/aleksejkolesnikov/IdeaProjects/virtual-office/README.md`
Expected: no remaining hits that assert human-merge unconditionally (the DESIGN.md/README.md text above should have replaced all of them). If any remain elsewhere in these files describing this same behavior, update them too — but don't touch unrelated occurrences (e.g. archive/historical notes).

- [x] **Step 5: Commit**

```bash
git add docs/DESIGN.md README.md docs/ONBOARDING.md
git commit -m "docs: describe auto_merge as an explicit per-project opt-in, not an absolute human-merge rule"
```

Shipped commit also included `docs/contracts/tracker-protocol.md` (see Step 3's addendum): `git add docs/DESIGN.md README.md docs/ONBOARDING.md docs/contracts/tracker-protocol.md && git commit -m "docs: describe auto_merge as an explicit per-project opt-in, not an absolute human-merge rule"` — commit `6fcf98a`.

---

## Task 7: Live verification

Corresponds to `tasks.md` §7 (7.1–7.3). Depends on Tasks 1–4 being merged and deployed to a real runner instance with a real GitHub-backed project (per the precedent in `docs/notes/stage-5-live-backlog.md`, which recorded the equivalent live checks for the original human-merge PR pass). This task has no source diff of its own — it's an operational checklist run against a live polygon, and its only artifact is confirmation that the checklist passed (record the outcome wherever this project's practice already records live-run results, e.g. a new dated entry in `docs/notes/`, following the existing `stage-5-live-*.md` convention — but do not create a new file speculatively; check with the project owner where this belongs before writing one).

**Files:** None modified by this task itself, beyond whatever config the live run needs on the runner machine (`${OFFICE_HOME}/projects.local.yaml`, machine-local, not in this repo).

- [ ] **Step 1: Smoke-test auto-merge end to end (`tasks.md` 7.1)**

On a real GitHub-backed polygon project (e.g. the `EXP` project used in prior stage-5 live runs), in that machine's `${OFFICE_HOME}/projects.local.yaml`, set:

```yaml
EXP:
  repo_url: <existing value>
  tracker: jira
  forge: github
  auto_merge:
    enabled: true
    target_branch: office-integration   # create this branch on the remote first — the office will not
```

Before enabling, manually create `office-integration` on the remote from the current default branch (per the design's explicit "the office never invents long-lived branches" rule) — e.g. `git push origin <default-branch>:office-integration`.

Run one task through the full pipeline (`analyst → implementer → reviewer → PR pass`) as usual. Confirm:
- the task's branch forks from `office-integration`, not the repo's real default branch (`git log --oneline origin/office-integration..origin/agent/<task-key>` should show only the task's own commits; check via the worktree or bare clone under `${OFFICE_HOME}/repos/EXP.git`);
- once `Approved`, the PR pass opens a PR targeting `office-integration`;
- on the next tick, the office calls `Merge()` and the task reaches `Done` **without any human comment or click** — check the tracker comment history for `event:merged` written by the `office` account, and confirm no `human_flag` was ever set on this task.

- [ ] **Step 2: Smoke-test the widened `BaseAdvanced` trigger live (`tasks.md` 7.2)**

With a second task on the same (or a similar) live project: after its PR is open (`Approved`), push a commit directly to the target branch (`office-integration` or the project's default branch, matching whichever this task's project targets) that does **not** textually conflict with the task's branch — e.g. edit an unrelated file. Run the PR pass again and confirm:
- the task returns to the implementer's queue (`Ready`) even though there is no git merge conflict;
- the tracker comment names the reason as the base having advanced, not a text conflict (the `mergeRefused`/`prConflict` wording from Task 4, Step 5);
- the implementer's next run sees the updated "Базовая ветка" context (Task 5's role.md wording) and completes the merge-and-continue procedure without confusion.

- [ ] **Step 3: Final full regression (`tasks.md` 7.3)**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go build ./... && go vet ./... && go test ./...`
Expected: clean. This is the same command as Task 4 Step 7, run once more after all documentation and role.md changes have landed, as the final gate before considering the change done.

- [ ] **Step 4: Close out**

Report the two live-run outcomes (Steps 1 and 2) back to the project owner in whatever form this project's practice uses for closing an OpenSpec change (see `docs/openspec/changes/pr-auto-merge/` for the change's own artifacts, and follow the `openspec-verify-change` / `comet-verify` workflow already established in this repo rather than improvising a new one here).
