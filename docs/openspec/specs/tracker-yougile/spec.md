# tracker-yougile Specification

## Purpose
A second real `Tracker` implementation, backed by YouGile's REST API,
letting the runner serve YouGile-backed projects with the same task
lifecycle, lease, and comment-protocol guarantees it already gives JIRA-
and mock-backed projects.

## Requirements

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
fact the recorded owner before reporting success. A task that has been
archived SHALL NOT be claimed.

#### Scenario: A claimant whose lease was overwritten is told it lost
- **WHEN** a caller's lease write on an unowned task is overwritten by
  another claimant before the caller re-reads the task
- **THEN** the caller's claim fails with a lost-claim error, and the
  recorded owner is the other claimant

YouGile offers no compare-and-set, so write-then-reread cannot close the
mirror race in which both claimants read the task as free and each
re-reads its own write; both may then report success. This is the same
known limitation the `jira` adapter documents on its `Claim`.

#### Scenario: A claim whose working column did not take effect fails
- **WHEN** a claim with a working status writes the lease and the column,
  and the re-read shows the lease is the caller's but the task is still
  outside the working status's column
- **THEN** the claim fails, does not report success, and releases the
  caller's lease at once rather than leaving it to expire (if that release
  itself fails, the lease is left to the reaper)

#### Scenario: Claiming an archived task fails
- **WHEN** a caller attempts to claim a task that has been archived
- **THEN** the claim fails with a lost-claim error and nothing is written
  to the task

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

### Requirement: Comments are written verbatim and tasks can be found by marker
Comments SHALL be written and read back verbatim, preserving the office's
marker convention. A task created with a marker among its labels SHALL be
locatable later by that marker, the same way the `jira` and `mock`
adapters match `FindByMarker` against task labels.

#### Scenario: A comment round-trips verbatim
- **WHEN** a comment carrying a given run marker is posted to a task
- **THEN** reading the task back returns that comment text unchanged

#### Scenario: A labeled task is found by its marker
- **WHEN** a task is created with a marker among its labels
- **THEN** a subsequent `FindByMarker` call with that marker locates the task

### Requirement: Human-authored replies are distinguishable from office-authored comments
The adapter SHALL expose enough information about a comment's author for
the caller to determine, by account rather than by content, whether it
was written by the office or by a human.

#### Scenario: The office account is identifiable
- **WHEN** the adapter reports its own operating account via `Whoami`
- **THEN** that account can be compared against a comment's author to
  decide whether the comment came from the office

#### Scenario: System events are not reported as comments
- **WHEN** a task is moved between columns or its fields are changed, and
  the task is read back
- **THEN** its comments contain only messages that people or the office
  wrote, not the system events the tracker records for those changes
