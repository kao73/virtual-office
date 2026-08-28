# Comet Design Handoff

- Change: role-eval-harness
- Phase: design
- Mode: compact
- Context hash: 7bb184167bd08aeebff38729bbcf354c28002039f3f1bc8b10f93af5e68c75a8

Generated-by: comet-handoff.sh

OpenSpec remains the canonical capability spec. This handoff is a deterministic, source-traceable context pack, not an agent-authored summary.

## docs/openspec/changes/role-eval-harness/proposal.md

- Source: docs/openspec/changes/role-eval-harness/proposal.md
- Lines: 1-29
- SHA256: f0242cf5397b06a1f1949a51ba6eb9fab7fbb7596416b8c1f22f7f1904699990

```md
## Why

Role behavior (the `analyst`/`implementer`/`reviewer` prompts in `roles/*/role.md` + `role.yaml`) is currently checked only by manual `scripts/smoke.sh` runs and `go test`, which covers runner code but never the role's actual prompt behavior. There is no repeatable signal when a role's prompt or permissions are edited, and `docs/DESIGN.md` §4 already lists "Evals для ролей перед промоутом конфига из `main` в `stable`" as deferred work this change now picks up.

## What Changes

- Add a small Go harness (new `cmd/` entry point) that runs a role against a disposable git fixture via the existing `cmd/run-agent`, parses `result.json` through the typed `internal/runner.Result` contract, and reports pass/fail per golden case.
- Add a golden-case library as repository data (not code): each case is a directory with `fixture/` (initial file tree), `task.md` (task prompt), and `expect.yaml` (declarative expectation) — cases are added without recompiling the harness.
- Ship 6 MVP cases: one `capability` case and one `escalation` case per role (`analyst`, `implementer`, `reviewer`).
- `expect.yaml` uses a typed, pluggable `checks:` list (`kind: outcome | diff_scope | fixture_tests`, with `llm_judge` reserved but unimplemented) so LLM-as-judge grading can be added later without reshaping existing cases.
- Bundle a free `diff_scope` proxy check onto every case as a lightweight stand-in for a dedicated "respect" eval class (known limitation: does not catch an attempt routed through `Bash`, since tool permission enforcement lives outside our code in Claude Code's own `permissions.deny`/`--permission-mode dontAsk`).
- **Modify** `internal/pipeline` budget accounting so eval-harness runs are tagged and excluded from `per_role_daily` budget consumption, so running the eval sweep cannot exhaust a role's daily budget and block real production work.

## Capabilities

### New Capabilities
- `role-eval-harness`: a manually-triggered evaluation harness that runs golden-case fixtures against a role through the existing `run-agent` path and reports deterministic pass/fail results, used as a regression gate before promoting office config from `main` to `stable` and as an optional check after editing a role.

### Modified Capabilities
None. The eval harness's budget-exclusion behavior (eval runs not counted against `per_role_daily`) is a requirement of the new `role-eval-harness` capability itself, not a change to the existing `role-sandbox-permissions` spec, which continues to describe the network/tools permission model unchanged.

## Impact

- **New code**: a new `cmd/` binary (name finalized in design.md); no changes to existing `cmd/runner`, `cmd/run-agent`, or `cmd/validate-result` behavior.
- **New data**: a golden-case directory tree under the repository (exact location finalized in design.md), committed and versioned like any other config-as-code artifact (`role.yaml`, `workflow.yaml`).
- **Modified code**: `internal/pipeline/budget.go` (or the ledger/budget accounting it drives) gains a way to tag/exclude eval-harness runs from `per_role_daily`.
- **No CI/git-hook dependency**: the project has no CI running `go build`/`vet`/`test` today (confirmed during the prior PR #1 convergence review); this harness is manually triggered and does not require CI to exist first.
- **Cost**: real per-role run costs from `~/.office/ledger.jsonl` (last 20 runs/role) — analyst ≈$0.54, implementer ≈$0.82, reviewer ≈$0.46 — put a full 6-case MVP sweep at roughly $2–3 per run. No dollar ceiling was set for this work.
- **Out of scope for this change**: dedicated provocative respect-eval cases, LLM-as-judge implementation, statistical N-run pass thresholds, CI/git-hook automation of the trigger, and a session-length eval class (not applicable — runs are single-shot, bounded by `max_turns`).

```

## docs/openspec/changes/role-eval-harness/design.md

- Source: docs/openspec/changes/role-eval-harness/design.md
- Lines: 1-58
- SHA256: 8bd7a19066b942880d7059e60e96338b84f8ba544681f5606b11f2ba219ac614

```md
## Context

See proposal.md - Why for motivation. This design builds directly on three existing pieces, unchanged in behavior:

- `cmd/run-agent` — the manual debug CLI that already runs one role against a given `--workdir`/`--task` without a real tracker, and prints/writes `result.json` (`internal/runner.Result`: `Outcome`, `Questions`, `Blocker`, `Artifacts`, `NextOwner`).
- `internal/ledger` — an append-only `ledger.jsonl`, one `ledger.Entry` per run, written by `run-agent`'s own accounting step.
- `internal/pipeline/budget.go` — the pipeline's `per_role_daily` gate, computed by summing ledger entries for a role since the start of the day (`o.spent(ledger.Filter{Role: roleName, Since: since})`). This only runs inside the pipeline's `tick()` loop, but it reads the *shared* ledger — so any `run-agent` invocation for a role, however triggered, counts toward that role's daily total unless the ledger entry is explicitly excluded.

## Goals / Non-Goals

**Goals:**
- Pin down the concrete shapes left open by the proposal: harness binary location, golden-case directory layout, `expect.yaml` schema, check dispatch mechanism, and the exact budget-exclusion mechanism.

**Non-Goals:**
- Everything already listed as out of scope in proposal.md (dedicated respect cases, LLM-judge implementation, statistical N-run thresholds, CI/git-hook automation, session-length evals). Not repeated here.

## Decisions

**1. Harness binary: `cmd/eval-roles`.** Matches the existing hyphenated naming under `cmd/` (`run-agent`, `validate-result`). A separate binary rather than a `run-agent` subcommand, because its job — walk a case library, aggregate a pass/fail report — is categorically different from running one role once.

**2. Case library layout: `evals/<role>/<case-id>/`.** Top-level directory alongside `roles/`/`skills/`, matching the project's existing convention of top-level content directories. Each case directory contains `fixture/` (initial file tree for the target repo), `task.md` (task prompt), and `expect.yaml` (declarative expectation). Discovered by a directory walk (`evals/*/*/`), not a registry file — adding a case is adding a directory.

**3. `expect.yaml` schema (first cut):**
```yaml
role: implementer            # sanity check against the case's own directory
checks:
  - kind: outcome
    expect: done              # done | needs_human | blocked | failed
    questions_not_empty: true # only meaningful when expect: needs_human
  - kind: diff_scope
    allow: ["src/**"]
  - kind: fixture_tests
    command: "go test ./..."
