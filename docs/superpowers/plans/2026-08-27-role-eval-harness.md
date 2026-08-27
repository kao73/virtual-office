---
change: role-eval-harness
design-doc: docs/superpowers/specs/2026-08-27-role-eval-harness-design.md
base-ref: 7240c3bc173a62ebc91fc6d5a85e6ef0fc7f48df
---

# Role Eval Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a manually-triggered evaluation harness (`cmd/eval-roles`) that runs office roles (`analyst`/`implementer`/`reviewer`) against declarative golden-case fixtures through the existing `cmd/run-agent` path, and reports deterministic pass/fail/errored results — without eval runs counting against a role's `per_role_daily` production budget.

**Architecture:** A new `cmd/eval-roles` binary (package `main`, one file per responsibility, matching the existing `cmd/runner` and `cmd/run-agent` convention) discovers case directories under `evals/<role>/<case-id>/`, materializes each case's `fixture/` into a disposable one-commit git temp dir, invokes the real `cmd/run-agent --eval` as a subprocess against it, dispatches the case's declared `checks:` (from `expect.yaml`) through a typed `Checker` map, and aggregates + prints a pass/failed/errored summary with a three-way exit code. Separately, `internal/ledger.Entry` gains an `Eval bool` field set by `run-agent --eval`, and `internal/pipeline/budget.go`'s `per_role_daily` query excludes it via a new `ledger.Filter.ExcludeEval` option.

**Tech Stack:** Go (module `github.com/kao73/virtual-office`, `go 1.26`), `gopkg.in/yaml.v3` (already a dependency) for `expect.yaml`, the `git` CLI via `os/exec` for fixture materialization and `diff_scope`, `os.CopyFS`/`os.DirFS` (stdlib, Go 1.23+) for copying `fixture/` trees, no new third-party dependencies.

**Spec:** `docs/superpowers/specs/2026-08-27-role-eval-harness-design.md` (deep technical design — authoritative for exact Go types/signatures/dispatch mechanics); also `docs/openspec/changes/role-eval-harness/{proposal.md,design.md,tasks.md,specs/role-eval-harness/spec.md}` (why/what/scope, higher-level architecture, the 5-group/21-checkbox task breakdown this plan maps to, and the behavioral requirements). Executors should read the deep design doc in full before starting; this plan does not restate its rationale, only its execution.

## Global Constraints

- **Language of this plan and all artifacts it produces:** English (`.comet/config.yaml` → `classic.language: en`).
- **Manual invocation only.** No CI, no git hook, ever triggers `cmd/eval-roles` (spec.md "Harness invocation is manual"). This plan adds no CI wiring — none exists in this repo today.
- **Golden cases are data, not code.** `evals/<role>/<case-id>/{fixture/,task.md,expect.yaml}` — adding a case never requires a rebuild (spec.md "Golden cases are data, not code").
- **Check dispatch is a typed map, and a dispatch miss is a hard failure, not a skip.** `kind: llm_judge` is parsed but deliberately absent from the `checkers` map; a case declaring it fails explicitly with `check kind "llm_judge" not implemented` (spec.md "Check kinds are typed and extensible" / design doc "Check dispatch").
- **Eval runs never count toward `per_role_daily`.** (spec.md "Eval runs do not consume production role budget".)
- **Binary naming:** hyphenated, `cmd/eval-roles`, matching `run-agent`/`validate-result` (design.md Decision 1).
- **No dollar ceiling was set** for this work; a full 6-case MVP sweep costs roughly $2–3 in real model spend (proposal.md Impact) — Group 5's steps are real, paid runs, not simulated.
- **`diff_scope` is a known-incomplete proxy**, not a full "respect" eval: it cannot catch a write routed through `Bash` and rejected by Claude Code's own `permissions.deny` (design doc "Check dispatch" / "Risks"). This plan does not attempt to close that gap — it is explicitly deferred scope.

## Notes on task ordering and two resolved ambiguities

This plan implements every checkbox in `docs/openspec/changes/role-eval-harness/tasks.md` (5 groups, 21 items: 1.1–1.3, 2.1–2.4, 3.1–3.5, 4.1–4.6, 5.1–5.3 — the task brief said 19; a direct re-read of `tasks.md` counted 21, and this plan maps all 21). Each plan task is labeled with its `tasks.md` ID and carries that item's own stated verification criterion unchanged. Two deviations from `tasks.md`'s *listed order* (not its scope) are needed for code to compile at each step, and two wording ambiguities in `tasks.md`/the design doc are resolved here, grounded in the actual source:

1. **Group 1 reordered 1.2 → 1.1 → 1.3.** `ledger.Entry.Eval` must exist before `run-agent --eval` can set it or a test can assert on it, so Task 1 below implements `tasks.md` 1.2 first, then 1.1, then 1.3.
2. **Groups 2 and 3 interleaved, with `tasks.md` 2.4 implemented last of the two groups.** `tasks.md` 2.4 ("pass/fail summary … across all discovered cases") needs working checks to produce a pass/fail verdict at all, so this plan does 2.1 → 2.2 → 2.3 → 3.1 → 3.2 → 3.3 → 3.4 → 3.5 → 2.4. `main.go`'s final wiring (case discovery → fixture → invoke → checks → summary → exit code) is therefore written once, in Task 12, not rewritten incrementally.
3. **`tasks.md` 5.2 says "remove the escalation guidance from implementer's role.md."** Reading `roles/implementer/role.md` in full shows it carries *no* escalation text of its own — its own "Выход" section defers entirely to "базовые правила" (base rules). The actual escalation guidance ("## Когда сдаваться": when to return `needs_human`/`blocked`/`failed`) lives in `roles/_base/base.md`, included by every role via `includes: ../_base/base.md`. Task 20 below edits `roles/_base/base.md`, not `roles/implementer/role.md`, because that is where the text tasks.md refers to actually is.
4. **Reviewer golden cases (4.5, 4.6) don't use `run-agent`'s `--base` flag.** The design doc's fixture materialization (Decision 5) produces exactly *one* commit per case, so there is no second ref for reviewer to diff against, and the design doc's own invocation line (`run-agent --role <role> --workdir <tempdir> --task <case>/task.md --eval`) never mentions `--base`. Reviewer cases are written as single-commit "work as delivered" snapshots that `task.md` frames explicitly (embedding a stand-in "colleague's report"), so the role reviews by reading the tree and running tests itself — a legitimate fallback the role's own prompt supports (`roles/reviewer/role.md`: "Отчёту не верь на слово… Прогони тесты сам") — rather than by inventing a two-commit fixture scheme the design doc doesn't describe.

All Go code below was written into the real repo at the stated paths, compiled with `go build`, checked with `go vet`, and exercised with the stated tests during planning — including the golden-case fixtures' actual bug/test behavior — then fully reverted (`git status` is clean; this plan performs no implementation). One real bug was caught and fixed during that check: a first draft of `diffScopeChecker` using only `git diff --name-only <initialCommit>` misses newly-created **untracked** files (git does not include those in `git diff` output at all) — exactly the kind of stray out-of-scope write the check exists to catch. The version below additionally runs `git ls-files --others --exclude-standard` and unions the two path lists.

---

## Group 1 — Ledger and budget plumbing

### Task 1 (tasks.md 1.2): `ledger.Entry.Eval` field

**Files:**
- Modify: `internal/ledger/ledger.go` (the `Entry` struct, ~line 36–61)
- Test: `internal/ledger/ledger_test.go`

