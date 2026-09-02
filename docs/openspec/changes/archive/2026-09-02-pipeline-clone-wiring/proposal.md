## Why

The tracker-driven pipeline (`internal/pipeline.Office`) always runs role agents on a bind-mounted worktree. Comet Native's lock coordinator (`root-move.lock`) is unreliable specifically on that bind-mounted (virtiofs) filesystem — root-caused live in `docs/superpowers/plans/2026-08-30-role-comet-native-workflow.md` Task 21 via an A/B test on the same sandbox instance — and can fail unpredictably within a single run, independent of any stale state left by a previous run. `sbx --clone` (added in commit `0b49599`) avoids this by running the agent on the sandbox's own disk instead, but it was deliberately left unwired from `internal/pipeline/agent.go`'s `SandboxAgent.Run` (Task 21 "Scope decision", Task 22 "planned, not started") because the pipeline's workspace model (bare-repo-plus-worktree) is a shape `--clone` rejects outright. Manual `run-agent --clone` runs already validate the role mechanics work under `--clone` (Task 22's "Manual real-task check"), but also found that `--clone`'s commit-only sync boundary silently drops Comet Native's `comet-state.yaml` progress across role handoffs unless something explicitly carries it over — a gap the plan left as a named follow-up rather than fixing on the spot.

## What Changes

- Wire `runagent.Options.Clone` into `SandboxAgent.Run` so pipeline-driven role runs on the `sbx` backend use `sbx --clone` instead of a bind-mount.
- Add a disposable clone-source step (`internal/workspace`): a fresh, ordinary (non-bare, non-worktree) clone of the task branch off the bare repo, used only as `--clone`'s primary path, removed after every run regardless of outcome (success, timeout, truncation).
- Generalize `cloneSyncIn`/`cloneSyncOut`'s `Dirs` sync (`internal/backends/sbx/clone.go`) to target `CloneSync.FetchInto` instead of the hardcoded `Workspaces[0].Path`, since the pipeline's clone source and its real persistent worktree are now different paths (today's only caller, `cmd/run-agent`, keeps them equal on purpose).
- Sync `docs/comet/changes/*/comet-state.yaml` back to the real worktree unconditionally after a `--clone` run, by glob (the change name isn't known statically), so Comet Native's phase state survives role-to-role handoffs through the pipeline the same way it already does under the bind-mount path.
- Route `fetchBranch`'s post-run fast-forward merge into the real worktree (`ws.Dir`) rather than the disposable clone source.

## Capabilities

### New Capabilities
- `pipeline-clone-isolation`: the tracker-driven pipeline's role runs execute on an isolated sandbox clone (not a bind-mount) on the `sbx` backend, with Comet Native state, the exchange directory, and agent commits all surviving the run and role-to-role handoffs correctly, and the disposable clone source never leaking.

### Modified Capabilities
(none — existing capabilities `role-native-workflow` and `role-sandbox-permissions` describe phase/permission behavior unaffected by which sandbox execution mode the pipeline uses.)

## Impact

- `internal/pipeline/agent.go` — `SandboxAgent.Run` sets `Options.Clone` for the `sbx` backend.
- `internal/pipeline/pipeline.go` — orchestrates creating/cleaning up the clone-source step per run.
- `internal/workspace` — new clone-source step (fresh plain clone off the bare repo).
- `internal/backends/sbx/clone.go` — `Dirs` sync retargeted to `FetchInto`; new `comet-state.yaml` glob sync.
- `internal/runner/launch.go` — `CloneSync` field additions if the retargeted sync needs them.
- Not touched: `roles/*/role.md`, `internal/pipeline/archive.go`, `cmd/run-agent`, `cmd/eval-roles` (already wired for `--clone` independently).
