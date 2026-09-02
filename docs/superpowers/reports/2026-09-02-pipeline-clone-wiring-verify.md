## Verification Report: pipeline-clone-wiring

Full verification (`verify_mode: full` — 17 tasks, 1 delta-spec capability, 32 files changed). Not a fresh keyword-search sweep: this change already carries stronger evidence than that — a live end-to-end run through the real `Office` conveyor (not a synthetic eval), 7 task-level code reviews (one per task, `review_mode: thorough`), and one final whole-branch review plus a scoped re-review of its fix wave. This report traces each spec requirement against that evidence rather than re-deriving it from scratch.

### Summary

| Dimension    | Status |
|--------------|--------|
| Completeness | 17/17 tasks done; all 5 delta-spec requirements have both code evidence and live-run evidence |
| Correctness  | 5/5 requirements match implementation; one scenario (truncated-run cleanup) verified by pre-existing unit tests only, not live — explicitly and honestly scoped that way by the plan itself |
| Coherence    | design.md and code agree; final review traced the `.agent/result.json` chain end-to-end across all four touched files and found no contradiction |

### Requirement-by-requirement

**1. Pipeline role runs use sandbox-local isolation on the `sbx` backend** — ✅ Implemented and live-confirmed.
- `internal/pipeline/agent.go`'s `cloneOptionsFor` builds a disposable clone source and `runagent.Options.Clone` on any non-`local` backend (Tasks 1-2).
- Live: `docs/superpowers/plans/2026-09-02-pipeline-clone-wiring.md`'s Task 5 "Result" section records a real `./bin/runner tick` sequence on the `sbx` backend running analyst → implementer → reviewer, each via `sbx create --clone`, not a bind-mount.

**2. Comet Native phase state survives role-to-role handoffs** — ✅ Implemented and live-confirmed.
- `internal/backends/sbx/clone.go`'s `syncCometState`/`copyCometStateMatches` (Task 4, hardened by the final-review fix wave) copy `comet-state.yaml` out of the sandbox unconditionally after `fetchBranch`.
- Live: the plan's Task 5 result records `comet-state.yaml` phase progress (`build` → `verify` → `archive`) surviving every real role handoff on task `EXP-1`, plus the analyst's own second attempt in the log correctly resuming from a Shape already confirmed by a prior attempt — the exact scenario this requirement names.

**3. The exchange directory and agent commits round-trip correctly** — ✅ Implemented; live-confirmed for the success path, unit-tested only for the truncated-run scenario.
- `internal/backends/sbx/clone.go`'s `Dirs` loop (Task 3) plus the pre-existing `fetchBranch`/`commitLeftovers` machinery (untouched, Non-Goal) deliver `.agent/result.json`, `.comet/current-change.json`, `.comet/runtime`, and git commits.
- Live: `EXP-1`'s full run shows real commits (`3e29b8a`, `d5cee35`, `331db8f`, `30def67`) and a real `.agent/result.json` at each stage, plus a `commitLeftovers` safety-net commit that swept stray `__pycache__` files — an incidental detail that independently corroborates the machinery worked, not just the happy path.
- ⚠️ The scenario's specific "truncated by timeout or step limit" clause was **not** exercised live — the plan says so explicitly (Task 5's own self-review notes) and relies on this file's pre-existing, already-passing unit test suite (`cloneSyncOut`'s "called on every return path" tests, unchanged by this plan). This is a reasonable, disclosed scope decision, not a gap anyone tried to hide — see WARNING below.

**4. Sandbox-local isolation leaves no disposable state behind** — ✅ Implemented and live-confirmed.
- `internal/pipeline/agent.go`'s `defer cleanup()` (Task 2) plus `workspace.CloneSource`'s internal-cleanup-on-failure contract (Task 1, `TestCloneSourcePropagatesErrorOnMissingBranch`).
- Live: `sbx ls --quiet` and a `pipeline-clone-*` tmpdir search both came back empty after the full `EXP-1` run.
- ⚠️ Same live/unit split as Requirement 3: the timeout-specific case rests on unit coverage, not a live truncated run.

