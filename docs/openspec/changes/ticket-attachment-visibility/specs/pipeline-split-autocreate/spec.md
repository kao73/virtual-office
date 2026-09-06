## ADDED Requirements

### Requirement: Confirmed split copies the parent's human attachments to each child
When the runner performs batch creation of a confirmed split's children, it
SHALL also copy every human-uploaded attachment of the parent task onto
each child task, so a child that cannot read the tracker itself does not
lose context (a diagram, a spec document) that was only recorded on the
parent.

#### Scenario: Parent has a human attachment at confirmation time
- **WHEN** the runner completes a confirmed split whose parent task carries
  a human-uploaded attachment
- **THEN** each created child task carries a copy of that attachment

#### Scenario: Attachment copying is retried, not skipped, on resume
- **WHEN** a previous batch-creation attempt created a child but was
  interrupted before copying the parent's attachments to it, and the runner
  processes the same confirmed split again
- **THEN** the runner copies the parent's attachments the child is still
  missing, rather than treating the already-existing child as fully done

#### Scenario: Attachment already present on the child is not duplicated
- **WHEN** the runner re-processes a confirmed split and a child already
  carries an attachment matching one of the parent's by name
- **THEN** the runner does not copy that attachment to the child again
