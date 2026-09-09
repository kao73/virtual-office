## Purpose

Lets a project opt into the office merging an approved task's pull request
by itself, on a mechanical gate, instead of always waiting for a human to
click merge — while keeping a human-merged project's own behavior intact
except where staleness detection is deliberately shared across both.

## ADDED Requirements

### Requirement: Auto-merge is off by default and requires a forge
A project's pull requests SHALL continue to wait for a human to merge them
unless that project explicitly sets `auto_merge.enabled: true`. Enabling
auto-merge on a project with no forge configured SHALL be rejected as a
configuration error, not silently ignored.

#### Scenario: Auto-merge omitted
- **WHEN** a project's configuration does not set `auto_merge`
- **THEN** its tasks' pull requests behave exactly as before this
  capability existed — the office never merges them itself

#### Scenario: Auto-merge enabled without a forge
- **WHEN** a project sets `auto_merge.enabled: true` but has no `forge`
  configured
- **THEN** the office SHALL fail to load its configuration, naming the
  project and the missing `forge`, rather than starting with auto-merge
  silently inert

### Requirement: Office merges an approved task's pull request without a human click
On a project with `auto_merge.enabled: true`, once a task's pull request
has passed review (`Approved`) and its base is still current (no text
conflict, and the target branch has not advanced past what the task branch
already contains), the office SHALL merge the pull request itself, without
any further waiting period and without any human action.

#### Scenario: Clean auto-merge task reaches its terminal status unattended
- **WHEN** a task on an auto-merge-enabled project reaches `Approved` with
  a base that is still current
- **THEN** the task's pull request is merged and the task reaches the
  graph's configured terminal status for a merged task, with no human
  comment or action recorded in between

### Requirement: Merge target branch is configurable per project
A project MAY set `auto_merge.target_branch` to a branch other than its
repository's real default branch. When it is set, the office SHALL fork
new task branches from it, and SHALL target and merge pull requests into
it, instead of the repository's default branch.

#### Scenario: Task branches fork from the configured target branch
- **WHEN** a project sets `auto_merge.target_branch` to a branch other than
  its default branch
- **THEN** a new task on that project forks its branch from the configured
  target branch, and the office's pull request for that task targets the
  same branch — not the repository's default branch

#### Scenario: Configured target branch does not exist yet
- **WHEN** `auto_merge.target_branch` names a branch that does not exist on
  the project's remote
- **THEN** the office SHALL NOT create that branch on the task's behalf,
  and the task SHALL NOT proceed as if the branch existed

### Requirement: A stale base returns the task to the implementer, conflict or not
When a task's base branch has advanced since its own branch was created —
whether or not that produces a text conflict — the task SHALL return to the
implementer to re-merge and re-test against the current base, without
spending an attempt. This applies to every project, not only auto-merge
ones: a human-merge project's task also returns to the implementer when its
base has moved, even without a text conflict.

#### Scenario: Base advanced with a text conflict
- **WHEN** a task's branch text-conflicts with its base
- **THEN** the task returns to the implementer, its attempt count is
  unchanged, and the record accurately says the branch does not merge

#### Scenario: Base advanced without a text conflict
- **WHEN** a task's base has gained commits the task's branch does not yet
  contain, but merging them produces no text conflict
- **THEN** the task still returns to the implementer, its attempt count is
  unchanged, and the record does not claim there was a conflict

#### Scenario: Base has not moved
- **WHEN** a task's base branch is still exactly what the task's branch
  already contains
- **THEN** the task does not return to the implementer on this basis

### Requirement: Repeated merge refusal escalates to a human instead of looping
If the forge refuses to merge a pull request that the office's own checks
found clean, and that refusal repeats a configured number of times in a
row, the task SHALL escalate to a human instead of the office retrying
indefinitely without ever surfacing the problem.

#### Scenario: Refusal count reaches the configured limit
- **WHEN** the forge refuses to merge the same clean pull request as many
  times in a row as the project's configured limit allows
- **THEN** the task is handed to a human, naming the pull request and that
  the office's own checks found it clean

#### Scenario: A single refusal, then success
- **WHEN** the forge refuses a merge once and then succeeds on a later
  attempt
- **THEN** the task is not escalated to a human on account of that single
  refusal
