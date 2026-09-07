# ticket-attachment-visibility Specification

## Purpose
Defines the contract for a role seeing a human-uploaded attachment on the
ticket it is working, as a real file in its working directory, rather than
that attachment being invisible to it because the tracker model and agent
exchange never carried it.

## Requirements

### Requirement: A role's working directory carries the ticket's human attachments as real files
Before running an agent for a task, the runner SHALL download every
human-uploaded attachment of that task (excluding runner-internal
bookkeeping attachments) and write each as a file inside the agent's
exchange directory, so the agent can read it with its own file tools —
not as text embedded in the task statement.

#### Scenario: Ticket with a human attachment
- **WHEN** the runner prepares a run for a task that has a human-uploaded
  attachment
- **THEN** the agent's working directory contains that attachment's data as
  a file, and the task statement names it and where to find it

#### Scenario: Ticket without attachments
- **WHEN** the runner prepares a run for a task with no attachments
- **THEN** no attachment files are written, and the task statement does not
  mention any

### Requirement: Runner-internal attachments are never shown to the agent
An attachment the runner itself created for its own bookkeeping (for
example, the structured split proposal) SHALL NOT be materialized into the
agent's working directory or mentioned in the task statement.

#### Scenario: Task carries both a human attachment and a split proposal
- **WHEN** the runner prepares a run for a task that has one human-uploaded
  attachment and one runner-created split-proposal attachment
- **THEN** only the human-uploaded attachment appears as a file and in the
  task statement's mention

### Requirement: Attachment file names are safe and non-colliding
An attachment's name comes from a human, not the runner, and SHALL NOT be
trusted as a safe file path component: the runner SHALL confine the written
file to the attachment directory regardless of the name's content, and two
attachments delivered to the same run under the same name SHALL both be
preserved as distinct files.

#### Scenario: Attachment name attempts to escape the exchange directory
- **WHEN** an attachment's name, taken literally, would resolve outside the
  attachment directory (for example, a name of `..`)
- **THEN** the runner writes the attachment inside the attachment directory
  under a safe name instead of following the literal name

#### Scenario: Two attachments share the same name
- **WHEN** a run's attachments include two entries with an identical name
- **THEN** both are written as separate files, and neither overwrites the
  other
