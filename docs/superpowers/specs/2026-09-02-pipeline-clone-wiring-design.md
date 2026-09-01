---
comet_change: pipeline-clone-wiring
role: technical-design
canonical_spec: openspec
---

# Pipeline `--clone` wiring — technical design

See `docs/openspec/changes/pipeline-clone-wiring/proposal.md` for motivation and scope, and `docs/openspec/changes/pipeline-clone-wiring/specs/pipeline-clone-isolation/spec.md` for the behavior contract. This document is the implementation-level refinement of the open-phase `design.md`'s "path root" decision.

## Current shape

`internal/backends/sbx/clone.go`'s `cloneSyncIn`/`cloneSyncOut` read a single `primary := l.Workspaces[0].Path` and use it for two purposes that happen to coincide in today's only caller (`cmd/run-agent --clone`, which keeps `Workspaces[0].Path == CloneSync.FetchInto == workdir` by construction):

1. The path root `sbx cp`/`sbx exec` addresses **inside the container** (what `sbx create --clone` actually cloned into the sandbox).
2. The **host-side** path to read/write the exclude file and `Dirs` content — content `runner.PrepareInput` wrote directly onto the real worktree.

`sbx create --clone` rejects both a bare repo and a `git worktree` as its primary path (live-verified, plan Task 21). The pipeline's task worktree (`ws.Dir`, from `internal/workspace`) is a `git worktree`, so it cannot be `Workspaces[0].Path` directly.

## Sequence

Per pipeline role run, on the `sbx` backend:

