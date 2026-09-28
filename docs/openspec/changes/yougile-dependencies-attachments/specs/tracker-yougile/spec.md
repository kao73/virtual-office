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
