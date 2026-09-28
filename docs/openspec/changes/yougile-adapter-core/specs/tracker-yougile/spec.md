## Purpose

A second real `Tracker` implementation, backed by YouGile's REST API,
letting the runner serve YouGile-backed projects with the same task
lifecycle, lease, and comment-protocol guarantees it already gives JIRA-
and mock-backed projects.

## ADDED Requirements

### Requirement: Task status is represented by its board column
The adapter SHALL represent a task's status as the board column the task
currently sits in, mapped through a configured status-to-column
correspondence, so that transitioning a task's status moves it to the
corresponding column and listing tasks by status returns exactly the
tasks currently in that status's column.

#### Scenario: Transitioning a task moves it to the corresponding column
- **WHEN** a task's status is transitioned to a given status
- **THEN** the task's column becomes the column configured for that status

#### Scenario: Listing ready tasks matches the status's configured column
- **WHEN** a caller asks for ready tasks of a given project and status
- **THEN** every task returned is currently in the column configured for
  that status, and no task from a different column is included

### Requirement: Claim records lease ownership and verifies it after writing
Claiming a task SHALL record the claiming run as the task's owner together
with a lease expiry, then re-read the task to confirm the caller is in
fact the recorded owner before reporting success.

#### Scenario: A losing claimant is told it lost
- **WHEN** two callers attempt to claim the same unowned task at nearly
  the same time
- **THEN** exactly one claim succeeds, and the other is told it does not
  own the task, without silently overwriting the winner's lease

#### Scenario: Claiming an already-owned live lease fails
- **WHEN** a caller attempts to claim a task whose lease is owned by
  another run and has not expired
- **THEN** the claim fails and the task's existing owner is unchanged

### Requirement: A live lease can be renewed; an expired one cannot
Renewing a task's lease SHALL succeed only while the calling run still
holds a live lease on that task.

#### Scenario: Renewing a live lease extends it
- **WHEN** the owning run renews its own live lease with a later expiry
- **THEN** the task's lease expiry is updated to the later time

#### Scenario: Renewing an expired lease fails
- **WHEN** a run whose lease on a task has already expired attempts to
  renew it
- **THEN** the renewal fails and the task's lease is left as it was

### Requirement: Releasing a lease does not change the task's status
Releasing a task's lease SHALL remove the ownership and lease fields
without transitioning the task to a different status.

#### Scenario: A released task keeps its status
- **WHEN** a task's lease is released
- **THEN** the task is unowned and the caller can observe it retained its
  status from immediately before release

### Requirement: Task creation is idempotent
Creating a task with the same idempotency marker as an earlier, successful
creation SHALL return the earlier task rather than creating a duplicate.

#### Scenario: A repeated create returns the original task
- **WHEN** a task is created twice with the same idempotency marker
- **THEN** both calls report the same task identity, and only one task
  exists

### Requirement: Comments follow the office marker protocol and can be found by marker
Comments SHALL be written and read back verbatim so that a comment
carrying the office's marker convention can later be located by that
marker.

#### Scenario: A marked comment is found by its marker
- **WHEN** a comment carrying a given run marker is posted to a task
- **THEN** a subsequent search by that marker locates the task

### Requirement: Human-authored replies are distinguishable from office-authored comments
The adapter SHALL expose enough information about a comment's author for
the caller to determine, by account rather than by content, whether it
was written by the office or by a human.

#### Scenario: The office account is identifiable
- **WHEN** the adapter reports its own operating account via `Whoami`
- **THEN** that account can be compared against a comment's author to
  decide whether the comment came from the office
