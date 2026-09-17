## Purpose

Defines the boundary between the office repository, which carries the
framework, and `${OFFICE_HOME}`, which carries every setting of one
instance, so that a fresh clone starts on a new machine without editing a
single committed file — and so that the loader, not the memory of whoever
edits a file, keeps that boundary.

## ADDED Requirements

### Requirement: The office repository carries no instance configuration
The repository SHALL NOT contain a project list. `projects.yaml` SHALL NOT
exist in the shipped repository, and a `projects.yaml` found under the
configuration root SHALL be a start-up refusal that says where its contents
now live (`projects.local.yaml` for projects, `roles/_base/base.yaml` for
shared rules), never a file silently ignored.

#### Scenario: A clean clone starts without being edited
- **WHEN** the repository is the shipped tree, unmodified, and
  `${OFFICE_HOME}/projects.local.yaml` describes at least one project
- **THEN** the runner starts, and the configuration marker it writes into
  run records carries no `-dirty` suffix

#### Scenario: A leftover projects.yaml is refused
- **WHEN** a file named `projects.yaml` exists under the configuration root
- **THEN** the runner refuses to start, and the refusal names the file and
  says that projects live in `projects.local.yaml` and shared rules in
  `roles/_base/base.yaml`

### Requirement: A project is fully described by its machine entry
`${OFFICE_HOME}/projects.local.yaml` SHALL be the only place a project is
declared. Each entry SHALL require `repo_url`, `tracker`, and
`default_branch`; SHALL accept optional `branch_prefix` (default `agent/`),
`worktree_root`, `forge`, `auto_merge`, `network`, and `tools`; and SHALL
reject any other key. A file that declares no project SHALL be refused.

#### Scenario: A minimal entry is enough to run a task
- **WHEN** a project entry sets only `repo_url`, `tracker`, and
  `default_branch: main`
- **THEN** a task `KEY-1` of that project works on branch `agent/KEY-1`
  forked from `origin/main`

#### Scenario: A missing default branch is refused at start-up
- **WHEN** a project entry omits `default_branch`
- **THEN** the runner refuses to start, and the refusal names the project
  key, the missing key, and `projects.local.yaml`

#### Scenario: An explicit branch prefix is honoured
- **WHEN** a project entry sets `branch_prefix: office/`
- **THEN** a task `KEY-1` of that project works on branch `office/KEY-1`

#### Scenario: An unknown key in a project entry is refused
- **WHEN** a project entry contains a key the contract does not define
  (for example `repo: …`)
- **THEN** the runner refuses to start and names the project, the key, and
  the file

#### Scenario: No project at all is refused
- **WHEN** `projects.local.yaml` exists but declares no project
- **THEN** the runner refuses to start and says the file names no project

### Requirement: The reserved defaults key carries rules only
In `projects.local.yaml`, the reserved key `defaults` SHALL accept only
`network` and `tools`. Any per-project key under `defaults` — including
`auto_merge`, `default_branch`, and `branch_prefix` — SHALL be a start-up
refusal, never silently ignored.

#### Scenario: auto_merge under defaults is refused
- **WHEN** `projects.local.yaml` contains
  `defaults: {auto_merge: {enabled: true}}`
- **THEN** the runner refuses to start and names `auto_merge` as not
  belonging under `defaults`

#### Scenario: A relocated project key under defaults is refused
- **WHEN** `projects.local.yaml` contains `defaults: {default_branch: main}`
- **THEN** the runner refuses to start and names `default_branch` as not
  belonging under `defaults`

### Requirement: Shared role rules live with the roles
The rules every role inherits — network hosts and tool allow/deny — SHALL
ship in `roles/_base/base.yaml`, next to the shared prompt
`roles/_base/base.md`, and SHALL be applied to every role at load time,
whether the role is run by the pipeline or by hand. When the `roles/_base`
directory exists, `base.yaml` SHALL be required; a missing file SHALL be a
refusal to load any role, not an empty layer.

#### Scenario: A manual run without a project still carries the denies
- **WHEN** `run-agent` is invoked without `--project`
- **THEN** the role's effective `tools.deny` contains every rule from
  `roles/_base/base.yaml`, including the git-remote denies

#### Scenario: A pipeline run carries base, machine and role rules together
- **WHEN** a role runs on a project whose machine entry adds a host
- **THEN** the run's effective `network.allow` contains the base hosts, the
  machine's addition, and the role's own hosts

#### Scenario: A missing base file is loud
- **WHEN** `roles/_base/` exists but `roles/_base/base.yaml` does not
- **THEN** loading any role refuses and names the missing file

### Requirement: The shipped tracker example is copy-ready
`tracker.example.yaml` SHALL be written so that a verbatim copy to
`${OFFICE_HOME}/tracker.yaml` reads as the working file (its header
describes the working file and points back to the example, not the other
way round), loads after editing exactly `base_url` and the four
`customfield_*` ids, and enables no optional section — `accounts.roles`,
`also_agents`, `issue_type` — unless the operator uncomments it.

#### Scenario: A fresh copy needs five edits
- **WHEN** the example is copied verbatim and only `base_url` and the four
  field ids are changed
- **THEN** the runner loads the file, uses one account for every role, and
  treats no extra account as an agent

#### Scenario: The copied header describes the working file
- **WHEN** the operator opens the copy under `${OFFICE_HOME}`
- **THEN** its header does not describe the file as an example the runner
  does not read

### Requirement: The setup script provides everything the example references
A JIRA instance prepared by `scripts/jira-setup.sh` SHALL contain every
instance-side object the shipped example refers to by name: the statuses,
the four lease fields, and the `depends_on_link` issue link type. Creating
the link type SHALL be idempotent — a second run finds it and creates
nothing — and the script SHALL print its name next to the field ids.

#### Scenario: A fresh instance gains the link type
- **WHEN** the script runs against an instance with no `Depends` link type
- **THEN** afterwards the instance has exactly one link type named
  `Depends` with outward description `depends on`, and the script printed
  that name

#### Scenario: A second run creates nothing
- **WHEN** the script runs again against the same instance
- **THEN** the instance still has exactly one `Depends` link type