**5. Backends without sandbox isolation report non-application instead of failing silently** — ✅ Implemented and live-confirmed, verbatim.
- `internal/pipeline/agent.go` calls `runagent.CloneNotice(a.Backend, true)` unconditionally (Task 2).
- Live: a `local`-backend run (`EXP-2`) produced the exact notice text (`"бэкенд local --clone не поддерживает..."`) in its log, with the commit landing directly in the real worktree and no disposable clone source created.

### Design/Coherence cross-checks (no issues found)

- The final whole-branch review (recorded in the SDD ledger before it was cleaned up, and in the plan's own commit history) traced `.agent/result.json`'s full path — written in-container → synced by `cloneSyncOut` to `Clone.FetchInto` → read back by `runagent.Execute`'s `resultWorkdir` — and confirmed every link names the same host directory. This is exactly `design.md`'s central decision ("A disposable ordinary clone... separate from `Workspaces[0].Path`'s current identity with `FetchInto`") and it holds under tracing, not just by inspection.
- `cmd/run-agent --clone`'s pre-existing behavior (Task 21/22, this plan's Non-Goal) is provably unregressed: its caller keeps `Workdir == Clone.FetchInto`, which collapses every new split (`containerRoot`/`hostRoot`, `resultWorkdir`) back to the old single-`primary` behavior — confirmed by reading `cmd/run-agent/main.go`'s construction, not assumed.
- Two Important findings from the final review (`syncCometState`'s early return re-losing `.agent/result.json` on a transient failure; an unconditional overwrite of a tracked `comet-state.yaml` risking a wedged future merge) were fixed in commit `73597db` and independently re-reviewed clean — both were in Task 4's code, the newest and least-live-tested piece before the fix.

### Issues

#### CRITICAL

None.

#### WARNING

1. **Requirement 3 and Requirement 4's timeout/truncation scenarios are unit-tested but not live-verified.**
   - `specs/pipeline-clone-isolation/spec.md:24-26` and `:31-33` both specifically name a truncated/timed-out run.
   - The live `EXP-1`/`EXP-2` runs in Task 5 were both healthy, complete runs — none were truncated by timeout or step limit.
   - Coverage instead comes from this file's pre-existing unit tests (`cloneSyncOut`'s "called on every return path" contract, `cloneOutcome`'s timeout-classification tests) — all unchanged by this plan and already passing.
   - Not a blocker: the plan's own Task 5 self-review notes disclose this scope decision honestly rather than silently skipping it, and simulating a live timeout against a real paid agent run is a materially more expensive verification than this change's own acceptance criteria demand. Recommendation, non-blocking: if a future change touches this same code path again, consider an `eval-roles`-level fixture that forces a step-limit truncation mid-`--clone` run, mirroring how `role-comet-native-workflow`'s own verify report flagged an analogous gap for its resume scenario.

#### SUGGESTION

1. Several Minor items from the final whole-branch review were deferred, not fixed, as genuinely non-blocking cost/robustness polish (recorded in the now-deleted SDD ledger, summarized in the plan's commit history): `syncCometState` copies the whole `docs/comet/changes` tree per run rather than narrowing via the already-synced `.comet/current-change.json`; no startup sweep exists for a `pipeline-clone-*` temp directory leaked by a hard process kill (as opposed to a normal return path, which `defer cleanup()` already covers); `cloneDirs` (`internal/pipeline/agent.go`) duplicates a literal already present in `cmd/run-agent/main.go`; `workspace.CloneSource`'s `git clone` has no `context.Context`/timeout, inconsistent with `resolveExcludeFile`'s enclosing-timeout fix in the same plan. None are load-bearing — worth a follow-up if this area is touched again, not before archive.

### Final Assessment

No CRITICAL issues. One WARNING, disclosed and accepted rather than newly discovered, covering a scenario this change's own plan already flagged as out of live-verification scope for a defensible cost reason. **Ready for archive.**
