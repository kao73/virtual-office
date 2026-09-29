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

### Requirement: A task's declared dependency blocks claiming until resolved
The adapter SHALL record a `depends_on` reference from a task to another
task, and the runner's claim gate SHALL refuse to claim a task while any
task it depends on has not reached a terminal status.

#### Scenario: A dependent task is not claimable while its dependency is open
- **WHEN** task A declares it depends on task B, and B has not reached a
  terminal status
- **THEN** attempting to claim A fails as blocked by an unmet dependency

#### Scenario: A dependent task becomes claimable once its dependency resolves
- **WHEN** task A depends on task B, and B reaches a terminal status
- **THEN** A can be claimed on the next attempt, without any further
  action needed on A itself

#### Scenario: An archived dependency still releases its dependents
- **WHEN** task A depends on task B, B reaches a terminal status, and B is
  then archived
- **THEN** A can be claimed

#### Scenario: A task cannot depend on itself
- **WHEN** a caller declares that a task depends on that same task
- **THEN** the declaration fails and nothing is recorded

#### Scenario: A declared dependency is visible to a human reading the task
- **WHEN** a dependency from task A on task B is recorded
- **THEN** a person opening task A in the tracker's interface can see that
  A depends on B, without access to the office's internal data

### Requirement: Attachments can be added to a task and retrieved by id
The adapter SHALL accept raw attachment data for a task and return an id
that a later call can use to retrieve the same data back, byte for byte.

#### Scenario: An added attachment round-trips
- **WHEN** raw data is added as an attachment to a claimed task
- **THEN** retrieving that attachment by the returned id returns the same
  bytes that were added

#### Scenario: An unknown attachment id is rejected
- **WHEN** an attachment is requested by an id that was never added to
  that task
- **THEN** the request fails rather than returning unrelated data

#### Scenario: A file a person attached is among the task's attachments
- **WHEN** a person attaches a file to a task through the tracker's
  interface, in the task's chat or in its description
- **THEN** reading the task lists that file among its attachments, and
  retrieving it by its id returns the file's bytes

#### Scenario: Retrieving an attachment does not expose the tracker credentials
- **WHEN** an attachment is retrieved
- **THEN** the tracker's API credentials are not sent to the host that
  serves the file

### Requirement: Office data is kept apart from other data on the task
The adapter SHALL keep all of the office's own data about a task in a
single namespace of the task's free-form data, SHALL leave every other key
of that data unchanged on every write, and SHALL NOT let one task's
malformed office data stop listing the other tasks.

#### Scenario: Another integration's data survives office writes
- **WHEN** a task's free-form data carries keys written by another
  integration, and the office claims, renews, releases, or otherwise
  updates that task
- **THEN** those keys are unchanged afterwards

#### Scenario: One malformed task does not stop the queue
- **WHEN** one task in a status carries malformed office data
- **THEN** listing that status still returns the other tasks, and the
  skipped task is reported rather than dropped silently

#### Scenario: Newer office data is not overwritten
- **WHEN** a task's office data declares a newer schema version than the
  adapter understands
- **THEN** reading that task on its own or changing it fails, nothing is
  written, and a listing skips the task and reports it rather than failing
