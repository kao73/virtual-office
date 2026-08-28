# Brainstorm Summary

- Change: role-eval-harness
- Date: 2026-08-27

## Confirmed Technical Approach

`cmd/eval-roles` discovers golden cases under `evals/<role>/<case-id>/` (`fixture/`, `task.md`, `expect.yaml`). Per case: materialize `fixture/` into a temp git repo (git init/add/commit, capturing the initial commit SHA), invoke `run-agent --role <role> --workdir <tempdir> --task <case>/task.md --eval` as a subprocess (no `--project` — evals exercise the role's own contract, not a project's merged permission layers), then read the result via the existing `runner.ReadResult(workdir)` (no custom JSON parsing). Every check declared in `expect.yaml` runs against a `CheckContext{FixtureDir, InitialCommit, Result, Spec}` through a `Checker` interface dispatched by `map[string]Checker` keyed on `kind`. Three kinds implemented for MVP: `outcome`, `diff_scope`, `fixture_tests`; `kind: llm_judge` is reserved and deliberately absent from the dispatch map, so a lookup miss produces an explicit "not implemented" failure rather than a silent no-op.

## Key Trade-offs and Risks

- Malformed case (bad `expect.yaml`, missing fixture/task) → that case's status is `errored`, the sweep continues with the rest, rather than aborting the whole run.
- `fixture_tests` check has a 5-minute default timeout; on expiry it is reported as a normal failed check ("timed out after Ns"), not a harness crash.
- A case with multiple checks runs and reports every check, not just the first failure — better diagnostics per sweep.
- `cmd/eval-roles` exit codes mirror `run-agent`'s own 1=behavioral/2=infra split, one level up: `0` all cases passed, `1` swept cleanly with ≥1 genuine `failed` case, `2` ≥1 case `errored` or the harness itself couldn't run.
- `diff_scope` remains a proxy, not a full respect-eval — cannot catch a Bash-routed write attempt (documented, accepted limitation carried over from the Open-phase design.md).

## Testing Strategy

Two tiers: (1) `go test ./cmd/eval-roles/...` — unit tests per `Checker` against synthetic `runner.Result` values and small temp git repos, plus harness-plumbing tests (discovery, materialization, errored-then-continue) using a fake `run-agent` stand-in; free, deterministic, no LLM calls. (2) The 6 real golden cases are validated by an actual live run of the finished harness against the real roles (~$2-3, manual, per tasks.md 5.1) — not part of `go test`.

## Spec Patches

None. The existing delta spec (`specs/role-eval-harness/spec.md`) requirements and scenarios remain accurate and sufficient after this deeper technical pass — no missing acceptance scenarios were found.