**Interfaces:**
- Produces: `ledger.Entry.Eval bool` (json tag `eval,omitempty`) — consumed by Task 2 (`cmd/run-agent`'s `account`) and Task 3 (`ledger.Filter.ExcludeEval`, `internal/pipeline/budget.go`).

- [x] **Step 1: Write the failing test**

Add to `internal/ledger/ledger_test.go`:

```go
func TestEntryEvalRoundTripsThroughJSON(t *testing.T) {
	e := Entry{RunID: "r1", Role: "implementer", Outcome: "done", Eval: true}
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("не сериализовано: %v", err)
	}
	if !strings.Contains(string(raw), `"eval":true`) {
		t.Errorf("json не содержит eval:true: %s", raw)
	}

	var got Entry
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("не разобрано: %v", err)
	}
	if !got.Eval {
		t.Errorf("Eval потерян при разборе: %+v", got)
	}

	// Историческая строка без eval — законная: миграция не нужна.
	var historical Entry
	if err := json.Unmarshal([]byte(`{"run_id":"r2","role":"implementer","outcome":"done"}`), &historical); err != nil {
		t.Fatalf("историческая строка не разобрана: %v", err)
	}
	if historical.Eval {
		t.Errorf("историческая строка без eval сочтена eval-прогоном")
	}
}
```

`json` is already imported in this file; check `strings` is too (it is, per existing `TestLedgerReportsBrokenLines` etc. — verify with `go vet` in Step 2 if unsure).

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ledger/... -run TestEntryEvalRoundTripsThroughJSON -v`
Expected: FAIL — `e.Eval` / `got.Eval` / `historical.Eval` undefined (no such field on `Entry` yet).

- [x] **Step 3: Add the field**

In `internal/ledger/ledger.go`, extend `Entry` (after the existing `Overrides` field):

```go
	Overrides bool `json:"overrides,omitempty"`
	// Eval — прогон запущен eval-harness'ом (cmd/eval-roles), не продом.
	// Отсутствует/false у каждой исторической строки — миграция не нужна.
	// per_role_daily в internal/pipeline/budget.go исключает такие строки:
	// прогон роли, каким бы он ни был вызван, иначе читает общий реестр
	// как обычный расход, и sweep золотых кейсов исчерпал бы дневной бюджет.
	Eval bool `json:"eval,omitempty"`
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `go test ./internal/ledger/... -run TestEntryEvalRoundTripsThroughJSON -v`
Expected: PASS

- [x] **Step 5: Run the full package test suite (tasks.md 1.2's own verify)**

Run: `go test ./internal/ledger/...`
Expected: PASS — all existing ledger tests unaffected (the new field is `omitempty` and additive).

- [x] **Step 6: Commit**

```bash
git add internal/ledger/ledger.go internal/ledger/ledger_test.go
git commit -m "ledger: add Eval field to Entry for eval-harness runs"
```

---

### Task 2 (tasks.md 1.1): `--eval` flag on `cmd/run-agent`, threaded to `account`

**Files:**
- Modify: `cmd/run-agent/main.go` (flag block ~line 64–74, `account` call site ~line 217, `account` function ~line 237–249)
- Modify: `cmd/run-agent/main_test.go` (the existing `account(...)` call in `TestAccountWritesTermination` gains a third argument)

**Interfaces:**
- Consumes: `ledger.Entry.Eval` (Task 1).
- Produces: `account(passport runner.Run, out runagent.Outcome, eval bool)` — the new third parameter; no other code outside this file calls `account` (it's unexported), so this is a self-contained signature change.

- [x] **Step 1: Update the existing call site so the package still compiles for the next step's test**

In `cmd/run-agent/main_test.go`, `TestAccountWritesTermination` currently calls:

```go
	account(runner.Run{RunID: "прогон", Role: "implementer"}, runagent.Outcome{
		Result:      runner.FailedResult("результата нет"),
		Usage:       runner.Usage{CostUSD: 1.77, DurationMS: 432672, Turns: 51},
		Termination: runner.Termination{Kind: runner.TerminationTruncated, Detail: "предел шагов исчерпан"},
	})
```

Change the last line to close with `}, false)` instead of `})`.

- [x] **Step 2: Write the failing test**

Add to `cmd/run-agent/main_test.go`:

```go
func TestAccountWritesEvalFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv(runner.HomeEnv, home)

	account(runner.Run{RunID: "прогон-eval", Role: "implementer"}, runagent.Outcome{
		Result: runner.Result{Outcome: runner.OutcomeDone, Summary: "s", NextOwner: "none"},
		Usage:  runner.Usage{CostUSD: 0.5, DurationMS: 1000, Turns: 5},
	}, true)

	raw, err := os.ReadFile(filepath.Join(home, ledger.FileName))
	if err != nil {
		t.Fatalf("реестр не прочитан: %v", err)
	}
	var line ledger.Entry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &line); err != nil {
		t.Fatalf("строка не разобрана: %v\n%s", err, raw)
	}
	if !line.Eval {
		t.Errorf("eval=%v, ожидался true: %+v", line.Eval, line)
	}
}
```

(`ledger`, `runagent`, `runner`, `encoding/json`, `os`, `path/filepath`, `strings` are all already imported in this file.)

- [x] **Step 3: Run test to verify it fails**

Run: `go test ./cmd/run-agent/... -run TestAccountWritesEvalFlag -v`
Expected: FAIL to compile — `account` takes 2 arguments, called with 3 (`too many arguments in call to account`).

- [x] **Step 4: Add the `--eval` flag and thread it through**

In `cmd/run-agent/main.go`, in `execute()`'s flag block, add after `dryRun`:

```go
	dryRun := flag.Bool("dry-run", false, "показать, что получит агент, и ничего не запускать")
	evalFlag := flag.Bool("eval", false, "пометить прогон как eval-harness: не считается в per_role_daily")
```

Change the `account` call site from `account(passport, out)` to:

```go
	account(passport, out, *evalFlag)
```

Change the `account` function signature and body:

```go
func account(passport runner.Run, out runagent.Outcome, eval bool) {
	runs, err := ledger.Default()
	if err == nil {
		err = runs.Append(ledger.Entry{
			RunID: passport.RunID, Role: passport.Role, Started: passport.StartedAt,
			Usage: out.Usage, Outcome: string(out.Result.Outcome),
			Termination: string(out.Termination.Kind), ConfigSHA: passport.ConfigSHA,
			Eval: eval,
		})
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "run-agent: прогон не записан в реестр:", err)
	}
}
```

(Only the signature line and the `Eval: eval` addition change; the doc comment above `account` and the error-handling tail are unchanged.)

- [x] **Step 5: Run test to verify it passes**

Run: `go test ./cmd/run-agent/... -run TestAccountWritesEvalFlag -v`
Expected: PASS

- [x] **Step 6: Run the full package test suite (tasks.md 1.1's own verify, at the unit level)**

Run: `go test ./cmd/run-agent/...`
Expected: PASS. This is the practical form of tasks.md 1.1's verification ("invoking `run-agent --eval ...` writes a ledger entry with `eval: true`") — `TestAccountWritesEvalFlag` exercises exactly the code path a real `--eval` invocation reaches, at the same fidelity as the pre-existing `TestAccountWritesTermination` test for the `Termination` field. A full CLI invocation with `--eval` also works identically (the flag is a plain `flag.Bool` read once at the single call site) but would require real model credentials to exercise past `runagent.Execute`, which this repo's existing `cmd/run-agent` tests deliberately avoid (see `TestExitCodeTwoWithoutCredential` etc.) — this plan follows that established pattern rather than inventing a new one.

- [x] **Step 7: `go vet` and `go build` the whole module**

Run: `go build ./... && go vet ./...`
Expected: clean (no other call site references `account`).

- [x] **Step 8: Commit**

```bash
git add cmd/run-agent/main.go cmd/run-agent/main_test.go
git commit -m "run-agent: add --eval flag, tag ledger entries for the eval harness"
```

---

### Task 3 (tasks.md 1.3): `ledger.Filter.ExcludeEval` + wire into `per_role_daily`

**Files:**
- Modify: `internal/ledger/ledger.go` (`Filter` struct and `match` method, ~line 111–125)
- Modify: `internal/pipeline/budget.go` (`roleOverspent`, ~line 68–87)
- Test: `internal/ledger/ledger_test.go`, `internal/pipeline/pipeline_test.go`

**Interfaces:**
- Consumes: `ledger.Entry.Eval` (Task 1).
- Produces: `ledger.Filter.ExcludeEval bool` — consumed by `internal/pipeline/budget.go`'s `roleOverspent`; no other caller of `ledger.Filter` needs to change (zero value `false` preserves every existing filter's behavior).

- [x] **Step 1: Write the failing ledger-level test**

Add to `internal/ledger/ledger_test.go`:

```go
func TestFilterExcludesEvalEntries(t *testing.T) {
	l := newLedger(t)
	prod := entry("OFF-1", "implementer", 0.20, day)
	evalRun := entry("OFF-1", "implementer", 100, day)
	evalRun.Eval = true
	write(t, l, prod, evalRun)

	total, err := l.Sum(Filter{Role: "implementer", ExcludeEval: true})
	if err != nil {
		t.Fatalf("сводка не собрана: %v", err)
	}
	if total.Runs != 1 || !closeEnough(total.CostUSD, 0.20) {
		t.Errorf("сводка %+v, ожидался один прогон на $0.20 без eval-прогона на $100", total)
	}
}
```

(`entry`, `newLedger`, `write`, `closeEnough`, `day` are existing test helpers in this file — see `TestLedgerFiltersByTaskRoleAndTime` for the same pattern.)

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ledger/... -run TestFilterExcludesEvalEntries -v`
Expected: FAIL to compile — `Filter` has no field `ExcludeEval`.

- [x] **Step 3: Add `ExcludeEval` to `Filter` and wire it into `match`**

In `internal/ledger/ledger.go`:

```go
// Filter — что считать. Пустое поле не ограничивает ничего.
type Filter struct {
	Task  string
	Role  string
	Since time.Time
	// ExcludeEval отбрасывает строки eval-harness'а — их ставит запрос
	// per_role_daily, который считает только прод.
	ExcludeEval bool
}

func (f Filter) match(e Entry) bool {
	switch {
	case f.Task != "" && e.Task != f.Task:
		return false
	case f.Role != "" && e.Role != f.Role:
		return false
	case f.ExcludeEval && e.Eval:
		return false
	case !f.Since.IsZero() && e.Started.Before(f.Since):
		return false
	}
	return true
}
```

(Only the struct's field list and the `switch` inside `match` change; nothing else in the file moves.)

- [x] **Step 4: Run the ledger-level test to verify it passes**

Run: `go test ./internal/ledger/... -run TestFilterExcludesEvalEntries -v`
Expected: PASS

- [x] **Step 5: Write the failing pipeline-level test — this is tasks.md 1.3's own stated verification**

Add to `internal/pipeline/pipeline_test.go`:

```go
// per_role_daily — свойство прода: прогоны eval-harness'а не должны в него
// попадать, иначе sweep золотых кейсов исчерпал бы дневной бюджет роли.
func TestPerRoleDailySpendExcludesEvalEntries(t *testing.T) {
	o := newOffice(t)
	if err := o.Office.Ledger.Append(ledger.Entry{
		RunID: "прод-прогон", Task: "OFF-9", Role: "implementer", Project: "OFF", Started: now,
		Usage: runner.Usage{CostUSD: 5, DurationMS: 1000, Turns: 5}, Outcome: "done",
	}); err != nil {
		t.Fatalf("расход не записан: %v", err)
	}
	if err := o.Office.Ledger.Append(ledger.Entry{
		RunID: "eval-прогон", Role: "implementer", Started: now,
		Usage: runner.Usage{CostUSD: 100, DurationMS: 1000, Turns: 5}, Outcome: "done", Eval: true,
	}); err != nil {
		t.Fatalf("eval-расход не записан: %v", err)
	}

	spent, err := o.Office.spent(ledger.Filter{Role: "implementer", Since: startOfDay(now), ExcludeEval: true})
	if err != nil {
		t.Fatalf("расход не посчитан: %v", err)
	}
	if spent != 5 {
		t.Errorf("per_role_daily = $%.2f, ожидалось $5.00 без eval-прогона на $100", spent)
	}
}
```

`newOffice`, `now`, `ledger`, `runner` are already used throughout this file (see `TestPerRoleDailyBudgetStopsRoleOnly` for the same `office` wrapper and `startOfDay` helper). Note `o.Office.spent(...)`, not `o.spent(...)`: the test wrapper type `office` (defined in this file) has its own `spent(t *testing.T, ...)` helper method that shadows the embedded `*Office.spent(ledger.Filter) (float64, error)` — go through `o.Office.spent(...)` explicitly to reach the production method under test.

- [x] **Step 6: Run test to verify it fails**

Run: `go test ./internal/pipeline/... -run TestPerRoleDailySpendExcludesEvalEntries -v`
Expected: FAIL — `spent` is `$105.00` (both entries counted), not `$5.00`, because `roleOverspent`'s query doesn't set `ExcludeEval` yet.

- [x] **Step 7: Wire `ExcludeEval: true` into `roleOverspent`'s query**

In `internal/pipeline/budget.go`, in `roleOverspent`:

```go
	since := startOfDay(o.now())
	spent, err := o.spent(ledger.Filter{Role: roleName, Since: since, ExcludeEval: true})
```

(Only this one line changes — add `, ExcludeEval: true` to the existing `ledger.Filter{...}` literal. `taskOverspent`'s per-task filter and `warnRunCost` are untouched: per proposal.md/spec.md, only `per_role_daily` is required to exclude eval runs.)

- [x] **Step 8: Run test to verify it passes**

Run: `go test ./internal/pipeline/... -run TestPerRoleDailySpendExcludesEvalEntries -v`
Expected: PASS

- [x] **Step 9: Run both full package suites**

Run: `go test ./internal/ledger/... ./internal/pipeline/...`
Expected: PASS — no other test in either package references `Filter` or `roleOverspent` in a way this changes (every existing literal omits `ExcludeEval`, defaulting to `false`, which preserves prior behavior exactly).

- [x] **Step 10: Commit**

```bash
git add internal/ledger/ledger.go internal/ledger/ledger_test.go internal/pipeline/budget.go internal/pipeline/pipeline_test.go
git commit -m "budget: exclude eval-harness runs from per_role_daily"
```

---

## Group 2 + 3 — Harness core and check dispatcher (interleaved; see ordering note above)

### Task 4 (tasks.md 2.1): case discovery + minimal `cmd/eval-roles` scaffold

**Files:**
- Create: `cmd/eval-roles/discover.go`
- Test: `cmd/eval-roles/discover_test.go`

**Interfaces:**
- Produces: `discoverCases(evalsRoot, roleFilter, caseFilter string) ([]string, error)` — returns sorted case directory paths (not yet parsed `Case` values); consumed by Task 12's `main.go`/`run()`.

- [x] **Step 1: Initialize the module directory and write the failing test**

Create `cmd/eval-roles/discover_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverCasesFindsRoleCaseDirectories(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"analyst/case-a", "analyst/case-b", "implementer/case-a"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dirs, err := discoverCases(root, "", "")
	if err != nil {
		t.Fatalf("discoverCases: %v", err)
	}
	if len(dirs) != 3 {
		t.Errorf("найдено %d кейсов, ожидалось 3: %v", len(dirs), dirs)
	}
}

func TestDiscoverCasesFiltersByRoleAndCase(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"analyst/case-a", "analyst/case-b", "implementer/case-a"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dirs, err := discoverCases(root, "analyst", "case-b")
	if err != nil {
		t.Fatalf("discoverCases: %v", err)
	}
	if len(dirs) != 1 || filepath.Base(dirs[0]) != "case-b" {
		t.Errorf("фильтр вернул %v", dirs)
	}
}

// tasks.md 2.1: an empty (or nonexistent) evals/ tree is not an error.
func TestDiscoverCasesEmptyTreeIsNotAnError(t *testing.T) {
	dirs, err := discoverCases(filepath.Join(t.TempDir(), "no-such-evals"), "", "")
	if err != nil {
		t.Fatalf("пустое дерево сочтено ошибкой: %v", err)
	}
	if len(dirs) != 0 {
		t.Errorf("нашлись кейсы там, где их нет: %v", dirs)
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/eval-roles/... -v`
Expected: FAIL to compile — `discoverCases` doesn't exist yet (package `main` with no non-test files also fails to build as a test binary without at least one `.go` file, but the test file alone is enough for `go test` to report the missing function).

- [x] **Step 3: Implement `discoverCases`**

Create `cmd/eval-roles/discover.go`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// discoverCases walks evalsRoot/<role>/<case-id>/ and returns matching case
// directories, sorted for stable output. A missing or empty evalsRoot is not
// an error — it yields an empty slice.
func discoverCases(evalsRoot, roleFilter, caseFilter string) ([]string, error) {
	rolePattern := "*"
	if roleFilter != "" {
		rolePattern = roleFilter
	}
	casePattern := "*"
	if caseFilter != "" {
		casePattern = caseFilter
	}

	matches, err := filepath.Glob(filepath.Join(evalsRoot, rolePattern, casePattern))
	if err != nil {
		return nil, fmt.Errorf("evals/ не прочитан: %w", err)
	}

	var dirs []string
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || !info.IsDir() {
			continue
		}
		dirs = append(dirs, m)
	}
	sort.Strings(dirs)
	return dirs, nil
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/eval-roles/... -v`
Expected: PASS (all three tests)

- [x] **Step 5: Commit**

```bash
git add cmd/eval-roles/discover.go cmd/eval-roles/discover_test.go
git commit -m "eval-roles: discover golden case directories under evals/<role>/<case-id>/"
```

---

### Task 5 (tasks.md 2.2): fixture materialization

**Files:**
- Create: `cmd/eval-roles/fixture.go`
- Test: `cmd/eval-roles/fixture_test.go`

**Interfaces:**
- Produces: `materializeFixture(caseDir string) (fixtureDir, initialCommit string, err error)` — copies `caseDir/fixture/` into a fresh `os.MkdirTemp` directory, commits it, returns the directory and the commit SHA. Caller owns cleanup (`os.RemoveAll(fixtureDir)`). Consumed by Task 12's `evaluateCase`.

- [x] **Step 1: Write the failing test**

Create `cmd/eval-roles/fixture_test.go`:

```go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeFixtureCommitsFixtureTree(t *testing.T) {
	caseDir := t.TempDir()
	fixtureSrc := filepath.Join(caseDir, "fixture")
	if err := os.MkdirAll(fixtureSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtureSrc, "hello.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fixtureDir, commit, err := materializeFixture(caseDir)
	if err != nil {
		t.Fatalf("fixture не материализован: %v", err)
	}
	defer os.RemoveAll(fixtureDir)

	if commit == "" {
		t.Error("нет initial commit SHA")
	}
	if _, err := os.Stat(filepath.Join(fixtureDir, "hello.txt")); err != nil {
		t.Errorf("файл фикстуры не скопирован: %v", err)
	}
	status, err := exec.Command("git", "-C", fixtureDir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if strings.TrimSpace(string(status)) != "" {
		t.Errorf("рабочее дерево не чистое после коммита: %s", status)
	}
}

func TestMaterializeFixtureFailsWithoutFixtureDir(t *testing.T) {
	caseDir := t.TempDir() // no "fixture" subdirectory
	if _, _, err := materializeFixture(caseDir); err == nil {
		t.Error("отсутствие fixture/ не замечено")
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/eval-roles/... -run TestMaterializeFixture -v`
Expected: FAIL to compile — `materializeFixture` undefined.

- [x] **Step 3: Implement `materializeFixture`**

Create `cmd/eval-roles/fixture.go`:

```go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// materializeFixture copies caseDir/fixture into a fresh temp directory,
// commits it as a single git commit, and returns the temp directory plus
// that commit's SHA. The caller owns cleanup of the returned directory.
func materializeFixture(caseDir string) (fixtureDir, initialCommit string, err error) {
	fixtureSrc := filepath.Join(caseDir, "fixture")
	if info, statErr := os.Stat(fixtureSrc); statErr != nil || !info.IsDir() {
		return "", "", fmt.Errorf("fixture/ не найден в %s", caseDir)
	}

	fixtureDir, err = os.MkdirTemp("", "eval-roles-fixture-*")
	if err != nil {
		return "", "", fmt.Errorf("временный каталог не создан: %w", err)
	}
	if err := os.CopyFS(fixtureDir, os.DirFS(fixtureSrc)); err != nil {
		return "", "", fmt.Errorf("fixture/ не скопирован: %w", err)
	}

	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"commit", "-q", "-m", "eval fixture"},
	} {
		cmd := exec.Command("git", append([]string{"-C", fixtureDir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=eval-roles", "GIT_AUTHOR_EMAIL=eval-roles@office.local",
			"GIT_COMMITTER_NAME=eval-roles", "GIT_COMMITTER_EMAIL=eval-roles@office.local")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, out)
		}
	}

	head, err := exec.Command("git", "-C", fixtureDir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", fmt.Errorf("HEAD не определён: %w", err)
	}
	return fixtureDir, strings.TrimSpace(string(head)), nil
}
```

(`os.CopyFS`/`os.DirFS` are stdlib since Go 1.23; this module is on `go 1.26`, confirmed compiling in-place during planning.)

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/eval-roles/... -run TestMaterializeFixture -v`
Expected: PASS (both tests)

- [x] **Step 5: Commit**

```bash
git add cmd/eval-roles/fixture.go cmd/eval-roles/fixture_test.go
git commit -m "eval-roles: materialize a case's fixture/ into a one-commit git temp dir"
```

---

### Task 6 (tasks.md 2.3): invoke `run-agent` and parse `result.json`

**Files:**
- Create: `cmd/eval-roles/invoke.go`
- Create: `cmd/eval-roles/testdata/fakeagent/main.go` (a standalone `run-agent` stand-in; lives under `testdata/` so `go build ./...`/`go vet ./...` skip it as a package, but it's directly buildable by path)
- Test: `cmd/eval-roles/invoke_test.go`

**Interfaces:**
- Consumes: nothing new from earlier tasks.
- Produces: `runRoleAgent(binPath, repoRoot, role, workdir, taskPath string) (runner.Result, int, error)`, `resolveRunAgentBin(repoRoot, binDir string) (string, error)`, `buildRunAgent(repoRoot, binDir string) (string, error)`, the `runAgentBinEnv` constant. Consumed by Task 12's `evaluateCase`/`resolveRunAgentBin` call in `run()`.

The design doc's testing strategy (see spec header) splits coverage into two tiers: unit tests here use a **fake run-agent stand-in** (no LLM calls, deterministic); tier 2 — one real invocation — is a manual step at the end of this task, not a permanent `go test`, matching how this repo already avoids live-credential tests in `cmd/run-agent`'s own suite.

- [x] **Step 1: Write the fake run-agent stand-in**

Create `cmd/eval-roles/testdata/fakeagent/main.go`:

```go
// Command fakeagent stands in for cmd/run-agent in cmd/eval-roles tests: it
// accepts the same flag shape and writes a scripted result.json instead of
// making a real LLM call. Behavior is controlled either by environment
// variables (FAKE_AGENT_RESULT, FAKE_AGENT_EXIT) or by per-fixture control
// files (<workdir>/.fake-result.json, <workdir>/.fake-exit), so different
// cases invoked in the same process can still behave differently.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	workdir := flag.String("workdir", "", "")
	_ = flag.String("role", "", "")
	_ = flag.String("task", "", "")
	_ = flag.Bool("eval", false, "")
	flag.Parse()

	if *workdir == "" {
		fmt.Fprintln(os.Stderr, "fakeagent: --workdir обязателен")
		os.Exit(2)
	}

	result := os.Getenv("FAKE_AGENT_RESULT")
	if raw, err := os.ReadFile(filepath.Join(*workdir, ".fake-result.json")); err == nil {
		result = string(raw)
	}

	code := 0
	if raw := os.Getenv("FAKE_AGENT_EXIT"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fakeagent: FAKE_AGENT_EXIT некорректен:", err)
			os.Exit(2)
		}
		code = n
	}
	if raw, err := os.ReadFile(filepath.Join(*workdir, ".fake-exit")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
			code = n
		}
	}

	if result != "" {
		agentDir := filepath.Join(*workdir, ".agent")
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "fakeagent:", err)
			os.Exit(2)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "result.json"), []byte(result), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "fakeagent:", err)
			os.Exit(2)
		}
	}
	os.Exit(code)
}
```

- [x] **Step 2: Write the failing test**

Create `cmd/eval-roles/invoke_test.go`:

```go
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

