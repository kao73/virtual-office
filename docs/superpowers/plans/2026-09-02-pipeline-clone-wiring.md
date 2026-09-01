---
change: pipeline-clone-wiring
design-doc: docs/superpowers/specs/2026-09-02-pipeline-clone-wiring-design.md
base-ref: 445a2c1c030a193e462f349b4ee8dc9b1a671b01
---

# Pipeline `--clone` wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire `sbx --clone` into the tracker-driven pipeline (`internal/pipeline`) so role runs on the `sbx` backend execute on sandbox-local storage instead of a bind-mounted worktree, fixing Comet Native's lock-coordinator unreliability on virtiofs — while keeping the exchange directory, agent commits, and `comet-state.yaml` phase progress intact across every outcome and every role handoff.

**Architecture:** A disposable, ordinary (non-bare, non-worktree) git clone of the task branch is created fresh per run from the real worktree, used only as `--clone`'s primary path, and removed unconditionally afterward. `internal/backends/sbx/clone.go`'s sandbox-facing operations (`sbx cp`/`sbx exec`) keep addressing this disposable clone (`Workspaces[0].Path`, renamed `containerRoot`), while every host-facing read/write (exclude file, `Dirs`, the new `comet-state.yaml` glob sync) is redirected to `CloneSync.FetchInto`, the real persistent task worktree — a split that today's only caller (`cmd/run-agent --clone`) never needed because it keeps both paths equal on purpose.

**Tech Stack:** Go 1.26+, `git` (plain clones, worktrees, `git rev-parse --git-common-dir`), `sbx` (Docker Sandboxes CLI) for the live end-to-end task only — all other tasks are pure Go unit tests requiring no live sandbox.

**Spec:** `docs/superpowers/specs/2026-09-02-pipeline-clone-wiring-design.md` (implementation-level design); also see `docs/openspec/changes/pipeline-clone-wiring/proposal.md` (why/what) and `docs/openspec/changes/pipeline-clone-wiring/specs/pipeline-clone-isolation/spec.md` (acceptance scenarios) for full context. This plan implements all five ADDED requirements in that spec file.

## Global Constraints

- Pipeline role runs on the `sbx` backend SHALL execute on sandbox-local storage, not a bind-mounted host directory (spec.md, Requirement 1).
- `docs/comet/changes/*/comet-state.yaml` mutated during a role's run SHALL be present in the persistent task worktree after the run, regardless of whether the role committed it (spec.md, Requirement 2).
- The exchange directory (`.agent/result.json`, `.comet/current-change.json`, `.comet/runtime`) and agent commits SHALL round-trip to the persistent task worktree on every outcome — success, non-zero exit, timeout, or truncation (spec.md, Requirement 3).
- Any disposable, sandbox-only working copy SHALL be removed after the run ends, on every outcome, including timeout (spec.md, Requirement 4).
- On the `local` backend (no sandbox), the run SHALL proceed directly on the host worktree and the pipeline SHALL log that sandbox-local isolation was not applied — never fail silently (spec.md, Requirement 5).
- No data migration; `Launch.Clone` stays `nil` (today's bind-mount behavior) until `SandboxAgent.Run` is updated in the same change — no intermediate half-wired state (design.md, Migration Plan).
- Not touched: `roles/*/role.md`, `internal/pipeline/archive.go`, `cmd/run-agent`, `cmd/eval-roles` (proposal.md, Impact).
- Existing code comments in this repository are Russian; new Go code written for this change follows that established convention (identifiers/config keys stay English per project convention — see root `CLAUDE.md`).
- This is a `tdd_mode: tdd` change: every task below writes a failing test before the implementation that makes it pass.

---

### Task 1: Disposable clone-source step in `internal/workspace`

**Files:**
- Create: `internal/workspace/clone.go`
- Create: `internal/workspace/clone_test.go`

**Interfaces:**
- Produces: `func CloneSource(dir, branch string) (path string, cleanup func() error, err error)` — a package-level function in `internal/workspace`. On success, `path` is a fresh temp directory holding an ordinary (non-bare, non-worktree) git clone of `branch` as checked out in `dir` (which may itself be a worktree — git resolves `.git` transparently), and `cleanup` removes that temp directory. On failure, the temp directory is already removed internally; the caller has nothing left to clean up (`cleanup` and `path` are both zero-valued, do not call `cleanup`). Task 2 calls this with `req.Workdir` and `req.Branch`.

- [x] **Step 1: Write the failing tests**

Create `internal/workspace/clone_test.go`:

```go
package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// worktreeFixture заводит bare-репозиторий и настоящий git worktree на ветке
// branch — тем же приёмом, что setup() в workspace_test.go, но без Manager:
// CloneSource работает над уже готовой рабочей папкой, а не заводит её сама.
func worktreeFixture(t *testing.T, branch string) (bare, dir string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "master", bare).CombinedOutput(); err != nil {
		t.Fatalf("bare-репозиторий не создан: %v\n%s", err, out)
	}

	seed := filepath.Join(root, "seed")
	if out, err := exec.Command("git", "clone", "-q", bare, seed).CombinedOutput(); err != nil {
		t.Fatalf("посевной клон не создан: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# проект\n"), 0o644); err != nil {
		t.Fatalf("README не записан: %v", err)
	}
	gitT(t, seed, "add", "-A")
	gitT(t, seed, "commit", "-q", "-m", "начало")
	gitT(t, seed, "push", "-q", "origin", "master")

	dir = filepath.Join(root, "worktree")
	gitT(t, seed, "worktree", "add", "-q", "-b", branch, dir)
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "работа задачи")
	return bare, dir
}

func TestCloneSourceClonesCurrentBranchTip(t *testing.T) {
	_, dir := worktreeFixture(t, "agent/OFF-1")
	want := gitT(t, dir, "rev-parse", "HEAD")

	clone, cleanup, err := CloneSource(dir, "agent/OFF-1")
	if err != nil {
		t.Fatalf("CloneSource: %v", err)
	}
	defer cleanup()

	if got := gitT(t, clone, "rev-parse", "--abbrev-ref", "HEAD"); got != "agent/OFF-1" {
		t.Errorf("клон стоит на ветке %q, ожидалось agent/OFF-1", got)
	}
	if got := gitT(t, clone, "rev-parse", "HEAD"); got != want {
		t.Errorf("клон стоит на %s, ожидалось %s (тот же коммит, что и в worktree)", got, want)
	}
	// Обычный репозиторий, не worktree: у клона свой собственный .git-каталог,
	// а не файл-ссылка, как у worktree — --clone (sbx) отказывает на worktree,
	// и клон-источник обязан не быть им.
	info, err := os.Stat(filepath.Join(clone, ".git"))
	if err != nil {
		t.Fatalf(".git клона не найден: %v", err)
	}
	if !info.IsDir() {
		t.Error("клон оказался worktree'ем (.git — файл, не каталог), а обязан быть обычным репозиторием")
	}
}

func TestCloneSourceCleanupRemovesTempDir(t *testing.T) {
	_, dir := worktreeFixture(t, "agent/OFF-1")

	clone, cleanup, err := CloneSource(dir, "agent/OFF-1")
	if err != nil {
		t.Fatalf("CloneSource: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Errorf("временный каталог клона %s не убран", clone)
	}
}

// Отсутствие ветки не должно оставлять на диске частичный или пустой клон:
// git clone --branch на несуществующей ветке отказывает сам, и CloneSource
// обязана донести эту ошибку, а не тихо вернуть путь до пустоты.
func TestCloneSourcePropagatesErrorOnMissingBranch(t *testing.T) {
	_, dir := worktreeFixture(t, "agent/OFF-1")

	path, cleanup, err := CloneSource(dir, "нет-такой-ветки")
	if err == nil {
		t.Fatal("клонирование несуществующей ветки прошло без ошибки")
	}
	if path != "" || cleanup != nil {
		t.Errorf("на ошибке ожидались нулевые path/cleanup, получено path=%q cleanup=%v", path, cleanup)
	}
}
```

This file relies on `gitT`, the `git -C <dir> <args...>` test helper already defined in `internal/workspace/workspace_test.go` (same package, no import needed — reused as-is, matching that file's own convention of reusing `gitT` across `workspace_test.go` and `merge_test.go`).

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/workspace/... -run TestCloneSource -v`
Expected: FAIL with `undefined: CloneSource` (compile error) — the function doesn't exist yet.

- [x] **Step 3: Write minimal implementation**

Create `internal/workspace/clone.go`:

```go
package workspace

import (
	"fmt"
	"os"
	"os/exec"
)

// clonePrefix — префикс временных каталогов одноразового клона-источника,
// который заводит CloneSource для internal/pipeline/agent.go (SandboxAgent.Run,
// бэкенд sbx). Простой os.MkdirTemp вне хозяйства Manager: клон одноразовый,
// целиком свой одному прогону и не нуждается в общей уборке репозиториев/
// worktree'ев, которой занимается остальной этот пакет.
const clonePrefix = "pipeline-clone-*"

// CloneSource заводит одноразовый, обычный (не bare, не worktree) git-клон
// ветки branch рабочей папки dir в новый временный каталог. dir может быть
// как обычным репозиторием, так и worktree'ем — git разрешает оба как
// источник клона через .git-файл/-каталог одинаково; branch — уже выкаченная
// в dir ветка задачи (то же значение, что несёт ws.Branch/req.Branch дальше
// по конвейеру).
//
// `sbx create --clone` сам отказывает и на bare-репозитории, и на worktree
// как на своём первичном пути (см. internal/backends/sbx/clone.go, doc-
// комментарий excludeFile) — клон, сделанный здесь, всегда обычный
// репозиторий со своим .git-каталогом и годится туда без исключений.
//
// Возвращает путь к клону и функцию уборки, которую вызывающий обязан звать
// при любом исходе (успех, ошибка агента, таймаут) — тем же приёмом «cleanup
// всегда», каким cloneSyncOut убирает саму песочницу (internal/backends/sbx/
// sbx.go, defer remove(name)).
//
// Ошибка означает, что клон не состоялся: временный каталог в этом случае
// уже убран этой же функцией, и вызывающему чистить нечего — path и cleanup
// оба нулевые, звать cleanup(nil) было бы паникой.
func CloneSource(dir, branch string) (string, func() error, error) {
	tmp, err := os.MkdirTemp("", clonePrefix)
	if err != nil {
		return "", nil, fmt.Errorf("временный каталог клона-источника не заведён: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(tmp) }

	cmd := exec.Command("git", "clone", "--quiet", "--branch", branch, dir, tmp)
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = cleanup()
		return "", nil, fmt.Errorf("клон-источник (%s, ветка %s) не заведён: %w\n%s", dir, branch, err, out)
	}
	return tmp, cleanup, nil
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/workspace/... -run TestCloneSource -v`
Expected: PASS (`TestCloneSourceClonesCurrentBranchTip`, `TestCloneSourceCleanupRemovesTempDir`, `TestCloneSourcePropagatesErrorOnMissingBranch`).

Then run the whole package to confirm nothing else broke:

Run: `go test ./internal/workspace/... -v`
Expected: PASS, all existing tests plus the three new ones green.

- [x] **Step 5: Commit**

```bash
git add internal/workspace/clone.go internal/workspace/clone_test.go
git commit -m "feat(workspace): add disposable clone-source step for pipeline --clone"
```

---

### Task 2: Wire `Options.Clone` into `SandboxAgent.Run`, add `pipeline.Request.Branch`

**Files:**
- Modify: `internal/pipeline/pipeline.go:53-58` (`Request` struct), `internal/pipeline/pipeline.go:443-445` (`work()`'s `Request{...}` construction)
- Modify: `internal/pipeline/agent.go` (whole file — `SandboxAgent.Run` rewritten, new `cloneOptionsFor` helper)
- Create: `internal/pipeline/agent_test.go`
- Modify: `internal/pipeline/pipeline_test.go` (one new test, placed after `TestWorkRecomputesBaseCommitAfterPrepareInputCommit`, around line 395)

**Interfaces:**
- Consumes: `workspace.CloneSource(dir, branch string) (string, func() error, error)` (Task 1).
- Produces: `Request.Branch string` field, read by `SandboxAgent.Run`. `cloneOptionsFor(backend string, req Request) (workdir string, clone *runner.CloneSync, cleanup func() error, err error)` — unexported helper in `internal/pipeline`, used by `Run` and directly unit-tested.

- [ ] **Step 1: Write the failing tests**

Create `internal/pipeline/agent_test.go`:

```go
package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runagent"
	"github.com/kao73/virtual-office/internal/runner"
)

// agentWorktreeFixture — bare-репозиторий и настоящий git worktree на ветке
// branch, тем же приёмом, что internal/workspace/clone_test.go: cloneOptionsFor
// зовёт workspace.CloneSource на уже готовой рабочей папке, а не заводит её сама.
func agentWorktreeFixture(t *testing.T, branch string) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "master", bare).CombinedOutput(); err != nil {
		t.Fatalf("bare-репозиторий не создан: %v\n%s", err, out)
	}
	seed := filepath.Join(root, "seed")
	if out, err := exec.Command("git", "clone", "-q", bare, seed).CombinedOutput(); err != nil {
		t.Fatalf("посевной клон не создан: %v\n%s", err, out)
	}
	env := append(os.Environ(), "GIT_AUTHOR_NAME=тест", "GIT_AUTHOR_EMAIL=test@office.local",
		"GIT_COMMITTER_NAME=тест", "GIT_COMMITTER_EMAIL=test@office.local")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# проект\n"), 0o644); err != nil {
		t.Fatalf("README не записан: %v", err)
	}
	run(seed, "add", "-A")
	run(seed, "commit", "-q", "-m", "начало")
	run(seed, "push", "-q", "origin", "master")

	dir := filepath.Join(root, "worktree")
	run(seed, "worktree", "add", "-q", "-b", branch, dir)
	run(dir, "commit", "-q", "--allow-empty", "-m", "работа задачи")
	return dir
}

