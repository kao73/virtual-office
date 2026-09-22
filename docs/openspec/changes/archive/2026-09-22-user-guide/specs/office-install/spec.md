## MODIFIED Requirements

### Requirement: `runner init` lays out the office home without touching what exists
`runner init` SHALL create `${OFFICE_HOME}` (default `~/.office`) when it
is missing and SHALL place two samples there — `projects.local.example.yaml`
and `tracker.example.yaml` — each only when a file of that name does not
already exist. It SHALL also create `${OFFICE_HOME}/scheduler/` and place
three scheduler samples in it — `local.office.runner.plist`,
`office-runner.service` and `office-runner.timer` — under the same rule:
each only when a file of that name does not already exist. All three SHALL
be written on every platform, because the machine that edits a unit is not
always the machine that runs it. It SHALL never create, overwrite or rename
`projects.local.yaml`, `tracker.yaml` or `budgets.yaml`, and SHALL print
what it created, what it left alone, and the next step: copy each sample to
its working name and edit it. The samples SHALL be copy-ready in the sense
already required of `tracker.example.yaml`: the projects sample SHALL load
after editing only the project key, `repo_url`, `default_branch` and
`tracker`; each scheduler sample SHALL be installable after editing only the
paths that name the account and `${OFFICE_HOME}`.

#### Scenario: A fresh machine gets a home and two samples
- **WHEN** `${OFFICE_HOME}` does not exist and `runner init` runs
- **THEN** afterwards the directory exists and contains exactly
  `projects.local.example.yaml`, `tracker.example.yaml` and `scheduler/`;
  `scheduler/` contains exactly `local.office.runner.plist`,
  `office-runner.service` and `office-runner.timer`; and the output names
  every file as created and says to copy the two configuration samples to
  `projects.local.yaml` and `tracker.yaml`

#### Scenario: A configured home is left untouched
- **WHEN** `${OFFICE_HOME}` contains `projects.local.yaml`, `tracker.yaml`
  and an edited `projects.local.example.yaml`, and `runner init` runs
- **THEN** none of the three files changes, the output says the samples
  already exist, and the exit code is zero

#### Scenario: An edited scheduler sample survives a second init
- **WHEN** `${OFFICE_HOME}/scheduler/office-runner.service` has been edited
  to name a real account and path, and `runner init` runs again
- **THEN** the file is unchanged, the output says it already exists, and the
  exit code is zero

#### Scenario: The projects sample works with four edits
- **WHEN** `projects.local.example.yaml` is copied to
  `projects.local.yaml` and only the project key, `repo_url`,
  `default_branch` and `tracker` are changed
- **THEN** the runner starts and lists that project

## ADDED Requirements

### Requirement: The office ships one scheduler sample per unit, and none pins a role
The office SHALL keep exactly one copy of each scheduler sample, inside the
payload, and SHALL reach both kinds of reader from it: a reader with a clone
reads the file in the tree, a reader who installed from a release receives it
from `runner init`. No second copy SHALL be kept elsewhere in the repository
for the same unit, because two copies of a sample are two answers to one
question.

Every scheduler sample SHALL invoke the runner without restricting it to a
single role. A sample that passes `--role` names one of three roles, and the
two it omits never run: the pipeline does not fail, it stalls with no message
saying why.

#### Scenario: No shipped sample pins a role
- **WHEN** any scheduler sample shipped by the office is read — from the
  payload in a clone, or from `${OFFICE_HOME}/scheduler/` after `runner init`
- **THEN** its runner invocation carries no `--role` flag

#### Scenario: One unit, one file
- **WHEN** the repository is searched for launchd and systemd units that
  start the runner
- **THEN** each unit exists exactly once, under the payload, and the
  documentation that explains the units points at that one location

#### Scenario: A copied sample moves a task through every role
- **WHEN** a task sits in the first working status of the graph and the
  runner is driven by a shipped scheduler sample's command
- **THEN** the task is picked up, worked, reviewed and reaches a terminal
  status without a human passing it between roles