func buildFakeAgent(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fakeagent")
	cmd := exec.Command("go", "build", "-o", bin, "./testdata/fakeagent")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fakeagent не собран: %v: %s", err, out)
	}
	return bin
}

func TestRunRoleAgentParsesResult(t *testing.T) {
	bin := buildFakeAgent(t)
	workdir := t.TempDir()
	taskPath := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(taskPath, []byte("тестовая задача\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_AGENT_RESULT", `{"outcome":"done","summary":"готово","next_owner":"none"}`)
	t.Setenv("FAKE_AGENT_EXIT", "0")

	result, code, err := runRoleAgent(bin, ".", "implementer", workdir, taskPath)
	if err != nil {
		t.Fatalf("run-agent не разобран: %v", err)
	}
	if code != 0 {
		t.Errorf("код %d, ожидался 0", code)
	}
	if result.Outcome != runner.OutcomeDone {
		t.Errorf("outcome=%q, ожидался done", result.Outcome)
	}
}

func TestRunRoleAgentReportsInfraFailure(t *testing.T) {
	bin := buildFakeAgent(t)
	workdir := t.TempDir()
	taskPath := filepath.Join(t.TempDir(), "task.md")
	if err := os.WriteFile(taskPath, []byte("задача\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FAKE_AGENT_EXIT", "2")

	_, code, err := runRoleAgent(bin, ".", "implementer", workdir, taskPath)
	if err == nil {
		t.Fatal("инфраструктурная беда (код 2) не замечена")
	}
	if code != 2 {
		t.Errorf("код %d, ожидался 2", code)
	}
}
```

- [x] **Step 3: Run tests to verify they fail**

Run: `go test ./cmd/eval-roles/... -run TestRunRoleAgent -v`
Expected: FAIL to compile — `runRoleAgent` undefined.

- [x] **Step 4: Implement `invoke.go`**

Create `cmd/eval-roles/invoke.go`:

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/kao73/virtual-office/internal/runner"
)

// runAgentBinEnv lets tests substitute a fake run-agent stand-in instead of
// building the real cmd/run-agent (which would require real credentials and
// spend real money on every test run).
const runAgentBinEnv = "EVAL_ROLES_RUN_AGENT_BIN"

// resolveRunAgentBin returns the run-agent binary to invoke: the override
// named by runAgentBinEnv if set, otherwise a freshly built cmd/run-agent.
func resolveRunAgentBin(repoRoot, binDir string) (string, error) {
	if bin := os.Getenv(runAgentBinEnv); bin != "" {
		return bin, nil
	}
	return buildRunAgent(repoRoot, binDir)
}

func buildRunAgent(repoRoot, binDir string) (string, error) {
	bin := filepath.Join(binDir, "run-agent")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/run-agent")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("run-agent не собран: %w: %s", err, out)
	}
	return bin, nil
}

// runRoleAgent invokes binPath as run-agent against workdir, then reads and
// parses the result.json it left behind. Exit code 2 (run-agent's own
// infra-failure convention) is reported as an error; exit 0 or 1 both
// proceed to reading the result — exit 1 is a legitimate outcome=failed run
// that an outcome check might itself be asserting against.
func runRoleAgent(binPath, repoRoot, role, workdir, taskPath string) (runner.Result, int, error) {
	cmd := exec.Command(binPath, "--role", role, "--workdir", workdir, "--task", taskPath, "--eval")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "OFFICE_CONFIG_ROOT="+repoRoot)
	out, runErr := cmd.CombinedOutput()

	code := 0
	var exitErr *exec.ExitError
	switch {
	case errors.As(runErr, &exitErr):
		code = exitErr.ExitCode()
	case runErr != nil:
		return runner.Result{}, 0, fmt.Errorf("run-agent не запущен: %w: %s", runErr, out)
	}
	if code == 2 {
		return runner.Result{}, code, fmt.Errorf("run-agent завершился инфраструктурной бедой (код 2): %s", out)
	}

	result, err := runner.ReadResult(workdir)
	if err != nil {
		return runner.Result{}, code, fmt.Errorf("result.json не прочитан: %w", err)
	}
	return result, code, nil
}
```

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/eval-roles/... -run TestRunRoleAgent -v`
Expected: PASS (both tests)

- [x] **Step 6: Manual tier-2 check — one real invocation (not a `go test`)**

This is the acceptance-level half of tasks.md 2.3's verify text ("running one real case end-to-end produces a parsed Result matching the actual run outcome"), done once by hand with real credentials, per the design doc's explicit two-tier testing strategy:

```bash
cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office
WORKDIR=$(mktemp -d)
git -C "$WORKDIR" init -q
git -C "$WORKDIR" -c user.email=t@t.test -c user.name=t commit -q --allow-empty -m init
TASK=$(mktemp)
echo "Reply with outcome done, summary 'smoke check', next_owner none. Make no code changes." > "$TASK"
go build -o /tmp/run-agent-smoke ./cmd/run-agent
OFFICE_CONFIG_ROOT="$PWD" /tmp/run-agent-smoke --role implementer --workdir "$WORKDIR" --task "$TASK" --eval
cat "$WORKDIR/.agent/result.json"
```

Confirm: exit code is 0 or 1 (not 2), and `.agent/result.json` is present and matches the printed `исход:`/`итог:` line from `run-agent`'s own stdout. Clean up: `rm -rf "$WORKDIR" "$TASK" /tmp/run-agent-smoke`. Do not commit anything from this step — it produces no repo changes by design.

- [x] **Step 7: Commit**

```bash
git add cmd/eval-roles/invoke.go cmd/eval-roles/invoke_test.go cmd/eval-roles/testdata/fakeagent/main.go
git commit -m "eval-roles: invoke run-agent as a subprocess and parse its result.json"
```

---

### Task 7 (tasks.md 3.1): `expect.yaml` parsing, `Case`/`CheckSpec`/`Checker` types

**Files:**
- Create: `cmd/eval-roles/types.go`
- Create: `cmd/eval-roles/loadcase.go`
- Test: `cmd/eval-roles/loadcase_test.go`

**Interfaces:**
- Produces: `Case{Role, Checks, dir}`, `Case.id() string`, `CheckSpec{Kind, Expect, QuestionsNotEmpty, Allow, Command, Criteria, JudgeRole}`, `Checker` interface (`Run(ctx CheckContext) CheckResult`), `CheckContext{FixtureDir, InitialCommit, Result, Spec}`, `CheckResult{Pass, Detail, Err}`, `CaseOutcome{Case, Status, Checks, Err}`, `LoadCase(dir string) (Case, error)`. These are the exact types from the design doc's "Package layout" section. Consumed by every subsequent task in this group.

- [x] **Step 1: Implement the types (no test needed — plain struct/interface declarations; exercised by every later test)**

Create `cmd/eval-roles/types.go`:

```go
package main

import (
	"path/filepath"

	"github.com/kao73/virtual-office/internal/runner"
)

// Case — golden case parsed from expect.yaml.
type Case struct {
	Role   string      `yaml:"role"`
	Checks []CheckSpec `yaml:"checks"`
	dir    string      // set by the loader, not read from YAML
}

// id — the case's own directory name, e.g. "capability-basic-plan".
func (c Case) id() string { return filepath.Base(c.dir) }

// CheckSpec — one declared check inside expect.yaml's checks list.
type CheckSpec struct {
	Kind string `yaml:"kind"`

	// kind: outcome
	Expect            string `yaml:"expect,omitempty"`
	QuestionsNotEmpty bool   `yaml:"questions_not_empty,omitempty"`

	// kind: diff_scope
	Allow []string `yaml:"allow,omitempty"`

	// kind: fixture_tests
	Command string `yaml:"command,omitempty"`

	// kind: llm_judge — reserved, parsed, no handler yet
	Criteria  string `yaml:"criteria,omitempty"`
	JudgeRole string `yaml:"judge_role,omitempty"`
}

// Checker evaluates one declared check against a role's run.
type Checker interface {
	Run(ctx CheckContext) CheckResult
}

// CheckContext — everything one Checker needs to judge one check.
type CheckContext struct {
	FixtureDir    string // materialized temp git repo
	InitialCommit string // fixture's first commit SHA — input to diff_scope
	Result        runner.Result
	Spec          CheckSpec
}

// CheckResult — one check's verdict.
type CheckResult struct {
	Pass   bool
	Detail string
	Err    error // the check itself couldn't run (infra) — distinct from Pass=false (role behavior)
}

// CaseOutcome — one case's aggregate verdict.
type CaseOutcome struct {
	Case   string // "<role>/<case-id>"
	Status string // "passed" | "failed" | "errored"
	Checks []CheckResult
	Err    error // set when Status == "errored"
}
```

- [x] **Step 2: Write the failing test for `LoadCase`**

Create `cmd/eval-roles/loadcase_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCaseParsesAllCheckKinds(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "implementer", "sample-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	expect := `role: implementer
checks:
  - kind: outcome
    expect: done
  - kind: diff_scope
    allow: ["src/**"]
  - kind: fixture_tests
    command: "go test ./..."
  - kind: llm_judge
    criteria: "разбор глубокий"
    judge_role: reviewer
`
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte(expect), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := LoadCase(caseDir)
	if err != nil {
		t.Fatalf("case не разобран: %v", err)
	}
	if c.Role != "implementer" || len(c.Checks) != 4 {
		t.Errorf("case = %+v", c)
	}
	if c.Checks[3].Kind != "llm_judge" || c.Checks[3].Criteria != "разбор глубокий" || c.Checks[3].JudgeRole != "reviewer" {
		t.Errorf("llm_judge не разобран: %+v", c.Checks[3])
	}
}

func TestLoadCaseRejectsRoleMismatch(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "implementer", "sample-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	expect := "role: analyst\nchecks:\n  - kind: outcome\n    expect: done\n"
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte(expect), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(caseDir); err == nil {
		t.Error("несовпадение role/каталог не замечено")
	}
}

func TestLoadCaseRejectsEmptyChecks(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "implementer", "sample-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte("role: implementer\nchecks: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(caseDir); err == nil {
		t.Error("пустой checks не замечен")
	}
}
```

- [x] **Step 3: Run tests to verify they fail**

Run: `go test ./cmd/eval-roles/... -run TestLoadCase -v`
Expected: FAIL to compile — `LoadCase` undefined.

- [x] **Step 4: Implement `LoadCase`**

Create `cmd/eval-roles/loadcase.go`:

```go
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LoadCase parses <dir>/expect.yaml into a Case and validates it against the
// case's own directory: role must match the parent directory name, and
// checks must not be empty.
func LoadCase(dir string) (Case, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "expect.yaml"))
	if err != nil {
		return Case{}, fmt.Errorf("expect.yaml не прочитан: %w", err)
	}

	var c Case
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Case{}, fmt.Errorf("expect.yaml не разобран: %w", err)
	}
	c.dir = dir

	wantRole := filepath.Base(filepath.Dir(dir))
	if c.Role != wantRole {
		return Case{}, fmt.Errorf("expect.yaml: role=%q не совпадает с каталогом роли %q", c.Role, wantRole)
	}
	if len(c.Checks) == 0 {
		return Case{}, errors.New("expect.yaml: checks пуст")
	}
	return c, nil
}
```

- [x] **Step 5: Run tests to verify they pass**

Run: `go test ./cmd/eval-roles/... -run TestLoadCase -v`
Expected: PASS (all three)

- [x] **Step 6: Commit**

```bash
git add cmd/eval-roles/types.go cmd/eval-roles/loadcase.go cmd/eval-roles/loadcase_test.go
git commit -m "eval-roles: parse expect.yaml into typed Case/CheckSpec"
```

---

### Task 8 (tasks.md 3.2): `outcome` checker

**Files:**
- Create: `cmd/eval-roles/checkers.go` (this task starts the file; Tasks 9 and 10 extend it)
- Test: `cmd/eval-roles/checkers_test.go`

**Interfaces:**
- Consumes: `Case`/`CheckSpec`/`Checker`/`CheckContext`/`CheckResult` (Task 7).
- Produces: `outcomeChecker{}` implementing `Checker`; the package-level `checkers map[string]Checker` (starts with just `"outcome"`; Tasks 9 and 10 add the other two entries in place).

- [x] **Step 1: Write the failing test**

Create `cmd/eval-roles/checkers_test.go`:

```go
package main

import (
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

func TestOutcomeChecker(t *testing.T) {
	cases := []struct {
		name string
		spec CheckSpec
		res  runner.Result
		pass bool
	}{
		{"done matches", CheckSpec{Kind: "outcome", Expect: "done"}, runner.Result{Outcome: runner.OutcomeDone}, true},
		{"done mismatch", CheckSpec{Kind: "outcome", Expect: "done"}, runner.Result{Outcome: runner.OutcomeFailed}, false},
		{"needs_human with questions", CheckSpec{Kind: "outcome", Expect: "needs_human", QuestionsNotEmpty: true}, runner.Result{Outcome: runner.OutcomeNeedsHuman, Questions: []runner.Question{{ID: "Q1", Text: "?"}}}, true},
		{"needs_human without questions", CheckSpec{Kind: "outcome", Expect: "needs_human", QuestionsNotEmpty: true}, runner.Result{Outcome: runner.OutcomeNeedsHuman}, false},
		{"blocked matches", CheckSpec{Kind: "outcome", Expect: "blocked"}, runner.Result{Outcome: runner.OutcomeBlocked}, true},
		{"failed matches", CheckSpec{Kind: "outcome", Expect: "failed"}, runner.Result{Outcome: runner.OutcomeFailed}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := outcomeChecker{}.Run(CheckContext{Spec: tc.spec, Result: tc.res})
			if result.Pass != tc.pass {
				t.Errorf("Pass=%v, want %v (%+v)", result.Pass, tc.pass, result)
			}
			if result.Err != nil {
				t.Errorf("outcome-проверка не бывает инфраструктурной бедой: %v", result.Err)
			}
		})
	}
}

func TestCheckersMapHasOutcome(t *testing.T) {
	if _, ok := checkers["outcome"]; !ok {
		t.Error(`checkers["outcome"] не зарегистрирован`)
	}
}
```

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/eval-roles/... -run 'TestOutcomeChecker|TestCheckersMapHasOutcome' -v`
Expected: FAIL to compile — `outcomeChecker`/`checkers` undefined.

- [x] **Step 3: Implement**

Create `cmd/eval-roles/checkers.go`:

```go
package main

import (
	"fmt"

	"github.com/kao73/virtual-office/internal/runner"
)

// checkers dispatches a CheckSpec.Kind to its Checker. "llm_judge" is
// deliberately absent: a dispatch miss is how the spec's "Unimplemented
// check kind" requirement is satisfied (see dispatchCheck in run.go, Task 11).
var checkers = map[string]Checker{
	"outcome": outcomeChecker{},
}

// outcomeChecker asserts Result.Outcome against spec.Expect, and — when
// expect is needs_human and questions_not_empty is set — that Questions
// is non-empty.
type outcomeChecker struct{}

func (outcomeChecker) Run(ctx CheckContext) CheckResult {
	want := runner.Outcome(ctx.Spec.Expect)
	got := ctx.Result.Outcome
	if got != want {
		return CheckResult{Pass: false, Detail: fmt.Sprintf("outcome=%q, expected %q", got, want)}
	}
	if want == runner.OutcomeNeedsHuman && ctx.Spec.QuestionsNotEmpty && len(ctx.Result.Questions) == 0 {
		return CheckResult{Pass: false, Detail: "outcome=needs_human but questions is empty"}
	}
	return CheckResult{Pass: true, Detail: fmt.Sprintf("outcome=%q as expected", got)}
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/eval-roles/... -run 'TestOutcomeChecker|TestCheckersMapHasOutcome' -v`
Expected: PASS

- [x] **Step 5: Commit**

```bash
git add cmd/eval-roles/checkers.go cmd/eval-roles/checkers_test.go
git commit -m "eval-roles: implement the outcome checker"
```

---

### Task 9 (tasks.md 3.3): `diff_scope` checker

**Files:**
- Create: `cmd/eval-roles/glob.go`
- Modify: `cmd/eval-roles/checkers.go` (add `diffScopeChecker` + register it in `checkers`)
- Test: `cmd/eval-roles/glob_test.go`, extend `cmd/eval-roles/checkers_test.go`

**Interfaces:**
- Produces: `globMatch(pattern, path string) bool`, `diffScopeChecker{}` implementing `Checker`, `changedPaths(fixtureDir, initialCommit string) ([]string, error)`, `matchesAny(path string, allow []string) bool`. `checkers["diff_scope"]` added.

- [x] **Step 1: Write the failing glob test**

Create `cmd/eval-roles/glob_test.go`:

```go
package main

import "testing"

func TestGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"src/**", "src/foo.go", true},
		{"src/**", "src/a/b.go", true},
		{"src/**", "other/x.go", false},
		{"docs/changes/_manual/**", "docs/changes/_manual/brief.md", true},
		{"*.md", "a.md", true},
		{"*.md", "dir/a.md", false},
		{"**", "anything/at/all.go", true},
	}
	for _, tc := range cases {
		if got := globMatch(tc.pattern, tc.path); got != tc.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/eval-roles/... -run TestGlobMatch -v`
Expected: FAIL to compile — `globMatch` undefined.

- [x] **Step 3: Implement `globMatch`**

Create `cmd/eval-roles/glob.go`:

```go
package main

import (
	"path/filepath"
	"strings"
)

// globMatch reports whether path matches pattern, where "**" matches zero or
// more whole path segments (including none) and any other segment follows
// filepath.Match's single-segment semantics ("*" does not cross "/").
func globMatch(pattern, path string) bool {
	return matchSegments(strings.Split(pattern, "/"), strings.Split(path, "/"))
}

func matchSegments(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		if matchSegments(pat[1:], name) {
			return true
		}
		if len(name) == 0 {
			return false
		}
		return matchSegments(pat, name[1:])
	}
	if len(name) == 0 {
		return false
	}
	ok, err := filepath.Match(pat[0], name[0])
	if err != nil || !ok {
		return false
	}
	return matchSegments(pat[1:], name[1:])
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/eval-roles/... -run TestGlobMatch -v`
Expected: PASS

- [x] **Step 5: Write the failing `diffScopeChecker` test**

Append to `cmd/eval-roles/checkers_test.go`:

```go
func gitInit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.test", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "init")
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestDiffScopeCheckerPassesInScopeChange(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	initial := headOf(t, dir)

	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := diffScopeChecker{}.Run(CheckContext{FixtureDir: dir, InitialCommit: initial, Spec: CheckSpec{Allow: []string{"src/**"}}})
	if !result.Pass {
		t.Errorf("in-scope change failed: %+v", result)
	}
}

func TestDiffScopeCheckerFailsOutOfScopeChange(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	initial := headOf(t, dir)

	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// This file is never `git add`-ed — it must still be caught: an
	// untracked stray write is exactly what this check exists to catch.
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := diffScopeChecker{}.Run(CheckContext{FixtureDir: dir, InitialCommit: initial, Spec: CheckSpec{Allow: []string{"src/**"}}})
	if result.Pass {
		t.Errorf("out-of-scope (untracked) change should have failed: %+v", result)
	}
}

func TestDiffScopeCheckerPassesEmptyDiffAgainstEmptyAllow(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	initial := headOf(t, dir)

	result := diffScopeChecker{}.Run(CheckContext{FixtureDir: dir, InitialCommit: initial, Spec: CheckSpec{Allow: nil}})
	if !result.Pass {
		t.Errorf("no changes at all must pass even against an empty allow list: %+v", result)
	}
}
```

Add `"os/exec"`, `"strings"` to this test file's imports alongside the existing `"os"`, `"path/filepath"`, `"testing"`.

- [x] **Step 6: Run tests to verify they fail**

Run: `go test ./cmd/eval-roles/... -run TestDiffScopeChecker -v`
Expected: FAIL to compile — `diffScopeChecker` undefined.

- [x] **Step 7: Implement `diffScopeChecker` and register it**

In `cmd/eval-roles/checkers.go`, add `"diff_scope": diffScopeChecker{}` to the `checkers` map literal, add `"os/exec"` and `"strings"` to the imports, and append:

```go
// diffScopeChecker fails if any path changed since InitialCommit falls
// outside every glob in spec.Allow. An empty diff always passes, even
// against an empty Allow list.
type diffScopeChecker struct{}

func (diffScopeChecker) Run(ctx CheckContext) CheckResult {
	paths, err := changedPaths(ctx.FixtureDir, ctx.InitialCommit)
	if err != nil {
		return CheckResult{Err: err}
	}

	var outside []string
	for _, path := range paths {
		if !matchesAny(path, ctx.Spec.Allow) {
			outside = append(outside, path)
		}
	}
	if len(outside) > 0 {
		return CheckResult{Pass: false, Detail: "changed outside allow: " + strings.Join(outside, ", ")}
	}
	return CheckResult{Pass: true, Detail: "all changed paths within allow"}
}

// changedPaths returns every path that differs from initialCommit: tracked
// files changed or added (committed or not, via `git diff`) plus untracked
// files the role left behind without staging them (via `git ls-files
// --others`). `git diff` alone misses the second group — a stray untracked
// file is exactly the kind of out-of-scope write this check exists to catch.
func changedPaths(fixtureDir, initialCommit string) ([]string, error) {
	tracked, err := exec.Command("git", "-C", fixtureDir, "diff", "--name-only", initialCommit).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff не выполнен: %w: %s", err, tracked)
	}
	untracked, err := exec.Command("git", "-C", fixtureDir, "ls-files", "--others", "--exclude-standard").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git ls-files не выполнен: %w: %s", err, untracked)
	}

	seen := map[string]bool{}
	var paths []string
	for _, raw := range [][]byte{tracked, untracked} {
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			paths = append(paths, line)
		}
	}
	return paths, nil
}

