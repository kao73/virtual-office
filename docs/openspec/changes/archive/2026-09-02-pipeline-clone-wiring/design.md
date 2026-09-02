## Context

`internal/pipeline/agent.go`'s `SandboxAgent.Run` calls `runner.PrepareInput(ws.Dir, ...)` (writes `.agent/task.md`, `.agent/context.md`, `.agent/run.json`, git-exclude rules, and may itself add a system commit via `EnsureCometHookAllowPaths`) directly against the real, persistent worktree `ws.Dir`, then launches the role with `Mounts: ws.Mounts()` — i.e. `Workspaces[0].Path == ws.Dir` today. `ws.Dir` is a `git worktree` checked out from a bare repo (`internal/workspace`). `sbx create --clone` rejects both bare repos and worktrees as its primary path (live-verified, plan Task 21) — it needs an ordinary, non-bare, non-worktree, read/write git repository.

`internal/backends/sbx/clone.go`'s `cloneSyncIn`/`cloneSyncOut` today read a single `primary := l.Workspaces[0].Path` and use it for two distinct purposes that happen to coincide in today's only caller (`cmd/run-agent --clone`, where `Workspaces[0].Path == CloneSync.FetchInto == workdir` by construction):

1. The **host-side source/destination** for the untracked exchange directory (`Dirs`) and the git-exclude file — content `PrepareInput` wrote directly onto the real worktree.
2. The **path root sbx mirrors inside the container** — `sbx cp <host-path> <container>:<same-path>` relies on the container's internal clone having the identical absolute directory layout as `Workspaces[0].Path`.

See proposal.md for why this needs to change (virtiofs lock unreliability) and what changes (pipeline wiring, `comet-state.yaml` sync).

## Goals / Non-Goals

**Goals:**
- Give `sbx --clone` a primary path it will accept (ordinary, non-worktree) without changing where `PrepareInput`, `Workspaces.Push`, and the rest of the pipeline read and write (`ws.Dir` stays the single source of truth on the host).
- Keep the existing `cmd/run-agent --clone` / `cmd/eval-roles --clone` behavior byte-for-byte unchanged when `Workspaces[0].Path == CloneSync.FetchInto` (today's case).

**Non-Goals:**
- Solving Comet Native's virtiofs lock race itself (already root-caused; out of scope here — see proposal.md).
- A general-purpose "sync arbitrary uncommitted state" mechanism — this only extends the existing, narrowly-scoped `Dirs`/exclude-file/`comet-state.yaml` sync.
- Changing `internal/pipeline/archive.go`'s `isolation: current` handling.

## Decisions

**A disposable ordinary clone (`cloneSrc`), separate from `Workspaces[0].Path`'s current identity with `FetchInto`.** `SandboxAgent.Run` creates a fresh, ordinary `git clone` of `ws.Dir`'s current branch tip into a temp directory *after* `PrepareInput` returns (so it includes `PrepareInput`'s own commit, e.g. `EnsureCometHookAllowPaths`) and *before* calling `runagent.Execute`. This clone becomes `Launch.Workspaces[0].Path`; `Launch.Clone.FetchInto` stays `ws.Dir`. The clone is removed unconditionally after the run (success, timeout, or truncation), the same "always called, never skipped" discipline `cloneSyncOut` already follows.

**`cloneSyncIn`/`cloneSyncOut` must stop conflating "path root inside the container" with "host path to read/write".** Once `Workspaces[0].Path` (`cloneSrc`) and `FetchInto` (`ws.Dir`) diverge, the two purposes `primary` serves today split:
  - Host-side reads (`cloneSyncIn`: exclude file, `Dirs` sources) and writes (`cloneSyncOut`: `Dirs` destinations, plus the new `comet-state.yaml` glob) must target `FetchInto` — that's where `PrepareInput` actually wrote the exchange directory and where the rest of the pipeline expects results to land.
  - The path root the container mirrors internally (used to build the `sbx cp`/`sbx exec` argument that names the *in-container* location) must stay anchored to `Workspaces[0].Path` (`cloneSrc`'s layout) — that's what `sbx create --clone` actually cloned into the sandbox.

  This is a real code split, not a rename: today's single `primary` variable becomes two. The Deep Design phase works out the exact signatures (e.g. `cloneSyncIn(ctx, name, containerRoot, hostRoot, l, run)` or an equivalent split on `CloneSync` itself) and each call site (`syncExcludeFile`, the `Dirs` loop, the new `comet-state.yaml` glob).

**The exclude-file sync can no longer assume a non-worktree host source.** `excludeFile`'s current hardcoded `.git/info/exclude` path relies on `Workspaces[0].Path` never being a worktree (true today, enforced by `sbx create --clone`'s own rejection). Once the host-side source becomes `FetchInto` (`ws.Dir`, a worktree in the pipeline), `.git` there is a file, not a directory, and `info/exclude` lives in the common git directory instead. Resolving this (e.g. via `git rev-parse --git-common-dir` against `FetchInto`, with the existing direct-path shortcut kept as a fast path when the host source genuinely isn't a worktree) is Deep Design / implementation work, flagged here as a known, load-bearing subtlety — not a detail to improvise past.

**`comet-state.yaml` sync follows the same host-target rule, located by glob.** `docs/comet/changes/*/comet-state.yaml` isn't a fixed `Dirs` entry because the change name isn't known statically. After `fetchBranch`, glob for it inside the container (or infer the change name from `.comet/current-change.json`, already synced) and copy each match to `FetchInto`, the same way `.comet/runtime` already round-trips, using `cmd/eval-roles/fixture.go`'s existing `rewriteLocalExecutionPaths` glob as the pattern to follow, not reuse (it solves a different problem — rewriting a fixture's frozen paths, not copying files out of a sandbox).

**Clone mode is always requested on the `sbx` backend; the `local` backend stays untouched.** `SandboxAgent.Run` builds `Options.Clone` unconditionally (mirroring how `NetworkNotice`/`NetworkAudit` are already called unconditionally in the same function) and relies on the existing `runagent.CloneNotice(backend, clone)` to log when running on `local`, where `Launch.Clone` is already documented as ignored. No new backend-conditional branching needed — the existing notice pattern already covers it.

## Risks / Trade-offs

- **[Risk]** The container-root/host-root split in `cloneSyncIn`/`cloneSyncOut` is easy to get half-right (e.g. leaving one `sbx cp` call still anchored to the wrong root), which would silently misroute `.agent/result.json` or drop `comet-state.yaml` — exactly the class of bug this codebase's own history (Task 21/22 review rounds) has repeatedly found in this exact file. → Mitigation: table-driven tests per sync direction (mirroring existing `clone_test.go` conventions) asserting the *sandbox-side* path argument stays anchored to `Workspaces[0].Path` while the *host-side* argument moves to `FetchInto`, plus a live end-to-end pipeline run through Comet Native Shape→Build→Verify before calling this done.
- **[Risk]** The exclude-file worktree resolution is new, untested territory for this file. → Mitigation: covered by its own unit test against a real `git worktree`, not just a plain repo (existing `clone_test.go` fixtures use plain repos throughout).
- **[Trade-off]** A disposable clone adds a `git clone` (network-free, local, off the bare repo — cheap but not free) to every `sbx`-backend pipeline run. Accepted: still far cheaper than a truncated run burning its full turn/token budget on lock conflicts.

## Migration Plan

No data migration. Rollout is a single code change; `Launch.Clone` stays `nil` (today's bind-mount behavior) until `SandboxAgent.Run` is updated, so intermediate states aren't a concern. Rollback is reverting the commit — no persisted format changes.
