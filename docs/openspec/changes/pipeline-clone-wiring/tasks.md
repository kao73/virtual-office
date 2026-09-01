## 1. Disposable clone-source step

- [x] 1.1 Add a function in `internal/workspace` that creates a fresh, ordinary (non-bare, non-worktree) `git clone` of a worktree's current branch tip into a new temp directory, and a matching cleanup function.
- [x] 1.2 Unit-test it against a real worktree fixture (mirroring existing `internal/workspace` test style): clone succeeds, checked-out branch matches, cleanup removes the temp directory.

## 2. Wire `--clone` into `SandboxAgent.Run`

- [x] 2.1 Add a `Branch string` field to `pipeline.Request`, populated from `ws.Branch` in `pipeline.go`'s `work()` (reuses info the pipeline already has). In `internal/pipeline/agent.go`, after `runagent.Execute`'s prerequisites are ready but using the same request flow, create the disposable clone source from `req.Workdir` (post-`PrepareInput` state, so it captures any system commit `PrepareInput` made) when `a.Backend` is `sbx`, and build `runagent.Options.Clone` with `FetchInto: req.Workdir`, `Branch: req.Branch`, and the existing `Dirs` list.
- [x] 2.2 Ensure the clone source is removed on every return path (success, non-zero exit, timeout, exec error), matching `cloneSyncOut`'s "always called" discipline. If clone-source creation itself fails, fail the run outright (no silent bind-mount fallback) — the task stays leased and the reaper reclaims it on its next sweep.
- [x] 2.3 Confirm `runagent.CloneNotice` still fires correctly when `a.Backend` is `local` (Clone is still constructed but the `local` backend ignores it per `Launch.Clone`'s existing contract).

## 3. Split "container path root" from "host path root" in `internal/backends/sbx/clone.go`

- [ ] 3.1 Change `cloneSyncIn`/`cloneSyncOut` to use `l.Workspaces[0].Path` only for the in-container path root (what `sbx cp`/`sbx exec` addresses inside the sandbox) and `l.Clone.FetchInto` for every host-side read/write (exclude-file source, `Dirs` sources and destinations).
- [ ] 3.2 Resolve `syncExcludeFile`'s source path against `FetchInto` in a worktree-safe way (e.g. `git rev-parse --git-common-dir`), keeping the current direct `.git/info/exclude` lookup as a fast path when the host source is confirmed not a worktree.
- [ ] 3.3 Update `clone_test.go`'s fakes/table-driven tests to cover `Workspaces[0].Path != FetchInto`, asserting the sandbox-side argument stays anchored to `Workspaces[0].Path` and the host-side argument moves to `FetchInto`.
- [ ] 3.4 Add a test with `FetchInto` pointed at a real `git worktree` (not a plain repo) covering the exclude-file resolution from 3.2.

## 4. Sync `comet-state.yaml` unconditionally

- [ ] 4.1 After `fetchBranch` in `cloneSyncOut`, locate `docs/comet/changes/*/comet-state.yaml` inside the sandbox (by glob, or via the already-synced `.comet/current-change.json` when present) and copy each match to the corresponding path under `FetchInto`.
- [ ] 4.2 Handle the "no active Comet Native change" case as a legitimate no-op, consistent with how `Dirs` already treats a missing `.comet/runtime`.
- [ ] 4.3 Unit-test both the present and absent cases.

## 5. End-to-end verification

- [ ] 5.1 `go build ./... && go vet ./... && go test ./...` clean.
- [ ] 5.2 Run a real task through the tracker-driven pipeline (`Office` conveyor, not manual `run-agent --clone`) on the `sbx` backend through a full analyst → implementer → reviewer Comet Native Shape → Build → Verify chain, confirming `comet-state.yaml` phase progress survives each handoff and no disposable clone source is left behind (`sbx ls` empty afterward).
- [ ] 5.3 Confirm a `local`-backend debug run still proceeds directly on the host worktree and logs the non-application notice.
