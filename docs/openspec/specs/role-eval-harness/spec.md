# role-eval-harness Specification

## Purpose
Gives office roles (analyst/implementer/reviewer) a repeatable, manually-triggered quality signal by running each role against declarative golden-case fixtures and reporting deterministic pass/fail results, instead of relying only on manual smoke-testing.

## Requirements

### Requirement: Harness evaluates a role against a golden case
The system SHALL run a role through the existing agent-run path against a golden case's fixture and produce a pass/fail verdict for that case, based on the case's declared checks.

#### Scenario: All checks pass
- **WHEN** every check declared by a golden case evaluates successfully against the role's run
- **THEN** the harness reports that case as passed

#### Scenario: Any check fails
- **WHEN** at least one check declared by a golden case fails against the role's run
- **THEN** the harness reports that case as failed and identifies which check failed

### Requirement: Golden cases are data, not code
Golden cases SHALL be defined as self-contained repository directories (an initial fixture file tree, a task prompt, and a declarative expectation) that the harness discovers and evaluates without requiring a rebuild.

#### Scenario: Adding a new case
- **WHEN** a new case directory is added to the case library with a valid fixture, task prompt, and expectation
- **THEN** the harness includes that case in its next run without any code change

### Requirement: Check kinds are typed and extensible
Each check declared by a golden case SHALL specify a kind, and the harness SHALL evaluate it through a dispatcher keyed by that kind, so new kinds (including a future LLM-judged kind) can be added without changing existing cases.

#### Scenario: Unimplemented check kind
- **WHEN** a case declares a check kind the harness has no handler for
- **THEN** the harness fails that case explicitly, naming the unimplemented kind, rather than silently skipping the check

### Requirement: Eval runs do not consume production role budget
Runs triggered by the eval harness SHALL be excluded from the `per_role_daily` budget accounting used to gate production runs.

#### Scenario: Running the eval sweep does not affect daily budget
- **WHEN** the eval harness runs golden cases for a role
- **THEN** that role's `per_role_daily` budget consumption, as used to gate production runs, is unaffected

### Requirement: Harness invocation is manual
The system SHALL NOT trigger eval runs automatically; invocation SHALL always be an explicit manual action, with no CI or git-hook trigger.

#### Scenario: Editing a role does not trigger an eval run
- **WHEN** a role's prompt or permissions file is edited and committed
- **THEN** no eval run is triggered automatically by that commit