func TestCloneOptionsForLocalKeepsRealWorkdirAndNoClone(t *testing.T) {
	req := Request{Workdir: "/куда-угодно", Branch: "agent/OFF-1"}

	workdir, clone, cleanup, err := cloneOptionsFor(runagent.BackendLocal, req)
	if err != nil {
		t.Fatalf("cloneOptionsFor: %v", err)
	}
	if workdir != req.Workdir {
		t.Errorf("workdir = %q, ожидался req.Workdir %q — local обязан остаться на настоящей рабочей папке", workdir, req.Workdir)
	}
	if clone != nil {
		t.Errorf("Clone = %+v, ожидался nil на local", clone)
	}
	if cleanup == nil {
		t.Fatal("cleanup не должен быть nil даже на local — вызывающий зовёт его безусловно")
	}
	if err := cleanup(); err != nil {
		t.Errorf("cleanup на local не должна ничего чистить и не должна падать: %v", err)
	}
}

func TestCloneOptionsForSbxBuildsDisposableCloneSource(t *testing.T) {
	dir := agentWorktreeFixture(t, "agent/OFF-1")
	req := Request{Workdir: dir, Branch: "agent/OFF-1"}

	workdir, clone, cleanup, err := cloneOptionsFor(runagent.DefaultBackend, req)
	if err != nil {
		t.Fatalf("cloneOptionsFor: %v", err)
	}
	defer cleanup()

	if workdir == dir {
		t.Error("workdir не сменился на одноразовый клон-источник")
	}
	if _, err := os.Stat(workdir); err != nil {
		t.Errorf("клон-источник не найден: %v", err)
	}
	if clone == nil {
		t.Fatal("Clone не выставлен для бэкенда sbx")
	}
	if clone.FetchInto != dir {
		t.Errorf("FetchInto = %q, ожидалась настоящая рабочая папка %q", clone.FetchInto, dir)
	}
	if clone.Branch != "agent/OFF-1" {
		t.Errorf("Branch = %q, ожидалось agent/OFF-1", clone.Branch)
	}
	if len(clone.Dirs) != 2 || clone.Dirs[0] != runner.Dir || clone.Dirs[1] != ".comet/runtime" {
		t.Errorf("Dirs = %q, ожидалось [%s .comet/runtime]", clone.Dirs, runner.Dir)
	}

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("клон-источник %s не убран", workdir)
	}
}