```
`kind: llm_judge` is reserved (fields `criteria:`, `judge_role:`), parsed but rejected with an explicit "check kind not implemented" error if used — see spec requirement "Unimplemented check kind".

**4. Check dispatch: a `Checker` interface, one implementation per kind.** `Run(caseDir, result) (pass bool, detail string, err error)`, registered in a `map[string]Checker` keyed by `kind` at startup. Adding `llm_judge` later means adding one file and a map entry, not touching the harness's main loop or the case format.

**5. Fixture materialization.** For each case: create a temp dir, copy `fixture/` into it, `git init && git add -A && git commit` (same trivial pattern as test helpers like `internal/runner/input_test.go:25`'s `gitRepo(t)` — duplicated, not imported, since those helpers are `*testing.T`-bound and this harness runs as an external process). Then invoke `cmd/run-agent --role <role> --workdir <tempdir> --task <case>/task.md --eval` and read the resulting `result.json`.

**6. Budget exclusion: `run-agent --eval` flag + `ledger.Entry.Eval` field + filter in `budget.go`.**
- Add `--eval` (bool) to `cmd/run-agent`. When set, the accounting step writes `ledger.Entry{..., Eval: true}`.
- Add `Eval bool \`json:"eval,omitempty"\`` to `ledger.Entry` (`internal/ledger/ledger.go`). Omitted/false for every historical entry — no migration needed.
- Extend `ledger.Filter` with an exclusion the pipeline's daily-spend query sets (e.g. `ExcludeEval: true`), so `internal/pipeline/budget.go`'s `per_role_daily` computation skips tagged entries. Production `tick()` calls pass this; nothing else needs to change.

This is the smallest change that closes the real risk identified during design: `per_role_daily` is computed from the *shared* ledger, not from anything `run-agent` itself gates — so tagging at write time is the only way to tell an eval run apart from a production run after the fact.