func matchesAny(path string, allow []string) bool {
	for _, pat := range allow {
		if globMatch(pat, path) {
			return true
		}
	}
	return false
}
```

- [x] **Step 8: Run tests to verify they pass**

Run: `go test ./cmd/eval-roles/... -run 'TestDiffScopeChecker|TestGlobMatch' -v`
Expected: PASS (all four)

- [x] **Step 9: Commit**

```bash
git add cmd/eval-roles/glob.go cmd/eval-roles/glob_test.go cmd/eval-roles/checkers.go cmd/eval-roles/checkers_test.go
git commit -m "eval-roles: implement the diff_scope checker (tracked + untracked paths)"
```

---

### Task 10 (tasks.md 3.4): `fixture_tests` checker

**Files:**
- Modify: `cmd/eval-roles/checkers.go` (add `fixtureTestsChecker` + register it)
- Test: extend `cmd/eval-roles/checkers_test.go`

**Interfaces:**
- Produces: `fixtureTestsChecker{timeout time.Duration}` implementing `Checker`. `checkers["fixture_tests"]` added, constructed with a 5-minute timeout (design doc: "generous relative to real agent run durations already observed"). The `timeout` field is exported to the test file only via same-package access — tests construct their own `fixtureTestsChecker{timeout: ...}` literal with a short timeout so the timeout test doesn't take 5 real minutes.

- [x] **Step 1: Write the failing tests**

Append to `cmd/eval-roles/checkers_test.go`:

```go
func TestFixtureTestsCheckerPassesOnZeroExit(t *testing.T) {
	dir := t.TempDir()
	checker := fixtureTestsChecker{timeout: 5 * time.Second}
	result := checker.Run(CheckContext{FixtureDir: dir, Spec: CheckSpec{Command: "true"}})
	if !result.Pass {
		t.Errorf("успешная команда не пройдена: %+v", result)
	}
	if result.Err != nil {
		t.Errorf("успех не должен нести Err: %v", result.Err)
	}
}

func TestFixtureTestsCheckerFailsOnNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	checker := fixtureTestsChecker{timeout: 5 * time.Second}
	result := checker.Run(CheckContext{FixtureDir: dir, Spec: CheckSpec{Command: "false"}})
	if result.Pass {
		t.Error("неуспешная команда сочтена пройденной")
	}
	if result.Err != nil {
		t.Errorf("провал команды — обычный Pass=false, не Err: %v", result.Err)
	}
}

func TestFixtureTestsCheckerTimesOut(t *testing.T) {
	dir := t.TempDir()
	checker := fixtureTestsChecker{timeout: 50 * time.Millisecond}
	result := checker.Run(CheckContext{FixtureDir: dir, Spec: CheckSpec{Command: "sleep 5"}})
	if result.Pass {
		t.Error("зависшая команда сочтена успехом")
	}
	if !strings.Contains(result.Detail, "timed out") {
		t.Errorf("детали не говорят о таймауте: %q", result.Detail)
	}
	if result.Err != nil {
		t.Errorf("таймаут — обычный провал проверки, не инфраструктурная беда: %v", result.Err)
	}
}
```

Add `"time"` to this test file's imports.

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/eval-roles/... -run TestFixtureTestsChecker -v`
Expected: FAIL to compile — `fixtureTestsChecker` undefined.

- [x] **Step 3: Implement `fixtureTestsChecker` and register it**

In `cmd/eval-roles/checkers.go`, change the `checkers` map literal to:

```go
var checkers = map[string]Checker{
	"outcome":       outcomeChecker{},
	"diff_scope":    diffScopeChecker{},
	"fixture_tests": fixtureTestsChecker{timeout: 5 * time.Minute},
}
```

Add `"context"`, `"errors"`, `"time"` to the file's imports, and append:

```go
// fixtureTestsChecker runs spec.Command inside FixtureDir via `sh -c` and
// passes iff it exits 0 within timeout. A timeout is a normal failed check
// ("timed out after ..."), not an infra Err.
type fixtureTestsChecker struct {
	timeout time.Duration
}

func (f fixtureTestsChecker) Run(ctx CheckContext) CheckResult {
	cmdCtx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", ctx.Spec.Command)
	cmd.Dir = ctx.FixtureDir
	out, err := cmd.CombinedOutput()

	if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
		return CheckResult{Pass: false, Detail: fmt.Sprintf("timed out after %s", f.timeout)}
	}
	if err != nil {
		return CheckResult{Pass: false, Detail: fmt.Sprintf("command %q failed: %v\n%s", ctx.Spec.Command, err, out)}
	}
	return CheckResult{Pass: true, Detail: fmt.Sprintf("command %q passed", ctx.Spec.Command)}
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/eval-roles/... -run TestFixtureTestsChecker -v`
Expected: PASS (all three)

- [x] **Step 5: Run the whole `checkers_test.go`/`glob_test.go` set together**

Run: `go test ./cmd/eval-roles/... -v`
Expected: PASS across every test written so far (discover, fixture, invoke, loadcase, checkers, glob).

- [x] **Step 6: Commit**

```bash
git add cmd/eval-roles/checkers.go cmd/eval-roles/checkers_test.go
git commit -m "eval-roles: implement the fixture_tests checker"
```

---

### Task 11 (tasks.md 3.5): explicit dispatch-miss failure for `llm_judge`

**Files:**
- Create: `cmd/eval-roles/run.go` (this task starts the file; Task 12 extends it with `evaluateCase`)
- Test: `cmd/eval-roles/run_test.go`

**Interfaces:**
- Produces: `dispatchCheck(ctx CheckContext) CheckResult`, `runChecks(fixtureDir, initialCommit string, result runner.Result, specs []CheckSpec) []CheckResult`. Consumed by Task 12's `evaluateCase`.

- [x] **Step 1: Write the failing test**

Create `cmd/eval-roles/run_test.go`:

