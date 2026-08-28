---
comet_change: role-eval-harness
role: technical-design
canonical_spec: openspec
---

# role-eval-harness — Technical Design

This is the deep technical refinement of the Open-phase artifacts at
`docs/openspec/changes/role-eval-harness/` (`proposal.md`, `design.md`,
`specs/role-eval-harness/spec.md`, `tasks.md`), which remain the canonical
source for goals, scope, and requirements. This document goes one level
deeper: concrete types, execution flow, and the edge-case policy the
higher-level design didn't need to resolve.

## Package layout

New package `cmd/eval-roles`, matching the existing hyphenated `cmd/`
naming (`run-agent`, `validate-result`).

```go
type Case struct {
	Role   string      `yaml:"role"`
	Checks []CheckSpec `yaml:"checks"`
	dir    string      // set by the loader, not read from YAML
}

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

type Checker interface {
	Run(ctx CheckContext) CheckResult
}

type CheckContext struct {
	FixtureDir    string // materialized temp git repo
	InitialCommit string // fixture's first commit SHA — input to diff_scope
	Result        runner.Result
	Spec          CheckSpec
}

type CheckResult struct {
	Pass   bool
	Detail string
	Err    error // the check itself couldn't run (infra) — distinct from Pass=false (role behavior)
}

type CaseOutcome struct {
	Case   string // "<role>/<case-id>"
	Status string // "passed" | "failed" | "errored"
	Checks []CheckResult
	Err    error // set when Status == "errored"
}
```

## Execution flow (per case)

1. **Load and validate every `expect.yaml`, up front.** Before any fixture
   is materialized or `run-agent` is invoked, every discovered case
   directory's `expect.yaml` is loaded and validated: `role` must match the
   case's directory, `checks` must be non-empty. A failure on any one case
   **aborts the whole sweep immediately** (exit code 2) rather than becoming
   a per-case `errored` outcome — for a harness that spends real money per
   invocation, failing fast on a malformed case before a single paid
   `run-agent` call beats discovering the same typo only after the sweep has
   already spent money running the other cases.
2. **Materialize the fixture.** Copy `fixture/` into a temp dir, `git init
   && git add -A && git commit`, capture the initial commit SHA. This
   duplicates the trivial pattern already used by test helpers like
   `internal/runner/input_test.go:25`'s `gitRepo(t)` — those are
   `*testing.T`-bound and cannot be imported by an external-process
   harness, so the ~4 git commands are re-implemented here, not imported.
   Failure (e.g. `fixture/` missing) → `errored`.