func TestCloneOptionsForSbxPropagatesCloneSourceFailure(t *testing.T) {
	dir := agentWorktreeFixture(t, "agent/OFF-1")
	req := Request{Workdir: dir, Branch: "нет-такой-ветки"}

	if _, _, _, err := cloneOptionsFor(runagent.DefaultBackend, req); err == nil {
		t.Fatal("неудача клона-источника (несуществующая ветка) прошла без ошибки")
	}
}
```

Add the branch-threading test to `internal/pipeline/pipeline_test.go`, right after `TestWorkRecomputesBaseCommitAfterPrepareInputCommit`'s closing `}` (around line 395, before `TestTickRecordsRunInLedger`):

```go
// Задача 2 (docs/superpowers/specs/2026-09-02-pipeline-clone-wiring-design.md):
// SandboxAgent.Run строит одноразовый клон-источник --clone от той же ветки,
// на которой стоит рабочая папка задачи, — work() обязана донести её агенту
// в Request.Branch, а не заставлять его снова спрашивать об этом трекер.
func TestWorkPassesTaskBranchToAgent(t *testing.T) {
	o := newOffice(t)

	if !o.tick(t) {
		t.Fatal("цикл не взял задачу")
	}

	if o.agent.seen.Branch != "agent/OFF-1" {
		t.Errorf("Request.Branch = %q, ожидалась agent/OFF-1", o.agent.seen.Branch)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pipeline/... -run 'TestCloneOptionsFor|TestWorkPassesTaskBranchToAgent' -v`
Expected: FAIL to compile — `cloneOptionsFor` is undefined, and `Request` has no field `Branch`.

- [ ] **Step 3: Add `Branch` to `Request` and thread it through `work()`**

In `internal/pipeline/pipeline.go`, modify the `Request` struct (around line 51-58):

```go
// Request — что конвейер отдаёт агенту. Каталог обмена к этому моменту
// уже подготовлен: постановка, контекст и паспорт лежат в рабочей папке.
type Request struct {
	Role     runner.Role
	Workdir  string
	Passport runner.Run
	Mounts   []runner.Workspace
	// Branch — ветка задачи, на которой стоит рабочая папка (то же значение,
	// что PrepareInput уже отдавал контексту через runner.Input.Branch).
	// SandboxAgent.Run использует её для одноразового клона-источника
	// --clone (internal/workspace.CloneSource) — см. docs/superpowers/specs/
	// 2026-09-02-pipeline-clone-wiring-design.md.
	Branch string
}
```

In `work()` (around line 443-445), add `Branch: ws.Branch` to the `Request{...}` literal:

```go
	run, runErr := o.Agent.Run(ctx, Request{
		Role: role, Workdir: ws.Dir, Passport: passport, Mounts: ws.Mounts(), Branch: ws.Branch,
	})
```

- [ ] **Step 4: Rewrite `internal/pipeline/agent.go`**

Replace the whole file:

```go
package pipeline

import (
	"context"
	"fmt"
	"io"

	"github.com/kao73/virtual-office/internal/runagent"
	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/workspace"
)

// SandboxAgent — настоящий прогон агента: тот же путь, которым идёт ручной
// `run-agent`, только рабочую папку и контекст готовит конвейер.
type SandboxAgent struct {
	ConfigRoot string
	Backend    string
	Log        io.Writer
}

// cloneDirs — то же самое, что cmd/run-agent/main.go называет --clone'у своим
// Dirs: каталог обмена и кэш локального исполнения .comet/runtime — то, что
// git-клон не приносит сам (см. internal/backends/sbx/clone.go).
var cloneDirs = []string{runner.Dir, ".comet/runtime"}

// cloneOptionsFor решает, каким Workdir'ом и Clone'ом снабдить runagent.Execute
// для этого прогона, и отдаёт функцию уборки одноразового клона-источника.
//
// Вынесена из Run отдельно ради проверяемости: решение не трогает ни бэкенд,
// ни настоящего агента, и юнит-тест не должен тратиться на живой sbx или
// claude ради него.
//
// На бэкенде local изоляции --clone нет вовсе (см. runagent.CloneNotice) —
// прогон идёт прямо по req.Workdir, как и раньше, и Clone остаётся nil:
// local.Run это поле не смотрит по контракту, а держать его пустым здесь —
// не полагаться на это молча. cleanup всё равно небустой: вызывающий зовёт
// его безусловно, и на local это просто no-op.
//
// На остальных бэкендах (sbx) заводится одноразовый клон-источник
// (internal/workspace.CloneSource) от той же ветки, что несёт req.Branch —
// PrepareInput конвейера успевает отработать раньше (pipeline.go, work()),
// и клон захватывает любой её системный коммит. Неудача клонирования — повод
// провалить прогон целиком: тихого отката на бинд-маунт нет, задача останется
// арендованной, и её вернёт reaper (docs/superpowers/specs/
// 2026-09-02-pipeline-clone-wiring-design.md).
func cloneOptionsFor(backend string, req Request) (workdir string, clone *runner.CloneSync, cleanup func() error, err error) {
	if backend == runagent.BackendLocal {
		return req.Workdir, nil, func() error { return nil }, nil
	}

	src, cleanupSrc, err := workspace.CloneSource(req.Workdir, req.Branch)
	if err != nil {
		return "", nil, nil, fmt.Errorf("клон-источник для --clone не заведён: %w", err)
	}
	return src, &runner.CloneSync{
		FetchInto: req.Workdir,
		Branch:    req.Branch,
		Dirs:      cloneDirs,
	}, cleanupSrc, nil
}

// Run исполняет прогон.
//
// Ошибку возвращает только то, из-за чего прогона не случилось: конвейер
// понимает её как «задача осталась арендованной, вернёт reaper». Всё, что
// произошло с самим агентом, приходит исходом — включая синтетический failed.
func (a SandboxAgent) Run(ctx context.Context, req Request) (AgentRun, error) {
	// Сеть роли закрывает песочница. Бэкенд, который её не закрывает, обязан
	// сказать об этом вслух: молчание читалось бы как «применено».
	if notice := runagent.NetworkNotice(a.Backend, req.Role.Network.Allow); notice != "" {
		a.logf("%s: %s", req.Passport.TaskKey, notice)
	}
	// А закрывает ли что-нибудь сама машина — вопрос отдельный: базовую политику
	// ставит человек, и без неё список роли ничего не ограничивает. Спрашивается
	// это перед каждым прогоном, а не при старте раннера: политику машины меняют
	// одной командой, и прогон обязан говорить о той сети, которую получил сам.
	if notice := runagent.NetworkAudit(a.Backend); notice != "" {
		a.logf("%s: %s", req.Passport.TaskKey, notice)
	}
	// Изоляция --clone запрашивается конвейером всегда: применимость зависит
	// только от бэкенда, и об этом надо сказать вслух там, где он её не даёт —
	// тем же приёмом, что и NetworkNotice выше.
	if notice := runagent.CloneNotice(a.Backend, true); notice != "" {
		a.logf("%s: %s", req.Passport.TaskKey, notice)
	}

	workdir, clone, cleanup, err := cloneOptionsFor(a.Backend, req)
	if err != nil {
		return AgentRun{}, err
	}
	defer func() {
		if err := cleanup(); err != nil {
			a.logf("%s: клон-источник --clone не убран: %v", req.Passport.TaskKey, err)
		}
	}()

	opts := runagent.Options{
		ConfigRoot: a.ConfigRoot,
		Role:       req.Role,
		Workdir:    workdir,
		Backend:    a.Backend,
		Passport:   req.Passport,
		Clone:      clone,
	}
	// Mounts и Clone несовместимы (runagent.Prepare это проверяет сама) —
	// бинд-маунт нужен только там, где --clone нет вовсе.
	if clone == nil {
		opts.Mounts = req.Mounts
	}

	out, err := runagent.Execute(ctx, opts)
	// Пределы поставщика — наблюдение, а не решение: исход прогона от них
	// не зависит, но человек о них узнаёт. Об открытом окне не говорится —
	// оно открыто у каждого прогона, и строка о нём стояла бы над каждым.
	if notice := out.Limit.Notice(); notice != "" {
		a.logf("%s: %s", req.Passport.TaskKey, notice)
	}

	run := AgentRun{Result: out.Result, Usage: out.Usage, Termination: out.Termination}

	if err != nil {
		// Исход есть — значит прогон состоялся, а сорвалось что-то после него
		// (например, архивация). Хоронить из-за этого задачу незачем, но и молчать
		// нельзя: беда уходит в лог раннера.
		if out.Result.Outcome != "" {
			a.logf("%s: %v", req.Passport.TaskKey, err)
			return run, nil
		}
		return AgentRun{}, err
	}
	a.logf("%s: прогон кончился как %s, лог %s, архив %s",
		req.Passport.TaskKey, out.Termination.Kind, out.LogPath, out.Archive)
	return run, nil
}

func (a SandboxAgent) logf(format string, args ...any) {
	if a.Log == nil {
		return
	}
	fmt.Fprintf(a.Log, format+"\n", args...)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/pipeline/... -run 'TestCloneOptionsFor|TestWorkPassesTaskBranchToAgent' -v`
Expected: PASS for all four new tests.

Then run the whole package (this package's existing suite exercises `pipeline.Office` against `fakeAgent`, not `SandboxAgent`, so it should be unaffected by this change; this confirms that):

Run: `go test ./internal/pipeline/... -v`
Expected: PASS, no regressions.

Then confirm the whole repo still builds and vets clean (agent.go now imports `runner` and `workspace`, both already dependencies of this module):

Run: `go build ./... && go vet ./...`
Expected: clean, no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/pipeline/agent.go internal/pipeline/agent_test.go internal/pipeline/pipeline.go internal/pipeline/pipeline_test.go
git commit -m "feat(pipeline): wire sbx --clone into SandboxAgent.Run via disposable clone source"
```

---

### Task 3: Split "container path root" from "host path root" in `internal/backends/sbx/clone.go`

**Files:**
- Modify: `internal/backends/sbx/clone.go` (`cloneSyncIn`, `syncExcludeFile`, new `resolveExcludeFile`, `cloneSyncOut`'s `Dirs` loop; `commitLeftovers`, `clearStaleCometLocks`, `fetchBranch` keep their own internal `primary` parameter name and body unchanged — only the value passed in at the call site changes name)
- Modify: `internal/backends/sbx/clone_test.go` (rewrite the `cloneSyncIn`/`syncExcludeFile` tests to use two distinct roots; add a real-worktree exclude-file test; add a `cloneSyncOut` Dirs-routing test)

**Interfaces:**
- Consumes: `runner.Launch.Workspaces[0].Path` (unchanged type), `runner.Launch.Clone.FetchInto`/`.Dirs` (unchanged types, from Task 2's caller).
- Produces: `resolveExcludeFile(hostRoot string) (string, error)` — new unexported helper, worktree-safe resolution of the host `info/exclude` path (fast path for an ordinary repo, `git rev-parse --git-common-dir` for a worktree). `syncExcludeFile`'s signature changes to `func syncExcludeFile(ctx context.Context, name, containerRoot, hostRoot string, run step) error`.

- [ ] **Step 1: Write the failing tests**

In `internal/backends/sbx/clone_test.go`, add a new test for worktree-safe exclude-file resolution (this exercises the not-yet-existing new `syncExcludeFile` signature — 4 args instead of 3 — so it won't compile until Step 3 lands):

```go
// Задача 3 (docs/superpowers/specs/2026-09-02-pipeline-clone-wiring-design.md):
// хостовой источник exclude-правил теперь — l.Clone.FetchInto, и в
// конвейерном случае это настоящий git worktree, а не обычный репозиторий.
// У worktree'а .git — файл-ссылка, а не каталог, и info/exclude лежит
// не рядом с рабочей копией, а в общем git-каталоге основного репозитория.
func TestSyncExcludeFileResolvesRealWorktree(t *testing.T) {
	root := t.TempDir()
	mainRepo := filepath.Join(root, "main")
	if out, err := exec.Command("git", "init", "-q", "-b", "master", mainRepo).CombinedOutput(); err != nil {
		t.Fatalf("репозиторий не создан: %v\n%s", err, out)
	}
	runGit(t, mainRepo, "commit", "-q", "--allow-empty", "-m", "начало")

	worktree := filepath.Join(root, "worktree")
	runGit(t, mainRepo, "worktree", "add", "-q", "-b", "agent/OFF-1", worktree)

	commonExclude := filepath.Join(mainRepo, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(commonExclude), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(commonExclude, []byte("/.agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	containerRoot := t.TempDir()
	rec := &recordedStep{}

	if err := syncExcludeFile(context.Background(), "office-test", containerRoot, worktree, rec.run); err != nil {
		t.Fatalf("syncExcludeFile: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("ожидался 1 вызов, получено %d: %q", len(rec.calls), rec.calls)
	}
	want := []string{"cp", commonExclude, "office-test:" + filepath.Join(containerRoot, ".git/info") + "/"}
	if !slices.Equal(rec.calls[0], want) {
		t.Errorf("worktree-источник не разрешён верно\nполучено:  %q\nожидалось: %q", rec.calls[0], want)
	}
}
```

Also add a `cloneSyncOut` routing test proving `Dirs` addresses the sandbox via `Workspaces[0].Path` and the host via `FetchInto` when they differ (this compiles today but asserts on call args that only become correct after Step 3's `cloneSyncOut` rewrite, so it's red until then):

```go
// Задача 3: Workspaces[0].Path (одноразовый клон-источник) и l.Clone.FetchInto
// (настоящая рабочая папка задачи) — теперь разные пути, и Dirs обязана
// адресовать песочницу через первый, а хост — через второй.
func TestCloneSyncOutDirsUseContainerRootAndFetchIntoSeparately(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1", Dirs: []string{".agent"}},
	}
	rec := &recordedStep{errs: []error{exitErrWithCode(1), exitErrWithCode(1)}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, io.Discard); err != nil {
		t.Fatalf("cloneSyncOut: %v", err)
	}

	wantSrc := name + ":" + filepath.Join(primary, ".agent")
	wantDst := filepath.Dir(filepath.Join(fetchInto, ".agent")) + "/"
	var found bool
	for _, call := range rec.calls {
		if len(call) == 3 && call[0] == "cp" && call[1] == wantSrc {
			found = true
			if call[2] != wantDst {
				t.Errorf("host-сторона cp = %q, ожидалось %q (FetchInto, не Workspaces[0].Path)", call[2], wantDst)
			}
		}
	}
	if !found {
		t.Fatalf("cp .agent с сандбокс-стороной %q не позван: %q", wantSrc, rec.calls)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/backends/sbx/... -run 'TestSyncExcludeFileResolvesRealWorktree|TestCloneSyncOutDirsUseContainerRootAndFetchIntoSeparately' -v`
Expected: FAIL to compile (`syncExcludeFile` called with 5 args, current signature takes 4) — `TestSyncExcludeFileResolvesRealWorktree` won't build until Step 3. `TestCloneSyncOutDirsUseContainerRootAndFetchIntoSeparately` compiles today (same 5-arg `cloneSyncOut` signature) but currently PASSES for the wrong reason (today `Workspaces[0].Path` happens to be what `Dirs` still reads on both sides in this exact assertion shape) — re-run it after Step 3 specifically to confirm it exercises the real split; note in the commit that this second test is a routing *regression guard* landing green from Step 3 onward, not a red-then-green case on its own until the full `cloneSyncIn` split (this task) makes `primary`/`containerRoot` genuinely diverge in the rest of the suite.

- [ ] **Step 3: Implement the split in `internal/backends/sbx/clone.go`**

Replace `cloneSyncIn` (currently lines 61-109):

```go
// cloneSyncIn заносит в песочницу --clone то, чего не видно git-клону:
// правила git-исключения каталога обмена (excludeFile) и сами каталоги вне
// git (l.Clone.Dirs) — git-клон копирует только закоммиченное, а .agent
// и кэш локального исполнения .comet/runtime/ исключены из git нарочно
// (runner.ExcludeAgentDir, runner.ExcludeCometRuntime).
//
// containerRoot (l.Workspaces[0].Path) адресует только то, что sbx cp/sbx exec
// видят ВНУТРИ песочницы — тот путь, куда sbx create --clone сам клонировал
// свой источник. Всё, что читается с хоста (exclude-файл, содержимое Dirs),
// идёт через l.Clone.FetchInto — настоящую рабочую папку задачи, которая
// может не совпадать с containerRoot (пайплайновый клон-источник —
// одноразовый и отдельный от неё; см. internal/workspace.CloneSource).
// У сегодняшнего ручного `run-agent --clone` оба пути равны нарочно, и для
// него поведение не меняется.
//
// Возвращает те из l.Clone.Dirs, что реально нашлись на хосте на момент
// прогона: cloneSyncOut использует этот список, чтобы отличить «каталога
// в песочнице не было и на входе — не наше упущение» от «агент должен был
// его увидеть, а не увидел — это беда».
func cloneSyncIn(ctx context.Context, name string, l *runner.Launch, run step) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, cloneSyncTimeout)
	defer cancel()

	containerRoot := l.Workspaces[0].Path

	if err := syncExcludeFile(ctx, name, containerRoot, l.Clone.FetchInto, run); err != nil {
		return nil, fmt.Errorf("правила git-исключения не занесены: %w", err)
	}

	var present, paths []string
	for _, dir := range l.Clone.Dirs {
		hostSrc := filepath.Join(l.Clone.FetchInto, dir)
		containerDst := filepath.Join(containerRoot, dir)
		switch _, err := os.Stat(hostSrc); {
		case errors.Is(err, os.ErrNotExist):
			continue // .comet/ бывает не заведён вовсе — задача без изменения Comet Native
		case err != nil:
			return nil, fmt.Errorf("%s не проверен: %w", hostSrc, err)
		}
		// .comet/ может не существовать внутри свежего клона вовсе (роль ещё
		// не закоммитила .comet/config.yaml, и .comet/runtime/ тем более не
		// заведён) — тогда sbx cp либо откажет, либо поведёт себя
		// недокументированно. Заводим родителя явно, а не полагаемся на то,
		// что цель уже есть.
		if err := run(ctx, "exec", name, "mkdir", "-p", filepath.Dir(containerDst)); err != nil {
			return nil, fmt.Errorf("%s внутри песочницы не заведён: %w", filepath.Dir(containerDst), err)
		}
		if err := run(ctx, "cp", hostSrc, name+":"+filepath.Dir(containerDst)+"/"); err != nil {
			return nil, err
		}
		present = append(present, dir)
		paths = append(paths, containerDst)
	}
	if len(paths) == 0 {
		return present, nil
	}

	// sbx cp заносит каталог с хостовым владельцем (измерено вживую): без
	// chown агент внутри песочницы (uid agent) не может в него писать.
	// chown зовётся внутри песочницы, поэтому paths здесь — контейнерные пути.
	args := append([]string{"exec", "-u", "root", name, "chown", "-R", "agent:agent"}, paths...)
	if err := run(ctx, args...); err != nil {
		return nil, err
	}

	if err := clearStaleCometLocks(ctx, name, containerRoot, present, run); err != nil {
		return nil, err
	}
	return present, nil
}
```

Replace `syncExcludeFile` (currently lines 148-163) and add `resolveExcludeFile` right before it:

```go
// resolveExcludeFile находит, где на хосте на самом деле лежат правила
// git-исключения для hostRoot: у обычного репозитория это hostRoot/excludeFile
// напрямую (быстрый путь, без обращения к git — сегодняшний единственный
// вызывающий, cmd/run-agent --clone, всегда именно такой), а у worktree'а
// .git — файл-ссылка, а не каталог, и общий git-каталог (где реально лежит
// info/exclude) резолвится через `git rev-parse --git-common-dir`, тем же
// приёмом, что уже применяют appendExcludeRules/removeExcludeRule
// (internal/runner/input.go).
func resolveExcludeFile(hostRoot string) (string, error) {
	info, err := os.Stat(filepath.Join(hostRoot, ".git"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Хостовая папка ещё не git-репозиторий (сегодня такого не бывает
		// ни у одного вызывающего) — путь всё равно возвращаем, дальнейший
		// os.Stat в syncExcludeFile сам законно ответит «источника нет».
		return filepath.Join(hostRoot, excludeFile), nil
	case err != nil:
		return "", fmt.Errorf("%s/.git не проверен: %w", hostRoot, err)
	case info.IsDir():
		return filepath.Join(hostRoot, excludeFile), nil
	}

	// .git — файл: hostRoot — worktree, общий git-каталог лежит не здесь.
	out, err := exec.Command("git", "-C", hostRoot, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("общий git-каталог %s не определён: %w", hostRoot, err)
	}
	common := strings.TrimSpace(string(out))
	if !filepath.IsAbs(common) {
		common = filepath.Join(hostRoot, common)
	}
	return filepath.Join(common, "info", "exclude"), nil
}

// syncExcludeFile переносит хостовые правила git-исключения внутрь свежего
// git-клона песочницы. containerRoot — куда класть внутри песочницы (sbx
// cp/sbx exec это видят); hostRoot — l.Clone.FetchInto, где эти правила
// реально лежат на хосте (resolveExcludeFile разрешает worktree-случай).
// Источника может не быть только если эта рабочая папка никогда не проходила
// через runner.PrepareInput — такого сегодня не бывает ни у одного
// вызывающего, но падать на этом незачем: без файла агент просто увидит
// .agent/.comet как некомментированную грязь, что не хуже сегодняшнего
// поведения без --clone вовсе.
func syncExcludeFile(ctx context.Context, name, containerRoot, hostRoot string, run step) error {
	src, err := resolveExcludeFile(hostRoot)
	if err != nil {
		return err
	}
	switch _, err := os.Stat(src); {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("%s не проверен: %w", src, err)
	}
	dst := filepath.Join(containerRoot, excludeFile)
	return run(ctx, "cp", src, name+":"+filepath.Dir(dst)+"/")
}
```

In `cloneSyncOut` (currently lines 226-268), rename the local variable and retarget the `Dirs` loop:

```go
func cloneSyncOut(ctx context.Context, name string, l *runner.Launch, run step, presentOnEntry []string, log io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), cloneSyncTimeout)
	defer cancel()

	containerRoot := l.Workspaces[0].Path

	sweepErr := commitLeftovers(ctx, log, name, containerRoot, l.Clone.Dirs, run)
	if sweepErr != nil {
		fmt.Fprintf(log, "\nпесочница %s: подчистка незакоммиченного не удалась: %v\n", name, sweepErr)
	}
	if err := fetchBranch(ctx, name, containerRoot, l.Clone); err != nil {
		return err
	}

	for _, dir := range l.Clone.Dirs {
		containerSrc := filepath.Join(containerRoot, dir)
		hostDst := filepath.Join(l.Clone.FetchInto, dir)
		if err := os.MkdirAll(filepath.Dir(hostDst), 0o755); err != nil {
			return fmt.Errorf("%s на хосте не заведён: %w", filepath.Dir(hostDst), err)
		}
		err := run(ctx, "cp", name+":"+containerSrc, filepath.Dir(hostDst)+"/")
		if err == nil {
			continue
		}
		// Каталога в песочнице могло не быть вовсе: не каждая задача заводит
		// .comet/. Различаем по тексту, а не молчим о разнице совсем — но
		// только для того, чего не было и на входе: .agent заносит cloneSyncIn
		// сама, и его отсутствие на выходе — не законный случай, а повод
		// не молчать.
		if strings.Contains(err.Error(), notFoundInContainer) && !slices.Contains(presentOnEntry, dir) {
			continue
		}
		return err
	}
	return sweepErr
}
```

Leave `commitLeftovers`, `mergeInProgress`, `clearStaleCometLocks`, and `fetchBranch` bodies and their own `primary` parameter names untouched — every operation inside them addresses the sandbox (`sbx exec`) or reads `c.FetchInto` already (`fetchBranch`); only the value now passed in at the call site (`containerRoot`, formerly `primary`) changed name.

- [ ] **Step 4: Update the existing `cloneSyncIn`/`syncExcludeFile` tests for the two-root split**

These tests currently construct `Launch{Workspaces: [{Path: primary}], Clone: &runner.CloneSync{Dirs: ...}}` without `FetchInto`, and `cloneSyncIn`'s Dirs loop now reads the host side from `FetchInto` instead of `Workspaces[0].Path` — every one of these tests needs a distinct `containerRoot` and must set `FetchInto` to what was previously the sole `primary`. Replace each in `internal/backends/sbx/clone_test.go`:

```go
func TestCloneSyncInSyncsExcludeFileDirsAndChowns(t *testing.T) {
	hostRoot := primaryWithExclude(t, ".agent")
	containerRoot := t.TempDir()
	agentDirHost := filepath.Join(hostRoot, ".agent")
	agentDirContainer := filepath.Join(containerRoot, ".agent")
	// .comet намеренно не заводим: задача без активного изменения Comet Native
	// его не имеет вовсе, и это законный случай, а не пропуск.

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: containerRoot}},
		Clone:      &runner.CloneSync{FetchInto: hostRoot, Dirs: []string{".agent", ".comet"}},
	}
	rec := &recordedStep{}

	present, err := cloneSyncIn(context.Background(), "office-test", l, rec.run)
	if err != nil {
		t.Fatalf("cloneSyncIn: %v", err)
	}
	if !slices.Equal(present, []string{".agent"}) {
		t.Errorf("present = %q, ожидалось [.agent]", present)
	}

	if len(rec.calls) != 4 {
		t.Fatalf("ожидалось 4 вызова (exclude + mkdir + cp + chown), получено %d: %q", len(rec.calls), rec.calls)
	}
	excludeCall := rec.calls[0]
	wantExclude := []string{"cp", filepath.Join(hostRoot, excludeFile), "office-test:" + filepath.Join(containerRoot, ".git/info") + "/"}
	if !slices.Equal(excludeCall, wantExclude) {
		t.Errorf("перенос exclude\nполучено:  %q\nожидалось: %q", excludeCall, wantExclude)
	}
	mkdir := rec.calls[1]
	wantMkdir := []string{"exec", "office-test", "mkdir", "-p", containerRoot}
	if !slices.Equal(mkdir, wantMkdir) {
		t.Errorf("mkdir -p\nполучено:  %q\nожидалось: %q", mkdir, wantMkdir)
	}
	cp := rec.calls[2]
	wantCP := []string{"cp", agentDirHost, "office-test:" + containerRoot + "/"}
	if !slices.Equal(cp, wantCP) {
		t.Errorf("cp\nполучено:  %q\nожидалось: %q", cp, wantCP)
	}
	chown := rec.calls[3]
	if chown[0] != "exec" || chown[1] != "-u" || chown[2] != "root" {
		t.Fatalf("chown не запущен от root: %q", chown)
	}
	if !slices.Contains(chown, agentDirContainer) {
		t.Errorf("chown не назвал %s: %q", agentDirContainer, chown)
	}
	if slices.ContainsFunc(chown, func(s string) bool { return strings.Contains(s, ".comet") }) {
		t.Errorf("chown зацепил несуществующий .comet: %q", chown)
	}
}

func TestCloneSyncInClearsStaleLocksFromCometRuntime(t *testing.T) {
	hostRoot := primaryWithExclude(t, ".comet/runtime")
	containerRoot := t.TempDir()
	locksDir := filepath.Join(hostRoot, ".comet/runtime/native/locks")
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locksDir, "root-move.lock"), []byte("stale pid"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: containerRoot}},
		Clone:      &runner.CloneSync{FetchInto: hostRoot, Dirs: []string{".comet/runtime"}},
	}
	rec := &recordedStep{}

	if _, err := cloneSyncIn(context.Background(), "office-test", l, rec.run); err != nil {
		t.Fatalf("cloneSyncIn: %v", err)
	}

	last := rec.calls[len(rec.calls)-1]
	want := []string{"exec", "office-test", "rm", "-rf", filepath.Join(containerRoot, runner.CometRuntimeLocksRel)}
	if !slices.Equal(last, want) {
		t.Errorf("устаревшие locks не убраны последним вызовом\nполучено:  %q\nожидалось: %q", last, want)
	}
}

func TestCloneSyncInNoopWhenNothingToSync(t *testing.T) {
	hostRoot := t.TempDir() // ни .git/info/exclude, ни .agent, ни .comet не заведены
	containerRoot := t.TempDir()

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: containerRoot}},
		Clone:      &runner.CloneSync{FetchInto: hostRoot, Dirs: []string{".agent", ".comet"}},
	}
	rec := &recordedStep{}

	present, err := cloneSyncIn(context.Background(), "office-test", l, rec.run)
	if err != nil {
		t.Fatalf("cloneSyncIn: %v", err)
	}
	if len(present) != 0 {
		t.Errorf("present = %q, ожидалось пусто", present)
	}
	if len(rec.calls) != 0 {
		t.Errorf("sbx позван, хотя заносить было нечего: %q", rec.calls)
	}
}

func TestCloneSyncInPropagatesCPFailure(t *testing.T) {
	hostRoot := primaryWithExclude(t, ".agent")
	containerRoot := t.TempDir()

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: containerRoot}},
		Clone:      &runner.CloneSync{FetchInto: hostRoot, Dirs: []string{".agent"}},
	}
	// Первый вызов — перенос exclude, он должен пройти; второй — mkdir -p,
	// тоже должен пройти; беда — на самом cp каталога.
	rec := &recordedStep{errs: []error{nil, nil, errors.New("sbx cp: connection refused")}}

	if _, err := cloneSyncIn(context.Background(), "office-test", l, rec.run); err == nil {
		t.Fatal("неудача sbx cp потеряна")
	}
	if len(rec.calls) != 3 {
		t.Errorf("после упавшего cp прогремел ещё вызов: %q", rec.calls)
	}
}

func TestSyncExcludeFileCopiesHostRules(t *testing.T) {
	hostRoot := primaryWithExclude(t)
	containerRoot := t.TempDir()
	rec := &recordedStep{}

	if err := syncExcludeFile(context.Background(), "office-test", containerRoot, hostRoot, rec.run); err != nil {
		t.Fatalf("syncExcludeFile: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("ожидался 1 вызов, получено %d: %q", len(rec.calls), rec.calls)
	}
	want := []string{"cp", filepath.Join(hostRoot, excludeFile), "office-test:" + filepath.Join(containerRoot, ".git/info") + "/"}
	if !slices.Equal(rec.calls[0], want) {
		t.Errorf("получено:  %q\nожидалось: %q", rec.calls[0], want)
	}
}

func TestSyncExcludeFileNoopWithoutSource(t *testing.T) {
	hostRoot := t.TempDir() // .git/info/exclude не заведён
	containerRoot := t.TempDir()
	rec := &recordedStep{}

	if err := syncExcludeFile(context.Background(), "office-test", containerRoot, hostRoot, rec.run); err != nil {
		t.Fatalf("syncExcludeFile: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("sbx позван без источника: %q", rec.calls)
	}
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/backends/sbx/... -v`
Expected: PASS, all tests in the package green, including the two new tests from Step 1 and the six rewritten tests from Step 4.

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/backends/sbx/clone.go internal/backends/sbx/clone_test.go
git commit -m "refactor(sbx): split container path root from host path root in --clone sync"
```

---

### Task 4: Sync `comet-state.yaml` unconditionally

**Files:**
- Modify: `internal/backends/sbx/clone.go` (add `copyCometStateMatches`, `syncCometState`; wire into `cloneSyncOut`)
- Modify: `internal/backends/sbx/clone_test.go` (add present/absent tests for the new functions; fix three existing `cloneSyncOut` tests whose `recordedStep.errs` sequencing shifts because of the new unconditional call; fix one assertion that becomes too permissive; swap `realSandboxStep` for a comet-state-aware variant in the one real-git `cloneSyncOut` test)

**Interfaces:**
- Consumes: `runner.CometChangesDir` (`"docs/comet/changes"`), `runner.CometChangeDirForName(name string) string` (both already exist in `internal/runner/change.go`).
- Produces: `copyCometStateMatches(fromChangesDir, fetchInto string) error` — pure, real-filesystem function: globs `fromChangesDir/*/comet-state.yaml` and copies each match to `fetchInto/docs/comet/changes/<name>/comet-state.yaml`. `syncCometState(ctx context.Context, name, containerRoot, fetchInto string, run step) error` — pulls the whole `docs/comet/changes` directory out of the sandbox into a scratch temp dir via `run`, then calls `copyCometStateMatches`; a "not found in container" error from the `cp` is a legitimate no-op (no active Comet Native change).

- [ ] **Step 1: Write the failing tests for the new functions**

Add to `internal/backends/sbx/clone_test.go`:

```go
// copyCometStateMatches — присутствие: изменение "demo" нашлось в снимке,
// синхронизация переносит только comet-state.yaml, а не соседние файлы того
// же каталога изменения (их несёт git-слияние, а не эта маска).
func TestCopyCometStateMatchesCopiesEachMatch(t *testing.T) {
	scratch := t.TempDir()
	fetchInto := t.TempDir()

	demoDir := filepath.Join(scratch, "demo")
	if err := os.MkdirAll(demoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(demoDir, "comet-state.yaml"), []byte("phase: verify\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(demoDir, "brief.md"), []byte("# бриф\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyCometStateMatches(scratch, fetchInto); err != nil {
		t.Fatalf("copyCometStateMatches: %v", err)
	}

	want := filepath.Join(fetchInto, runner.CometChangeDirForName("demo"), "comet-state.yaml")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("comet-state.yaml не появился в FetchInto: %v", err)
	}
	if string(got) != "phase: verify\n" {
		t.Errorf("содержимое = %q, ожидалось %q", got, "phase: verify\n")
	}
	if _, err := os.Stat(filepath.Join(fetchInto, runner.CometChangeDirForName("demo"), "brief.md")); err == nil {
		t.Error("brief.md скопирован — синхронизация обязана трогать только comet-state.yaml")
	}
}

// copyCometStateMatches — отсутствие: снимок без единого изменения не должен
// быть ошибкой и не должен ничего создать в FetchInto.
func TestCopyCometStateMatchesNoopWhenNoMatches(t *testing.T) {
	scratch := t.TempDir()
	fetchInto := t.TempDir()

	if err := copyCometStateMatches(scratch, fetchInto); err != nil {
		t.Fatalf("отсутствие совпадений не должно быть ошибкой: %v", err)
	}
	entries, err := os.ReadDir(fetchInto)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("в FetchInto появилось лишнее: %v", entries)
	}
}

// syncCometState — отсутствие: docs/comet/changes нет в песочнице вовсе
// (нет активного изменения Comet Native) — тот же принцип, что уже есть
// у Dirs' отсутствующего .comet/runtime.
func TestSyncCometStateNoopWhenChangesDirMissing(t *testing.T) {
	fetchInto := t.TempDir()
	rec := &recordedStep{errs: []error{errors.New(`ERROR: path ".../docs/comet/changes" not found in container`)}}

	if err := syncCometState(context.Background(), "office-test", "/container/primary", fetchInto, rec.run); err != nil {
		t.Fatalf("отсутствие docs/comet/changes не должно быть ошибкой: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Errorf("ожидался ровно 1 вызов (сам cp), получено %d: %q", len(rec.calls), rec.calls)
	}
}

// syncCometState — настоящая неудача sbx cp обязана дойти до вызывающего,
// а не раствориться в допущении «изменений нет».
func TestSyncCometStatePropagatesRealCPFailure(t *testing.T) {
	fetchInto := t.TempDir()
	rec := &recordedStep{errs: []error{errors.New("sbx cp: connection refused")}}

	if err := syncCometState(context.Background(), "office-test", "/container/primary", fetchInto, rec.run); err == nil {
		t.Fatal("настоящая неудача sbx cp растворилась в допущении «изменений нет»")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/backends/sbx/... -run 'TestCopyCometStateMatches|TestSyncCometState' -v`
Expected: FAIL to compile — `copyCometStateMatches` and `syncCometState` don't exist yet.

- [ ] **Step 3: Implement `copyCometStateMatches` and `syncCometState`, wire into `cloneSyncOut`**

Add to `internal/backends/sbx/clone.go`, after `fetchBranch`/`gitOutput` and before `cloneOutcome`:

```go
// copyCometStateMatches переносит из fromChangesDir (снимок docs/comet/changes,
// уже выгруженный из песочницы на хост — см. syncCometState) только файлы
// comet-state.yaml, по одному на найденное изменение, в fetchInto/docs/comet/
// changes/<name>/comet-state.yaml. Соседние файлы того же каталога изменения
// (brief.md, tasks.md, design.md, spec.md) сюда не идут: их несёт обычное
// git-слияние (fetchBranch выше), а comet-state.yaml — единственное, что роль
// не коммитит и что comet native правит прямыми CLI-вызовами как побочный
// эффект протокола.
//
// Отсутствие совпадений — законный случай (нет ни одного изменения Comet
// Native), не ошибка.
func copyCometStateMatches(fromChangesDir, fetchInto string) error {
	matches, err := filepath.Glob(filepath.Join(fromChangesDir, "*", "comet-state.yaml"))
	if err != nil {
		return fmt.Errorf("маска comet-state.yaml не разобрана: %w", err)
	}
	for _, match := range matches {
		name := filepath.Base(filepath.Dir(match))
		dst := filepath.Join(fetchInto, runner.CometChangeDirForName(name), "comet-state.yaml")
		data, err := os.ReadFile(match)
		if err != nil {
			return fmt.Errorf("%s не прочитан: %w", match, err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return fmt.Errorf("%s на хосте не заведён: %w", filepath.Dir(dst), err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("%s не записан: %w", dst, err)
		}
	}
	return nil
}

// syncCometState подтягивает длящееся состояние Comet Native
// (docs/comet/changes/<name>/comet-state.yaml) из песочницы в fetchInto —
// шагом, независимым от git: роль этот файл не коммитит (comet native правит
// им напрямую как побочным эффектом своих CLI-вызовов), а имя <name>
// изменения заранее не известно, поэтому используется маска по всему
// docs/comet/changes, а не явное имя, как у Dirs.
//
// Выгружает docs/comet/changes целиком в одноразовый хостовой каталог одним
// sbx cp (тем же приёмом, каким Dirs уже выгружает .comet/runtime), затем
// copyCometStateMatches забирает из этого снимка только comet-state.yaml.
// Отсутствие docs/comet/changes в песочнице — законный no-op (нет активного
// изменения Comet Native вовсе), тем же принципом, что уже есть у Dirs'
// отсутствующего .comet/runtime.
func syncCometState(ctx context.Context, name, containerRoot, fetchInto string, run step) error {
	scratch, err := os.MkdirTemp("", "clone-comet-state-*")
	if err != nil {
		return fmt.Errorf("временный каталог для comet-state.yaml не заведён: %w", err)
	}
	defer os.RemoveAll(scratch)

	src := filepath.Join(containerRoot, runner.CometChangesDir)
	if err := run(ctx, "cp", name+":"+src, scratch+"/"); err != nil {
		if strings.Contains(err.Error(), notFoundInContainer) {
			return nil
		}
		return fmt.Errorf("%s не выгружен из песочницы: %w", src, err)
	}

	return copyCometStateMatches(filepath.Join(scratch, filepath.Base(runner.CometChangesDir)), fetchInto)
}
```

Wire it into `cloneSyncOut`, right after the `fetchBranch` call and before the `Dirs` loop:

```go
	if err := fetchBranch(ctx, name, containerRoot, l.Clone); err != nil {
		return err
	}
	// Длящееся состояние Comet Native роль не коммитит — та же потеря данных,
	// что и неудача fetchBranch выше, если её пропустить молча, и трактуется
	// так же: немедленный возврат, до цикла по Dirs.
	if err := syncCometState(ctx, name, containerRoot, l.Clone.FetchInto, run); err != nil {
		return fmt.Errorf("длящееся состояние Comet Native не подтянуто из песочницы %s: %w", name, err)
	}

	for _, dir := range l.Clone.Dirs {
```

- [ ] **Step 4: Fix existing `cloneSyncOut` tests whose call sequencing shifts**

The new `syncCometState` call issues one extra `run()` call between `commitLeftovers` and the `Dirs` loop on every `cloneSyncOut` invocation where `fetchBranch` succeeds. Three `recordedStep`-based tests hard-code `errs` indices that now shift by one; update them in `internal/backends/sbx/clone_test.go`:

```go
func TestCloneSyncOutTreatsMissingContainerPathAsNotFatalOnlyWhenAbsentOnEntry(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1", Dirs: []string{".agent", ".comet"}},
	}
	// Первые два вызова, до fetchBranch, — commitLeftovers (дерево чистое, до
	// коммита дело не доходит); третий — syncCometState: в песочнице нет
	// docs/comet/changes вовсе, sbx cp отвечает так же, как на любой другой
	// отсутствующий путь; дальше — .agent синхронизировался штатно (значит,
	// presentOnEntry его называет), .comet в песочнице не заведён — sbx cp
	// отвечает так, как отвечает вживую (см. notFoundInContainer в clone.go).
	rec := &recordedStep{errs: []error{
		exitErrWithCode(1), exitErrWithCode(1),
		errors.New(`ERROR: path ".../docs/comet/changes" not found in container`),
		nil,
		errors.New(`ERROR: path ".../.comet" not found in container`),
	}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, []string{".agent"}, io.Discard); err != nil {
		t.Fatalf("отсутствие .comet в песочнице (не занесённого на входе) не должно проваливать выгрузку: %v", err)
	}
	if len(rec.calls) != 5 {
		t.Fatalf("ожидалось 5 вызовов (commitLeftovers x2 + syncCometState + 2 cp), получено %d: %q", len(rec.calls), rec.calls)
	}
}

func TestCloneSyncOutFailsWhenExpectedDirMissingOnExit(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1", Dirs: []string{".agent"}},
	}
	// .agent был занесён на входе (presentOnEntry его называет), но на
	// выходе почему-то пропал — это уже не «роль его не завела», а беда.
	// Первые два вызова — commitLeftovers (дерево чистое), третий —
	// syncCometState (законный no-op), дальше — сам упавший cp.
	rec := &recordedStep{errs: []error{
		exitErrWithCode(1), exitErrWithCode(1),
		errors.New(`ERROR: path ".../docs/comet/changes" not found in container`),
		errors.New(`ERROR: path ".../.agent" not found in container`),
	}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, []string{".agent"}, io.Discard); err == nil {
		t.Fatal("пропажа каталога, который сама же занесла cloneSyncIn, прошла молча")
	}
}

func TestCloneSyncOutPropagatesRealCPFailure(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1", Dirs: []string{".agent"}},
	}
	// Первые два вызова — commitLeftovers (дерево чистое), третий —
	// syncCometState (законный no-op), дальше — сам упавший cp.
	rec := &recordedStep{errs: []error{
		exitErrWithCode(1), exitErrWithCode(1),
		errors.New(`ERROR: path ".../docs/comet/changes" not found in container`),
		errors.New("sbx cp: connection refused"),
	}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, io.Discard); err == nil {
		t.Fatal("настоящая неудача sbx cp растворилась в допущении «каталога не было»")
	}
}
```

Fix `TestCloneSyncOutRetrievesDirsEvenWhenCommitLeftoversFails`, whose loose "any cp call happened" assertion now also matches `syncCometState`'s own `cp` call and would pass even if the `.agent` retrieval broke — make it check for the specific `.agent` call:

```go
func TestCloneSyncOutRetrievesDirsEvenWhenCommitLeftoversFails(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)
	source := runGitOutput(t, primary, "remote", "get-url", "sandbox-"+name)
	commit(t, source, "настоящий коммит агента")

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1", Dirs: []string{".agent"}},
	}
	rec := &recordedStep{errs: []error{exitErrWithCode(1), nil, errors.New("git: identity unknown")}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, io.Discard); err == nil {
		t.Fatal("неудача commitLeftovers не вернула ошибку")
	}

	wantAgentSrc := name + ":" + filepath.Join(primary, ".agent")
	var sawCopy bool
	for _, call := range rec.calls {
		if len(call) == 3 && call[0] == "cp" && call[1] == wantAgentSrc {
			sawCopy = true
		}
	}
	if !sawCopy {
		t.Error(".agent не подтянут после провалившейся подчистки — sweepErr отменил retrieval")
	}
}
```

`TestCloneSyncOutStopsAtDirsWhenFetchFails` (fetchBranch itself fails, so `syncCometState` is never reached) and `TestCloneSyncOutPropagatesCommitLeftoversFailure`/`TestCloneSyncOutFetchesRealCommitsEvenWhenCommitLeftoversFails` (neither asserts a call count) need no changes.

Finally, fix `TestCommitLeftoversReachesFetchIntoThroughFetchBranch`, the one test that drives `cloneSyncOut` through `realSandboxStep` (real `exec` calls, no faking) — `realSandboxStep` only understands `exec` argv and will now `t.Fatalf` the moment `syncCometState` issues its `cp` call, since that call was never exercised before this task. Add a thin wrapper right after `realSandboxStep`'s definition in `internal/backends/sbx/clone_test.go`:

```go
// realSandboxStepNoCometState — realSandboxStep, но отвечает «не найдено» на
// любой cp-вызов вместо того, чтобы пытаться исполнить его по-настоящему:
// source в тестах этого файла — голый репозиторий без docs/comet/changes,
// и настоящий sbx cp на таком пути ответил бы тем же текстом. realSandboxStep
// сама умеет только exec, поэтому syncCometState's cp здесь нуждается
// в отдельном перехвате, а не в расширении realSandboxStep под cp вообще.
func realSandboxStepNoCometState(t *testing.T, primary, source string) step {
	t.Helper()
	inner := realSandboxStep(t, primary, source)
	return func(ctx context.Context, args ...string) error {
		if len(args) == 3 && args[0] == "cp" {
			return errors.New(`ERROR: path ".../docs/comet/changes" not found in container`)
		}
		return inner(ctx, args...)
	}
}
```

And change `TestCommitLeftoversReachesFetchIntoThroughFetchBranch`'s call from `realSandboxStep(t, primary, source)` to `realSandboxStepNoCometState(t, primary, source)`:

```go
	if err := cloneSyncOut(context.Background(), name, l, realSandboxStepNoCometState(t, primary, source), nil, io.Discard); err != nil {
		t.Fatalf("cloneSyncOut: %v", err)
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/backends/sbx/... -v`
Expected: PASS, every test in the package green — the four new tests from Step 1, and every previously-existing test (including the four fixed in Step 4 and the two carried over unchanged from Task 3).

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/backends/sbx/clone.go internal/backends/sbx/clone_test.go
git commit -m "feat(sbx): sync comet-state.yaml out of --clone sandboxes unconditionally"
```

---

### Task 5: End-to-end verification

**Files:** none modified — this task only runs commands and inspects live state.

**Interfaces:** none new.

- [ ] **Step 1: Full build/vet/test pass**

Run from the repo root:

```bash
go build ./... && go vet ./... && go test ./...
```

Expected: all three commands exit 0; `go test ./...` reports `ok` for every package, including `internal/workspace`, `internal/pipeline`, and `internal/backends/sbx` (Tasks 1-4's new and modified tests all included).

- [ ] **Step 2: Live pipeline run through Comet Native Shape → Build → Verify on the `sbx` backend, via the `Office` conveyor**

This must go through `./bin/runner tick` (the tracker-driven `Office` conveyor), not a manual `run-agent --clone` invocation — the whole point of this change is the pipeline's own wiring, and a manual run would validate the wrong code path.

Set up a scratch environment, mirroring `README.md`'s "От нуля до первой задачи" but pointed at a fresh `OFFICE_HOME` so it doesn't collide with any existing local setup:

```bash
export OFFICE_HOME=$(mktemp -d)
export CLAUDE_CODE_OAUTH_TOKEN='...'   # or ANTHROPIC_API_KEY — see docs/notes/auth.md

git init --bare -b master /tmp/pcw-client.git
git clone -q /tmp/pcw-client.git /tmp/pcw-client
git -C /tmp/pcw-client -c user.name=you -c user.email=you@local commit -q --allow-empty -m init
git -C /tmp/pcw-client push -q origin master

mkdir -p "$OFFICE_HOME"
cat > "$OFFICE_HOME/projects.local.yaml" <<'EOF'
OFF:
  repo_url: /tmp/pcw-client.git
  tracker: mock
EOF

go build -o bin/runner ./cmd/runner
./bin/runner mock add OFF-1 --status Analysis \
  --summary "Добавить hello.py" \
  --description "Создай hello.py, печатающий приветствие, и тест к нему. Закоммить."
```

Before starting, capture the baseline sandbox list: `sbx ls --quiet` (expect empty or unrelated to this run).

Run the conveyor through all three roles plus a final systems pass, same cadence as the README quick-start (`--backend` defaults to `sbx`, so no flag needed):

```bash
./bin/runner tick   # analyst: plan
./bin/runner tick   # implementer: build
./bin/runner tick   # reviewer: verify
./bin/runner tick   # systems pass: PR/cleanup
```

After each tick, check the **persistent** task worktree (not a vanished sandbox) for `docs/comet/changes/*/comet-state.yaml` and confirm its `phase`/loop-stage field has advanced relative to the previous tick — this is exactly the "phase state survives role-to-role handoffs" acceptance scenario from `spec.md`. The persistent worktree lives under `${OFFICE_HOME}/worktrees/OFF/OFF-1` by default (`internal/workspace/workspace.go`'s `worktreeRoot`, unless `projects.local.yaml` overrides `worktree_root`):

```bash
cat "$OFFICE_HOME"/worktrees/OFF/OFF-1/docs/comet/changes/*/comet-state.yaml
```

After the full run, confirm no leaked state:

```bash
sbx ls --quiet   # expect back to baseline — no lingering sandbox from this run
ls "$TMPDIR" 2>/dev/null | grep pipeline-clone   # expect no match — disposable clone source removed
```

Also confirm via `./bin/runner mock show OFF-1` and `git -C /tmp/pcw-client.git log --oneline agent/OFF-1` that the task reached a terminal state with real commits, the same shape as the README quick-start's happy path.

- [ ] **Step 3: Confirm the `local` backend still proceeds directly on the host worktree and logs the non-application notice**

Using the same scratch `OFFICE_HOME`/client repo (or a fresh task), run one role on `local`:

```bash
./bin/runner mock add OFF-2 --status Ready \
  --summary "Задача на local" --description "Любая мелкая правка."
./bin/runner tick --backend local --role implementer 2>&1 | tee /tmp/pcw-local.log
```

Expected: `/tmp/pcw-local.log` contains the exact `runagent.CloneNotice` text — `"бэкенд local --clone не поддерживает: у него нет песочницы, которую можно клонировать, — агент бежит прямо в рабочей папке, как обычно"` — and the run's commits land directly in `${OFFICE_HOME}/worktrees/OFF/OFF-2` (no disposable clone source directory is created at all for this run, since `cloneOptionsFor` returns `req.Workdir` unchanged on `local`).

- [ ] **Step 4: Record findings**

If Steps 2-3 surface any behavior that diverges from the design doc's Testing Strategy or from `spec.md`'s acceptance scenarios, stop and fix it in the relevant earlier task (Task 2, 3, or 4) before proceeding — do not patch around it in Task 5. Once all three steps pass cleanly, the change is complete; no separate commit is needed for this task since it produces no file changes (unless Step 4 required a fix, in which case that fix gets its own commit against the task it belongs to, per this plan's earlier commit-message conventions).

---

## Self-review notes

- **Spec coverage:** Requirement 1 (sandbox-local isolation) — Tasks 1+2. Requirement 2 (`comet-state.yaml` survives handoffs) — Task 4, verified live in Task 5 Step 2. Requirement 3 (exchange directory + commits round-trip on every outcome) — Task 3 (Dirs retargeting) plus the pre-existing `cloneSyncOut`/`fetchBranch`/`commitLeftovers` machinery this plan deliberately leaves untouched; verified live in Task 5 Step 2 (a normal successful run) — a live timeout/truncation scenario is out of scope for this plan's live check (already covered by this file's own existing unit tests, unaffected by this change). Requirement 4 (disposable clone source always removed) — Task 2's `defer cleanup()` plus Task 1's internal-cleanup-on-failure contract, verified live in Task 5 Step 2's `pipeline-clone` tmpdir check. Requirement 5 (`local` backend reports non-application) — Task 2's unconditional `CloneNotice(a.Backend, true)` call, verified live in Task 5 Step 3.
- **Placeholder scan:** every step above carries real Go code or a real shell command; no "TODO"/"add appropriate handling"/"similar to Task N" placeholders.
- **Type consistency:** `CloneSource(dir, branch string) (string, func() error, error)` (Task 1) is called identically in Task 2's `cloneOptionsFor`. `cloneOptionsFor(backend string, req Request) (workdir string, clone *runner.CloneSync, cleanup func() error, err error)` (Task 2) is used identically inside `SandboxAgent.Run`. `syncExcludeFile`'s new 5-argument signature (Task 3) is used consistently across `cloneSyncIn` and every rewritten test. `copyCometStateMatches`/`syncCometState` (Task 4) signatures match between their definitions and every call site, including the new wiring inside `cloneSyncOut`.
