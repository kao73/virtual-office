# Brainstorm Summary

- Change: pipeline-clone-wiring
- Date: 2026-09-02

## Confirmed Technical Approach

Wire `runagent.Options.Clone` into `internal/pipeline/agent.go`'s `SandboxAgent.Run` on the `sbx` backend:

1. `pipeline.go`'s `work()` runs `PrepareInput(ws.Dir, ...)` as today, unchanged.
2. `SandboxAgent.Run` creates a disposable, ordinary clone of the task branch (`git clone --branch <branch> <ws.Dir> <tmp-dir>`, `tmp-dir` from `os.MkdirTemp("", "pipeline-clone-*")` — plain system temp, not under `workspace.Manager`'s task-worktree tree, to avoid entangling it with existing worktree sweep/cleanup logic). The branch name comes from a new `Branch string` field on `pipeline.Request`, populated from `ws.Branch` in `work()` (reuses info the pipeline already has, instead of re-deriving via a fresh `git rev-parse` inside `agent.go`).
3. Builds `Launch{Workspaces: [{Path: cloneSrc}], Clone: &CloneSync{FetchInto: req.Workdir, Branch: req.Branch, Dirs: [...]}}`.
4. In `internal/backends/sbx/clone.go`, `cloneSyncIn`/`cloneSyncOut` split the single `primary` variable's two conflated roles: `containerRoot` (`l.Workspaces[0].Path`, the disposable clone — what `sbx cp`/`sbx exec` addresses inside the container) stays as-is; host-side reads/writes (exclude-file source, `Dirs` sources/destinations, the new `comet-state.yaml` glob) move to `l.Clone.FetchInto`. Each affected call site builds the container path and the host path as two separate `filepath.Join` calls instead of deriving one from the other's string (today's latent coupling, safe only because both roots are equal in the one caller that exists today).
5. `commitLeftovers` and `fetchBranch` need **no changes** — confirmed both already operate correctly against `containerRoot`/`c.FetchInto` respectively.
6. The exclude-file sync (`syncExcludeFile`) needs worktree-safe resolution once its host source is `FetchInto` (a real worktree in the pipeline case): resolve via `git rev-parse --git-common-dir` against `FetchInto`, keeping the current direct `.git/info/exclude` path as a fast path when the host source is confirmed not a worktree.
7. Disposable clone source is removed unconditionally after every run (success, timeout, truncation) — same discipline `cloneSyncOut` already follows.

Rejected alternatives (path-root split): a second parallel string parameter (works, but two same-typed strings at call sites risk silent transposition) and a small `syncRoots{Container, Host}` struct (safer via field names, but more ceremony than this codebase's established style for `clone.go`). Chose the no-new-parameter approach: rename local `primary` to `containerRoot`, read `l.Clone.FetchInto` directly at the handful of host-facing call sites.

## Key Trade-offs and Risks

- **[Risk]** Splitting container/host roots is easy to get half-right (leaving one `sbx cp` call anchored to the wrong root) → silently misroutes `.agent/result.json` or drops `comet-state.yaml`. Mitigation: table-driven tests per sync direction with `Workspaces[0].Path != FetchInto` fixtures, plus a live end-to-end pipeline run through Comet Native Shape→Build→Verify.
- **[Risk]** Exclude-file worktree resolution is new territory for this file. Mitigation: dedicated test against a real `git worktree`, not a plain repo.
- **[Trade-off]** One extra local `git clone` per `sbx`-backend pipeline run (cheap, local, off the bare repo) — accepted, far cheaper than a truncated run burning its turn budget on lock conflicts.
- **Confirmed with user:** clone-source creation failure fails the run outright (no silent bind-mount fallback — matches this codebase's existing `NetworkNotice`/`CloneNotice` convention against silent non-application). Clone source lives in system temp (`os.MkdirTemp`), not under `workspace.Manager`'s tree.

## Testing Strategy

- `internal/workspace`: new clone-source function tested against a real worktree fixture (clone succeeds, branch matches, cleanup removes the temp dir).
- `internal/backends/sbx/clone_test.go`: table-driven tests with `Workspaces[0].Path != FetchInto`, asserting container-side args stay pinned to `Workspaces[0].Path` and host-side args move to `FetchInto`; a dedicated test for the exclude-file resolution against a real `git worktree`; present/absent cases for the new `comet-state.yaml` glob sync.
- `go build/vet/test ./...` clean.
- Live end-to-end: a real task through the tracker-driven `Office` conveyor on the `sbx` backend, full analyst → implementer → reviewer Comet Native Shape → Build → Verify chain, confirming `comet-state.yaml` phase progress survives each handoff and `sbx ls` is empty afterward (no leaked clone source).
- Confirm a `local`-backend debug run still proceeds directly on the host worktree with the existing `CloneNotice` firing.

## Spec Patches

None — `specs/pipeline-clone-isolation/spec.md` from the open phase already covers the observable behavior; nothing found during design that changes scope or needs a new acceptance scenario.
