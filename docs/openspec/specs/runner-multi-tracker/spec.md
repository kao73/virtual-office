# runner-multi-tracker Specification

## Purpose
The runner serves every tracker its projects name, so that which tracker to
work against is a property of each project on this machine, never a
decision repeated on every command line.

## Requirements

### Requirement: There is no tracker selection flag
`runner` subcommands SHALL NOT accept a `--tracker` flag. The set of
trackers a runner works against SHALL be exactly the set of distinct
`tracker` values declared by the projects in `projects.local.yaml`.

#### Scenario: The removed flag is rejected
- **WHEN** any `runner` subcommand is invoked with `--tracker jira`
- **THEN** the command fails as it would for any unknown flag, before
  touching the configuration

#### Scenario: Two trackers are served without a flag
- **WHEN** `projects.local.yaml` declares `OFFICE` with `tracker: mock` and
  `VO` with `tracker: jira`
- **THEN** `runner tick` with no tracker flag serves both: a `mock` task of
  `OFFICE` and a `jira` task of `VO` can each be claimed in the same tick

### Requirement: The tracker connection file is required only when used
`${OFFICE_HOME}/tracker.yaml` SHALL be opened only when at least one
project declares `tracker: jira`. When no project does, the file SHALL
neither be required nor listed among the configuration sources.

#### Scenario: A mock-only machine has no tracker file
- **WHEN** every project declares `tracker: mock` and `tracker.yaml` does
  not exist
- **THEN** the runner starts, and its configuration-source listing has no
  `tracker.yaml` line

#### Scenario: A jira project without the file is refused
- **WHEN** some project declares `tracker: jira` and `tracker.yaml` does
  not exist
- **THEN** the runner refuses to start and names the missing file

### Requirement: Each tracker is served as its own office
For each tracker in use the runner SHALL build one office holding only the
projects that declare that tracker. `tick`, `loop`, `reap`, `ls`, and
`worktree rm` SHALL walk the offices in a stable order — ascending by
tracker name — and apply their existing per-office behaviour to each. The
limit of one claimed task per role per tick SHALL hold per office.

#### Scenario: A tracker sees only its own projects
- **WHEN** `OFFICE` declares `tracker: mock` and `VO` declares
  `tracker: jira`
- **THEN** the `jira` office never queries the tracker for `OFFICE` tasks
  and never touches `OFFICE` working directories, and the `mock` office
  likewise ignores `VO`

#### Scenario: A loop tick covers every office
- **WHEN** `runner loop` runs on a machine with two trackers in use
- **THEN** each iteration ticks both offices, and a stop signal received
  between runs ends the loop after the current run, never in the middle of
  one

#### Scenario: The board lists tasks per tracker
- **WHEN** `runner ls` runs on a machine with two trackers in use
- **THEN** the output lists the tasks of each tracker under that tracker's
  name, and the configuration-source listing is printed once

### Requirement: Offices are built strictly and ticked independently
Building the offices SHALL be strict: a tracker that cannot be opened —
missing connection file, rejected credentials, unreachable address — SHALL
refuse the whole command before any office does work, as a single tracker
does today. Within one `tick`, an error in one office SHALL NOT stop the
remaining offices: every office is visited, the errors are reported
together, and the exit code is non-zero. `loop` SHALL log a failing
office's error and continue, as it does today for a single office.

#### Scenario: A tracker that cannot be opened stops the command
- **WHEN** `OFFICE` declares `tracker: mock`, `VO` declares
  `tracker: jira`, and the jira credentials are rejected
- **THEN** `runner tick` refuses to start, names the jira account problem,
  and the `mock` office claims nothing

#### Scenario: A failing tracker does not stop the others within one tick
- **WHEN** both offices are built, and during `runner tick` the jira
  search fails
- **THEN** the `mock` office still completes its tick, the command reports
  the jira error, and exits non-zero

### Requirement: Machine-wide resources are shared across offices
Resources that belong to the machine, not to a tracker — the run ledger,
the budget policy, the bare clones and working directories, the sandbox
cleaner — SHALL be one instance shared by all offices. Cleanup that walks
the machine's folders SHALL touch only the folders of the projects the
walking office owns.

#### Scenario: A finished folder is swept only by its own office
- **WHEN** a `jira` project's task has reached a terminal status and its
  working directory still exists, and the `mock` office runs its cleanup
  pass first
- **THEN** the `mock` office leaves that directory alone, and the `jira`
  office removes it on its own pass of the same invocation

#### Scenario: A role's daily budget counts every tracker
- **WHEN** `per_role_daily` is set and a role has spent against it on both
  a `mock` and a `jira` task today
- **THEN** the limit is compared against the sum of both, because the
  ledger is one per machine
