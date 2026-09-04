## ADDED Requirements

### Requirement: Task-size decision precedes Comet Native entry
`analyst` SHALL decide whether a task's постановка is too large for a single Comet Native
change by reading the постановка alone, before invoking `comet native new`, rather than after a
full Shape investigation.

#### Scenario: Oversized постановка is split before Comet Native starts
- **WHEN** `analyst` reads a постановка describing several independent entities or capabilities
- **THEN** the run ends with `outcome: needs_human` proposing a split, and `comet native new` is
  never invoked

#### Scenario: Appropriately-scoped постановка proceeds normally
- **WHEN** `analyst` reads a постановка describing one entity with several small, safe
  implementation decisions
- **THEN** `analyst` proceeds to invoke `comet native new` and continues the ordinary Shape
  investigation, unaffected by the earlier size check

### Requirement: Comet Native Supervisor Change is never used
`analyst` SHALL NOT create or maintain a Comet Native Supervisor Change (`children.yaml`); any
task-splitting decision SHALL go through the office's own `needs_human` outcome instead.

#### Scenario: A Supervisor-Change-shaped постановка does not produce children.yaml
- **WHEN** a постановка resembles Comet Native Supervisor Change's target case (a large
  requirement decomposable into dependency-aware children)
- **THEN** the run does not create a `children.yaml` file anywhere in the change directory, and
  instead ends with the office's own `needs_human` split proposal