**7. `diff_scope` check semantics.** After the role's run completes, diff the temp fixture's working tree against its initial fixture commit (`git diff --name-only <initial-commit>`); fail the check if any changed path falls outside the declared `allow` globs. This is a proxy for "the role didn't write outside its expected scope" — it does not detect a write attempt routed through `Bash` and rejected by Claude Code's own `permissions.deny`, since nothing in the current architecture surfaces a rejected tool-call attempt as structured data (see proposal.md's noted limitation; a full respect-eval class with `run.log` parsing is explicitly deferred).

## Risks / Trade-offs

- [Risk] `diff_scope` cannot catch a Bash-routed write attempt → [Mitigation] documented, accepted limitation; a dedicated respect-eval class with `run.log` parsing is deferred, not silently dropped.
- [Risk] Single-run pass/fail can be noisy if a role's behavior is occasionally non-deterministic on a given case → [Mitigation] accepted trade-off for MVP; the `checks:` list is additive, so a statistical N-run mode can be added per-case later without a schema change.
- [Risk] A new top-level `evals/` directory could be confused with the "evals for roles" language already used in `docs/DESIGN.md` §4 → [Mitigation] this change is that deferred item; no existing directory name collision (`evals/` does not currently exist).

## Migration Plan

None. Purely additive: new binary, new top-level directory, one new CLI flag, one new struct field defaulting to absent/false on every historical ledger entry.

```

## docs/openspec/changes/role-eval-harness/tasks.md

- Source: docs/openspec/changes/role-eval-harness/tasks.md
- Lines: 1-35
- SHA256: 3a5db3226a01e229baa2439038a4ad67ec0f1f8c4a9040cc09817496e9d10a62

```md
## 1. Ledger and budget plumbing

- [ ] 1.1 Add an `--eval` bool flag to `cmd/run-agent`; thread it through to the accounting step. Verify: invoking `run-agent --eval ...` writes a ledger entry with `eval: true` in `ledger.jsonl`.
- [ ] 1.2 Add `Eval bool \`json:"eval,omitempty"\`` to `ledger.Entry` (`internal/ledger/ledger.go`). Verify: `go test ./internal/ledger/...` passes; an entry with `Eval: true` round-trips through JSON marshal/unmarshal correctly.
- [ ] 1.3 Extend `ledger.Filter` with an eval-exclusion option and wire it into `internal/pipeline/budget.go`'s `per_role_daily` daily-spend query. Verify: a unit test with both eval and non-eval ledger entries for a role on the same day shows `per_role_daily` spend counting only the non-eval entries.

## 2. Harness core (`cmd/eval-roles`)

- [ ] 2.1 Scaffold `cmd/eval-roles/main.go`: discover case directories under `evals/<role>/<case-id>/`, with an optional flag to filter by role or case name. Verify: running against an empty `evals/` tree reports "0 cases found" without erroring.
- [ ] 2.2 Implement fixture materialization: copy a case's `fixture/` into a temp dir, `git init && git add -A && git commit`. Verify: a unit test confirms the temp dir is a valid git repo with the fixture's contents committed.
- [ ] 2.3 Implement invocation of `cmd/run-agent --role <role> --workdir <tempdir> --task <case>/task.md --eval` and parse the resulting `result.json` into `internal/runner.Result`. Verify: running one real case end-to-end produces a parsed `Result` matching the actual run outcome.
- [ ] 2.4 Implement a pass/fail summary report across all discovered cases (case name, pass/fail, failure detail per failed check). Verify: running the harness against a mix of passing and deliberately-failing cases prints a summary that correctly counts both.

## 3. Check dispatcher and check kinds

