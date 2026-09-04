## MODIFIED Requirements

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

## ADDED Requirements

### Requirement: Split proposal carries a validated dependency graph
A `split` outcome's `split.children[]` SHALL be a non-empty list of entries with unique `id`
values, and each entry's `depends_on` SHALL reference only `id` values present in the same list,
forming no cycle. `depends_on` is informational for the human in this wave; nothing programmatic
relies on it yet.

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

### Requirement: A human's reply to a split proposal is not re-investigated as Shape work
When `analyst` resumes a task whose context shows a human reply to a previously posted `split`
question, `analyst` SHALL NOT invoke `comet native new` or perform a Shape investigation of the
original постановка before branching on that reply: a confirming reply re-affirms `split` without
new investigation, a declining reply proceeds with ordinary Shape on the whole постановка, and any
other reply is treated as new information requiring reassessment before either path is taken.

#### Scenario: Confirmed split ends the resumed run without invoking Comet Native
- **WHEN** `analyst` resumes a task and its context shows the human confirmed a previously
  proposed split
- **THEN** the run ends with `outcome: split` again, `comet native new` is never invoked, and the
  summary defers further progress to the human creating the proposed child tickets

#### Scenario: Declined split proceeds with ordinary Shape
- **WHEN** `analyst` resumes a task and its context shows the human declined the proposed split,
  choosing to carry the постановка as one task
- **THEN** `analyst` invokes `comet native new` on the original постановка and continues the
  ordinary Shape investigation