1. `internal/pipeline/pipeline.go`'s `work()` runs `runner.PrepareInput(ws.Dir, role, passport, input)` exactly as today — writes `.agent/task.md`, `.agent/context.md`, `.agent/run.json`, git-exclude rules, may add a system commit (`EnsureCometHookAllowPaths`).
2. `work()` builds `pipeline.Request` with a new `Branch` field set to `ws.Branch` (the pipeline already has this value; no new lookup).
3. `SandboxAgent.Run` (backend `sbx`): after `PrepareInput` has run (so the clone captures any system commit it made), creates a disposable ordinary clone: `git clone --branch <req.Branch> <req.Workdir> <tmp>`, where `tmp` is a fresh `os.MkdirTemp("", "pipeline-clone-*")` directory — plain system temp, deliberately outside `workspace.Manager`'s task-worktree tree, so it needs no changes to that package's existing sweep/cleanup logic and is fully owned by this one call.
4. Builds `runagent.Options{..., Clone: &runner.CloneSync{FetchInto: req.Workdir, Branch: req.Branch, Dirs: [".agent", ".comet/current-change.json", ".comet/runtime"]}}` with `Workspaces: [{Path: cloneSrc}]`, calls `runagent.Execute`.
5. If step 3 fails (disk full, git error), the run fails outright — propagates like any other pre-flight error in `work()` today. The task stays leased; the reaper reclaims it on its next sweep. No silent bind-mount fallback (matches this file's own `NetworkNotice`/`CloneNotice` convention: silent non-application must not read as applied).
6. `sbx create --clone <cloneSrc>` creates the sandbox; `cloneSyncIn` copies the exclude file and `Dirs` content in (see "Path-root split" below); the agent runs; `cloneSyncOut` runs on every return path (success, non-zero exit, timeout, exec error) before the sandbox is removed.
7. The disposable `cloneSrc` directory is removed unconditionally once `runagent.Execute` returns, regardless of outcome — the same "always called" discipline `cloneSyncOut` already follows for the sandbox itself.

## Path-root split (the core change)

Rename the local `primary` in `cloneSyncIn`/`cloneSyncOut` to `containerRoot` (still `l.Workspaces[0].Path`) and read `l.Clone.FetchInto` directly at the specific host-facing call sites:

- `syncExcludeFile`: source is `filepath.Join(l.Clone.FetchInto, excludeFile)`; the sandbox-side destination is computed separately as `filepath.Join(containerRoot, excludeFile)`'s directory, not derived from the host source string.
- `cloneSyncIn`'s `Dirs` loop: `os.Stat`/`sbx cp` source is `filepath.Join(l.Clone.FetchInto, dir)`; the in-container destination argument to `sbx cp` is built from `containerRoot`.
- `cloneSyncOut`'s `Dirs` loop: `sbx cp` source (in-container) is built from `containerRoot`; the host destination is `filepath.Join(l.Clone.FetchInto, dir)`.
- New `comet-state.yaml` glob sync (after `fetchBranch`, same function): locate `docs/comet/changes/*/comet-state.yaml` inside the sandbox (relative to `containerRoot`), copy each match to the corresponding path under `l.Clone.FetchInto`. A missing `docs/comet/changes/` is a legitimate no-op (no active Comet Native change), the same principle `Dirs`' missing-`.comet/runtime` case already follows.

**Unchanged, confirmed by re-reading each function against this split:**
- `commitLeftovers` — every operation is an `sbx exec` script running *inside* the container; its `primary` argument stays `containerRoot` untouched.
- `fetchBranch` — its `primary` argument is used only to read the `sandbox-<name>` git remote URL that `sbx create --clone` wrote onto the container-source path (`containerRoot`); it already merges into `c.FetchInto` correctly today and needs no code change.

**Exclude-file worktree subtlety:** the current hardcoded `.git/info/exclude` lookup is valid only because its host source (`Workspaces[0].Path` today) can never be a worktree — `sbx create --clone` itself refuses one. Once the host source for this read becomes `l.Clone.FetchInto` (a real `git worktree` in the pipeline case), `.git` there is a file, not a directory, and `info/exclude` lives in the common git directory instead. Resolve via `git rev-parse --git-common-dir` against `FetchInto` when it's a worktree; keep today's direct path as a fast path when it's confirmed not one (covers the existing `cmd/run-agent` caller unchanged).

## Testing Strategy

- `internal/workspace`: new clone-source function, tested against a real worktree fixture — clone succeeds, checked-out branch matches, cleanup removes the temp directory, failure (e.g. missing branch) propagates as an error rather than a partial/empty clone.
- `internal/backends/sbx/clone_test.go`: extend the existing fake-`step` table-driven tests with `Workspaces[0].Path != FetchInto` fixtures, asserting the sandbox-side `sbx cp`/`sbx exec` argument stays anchored to `Workspaces[0].Path` while the host-side argument moves to `FetchInto`. New dedicated test for the exclude-file resolution against a real `git worktree` (not the plain-repo fixtures used elsewhere in this file). New present/absent-case tests for the `comet-state.yaml` glob sync.
- `go build ./... && go vet ./... && go test ./...` clean.
- Live end-to-end (not a golden/eval fixture): a real task through the tracker-driven `Office` conveyor on the `sbx` backend, a full analyst → implementer → reviewer Comet Native Shape → Build → Verify chain, confirming `comet-state.yaml` phase progress survives each handoff and `sbx ls` is empty afterward (no leaked clone source, no leaked sandbox).
- Confirm a `local`-backend debug run still proceeds directly on the host worktree and the existing `runagent.CloneNotice` fires (backend never sees `Launch.Clone`, per its existing documented contract).

## Risks / Trade-offs

- **[Risk]** Getting the container/host split half-right silently misroutes `.agent/result.json` or drops `comet-state.yaml` — the exact class of bug this same file's own review history (plan Task 21/22) has repeatedly found. → Mitigated by the directional table-driven tests above plus the live end-to-end run before calling this done.
- **[Trade-off]** One extra local `git clone` per `sbx`-backend pipeline run. Accepted — cheap and local, and far less costly than a truncated run burning its turn/token budget on lock conflicts.

## Migration Plan

No data migration. `Launch.Clone` stays `nil` (today's bind-mount behavior) until `SandboxAgent.Run` is updated in the same change, so there's no intermediate half-wired state to worry about. Rollback is reverting the commit — no persisted format changes.