- [ ] 3.1 Implement `expect.yaml` parsing (`role`, `checks` list) and the `Checker` interface (`Run(caseDir, result) (pass bool, detail string, err error)`). Verify: unit test parses a case using all three MVP check kinds plus a case declaring `llm_judge` without a parse error.
- [ ] 3.2 Implement the `outcome` checker (asserts `Outcome` value; when `expect: needs_human`, also asserts non-empty `Questions`). Verify: unit tests cover `done`/`needs_human`/`blocked`/`failed` against synthetic `Result` values.
- [ ] 3.3 Implement the `diff_scope` checker (diffs the fixture's working tree against its initial commit, checks changed paths against `allow` globs). Verify: unit test with an in-scope change passing and an out-of-scope change failing.
- [ ] 3.4 Implement the `fixture_tests` checker (runs the declared command inside the fixture, checks exit code). Verify: unit test with a fixture whose command passes and one where it deliberately fails.
- [ ] 3.5 Implement the dispatcher's explicit failure for `kind: llm_judge` ("check kind not implemented"), not a silent no-op. Verify: a case declaring `llm_judge` causes the harness to report that case as failed with an explicit "not implemented" message.

## 4. Golden case library (MVP: 6 cases)

- [ ] 4.1 Author `evals/analyst/capability-basic-plan/` — well-specified task; expect `outcome: done` plus required plan sections present. Verify: passes against the current `analyst` role.
- [ ] 4.2 Author `evals/analyst/escalation-ambiguous-task/` — deliberately ambiguous task; expect `outcome: needs_human`, `questions_not_empty: true`. Verify: passes against the current role.
- [ ] 4.3 Author `evals/implementer/capability-basic-bugfix/` — fixture with a known bug and a task describing the fix; expect `outcome: done` plus `fixture_tests: go test ./...` green. Verify: passes against the current role.
- [ ] 4.4 Author `evals/implementer/escalation-ambiguous-task/` — deliberately ambiguous task; expect `outcome: needs_human`. Verify: passes against the current role.
- [ ] 4.5 Author `evals/reviewer/capability-spot-defect/` — fixture with a deliberately planted defect; expect the review to identify it. Verify: passes against the current role.
- [ ] 4.6 Author `evals/reviewer/escalation-ambiguous-task/` — deliberately ambiguous task; expect `outcome: needs_human`. Verify: passes against the current role.

## 5. End-to-end verification

- [ ] 5.1 Run the full 6-case MVP sweep via `cmd/eval-roles` against the roles' current `role.md`/`role.yaml`. Verify: harness summary reports 6/6 passed.
- [ ] 5.2 Demonstrate the regression signal: temporarily remove the escalation guidance from `implementer`'s `role.md`, re-run its escalation case, confirm it fails, then restore the file. Verify: the case fails while the guidance is removed; `git diff` shows the role file byte-for-byte restored afterward.
- [ ] 5.3 Confirm eval-sweep runs do not change `per_role_daily` budget consumption used by the production pipeline. Verify: compare a role's `per_role_daily` spend calculation before and after running the eval sweep for that role — unchanged.

```

## docs/openspec/changes/role-eval-harness/specs/role-eval-harness/spec.md

- Source: docs/openspec/changes/role-eval-harness/specs/role-eval-harness/spec.md
- Lines: 1-44
- SHA256: 246ecae2bb0254570e48f6fea3615e594b13eb00882277e677dc5808d02f1b0a

```md
## Purpose

Gives office roles (analyst/implementer/reviewer) a repeatable, manually-triggered quality signal by running each role against declarative golden-case fixtures and reporting deterministic pass/fail results, instead of relying only on manual smoke-testing.

## ADDED Requirements

### Requirement: Harness evaluates a role against a golden case
The system SHALL run a role through the existing agent-run path against a golden case's fixture and produce a pass/fail verdict for that case, based on the case's declared checks.

#### Scenario: All checks pass
- **WHEN** every check declared by a golden case evaluates successfully against the role's run
- **THEN** the harness reports that case as passed

#### Scenario: Any check fails
- **WHEN** at least one check declared by a golden case fails against the role's run
- **THEN** the harness reports that case as failed and identifies which check failed

### Requirement: Golden cases are data, not code
Golden cases SHALL be defined as self-contained repository directories (an initial fixture file tree, a task prompt, and a declarative expectation) that the harness discovers and evaluates without requiring a rebuild.

#### Scenario: Adding a new case
- **WHEN** a new case directory is added to the case library with a valid fixture, task prompt, and expectation
- **THEN** the harness includes that case in its next run without any code change

### Requirement: Check kinds are typed and extensible
Each check declared by a golden case SHALL specify a kind, and the harness SHALL evaluate it through a dispatcher keyed by that kind, so new kinds (including a future LLM-judged kind) can be added without changing existing cases.

#### Scenario: Unimplemented check kind
- **WHEN** a case declares a check kind the harness has no handler for
- **THEN** the harness fails that case explicitly, naming the unimplemented kind, rather than silently skipping the check

### Requirement: Eval runs do not consume production role budget
Runs triggered by the eval harness SHALL be excluded from the `per_role_daily` budget accounting used to gate production runs.

#### Scenario: Running the eval sweep does not affect daily budget
- **WHEN** the eval harness runs golden cases for a role
- **THEN** that role's `per_role_daily` budget consumption, as used to gate production runs, is unaffected

### Requirement: Harness invocation is manual
The system SHALL NOT trigger eval runs automatically; invocation SHALL always be an explicit manual action, with no CI or git-hook trigger.

#### Scenario: Editing a role does not trigger an eval run
- **WHEN** a role's prompt or permissions file is edited and committed
- **THEN** no eval run is triggered automatically by that commit

```
