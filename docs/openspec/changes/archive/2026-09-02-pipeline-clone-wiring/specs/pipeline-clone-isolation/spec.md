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
