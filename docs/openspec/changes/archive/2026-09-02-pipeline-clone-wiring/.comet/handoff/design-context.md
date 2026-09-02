# Comet Design Handoff

- Change: pipeline-clone-wiring
- Phase: design
- Mode: compact
- Context hash: 7a95bff660387383d2ec1721564bf0b6402e0482bfed73ae14be31bfdbf3d0c9

Generated-by: comet-handoff.sh

OpenSpec remains the canonical capability spec. This handoff is a deterministic, source-traceable context pack, not an agent-authored summary.

## docs/openspec/changes/pipeline-clone-wiring/proposal.md

- Source: docs/openspec/changes/pipeline-clone-wiring/proposal.md
- Lines: 1-28
- SHA256: 7dd8e814462e0146983893c119f0104c0cd669d783fbfe51ee4fd9fc53415360

```md
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

```

## docs/openspec/changes/pipeline-clone-wiring/design.md

- Source: docs/openspec/changes/pipeline-clone-wiring/design.md
- Lines: 1-47
- SHA256: 992f78cafd928172ed85da45c7e85e77dd6686610f6c8d432a45b1aa32df8042

```md
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

```

## docs/openspec/changes/pipeline-clone-wiring/tasks.md

- Source: docs/openspec/changes/pipeline-clone-wiring/tasks.md
- Lines: 1-29
- SHA256: 3be2841be28de495ea78a9a8ba4051fd2075bf6436ef6fddf9bd43b35bb9860b

```md
## 1. Disposable clone-source step

- [ ] 1.1 Add a function in `internal/workspace` that creates a fresh, ordinary (non-bare, non-worktree) `git clone` of a worktree's current branch tip into a new temp directory, and a matching cleanup function.
- [ ] 1.2 Unit-test it against a real worktree fixture (mirroring existing `internal/workspace` test style): clone succeeds, checked-out branch matches, cleanup removes the temp directory.

## 2. Wire `--clone` into `SandboxAgent.Run`

- [ ] 2.1 Add a `Branch string` field to `pipeline.Request`, populated from `ws.Branch` in `pipeline.go`'s `work()` (reuses info the pipeline already has). In `internal/pipeline/agent.go`, after `runagent.Execute`'s prerequisites are ready but using the same request flow, create the disposable clone source from `req.Workdir` (post-`PrepareInput` state, so it captures any system commit `PrepareInput` made) when `a.Backend` is `sbx`, and build `runagent.Options.Clone` with `FetchInto: req.Workdir`, `Branch: req.Branch`, and the existing `Dirs` list.
- [ ] 2.2 Ensure the clone source is removed on every return path (success, non-zero exit, timeout, exec error), matching `cloneSyncOut`'s "always called" discipline. If clone-source creation itself fails, fail the run outright (no silent bind-mount fallback) — the task stays leased and the reaper reclaims it on its next sweep.
- [ ] 2.3 Confirm `runagent.CloneNotice` still fires correctly when `a.Backend` is `local` (Clone is still constructed but the `local` backend ignores it per `Launch.Clone`'s existing contract).

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

```

## docs/openspec/changes/pipeline-clone-wiring/specs/pipeline-clone-isolation/spec.md

- Source: docs/openspec/changes/pipeline-clone-wiring/specs/pipeline-clone-isolation/spec.md
- Lines: 1-40
- SHA256: 2cd1bee7f183475e04e11e4fda73d9609a3ce388be99035c7cbc56cdf2c77629

```md
## Purpose

Defines how the tracker-driven pipeline isolates a role run's filesystem operations from the host's bind-mounted worktree, and what must survive that isolation intact: Comet Native's phase state, the exchange directory, and the agent's own commits.

## ADDED Requirements

### Requirement: Pipeline role runs use sandbox-local isolation on the `sbx` backend
When the pipeline executes a role run on the `sbx` backend, the run SHALL execute on the sandbox's own filesystem rather than a bind-mounted copy of the host worktree.

#### Scenario: Office conveyor run avoids the bind-mount
- **WHEN** the tracker-driven pipeline runs a role (analyst, implementer, or reviewer) on the `sbx` backend
- **THEN** the agent's file operations happen on storage local to the sandbox, not on a bind-mounted host directory

### Requirement: Comet Native phase state survives role-to-role handoffs
The pipeline SHALL ensure that any `comet-state.yaml` under `docs/comet/changes/*/` mutated during a role's run is present in the persistent task worktree after the run completes, regardless of whether the role committed it itself.

#### Scenario: Implementer's Builder handoff is visible to the next role
- **WHEN** an implementer run advances a Comet Native change's `comet-state.yaml` to `phase: verify` without committing that file itself
- **THEN** the persistent task worktree reflects `phase: verify` after the run, and the next role's run starts from that state

### Requirement: The exchange directory and agent commits round-trip correctly
The pipeline SHALL deliver the role's committed work (via git) and its exchange-directory artifacts (`.agent/result.json`, `.comet/current-change.json`, `.comet/runtime`) back to the persistent task worktree after every run, on every outcome (success, non-zero exit, timeout, or step-limit truncation).

#### Scenario: A truncated run still delivers its result
- **WHEN** a role's run is truncated by its timeout or step limit after making commits and writing `.agent/result.json`
- **THEN** those commits and that result file are both present in the persistent task worktree once the run ends

### Requirement: Sandbox-local isolation leaves no disposable state behind
Any disposable, sandbox-only working copy the pipeline creates to satisfy sandbox-local isolation SHALL be removed after the run ends, on every outcome.

#### Scenario: Cleanup happens even when the run times out
- **WHEN** a role's run in a sandbox-local isolation mode is truncated by timeout
- **THEN** no disposable working copy created for that run remains on the host filesystem afterward

### Requirement: Backends without sandbox isolation report non-application instead of failing silently
When the pipeline requests sandbox-local isolation on a backend that has no sandbox (the `local` backend), the run SHALL proceed on the host filesystem directly and the pipeline SHALL log that the isolation was not applied, rather than silently ignoring the request.

#### Scenario: A debug run on the local backend is not silently treated as isolated
- **WHEN** the pipeline runs a role on the `local` backend
- **THEN** the run proceeds directly on the host worktree, and the run's log states that sandbox-local isolation was not applied

```
