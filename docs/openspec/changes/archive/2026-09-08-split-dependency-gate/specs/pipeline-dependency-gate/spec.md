## Purpose

Задаёт поведенческий контракт: раннер не даёт ни одной роли графа начать
работу над задачей, чья `depends_on`-зависимость ещё не в терминальном
статусе, — одинаково на файловом и JIRA-трекере, с видимостью в `runner ls`.

## ADDED Requirements

### Requirement: A candidate with an unresolved dependency is not claimed
When `claim()` considers a ready candidate whose `depends_on` names another
task, and that named task is not in a terminal status, the runner SHALL skip
the candidate for this tick rather than claiming it.

#### Scenario: Unresolved dependency blocks claim
- **WHEN** `claim()` considers a candidate task whose `depends_on` names a
  task that is not yet in a terminal status
- **THEN** the runner does not claim the candidate this tick, and logs why

#### Scenario: Dependency reaching a terminal status unblocks the candidate
- **WHEN** a previously unresolved dependency of a candidate task reaches a
  terminal status
- **THEN** a later tick claims the candidate normally, with no manual step

#### Scenario: A missing dependency task blocks the candidate
- **WHEN** a candidate's `depends_on` names a task the tracker cannot find
- **THEN** the runner treats the dependency as unresolved and does not claim
  the candidate, rather than treating the missing task as satisfied

### Requirement: The gate applies uniformly across workflow roles
The dependency check in `claim()` SHALL apply the same way regardless of
which role is claiming — no role is exempted by name.

#### Scenario: An analyst candidate is gated the same as an implementer candidate
- **WHEN** a task ready for `analyst` and a task ready for `implementer` both
  have an unresolved `depends_on`
- **THEN** `claim()` skips both, using the same check

### Requirement: Dependency status is read consistently across tracker backends
A task's `depends_on` links, once recorded by the tracker, SHALL be
readable back through `Get`/`List`/`ListReady` on every tracker
implementation the office supports — not only the file-based one.

#### Scenario: A JIRA-backed dependency is read back like a file-backed one
- **WHEN** a task's dependency link was recorded through the JIRA tracker
  implementation
- **THEN** a subsequent read of that task through the same implementation
  reports the same dependency the file-based tracker would report for an
  equivalent link

### Requirement: A blocked candidate's wait is visible without extra tooling
`runner ls` SHALL show, for a listed task with an unresolved dependency,
which task it is waiting on and that task's current status.

#### Scenario: ls names what a blocked task is waiting on
- **WHEN** a listed task has an unresolved `depends_on` dependency
- **THEN** `runner ls` shows the dependency's key and current status next to
  the task, without a separate command
