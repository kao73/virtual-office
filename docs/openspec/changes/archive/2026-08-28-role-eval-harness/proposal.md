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
