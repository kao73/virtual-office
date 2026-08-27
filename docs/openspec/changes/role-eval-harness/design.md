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