3. **Invoke `run-agent`** as a subprocess: `run-agent --role <role>
   --workdir <tempdir> --task <case>/task.md --eval`. No `--project` flag
   — a golden case exercises the role's own `role.yaml` contract in
   isolation, not any specific project's merged network/tools permission
   layers (see `docs/contracts/role-sandbox-permissions.md` for that
   layering, which this harness deliberately does not engage). Exit code
   `2` (run-agent's own infra-failure convention) → `errored`. Exit `0` or
   `1` → proceed; exit `1` is a legitimate `outcome: failed` that an
   `outcome` check might itself be asserting against, not necessarily a
   harness problem.
4. **Read the result.** `runner.ReadResult(tempdir)` — reused directly
   from `internal/runner`, no custom JSON parsing. Failure → `errored`.
5. **Run every declared check**, not just until the first failure —
   better diagnostics: a case with three checks and two real problems
   should report both in one sweep, not require two fix-and-rerun cycles.
   Only the `fixture_tests` kind carries a timeout (5 minutes, generous
   relative to real agent run durations already observed — implementer
   averages ~41 steps per run in `~/.office/ledger.jsonl`); on expiry it
   is reported as a normal failed check ("timed out after Ns"), not a
   harness crash. `outcome` and `diff_scope` are local/fast and need none.
6. **Aggregate.** `passed` if every check reports `Pass: true`. `failed`
   if every check ran without an infra-level `Err` and at least one
   reports `Pass: false` — a trustworthy signal about role behavior.
   `errored` if any check itself returned `Err` — the sweep could not
   produce a trustworthy signal for that case at all.

## Check dispatch

```go
var checkers = map[string]Checker{
	"outcome":       outcomeChecker{},
	"diff_scope":    diffScopeChecker{},
	"fixture_tests": fixtureTestsChecker{},
}
```

`kind: llm_judge` is deliberately **absent** from this map. A dispatch
miss produces `CheckResult{Pass: false, Detail: "check kind \"llm_judge\"
not implemented"}` — the spec's "Unimplemented check kind" requirement is
satisfied by the map lookup itself, not a special-cased branch, so adding
a real `llm_judge` implementation later is exactly one new `Checker` plus
one new map entry.

- **`outcomeChecker`**: asserts `result.Outcome == spec.Expect`; when
  `spec.Expect == "needs_human"` and `spec.QuestionsNotEmpty`, also
  asserts `len(result.Questions) > 0`.
- **`diffScopeChecker`**: `git diff --name-only <InitialCommit>` inside
  `FixtureDir`; fails if any changed path doesn't match one of
  `spec.Allow`'s globs. Known, accepted limitation carried over from the
  Open-phase design: this cannot catch a write attempt routed through
  `Bash` and rejected by Claude Code's own `permissions.deny`, since
  nothing in the current architecture surfaces a rejected tool-call
  attempt as structured data. A real respect-eval class with `run.log`
  parsing is explicitly deferred, not silently dropped.
- **`fixtureTestsChecker`**: runs `spec.Command` inside `FixtureDir` with
  a 5-minute timeout; passes iff exit code 0.

## Budget exclusion

- `cmd/run-agent` gains a `--eval` bool flag. When set, the accounting
  step writes `ledger.Entry{..., Eval: true}`.
- `internal/ledger.Entry` gains `Eval bool \`json:"eval,omitempty"\`` —
  omitted/false on every historical entry, no migration needed.
- `internal/ledger.Filter` gains an eval-exclusion option;
  `internal/pipeline/budget.go`'s `per_role_daily` daily-spend query
  (`o.spent(ledger.Filter{Role: roleName, Since: since})`) sets it, so
  eval-harness runs never count toward a role's daily production budget.

## CLI and exit codes

`cmd/eval-roles [--role <name>] [--case <id>]` — no flags runs every case
discovered under `evals/*/*/ `; `--case` requires `--role`. Exit codes
mirror `run-agent`'s own 1=behavioral/2=infra split, one level up:

| Code | Meaning |
|---|---|
| 0 | Swept cleanly; every case `passed` |
| 1 | Swept cleanly; ≥1 case `failed` (trustworthy failure signal) |
| 2 | ≥1 case `errored`, or the harness itself could not run (e.g. temp dir creation failed) |

An empty `evals/` tree is **not** an error: it prints "0 cases found" and
exits `0` (nothing to report is not the same as something being wrong).

## Testing strategy

Two distinct tiers, matching `tasks.md`'s verification criteria:

1. **`go test ./cmd/eval-roles/...`** — unit tests per `Checker` against
   synthetic `runner.Result` values and small temp git repos (table-driven,
   no LLM calls, deterministic, runs in CI-less local `go test` like any
   other package in this repo). Also covers harness-plumbing behavior
   (case discovery, fixture materialization, the `errored`-then-continue
   policy) using a fake `run-agent` stand-in rather than a real one.
2. **The 6 real golden cases** (`tasks.md` §4) are validated by an actual
   live run of the finished harness against the real roles — real LLM
   calls, real cost (~$2-3 for the full MVP sweep per current ledger
   medians), run manually per `tasks.md` 5.1-5.3. This is acceptance-level
   verification of the *cases themselves*, not unit coverage of the
   harness's own code, and is not part of `go test`.

## Risks / Trade-offs

- [Risk] `diff_scope` cannot catch a Bash-routed write attempt →
  [Mitigation] documented, accepted limitation; a dedicated respect-eval
  class with `run.log` parsing is deferred, not silently dropped.
- [Risk] A role's behavior can be occasionally non-deterministic, making
  single-run pass/fail noisy for some case → [Mitigation] accepted
  trade-off for MVP; the `checks:` list is additive, so a statistical
  N-run mode can be added per-case later without a schema change.
- [Risk] `errored`-then-continue could mask a systematically broken case
  authoring pattern if nobody looks at `errored` counts → [Mitigation]
  the summary report always surfaces `errored` cases distinctly from
  `failed` ones; exit code `2` makes an `errored` case impossible to
  mistake for a clean pass in scripted use.
