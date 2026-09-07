# role-native-workflow Specification

## Purpose
Gives `analyst`, `implementer`, and `reviewer` one shared, git-resumable Comet Native change
(Shape/Build/Verify/Archive) to drive a task through, in place of each role's previously unrelated
planning/build/review mechanism, with Archive left to deterministic runner code rather than a role.

## Requirements

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
keep`) as part of the runner's own deterministic pre-PR processing, before the task's pull request
is opened, without dispatching any role or agent to perform it.

#### Scenario: Archive runs before the pull request opens, with no agent invocation
- **WHEN** the runner is about to open a task's pull request
- **THEN** the runner itself calls `comet native archive` for that task's change on the task's own
  branch before calling `OpenPR`, and no role's `.agent/task.md` is ever generated for the purpose
  of archiving

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

### Requirement: Task-size decision precedes Comet Native entry
`analyst` SHALL decide whether a task's постановка is too large for a single Comet Native
change by reading the постановка alone, before invoking `comet native new`, rather than after a
full Shape investigation.

#### Scenario: Oversized постановка is split before Comet Native starts
- **WHEN** `analyst` reads a постановка describing several independent entities or capabilities
- **THEN** the run ends with `outcome: split`, carrying a `split.children[]` proposal, and
  `comet native new` is never invoked

#### Scenario: Appropriately-scoped постановка proceeds normally
- **WHEN** `analyst` reads a постановка describing one entity with several small, safe
  implementation decisions
- **THEN** `analyst` proceeds to invoke `comet native new` and continues the ordinary Shape
  investigation, unaffected by the earlier size check

### Requirement: Comet Native Supervisor Change is never used
`analyst` SHALL NOT create or maintain a Comet Native Supervisor Change (`children.yaml`); any
task-splitting decision SHALL go through the office's own `split` outcome instead.

#### Scenario: A Supervisor-Change-shaped постановка does not produce children.yaml
- **WHEN** a постановка resembles Comet Native Supervisor Change's target case (a large
  requirement decomposable into dependency-aware children)
- **THEN** the run does not create a `children.yaml` file anywhere in the change directory, and
  instead ends with the office's own `split` proposal

### Requirement: Split proposal carries a validated dependency graph
A `split` outcome's `split.children[]` SHALL be a non-empty list of entries with unique,
whitespace-free `id` values, single-line `title` and `description`, and each entry's `depends_on`
SHALL reference only `id` values present in the same list, forming no cycle. `depends_on` is
informational for the human in this wave; nothing programmatic relies on it yet.

#### Scenario: Empty children list is rejected
- **WHEN** a `split` outcome's `split.children` is an empty list
- **THEN** result validation rejects the run's output as malformed, the same way it rejects any
  other contract violation

#### Scenario: Duplicate child id is rejected
- **WHEN** two entries in `split.children[]` share the same `id`
- **THEN** result validation rejects the run's output as malformed

#### Scenario: Dangling or cyclic dependency is rejected
- **WHEN** an entry's `depends_on` names an `id` absent from `split.children[]`, or the
  `depends_on` edges form a cycle
- **THEN** result validation rejects the run's output as malformed

#### Scenario: Whitespace in id, or a line break in title or description, is rejected
- **WHEN** a `split.children[]` entry's `id` contains whitespace, or its `title` or `description`
  contains a line break
- **THEN** result validation rejects the run's output as malformed, because each entry renders as
  one bullet in the ticket comment

### Requirement: A human's reply to a split proposal is not re-investigated as Shape work
When `analyst` resumes a task whose context shows a human reply to a previously posted `split`
question, `analyst` SHALL NOT invoke `comet native new` or perform a Shape investigation of the
original постановка before branching on that reply: a confirming reply re-affirms `split` without
new investigation, a declining reply proceeds with ordinary Shape on the whole постановка, and any
other reply is treated as new information: `analyst` SHALL respond with `needs_human` and
a clarifying question rather than re-affirming `split` or choosing either path outright — a second
`split` marker is what the runner treats as an unconditional confirmation, and an ambiguous reply
does not warrant one. The
re-affirmed `split` carries the same confirmation question as the original proposal: creation of
the confirmed children is deterministic runner behavior outside any agent run, not something a
human confirms in a further reply, so `analyst` SHALL NOT invent a follow-up question asking
whether the children have been created.

#### Scenario: Confirmed split ends the resumed run without invoking Comet Native
- **WHEN** `analyst` resumes a task and its context shows the human confirmed a previously
  proposed split
- **THEN** the run ends with `outcome: split` again, `comet native new` is never invoked, and the
  summary states that the runner will create and link the proposed child tickets automatically —
  the run does not ask a new question about ticket creation, and does not defer that step to the
  human

#### Scenario: Declined split proceeds with ordinary Shape
- **WHEN** `analyst` resumes a task and its context shows the human declined the proposed split,
  choosing to carry the постановка as one task
- **THEN** `analyst` invokes `comet native new` on the original постановка and continues the
  ordinary Shape investigation

#### Scenario: Confirmed child creation ends the split resume loop
- **WHEN** `analyst` resumes a task and its context shows the human confirmed the proposed
  children have been created
- **THEN** this scenario is retired by `split-autocreate-tickets`: its WHEN condition can no
  longer arise, because `analyst` no longer asks whether children have been created — ticket
  creation and linking is now deterministic runner behavior (`CompleteSplits`) triggered by the
  same second `outcome: split` confirmation the "Confirmed split ends the resumed run" scenario
  above describes, not by a further human reply. Kept under its original name, not deleted, to
  preserve the historical record that wave 1's manual-creation flow existed and was deliberately
  retired — see `design.md` decision #6 and `docs/notes/analyst-task-splitting.md`

### Requirement: `depends_on` reflects a real merge-order dependency, not a preferred ordering
When `analyst` proposes `split.children[]`, it SHALL set `depends_on` from
one child to another only when the dependent child's work cannot begin
before code from the other child is merged (a shared schema, migration,
data model, or exported interface the dependent child's implementation
relies on) — not merely because one ordering reads more naturally than
another.

#### Scenario: A genuine interface dependency is recorded
- **WHEN** one proposed child's implementation needs a data model,
  migration, or interface that another proposed child creates
- **THEN** `analyst` sets `depends_on` from the dependent child to the one
  it needs

#### Scenario: A convenience ordering is not recorded as a dependency
- **WHEN** two proposed children do not need each other's merged code to
  begin, even though one would conventionally be built before the other
- **THEN** `analyst` leaves `depends_on` empty between them
