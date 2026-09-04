## Purpose

Задаёт поведенческий контракт: когда человек второй раз подряд подтверждает
предложенное аналитиком разбиение задачи, раннер сам создаёт и связывает
тикеты-дети в трекере — без ручного шага между одобрением и появлением
тикетов, и без прогона агента на сам шаг создания.

## ADDED Requirements

### Requirement: A split proposal's structured data is preserved as a tracker attachment
When `analyst` reports `outcome: split`, in addition to the human-readable
comment, the runner SHALL attach the machine-readable `split.children[]` data
to the task, tagged so a later run can retrieve exactly that attachment
without ambiguity.

#### Scenario: Split proposal carries a retrievable attachment
- **WHEN** the runner processes an `outcome: split` report from `analyst`
- **THEN** the runner adds an attachment containing the reported
  `split.children[]` data to the task, and the comment marking this outcome
  names that attachment so it can be retrieved unambiguously later

### Requirement: Confirmed split triggers automatic ticket creation without an agent run
When the runner observes a second consecutive `outcome: split` from `analyst`
on the same task — a confirmation of a previously proposed split, not a first
proposal — the runner SHALL perform batch creation of the proposed child
tickets, linking, and parent disposition as deterministic code, without
dispatching any role or agent for this step.

#### Scenario: Second confirmation triggers automatic creation
- **WHEN** the runner processes a task's second consecutive `outcome: split`
  report from `analyst`
- **THEN** the runner creates each proposed child from the last `split`
  attachment, links them by `depends_on`, posts a summary comment naming the
  created children, and transitions the parent task to `Done`, without
  invoking any role's agent for this step

#### Scenario: First proposal does not trigger creation
- **WHEN** the runner processes a task's first `outcome: split` report from
  `analyst`
- **THEN** the runner posts the split proposal and waits for a human reply,
  exactly as in wave 1, and does not create any ticket

### Requirement: Batch ticket creation is idempotent under retry
Batch creation of split children SHALL be safe to retry: before creating a
child, the runner SHALL query the tracker for an existing ticket carrying
that child's identifying marker, and SHALL create only children not already
found.

#### Scenario: Interrupted batch resumes without duplicating created children
- **WHEN** a previous attempt at batch creation created some but not all of a
  split's children before failing
- **THEN** the next attempt creates only the children not yet found by their
  marker, and does not create a duplicate for any child already created

### Requirement: Failed creation attempts are visible, not silent
When a batch-creation attempt fails, the runner SHALL post a system notice to
the task naming the failure, rather than retrying silently with no trace in
the task's correspondence.

#### Scenario: A failed attempt leaves a visible notice
- **WHEN** a batch-creation attempt fails partway
- **THEN** the runner posts a system-marked notice comment on the task
  before the next attempt, and the task is not left with only silent,
  invisible retries