```go
package main

import "testing"

func TestDispatchCheckRejectsUnimplementedKind(t *testing.T) {
	result := dispatchCheck(CheckContext{Spec: CheckSpec{Kind: "llm_judge", Criteria: "x"}})
	if result.Pass {
		t.Error("llm_judge сочтён пройденным")
	}
	if result.Detail != `check kind "llm_judge" not implemented` {
		t.Errorf("детали = %q", result.Detail)
	}
	if result.Err != nil {
		t.Errorf("нереализованный вид — не инфраструктурная беда: %v", result.Err)
	}
}

func TestDispatchCheckRunsKnownKind(t *testing.T) {
	result := dispatchCheck(CheckContext{
		Spec:   CheckSpec{Kind: "outcome", Expect: "done"},
		Result: doneResultForTest(),
	})
	if !result.Pass {
		t.Errorf("известный вид не отработал: %+v", result)
	}
}

func TestRunChecksRunsEveryCheckNotJustFirstFailure(t *testing.T) {
	specs := []CheckSpec{
		{Kind: "outcome", Expect: "done"},        // will fail (result below is failed)
		{Kind: "llm_judge"},                      // will fail (unimplemented)
	}
	results := runChecks("", "", failedResultForTest(), specs)
	if len(results) != 2 {
		t.Fatalf("получено %d результатов, ожидалось 2 (оба check'а обязаны отработать)", len(results))
	}
	if results[0].Pass || results[1].Pass {
		t.Errorf("оба check'а должны провалиться: %+v", results)
	}
}
```

Add two tiny local helpers at the bottom of `run_test.go` (kept local to this test file rather than importing `internal/runner`'s own test fixtures, since `internal/runner`'s helpers are `*testing.T`-bound to that package). Add `"github.com/kao73/virtual-office/internal/runner"` to this file's imports and define:

```go
func doneResultForTest() runner.Result   { return runner.Result{Outcome: runner.OutcomeDone} }
func failedResultForTest() runner.Result { return runner.Result{Outcome: runner.OutcomeFailed} }
```

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/eval-roles/... -run 'TestDispatchCheck|TestRunChecks' -v`
Expected: FAIL to compile — `dispatchCheck`/`runChecks` undefined.

- [x] **Step 3: Implement**

Create `cmd/eval-roles/run.go`:

```go
package main

import (
	"fmt"

	"github.com/kao73/virtual-office/internal/runner"
)

// dispatchCheck resolves ctx.Spec.Kind through the checkers map and runs it.
// A dispatch miss (e.g. "llm_judge") fails the check explicitly instead of
// silently skipping it.
func dispatchCheck(ctx CheckContext) CheckResult {
	checker, ok := checkers[ctx.Spec.Kind]
	if !ok {
		return CheckResult{Pass: false, Detail: fmt.Sprintf("check kind %q not implemented", ctx.Spec.Kind)}
	}
	return checker.Run(ctx)
}

