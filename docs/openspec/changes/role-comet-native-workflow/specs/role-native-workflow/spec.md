## Purpose
Gives `analyst`, `implementer`, and `reviewer` one shared, git-resumable Comet Native change
(Shape/Build/Verify/Archive) to drive a task through, in place of each role's previously unrelated
planning/build/review mechanism, with Archive left to deterministic runner code rather than a role.

## ADDED Requirements

### Requirement: Phase-to-role boundary
Each role SHALL drive exactly the Comet Native phase(s) assigned to it and SHALL end its run at
that phase's boundary without performing the work of another role's phase.

#### Scenario: analyst stops at Shape confirmation
- **WHEN** `analyst` confirms Shape for a task's Comet Native change
- **THEN** the run ends with `outcome: done` and `next_owner: implementer` without writing any
  implementation code

#### Scenario: implementer does not redo Shape
- **WHEN** `implementer` picks up a task whose Comet Native change is already in the `build` phase
- **THEN** `implementer` reads the existing brief and target spec as given and does not reopen or
  re-litigate Shape decisions already recorded there

### Requirement: Archive is deterministic runner code, not a role
The system SHALL execute Comet Native's Archive step (`comet native archive --confirmed --finish
keep`) as part of the runner's own deterministic post-merge processing, without dispatching any
role or agent to perform it.

#### Scenario: Archive runs after human PR merge with no agent invocation
- **WHEN** the runner detects that a task's pull request has been merged
- **THEN** the runner itself calls `comet native archive` for that task's change, and no role's
  `.agent/task.md` is ever generated for the purpose of archiving

### Requirement: Cross-run resumability through git-committed state
A role resuming a task already in progress SHALL continue the same Comet Native change from its
committed `comet-state.yaml` rather than re-deriving progress from the working tree or starting
over.

#### Scenario: Truncated Build run resumes from the same change
- **WHEN** `implementer`'s previous run on a task was truncated mid-Build without submitting a
  Builder handoff
- **THEN** the next `implementer` run reads the same `docs/comet/changes/<name>/comet-state.yaml`,
  continues the same change, and does not create a second Native change for the task

### Requirement: Phase-scoped writes are hook-enforced, not convention-only
When a role's session attempts to edit an existing implementation file while its Comet Native
change is not in the `build` phase, Comet's guard hook (wired as a `PreToolUse` hook by the
adapter) SHALL block the edit, independent of what the role's own `role.md` instructions say.

#### Scenario: An edit attempt outside Build is rejected
- **WHEN** a role's session issues an `Edit` tool call against a tracked implementation file while
  the session's bound Comet Native change is in `shape`, `verify`, or `archive` phase
- **THEN** the `PreToolUse` hook denies the tool call with a non-zero exit and a message naming the
  current phase, before the file is modified
