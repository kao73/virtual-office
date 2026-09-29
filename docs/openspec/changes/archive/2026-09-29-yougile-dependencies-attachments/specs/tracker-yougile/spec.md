## ADDED Requirements

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