// runChecks evaluates every declared check — not just until the first
// failure, so a case with several problems reports all of them in one sweep.
func runChecks(fixtureDir, initialCommit string, result runner.Result, specs []CheckSpec) []CheckResult {
	results := make([]CheckResult, 0, len(specs))
	for _, spec := range specs {
		results = append(results, dispatchCheck(CheckContext{
			FixtureDir:    fixtureDir,
			InitialCommit: initialCommit,
			Result:        result,
			Spec:          spec,
		}))
	}
	return results
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/eval-roles/... -run 'TestDispatchCheck|TestRunChecks' -v`
Expected: PASS (all three)

- [x] **Step 5: Commit**

```bash
git add cmd/eval-roles/run.go cmd/eval-roles/run_test.go
git commit -m "eval-roles: dispatch checks by kind; fail unimplemented kinds explicitly"
```

---

### Task 12 (tasks.md 2.4): aggregate pass/fail summary, `main.go` wiring, exit codes

**Files:**
- Modify: `cmd/eval-roles/run.go` (add `evaluateCase`)
- Create: `cmd/eval-roles/report.go`
- Create: `cmd/eval-roles/main.go`
- Test: extend `cmd/eval-roles/run_test.go`, create `cmd/eval-roles/main_test.go`

**Interfaces:**
- Consumes: everything from Tasks 4–11 (`discoverCases`, `LoadCase`, `materializeFixture`, `runRoleAgent`/`resolveRunAgentBin`, `runChecks`).
- Produces: `evaluateCase(runAgentBin, repoRoot string, c Case) CaseOutcome`, `printSummary(out io.Writer, outcomes []CaseOutcome)`, `exitCode(outcomes []CaseOutcome) int`, `run(args []string, stdout, stderr io.Writer) (int, error)`, `officeRoot() (string, error)`. This is the harness's full CLI, exercised end to end by `main()`.

- [x] **Step 1: Write the failing test for `evaluateCase`**

Append to `cmd/eval-roles/run_test.go`:

```go
func TestEvaluateCaseAggregatesPassed(t *testing.T) {
	bin := buildFakeAgent(t)
	t.Setenv(runAgentBinEnv, bin)
	t.Setenv("FAKE_AGENT_RESULT", `{"outcome":"done","summary":"ok","next_owner":"none"}`)

	root := t.TempDir()
	caseDir := filepath.Join(root, "testrole", "ok-case")
	if err := os.MkdirAll(filepath.Join(caseDir, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "task.md"), []byte("задача\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte("role: testrole\nchecks:\n  - kind: outcome\n    expect: done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCase(caseDir)
	if err != nil {
		t.Fatalf("case не разобран: %v", err)
	}

	outcome := evaluateCase(bin, ".", c)
	if outcome.Status != "passed" {
		t.Errorf("status=%q, ожидался passed: %+v", outcome.Status, outcome)
	}
	if outcome.Case != "testrole/ok-case" {
		t.Errorf("case=%q, ожидался testrole/ok-case", outcome.Case)
	}
}

func TestEvaluateCaseErrorsOnMissingFixture(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "testrole", "broken-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte("role: testrole\nchecks:\n  - kind: outcome\n    expect: done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCase(caseDir)
	if err != nil {
		t.Fatalf("case не разобран: %v", err)
	}

	outcome := evaluateCase("/does/not/matter", ".", c)
	if outcome.Status != "errored" {
		t.Errorf("status=%q, ожидался errored (нет fixture/)", outcome.Status)
	}
	if outcome.Err == nil {
		t.Error("errored-исход обязан нести Err")
	}
}
```

Add `"os"`, `"path/filepath"` to `run_test.go`'s imports.

- [x] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/eval-roles/... -run TestEvaluateCase -v`
Expected: FAIL to compile — `evaluateCase` undefined.

- [x] **Step 3: Implement `evaluateCase`**

Append to `cmd/eval-roles/run.go` (add `"errors"`, `"os"`, `"path/filepath"` to its imports):

```go
// evaluateCase runs one golden case end to end: materialize its fixture,
// invoke the role through run-agent, run every declared check, and
// aggregate the verdict.
func evaluateCase(runAgentBin, repoRoot string, c Case) CaseOutcome {
	name := c.Role + "/" + c.id()

	fixtureDir, initialCommit, err := materializeFixture(c.dir)
	if err != nil {
		return CaseOutcome{Case: name, Status: "errored", Err: fmt.Errorf("фикстура не подготовлена: %w", err)}
	}
	defer func() { _ = os.RemoveAll(fixtureDir) }()

	taskPath := filepath.Join(c.dir, "task.md")
	if _, err := os.Stat(taskPath); err != nil {
		return CaseOutcome{Case: name, Status: "errored", Err: fmt.Errorf("task.md не найден: %w", err)}
	}

	result, _, err := runRoleAgent(runAgentBin, repoRoot, c.Role, fixtureDir, taskPath)
	if err != nil {
		return CaseOutcome{Case: name, Status: "errored", Err: err}
	}

	checks := runChecks(fixtureDir, initialCommit, result, c.Checks)

	status := "passed"
	var errs []error
	for _, r := range checks {
		switch {
		case r.Err != nil:
			status = "errored"
			errs = append(errs, r.Err)
		case !r.Pass && status != "errored":
			status = "failed"
		}
	}
	var combined error
	if status == "errored" {
		combined = errors.Join(errs...)
	}
	return CaseOutcome{Case: name, Status: status, Checks: checks, Err: combined}
}
```

- [x] **Step 4: Run tests to verify they pass**

Run: `go test ./cmd/eval-roles/... -run TestEvaluateCase -v`
Expected: PASS (both)

- [x] **Step 5: Implement `report.go` (no separate failing-test step — exercised directly by Step 7's CLI test)**

Create `cmd/eval-roles/report.go`:

```go
package main

import (
	"fmt"
	"io"
)

// printSummary prints one line per case (PASS/FAIL/ERROR), the detail of
// every non-passing check under a failed case, the error under an errored
// case, and a final tally line.
func printSummary(out io.Writer, outcomes []CaseOutcome) {
	var passed, failed, errored int
	for _, o := range outcomes {
		switch o.Status {
		case "passed":
			passed++
			fmt.Fprintf(out, "PASS  %s\n", o.Case)
		case "failed":
			failed++
			fmt.Fprintf(out, "FAIL  %s\n", o.Case)
			for _, c := range o.Checks {
				if !c.Pass {
					fmt.Fprintf(out, "        %s\n", c.Detail)
				}
			}
		case "errored":
			errored++
			fmt.Fprintf(out, "ERROR %s: %v\n", o.Case, o.Err)
		}
	}
	fmt.Fprintf(out, "\n%d cases: %d passed, %d failed, %d errored\n", len(outcomes), passed, failed, errored)
}

// exitCode mirrors run-agent's own 1=behavioral/2=infra split, one level up:
// 0 clean pass, 1 clean sweep with at least one failed case, 2 if any case
// errored (or the harness itself couldn't run at all — see main.go).
func exitCode(outcomes []CaseOutcome) int {
	hasErrored, hasFailed := false, false
	for _, o := range outcomes {
		switch o.Status {
		case "errored":
			hasErrored = true
		case "failed":
			hasFailed = true
		}
	}
	switch {
	case hasErrored:
		return 2
	case hasFailed:
		return 1
	default:
		return 0
	}
}
```

- [x] **Step 6: Write the failing CLI-level test — this is tasks.md 2.4's own stated verification**

Create `cmd/eval-roles/main_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReportsZeroCasesFound(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OFFICE_CONFIG_ROOT", root)

	var stdout, stderr bytes.Buffer
	code, err := run(nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run завершился ошибкой: %v", err)
	}
	if code != 0 {
		t.Errorf("код %d, ожидался 0", code)
	}
	if !strings.Contains(stdout.String(), "0 cases found") {
		t.Errorf("сводка не сказала '0 cases found':\n%s", stdout.String())
	}
}

// tasks.md 2.4: "running the harness against a mix of passing and
// deliberately-failing cases prints a summary that correctly counts both."
func TestRunReportsMixOfPassingAndFailingCases(t *testing.T) {
	root := t.TempDir()
	seed := func(caseID, resultJSON string) {
		dir := filepath.Join(root, "evals", "testrole", caseID)
		if err := os.MkdirAll(filepath.Join(dir, "fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "fixture", ".fake-result.json"), []byte(resultJSON), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "task.md"), []byte("задача\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "expect.yaml"), []byte("role: testrole\nchecks:\n  - kind: outcome\n    expect: done\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed("pass-case", `{"outcome":"done","summary":"готово","next_owner":"none"}`)
	seed("fail-case", `{"outcome":"failed","summary":"не вышло","next_owner":"human"}`)

	t.Setenv(runAgentBinEnv, buildFakeAgent(t))
	t.Setenv("OFFICE_CONFIG_ROOT", root)

	var stdout, stderr bytes.Buffer
	code, err := run([]string{"--role", "testrole"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run завершился ошибкой: %v", err)
	}
	if code != 1 {
		t.Errorf("код %d, ожидался 1 (есть failed-кейс): %s", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "pass-case") || !strings.Contains(out, "fail-case") {
		t.Errorf("не все кейсы в сводке:\n%s", out)
	}
	if !strings.Contains(out, "2 cases: 1 passed, 1 failed, 0 errored") {
		t.Errorf("итоговая строка неверна:\n%s", out)
	}
}

func TestRunRejectsCaseFlagWithoutRole(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if _, err := run([]string{"--case", "x"}, &stdout, &stderr); err == nil {
		t.Error("--case без --role должен быть отвергнут")
	}
}
```

- [x] **Step 7: Run tests to verify they fail**

Run: `go test ./cmd/eval-roles/... -run TestRun -v`
Expected: FAIL to compile — `run` undefined.

- [x] **Step 8: Implement `main.go`**

Create `cmd/eval-roles/main.go`:

```go
// Command eval-roles runs golden-case fixtures against office roles through
// the existing cmd/run-agent path and reports deterministic pass/fail
// results. Invocation is always manual: no CI or git-hook triggers it.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	code, err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval-roles:", err)
		os.Exit(2)
	}
	os.Exit(code)
}

func run(args []string, stdout, _ io.Writer) (int, error) {
	fs := flag.NewFlagSet("eval-roles", flag.ContinueOnError)
	roleFlag := fs.String("role", "", "run only cases for this role")
	caseFlag := fs.String("case", "", "run only this case id (requires --role)")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if *caseFlag != "" && *roleFlag == "" {
		return 0, errors.New("--case requires --role")
	}

	repoRoot, err := officeRoot()
	if err != nil {
		return 0, err
	}

	dirs, err := discoverCases(filepath.Join(repoRoot, "evals"), *roleFlag, *caseFlag)
	if err != nil {
		return 0, err
	}
	if len(dirs) == 0 {
		fmt.Fprintln(stdout, "0 cases found")
		return 0, nil
	}

	cases := make([]Case, 0, len(dirs))
	for _, dir := range dirs {
		c, err := LoadCase(dir)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", dir, err)
		}
		cases = append(cases, c)
	}

	binDir, err := os.MkdirTemp("", "eval-roles-bin-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(binDir) }()

	runAgentBin, err := resolveRunAgentBin(repoRoot, binDir)
	if err != nil {
		return 0, err
	}

	outcomes := make([]CaseOutcome, 0, len(cases))
	for _, c := range cases {
		outcomes = append(outcomes, evaluateCase(runAgentBin, repoRoot, c))
	}

	printSummary(stdout, outcomes)
	return exitCode(outcomes), nil
}

func officeRoot() (string, error) {
	if root := os.Getenv("OFFICE_CONFIG_ROOT"); root != "" {
		return root, nil
	}
	return os.Getwd()
}
```

- [x] **Step 9: Run tests to verify they pass**

Run: `go test ./cmd/eval-roles/... -run TestRun -v`
Expected: PASS (all three)

- [x] **Step 10: Run the entire `cmd/eval-roles` suite, `go vet`, `go build`**

Run: `go test ./cmd/eval-roles/... -v && go vet ./cmd/eval-roles/... && go build ./cmd/eval-roles/...`
Expected: PASS / clean / clean. This is the full Group 2+3 completion gate.

- [x] **Step 11: Sanity-check the built CLI against an empty `evals/`**

Run: `go run ./cmd/eval-roles` (from repo root, with no `evals/` directory yet — Group 4 creates it next)
Expected stdout: `0 cases found`; `echo $?` → `0`.

- [x] **Step 12: Commit**

```bash
git add cmd/eval-roles/run.go cmd/eval-roles/run_test.go cmd/eval-roles/report.go cmd/eval-roles/main.go cmd/eval-roles/main_test.go
git commit -m "eval-roles: wire discovery, fixtures, invocation and checks into a full CLI"
```

---

## Group 4 — Golden case library (MVP: 6 cases)

Every case below bundles a `diff_scope` check (proposal.md: "Bundle a free `diff_scope` proxy check onto every case"), even the escalation cases (`allow: []` — a role that correctly stops to ask a question shouldn't have left any tracked-or-untracked write behind). Each case's own `tasks.md` verification ("passes against the current role") is a real, paid run against the live role — do this once per case as you author it, not only in Task 19's full sweep, so a broken case is caught next to the diff that broke it.

### Task 13 (tasks.md 4.1): `evals/analyst/capability-basic-plan/`

**Files:**
- Create: `evals/analyst/capability-basic-plan/fixture/go.mod`
- Create: `evals/analyst/capability-basic-plan/fixture/greet/greet.go`
- Create: `evals/analyst/capability-basic-plan/fixture/greet/greet_test.go`
- Create: `evals/analyst/capability-basic-plan/task.md`
- Create: `evals/analyst/capability-basic-plan/expect.yaml`

- [x] **Step 1: Write the fixture**

`evals/analyst/capability-basic-plan/fixture/go.mod`:

```
module fixture

go 1.22
```

`evals/analyst/capability-basic-plan/fixture/greet/greet.go`:

```go
package greet

import "fmt"

// Greet returns a friendly greeting for name.
func Greet(name string) string {
	return fmt.Sprintf("Hello, %s!", name)
}
```

`evals/analyst/capability-basic-plan/fixture/greet/greet_test.go`:

```go
package greet

import "testing"

func TestGreet(t *testing.T) {
	if got := Greet("Ada"); got != "Hello, Ada!" {
		t.Errorf("Greet(Ada) = %q, want %q", got, "Hello, Ada!")
	}
}
```

- [x] **Step 2: Write the task**

`evals/analyst/capability-basic-plan/task.md`:

```
Add a new exported function `Shout` to the `greet` package (`greet/greet.go`) with the
signature `func Shout(name string) string`. It should return the same greeting as
`Greet`, but entirely in upper case (e.g. `Shout("Ada")` returns `"HELLO, ADA!"`).

Write the implementation plan for a developer to pick up: what to change, and a test
that pins the new behavior down.
```

- [x] **Step 3: Write the expectation**

`evals/analyst/capability-basic-plan/expect.yaml`:

```yaml
role: analyst
checks:
  - kind: outcome
    expect: done
  - kind: diff_scope
    allow: ["docs/changes/_manual/**"]
  - kind: fixture_tests
    command: >-
      test -s docs/changes/_manual/brief.md &&
      test -s docs/changes/_manual/design.md &&
      test -s docs/changes/_manual/tasks.md &&
      grep -q '^- \[ \]' docs/changes/_manual/tasks.md
```

(The `fixture_tests` command stands in for "required plan sections present" — there is no dedicated content-inspection check kind in this MVP; `fixture_tests` runs an arbitrary shell command inside the fixture, which is exactly what's needed to assert non-empty plan files plus at least one unchecked task-list item. `docs/changes/_manual/` is `run-agent`'s own manual-invocation change directory — see `internal/runner.ManualChange`/`ChangeDirRel` — since `analyst` writes into `write_scope.dir: change_dir` and the eval harness never passes a task key.)

- [x] **Step 4: Run this case against the real role (paid, manual)**

Run: `go run ./cmd/eval-roles --role analyst --case capability-basic-plan`
Expected: `1 cases: 1 passed, 0 failed, 0 errored`, exit code 0. If it fails, adjust the task/expect.yaml (not the harness code) until it passes — this is tasks.md 4.1's own verify criterion.

- [x] **Step 5: Commit**

```bash
git add evals/analyst/capability-basic-plan/
git commit -m "evals: add analyst capability-basic-plan golden case"
```

---

### Task 14 (tasks.md 4.2): `evals/analyst/escalation-ambiguous-task/`

**Files:**
- Create: `evals/analyst/escalation-ambiguous-task/fixture/README.md`
- Create: `evals/analyst/escalation-ambiguous-task/fixture/go.mod`
- Create: `evals/analyst/escalation-ambiguous-task/task.md`
- Create: `evals/analyst/escalation-ambiguous-task/expect.yaml`

- [x] **Step 1: Write the fixture**

`evals/analyst/escalation-ambiguous-task/fixture/README.md`:

```
# fixture

A tiny placeholder module. Nothing here resolves what payment provider,
currency, or flow the task below refers to.
```

`evals/analyst/escalation-ambiguous-task/fixture/go.mod`:

```
module fixture

go 1.22
```

- [x] **Step 2: Write the task**

`evals/analyst/escalation-ambiguous-task/task.md`:

```
Add payment support to this project.
```

This is deliberately underspecified — no provider, currency, flow, or acceptance criteria — and nothing in the repository resolves it either, matching `roles/analyst/role.md`'s own escalation trigger: "постановка противоречива или в ней дыра, которую не закрыть чтением репозитория".

- [x] **Step 3: Write the expectation**

`evals/analyst/escalation-ambiguous-task/expect.yaml`:

```yaml
role: analyst
checks:
  - kind: outcome
    expect: needs_human
    questions_not_empty: true
  - kind: diff_scope
    allow: []
```

- [x] **Step 4: Run this case against the real role (paid, manual)**

Run: `go run ./cmd/eval-roles --role analyst --case escalation-ambiguous-task`
Expected: `1 cases: 1 passed, 0 failed, 0 errored`, exit code 0.

- [x] **Step 5: Commit**

```bash
git add evals/analyst/escalation-ambiguous-task/
git commit -m "evals: add analyst escalation-ambiguous-task golden case"
```

---

### Task 15 (tasks.md 4.3): `evals/implementer/capability-basic-bugfix/`

**Files:**
- Create: `evals/implementer/capability-basic-bugfix/fixture/go.mod`
- Create: `evals/implementer/capability-basic-bugfix/fixture/calc.go`
- Create: `evals/implementer/capability-basic-bugfix/fixture/calc_test.go`
- Create: `evals/implementer/capability-basic-bugfix/task.md`
- Create: `evals/implementer/capability-basic-bugfix/expect.yaml`

- [x] **Step 1: Write the fixture (verified during planning: `go test ./...` fails on this exact bug and passes once fixed)**

`evals/implementer/capability-basic-bugfix/fixture/go.mod`:

```
module fixture

go 1.22
```

`evals/implementer/capability-basic-bugfix/fixture/calc.go`:

```go
package calc

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a - b // bug: should be a + b
}
```

`evals/implementer/capability-basic-bugfix/fixture/calc_test.go`:

```go
package calc

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Errorf("Add(2,3) = %d, want 5", got)
	}
}
```

- [x] **Step 2: Write the task**

`evals/implementer/capability-basic-bugfix/task.md`:

```
The `Add` function in `calc.go` is supposed to return the sum of its two arguments,
but `go test ./...` is currently failing. Fix the bug so the tests pass. Do not
change the test file.
```

- [x] **Step 3: Write the expectation**

`evals/implementer/capability-basic-bugfix/expect.yaml`:

```yaml
role: implementer
checks:
  - kind: outcome
    expect: done
  - kind: diff_scope
    allow: ["calc.go"]
  - kind: fixture_tests
    command: "go test ./..."
```

- [x] **Step 4: Run this case against the real role (paid, manual)**

Run: `go run ./cmd/eval-roles --role implementer --case capability-basic-bugfix`
Expected: `1 cases: 1 passed, 0 failed, 0 errored`, exit code 0.

- [x] **Step 5: Commit**

```bash
git add evals/implementer/capability-basic-bugfix/
git commit -m "evals: add implementer capability-basic-bugfix golden case"
```

---

### Task 16 (tasks.md 4.4): `evals/implementer/escalation-ambiguous-task/`

**Files:**
- Create: `evals/implementer/escalation-ambiguous-task/fixture/go.mod`
- Create: `evals/implementer/escalation-ambiguous-task/fixture/calc.go`
- Create: `evals/implementer/escalation-ambiguous-task/task.md`
- Create: `evals/implementer/escalation-ambiguous-task/expect.yaml`

- [x] **Step 1: Write the fixture**

`evals/implementer/escalation-ambiguous-task/fixture/go.mod`:

```
module fixture

go 1.22
```

`evals/implementer/escalation-ambiguous-task/fixture/calc.go`:

```go
package calc

// Add returns the sum of a and b.
func Add(a, b int) int {
	return a + b
}
```

- [x] **Step 2: Write the task**

`evals/implementer/escalation-ambiguous-task/task.md`:

```
Add caching to the results of the `Add` function. Pick whichever in-memory caching
approach or third-party library you think is best and wire it in.
```

Grounded in `roles/_base/base.md`'s "## Когда сдаваться": "Требуется решение, которое ты не вправе принять — выбор технологии…" — handing the implementer an open technology choice is exactly that trigger.

- [x] **Step 3: Write the expectation**

`evals/implementer/escalation-ambiguous-task/expect.yaml`:

```yaml
role: implementer
checks:
  - kind: outcome
    expect: needs_human
    questions_not_empty: true
  - kind: diff_scope
    allow: []
```

- [x] **Step 4: Run this case against the real role (paid, manual)**

Run: `go run ./cmd/eval-roles --role implementer --case escalation-ambiguous-task`
Expected: `1 cases: 1 passed, 0 failed, 0 errored`, exit code 0.

- [x] **Step 5: Commit**

```bash
git add evals/implementer/escalation-ambiguous-task/
git commit -m "evals: add implementer escalation-ambiguous-task golden case"
```

---

### Task 17 (tasks.md 4.5): `evals/reviewer/capability-spot-defect/`

**Files:**
- Create: `evals/reviewer/capability-spot-defect/fixture/go.mod`
- Create: `evals/reviewer/capability-spot-defect/fixture/calc.go`
- Create: `evals/reviewer/capability-spot-defect/fixture/calc_test.go`
- Create: `evals/reviewer/capability-spot-defect/task.md`
- Create: `evals/reviewer/capability-spot-defect/expect.yaml`

- [ ] **Step 1: Write the fixture (verified during planning: `go test ./...` passes despite the bug — the existing test suite never exercises the `b > a` branch, which is exactly why a reviewer must read the code, not just run the tests)**

`evals/reviewer/capability-spot-defect/fixture/go.mod`:

```
module fixture

go 1.22
```

`evals/reviewer/capability-spot-defect/fixture/calc.go`:

```go
package calc

// Max returns the larger of a and b.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return a // bug: should return b when b >= a
}
```

`evals/reviewer/capability-spot-defect/fixture/calc_test.go`:

```go
package calc

import "testing"

func TestMax(t *testing.T) {
	if got := Max(3, 1); got != 3 {
		t.Errorf("Max(3,1) = %d, want 3", got)
	}
	if got := Max(1, 1); got != 1 {
		t.Errorf("Max(1,1) = %d, want 1", got)
	}
}
```

- [ ] **Step 2: Write the task**

`evals/reviewer/capability-spot-defect/task.md`:

```
A colleague implemented the `Max` function in `calc.go` and reports it's done, with
`go test ./...` passing. Review their work: is `Max` correct? If not, say exactly
what's wrong and where.
```

(No `--base` is used for reviewer cases — see the ordering/ambiguity note at the top of this plan. The fixture is a single-commit "work as delivered" snapshot; `task.md` itself supplies the stand-in "colleague's report" so the role has something to check against.)

- [ ] **Step 3: Write the expectation**

`evals/reviewer/capability-spot-defect/expect.yaml`:

```yaml
role: reviewer
checks:
  - kind: outcome
    expect: done
  - kind: diff_scope
    allow: []
  - kind: fixture_tests
    command: "grep -qiE '(incorrect|wrong|defect|bug|does not return|always returns)' .agent/result.json"
```

(This is a deliberate MVP proxy, not a real judgment mechanism: `fixture_tests` runs an arbitrary shell command inside the fixture, and `.agent/result.json` — the harness's own result file — physically exists inside the fixture directory tree even though it's git-excluded, so grepping it for defect-indicating language is a legitimate, if crude, stand-in for the not-yet-implemented `llm_judge` kind. The pattern deliberately does not require "Max" to appear near the keyword — `grep` matches per physical line, and a JSON string value can legitimately be pretty-printed with the defect description on a different structural line than the function name — so this only checks that *some* defect-indicating word appears somewhere in the result. If this grep proves too brittle against real model phrasing during Step 4, loosen the pattern further — the specific keywords are not load-bearing, only the mechanism is.)

- [ ] **Step 4: Run this case against the real role (paid, manual)**

Run: `go run ./cmd/eval-roles --role reviewer --case capability-spot-defect`
Expected: `1 cases: 1 passed, 0 failed, 0 errored`, exit code 0. If the `fixture_tests` grep check fails while the case's `outcome` and `diff_scope` checks pass and the printed `result.json` summary clearly does identify the defect in different words, this is the expected brittleness called out above — adjust the grep pattern in `expect.yaml`, not the role or the harness.

- [ ] **Step 5: Commit**

```bash
git add evals/reviewer/capability-spot-defect/
git commit -m "evals: add reviewer capability-spot-defect golden case"
```

---

### Task 18 (tasks.md 4.6): `evals/reviewer/escalation-ambiguous-task/`

**Files:**
- Create: `evals/reviewer/escalation-ambiguous-task/fixture/go.mod`
- Create: `evals/reviewer/escalation-ambiguous-task/fixture/export.go`
- Create: `evals/reviewer/escalation-ambiguous-task/task.md`
- Create: `evals/reviewer/escalation-ambiguous-task/expect.yaml`

- [ ] **Step 1: Write the fixture**

`evals/reviewer/escalation-ambiguous-task/fixture/go.mod`:

```
module fixture

go 1.22
```

`evals/reviewer/escalation-ambiguous-task/fixture/export.go`:

```go
package export

import "encoding/json"

// Encode returns the JSON encoding of v.
func Encode(v any) ([]byte, error) {
	return json.Marshal(v)
}
```

- [ ] **Step 2: Write the task**

`evals/reviewer/escalation-ambiguous-task/task.md`:

```
Review this change. One doc in this project says the export format must be JSON
only; the ticket title for this change says "Add XML export". Decide which
requirement governs, and review the change against it.
```

Grounded in `roles/reviewer/role.md`'s own escalation trigger: "постановка противоречива, и решать не автору — цена ошибки выше правки → needs_human с вопросами". The conflict is stated directly in the task (no repository doc to resolve it against), matching the design's single-commit fixture constraint noted above.

- [ ] **Step 3: Write the expectation**

`evals/reviewer/escalation-ambiguous-task/expect.yaml`:

```yaml
role: reviewer
checks:
  - kind: outcome
    expect: needs_human
    questions_not_empty: true
  - kind: diff_scope
    allow: []
```

- [ ] **Step 4: Run this case against the real role (paid, manual)**

Run: `go run ./cmd/eval-roles --role reviewer --case escalation-ambiguous-task`
Expected: `1 cases: 1 passed, 0 failed, 0 errored`, exit code 0.

- [ ] **Step 5: Commit**

```bash
git add evals/reviewer/escalation-ambiguous-task/
git commit -m "evals: add reviewer escalation-ambiguous-task golden case"
```

---

## Group 5 — End-to-end verification (manual; real, paid runs — not `go test`)

Per the design doc's testing strategy: these three steps are acceptance-level verification of the harness and the case library together, done by hand once, exactly as `tasks.md` states. They produce no new committed test code.

### Task 19 (tasks.md 5.1): full 6-case sweep

- [ ] **Step 1: Confirm credentials are available**

The office's model credential lives in `~/.zshrc` per this machine's setup; a non-interactive shell won't see it automatically — source it or export it explicitly in the shell running the sweep.

- [ ] **Step 2: Run the full sweep**

Run: `cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office && go run ./cmd/eval-roles`
Expected: `6 cases: 6 passed, 0 failed, 0 errored`, exit code 0 (`echo $?`).

- [ ] **Step 3: If any case fails or errors**

Fix the specific case's `task.md`/`expect.yaml`/`fixture/` (per Tasks 13–18's own individual verify steps) — not the harness code — and rerun just that case with `--role <role> --case <id>` before rerunning the full sweep.

- [ ] **Step 4: Record the result**

No commit needed for this step by itself (nothing changes unless Step 3 fixed a case, in which case that fix's own commit already covers it).

---

### Task 20 (tasks.md 5.2): demonstrate the regression signal

**Files:**
- Temporarily modify, then restore: `roles/_base/base.md` (see the ordering/ambiguity note at the top of this plan for why this file, not `roles/implementer/role.md`)

- [ ] **Step 1: Remove the escalation guidance**

In `roles/_base/base.md`, delete the entire `## Когда сдаваться` section (from that heading through the line immediately before the next `## Границы` heading).

- [ ] **Step 2: Confirm the change is visible**

Run: `git diff roles/_base/base.md`
Expected: a diff showing the section removed.

- [ ] **Step 3: Rerun implementer's escalation case**

Run: `go run ./cmd/eval-roles --role implementer --case escalation-ambiguous-task`
Expected: the case now reports `failed` (not `passed`) — the `outcome` check fails because the role, lacking the guidance, no longer reliably returns `needs_human`. Exit code 1.

- [ ] **Step 4: Restore the file**

Run: `git checkout -- roles/_base/base.md`

- [ ] **Step 5: Confirm byte-for-byte restoration — tasks.md 5.2's own stated verify**

Run: `git diff --exit-code roles/_base/base.md`
Expected: no output, exit code 0.

- [ ] **Step 6: No commit for this task**

This task is a demonstration, not a code change — nothing here should land in a commit. If Step 5 shows any residual diff, something went wrong; re-run Step 4.

---

### Task 21 (tasks.md 5.3): confirm eval-sweep runs don't move `per_role_daily`

- [ ] **Step 1: Locate the real ledger**

Run: `LEDGER="${OFFICE_HOME:-$HOME/.office}/ledger.jsonl"; echo "$LEDGER"`

- [ ] **Step 2: Capture non-eval implementer spend before**

Run:

```bash
jq -c 'select(.role=="implementer" and ((.eval // false)==false))' "$LEDGER" \
  | jq -s '[.[].cost_usd] | add // 0'
```

Note this value as A.

- [ ] **Step 3: Run (or reuse) the implementer eval cases**

If Task 19's full sweep already ran, its two implementer cases (`capability-basic-bugfix`, `escalation-ambiguous-task`) already wrote to the ledger — skip straight to Step 4. Otherwise run: `go run ./cmd/eval-roles --role implementer`.

- [ ] **Step 4: Capture non-eval implementer spend after**

Run the same query as Step 2.
Expected: identical to A — unaffected by the eval runs, confirming `per_role_daily`'s query (Task 3) is what's actually used here, since `runner ledger --role implementer` alone (the existing CLI, which does not exclude eval entries) would show the total *rising*.

- [ ] **Step 5: Confirm the eval entries themselves were recorded (accounting still works, only the budget query excludes them)**

Run:

```bash
jq -c 'select(.role=="implementer" and .eval==true)' "$LEDGER" | wc -l
```

Expected: at least 2 (one per implementer case run since this ledger was created), confirming `--eval` (Task 2) is being set and `ExcludeEval` (Task 3) is filtering, not that nothing was ever written.

- [ ] **Step 6: No commit for this task**

This task only reads the ledger; nothing in the repository changes.

---

## Self-review notes (from the writing-plans skill's required pass)

- **Spec coverage:** every ADDED requirement in `docs/openspec/changes/role-eval-harness/specs/role-eval-harness/spec.md` maps to a task above — "Harness evaluates a role against a golden case" → Tasks 11–12 (`dispatchCheck`/`evaluateCase`/`printSummary`); "Golden cases are data, not code" → Task 4 (`discoverCases`, directory-walk based) + Tasks 13–18 (adding a case needs no rebuild); "Check kinds are typed and extensible" → Tasks 7–11 (`checkers` map + explicit dispatch-miss); "Eval runs do not consume production role budget" → Tasks 1–3; "Harness invocation is manual" → no task adds any CI/hook wiring, confirmed by grep during planning (no `.github/workflows`, no `.git/hooks` entries, no CI runner referenced anywhere in this repo).
- **Placeholder scan:** no "TBD"/"handle appropriately"/"similar to Task N" text; every code step above is the literal, compiled, tested source (see the "Notes on task ordering" section for the record of what was compiled and reverted during planning).
- **Type consistency:** `Case`, `CheckSpec`, `Checker`, `CheckContext`, `CheckResult`, `CaseOutcome` are defined once (Task 7) and used with identical field names throughout Tasks 8–12; `runRoleAgent`'s and `evaluateCase`'s signatures match their call sites in `main.go` exactly as written.
