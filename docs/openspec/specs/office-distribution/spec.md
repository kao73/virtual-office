# office-distribution Specification

## Purpose
Lets the runner carry the office — roles, skills, hooks, the workflow graph,
default budgets, the tracker example and the sandbox kit — inside its own
binary, unpack one version of it under `${OFFICE_HOME}`, and sign every run
with the version it came from, so that a machine needs neither a clone of
this repository nor a Go toolchain to run the office.

## Requirements

### Requirement: The runner carries the office and unpacks it by version
The runner and `run-agent` binaries SHALL carry the office payload:
`roles/`, `skills/`, `hooks/`, `workflow.yaml`, `budgets.yaml`,
`tracker.example.yaml`, and the sandbox kit `sbx-kits/comet-cli` with its
bake script. When no configuration root is given by the environment, the
runner SHALL read roles, skills, hooks, the workflow graph and the default
budgets from `${OFFICE_HOME}/office/<version>/`, unpacking the payload
there first when that directory does not exist. Unpacking SHALL be atomic
from the reader's point of view: a partially written directory SHALL never
be read as an office.

#### Scenario: First run on a fresh office home unpacks the payload
- **WHEN** `${OFFICE_HOME}/office/<version>/` does not exist and the runner
  starts without a configuration root in the environment
- **THEN** afterwards that directory contains `roles/_base/base.yaml`,
  every role directory, `skills/`, `hooks/`, `workflow.yaml`,
  `budgets.yaml`, `tracker.example.yaml` and `sbx-kits/comet-cli/`, and the
  runner's configuration listing names files under that directory as the
  office files it opened

#### Scenario: An unpacked version is left alone
- **WHEN** `${OFFICE_HOME}/office/<version>/` already exists and one of its
  files has been edited by hand
- **THEN** the runner starts, reads the edited file as is, and rewrites
  nothing under that directory

#### Scenario: Two versions live side by side
- **WHEN** a runner of version A has unpacked its office and a runner of
  version B starts against the same `${OFFICE_HOME}`
- **THEN** afterwards both `office/A/` and `office/B/` exist, and B reads
  only from `office/B/`

#### Scenario: A failed unpack leaves no half office behind
- **WHEN** unpacking is interrupted before completion
- **THEN** `${OFFICE_HOME}/office/<version>/` does not exist, the runner
  refuses to start naming the unpack failure, and the next start unpacks
  again from scratch

### Requirement: Unpacked hooks and skill scripts are executable
Every payload file that is executable in the repository SHALL be executable
after unpacking, so that a role's Stop hooks and skill-embedded PreToolUse
hooks load and run exactly as from a clone. The repository SHALL hold a test
that every executable file in the payload begins with a shebang line, since
that is what the unpack step keys on.

#### Scenario: A role with hooks loads from the unpacked office
- **WHEN** the office has been unpacked and a role that declares a Stop
  hook and a PreToolUse hook is loaded
- **THEN** loading succeeds without any "not executable" refusal, and both
  hook scripts carry the execute bit

#### Scenario: A non-executable payload script is caught in the repository
- **WHEN** a script under `hooks/` or `skills/` is committed with the
  execute bit but without a shebang line
- **THEN** the repository test fails naming that file

### Requirement: The result checker ships with the runner
The runner SHALL carry the `validate-result` checker built for its own
platform and, on hosts whose `sbx` backend runs a Linux microVM of the same
architecture, for that Linux platform as well. A release-built runner
SHALL provide the checker to a run without invoking a Go toolchain. A
checker for a platform the runner does not carry SHALL be a refusal that
names the platform and says the runner was built without it — never a
silent fallback to a checker of another platform.

#### Scenario: A sandbox run on Apple Silicon needs no Go
- **WHEN** a release runner for darwin/arm64 prepares a run on the `sbx`
  backend on a machine with no `go` on `PATH`
- **THEN** the run is prepared with a Linux/arm64 checker taken from the
  runner itself, and no build command is attempted

#### Scenario: A local run uses the host checker
- **WHEN** a release runner prepares a run on the `local` backend
- **THEN** the checker handed to the run is the one built for the host
  platform

#### Scenario: A missing platform is refused loudly
- **WHEN** a release runner is asked for a checker of a platform it does
  not carry
- **THEN** run preparation fails naming that platform, and no checker of
  another platform is handed to the run

### Requirement: Every run is signed with the office version
Every run marker written into a tracker, every ledger row and every run
passport SHALL carry the identity of the office the run used. For a release
build that identity SHALL be the release version (`v<X.Y.Z>`), written whole
— shortening SHALL apply only to commit hashes. For a build from source that
carries no release version, the identity SHALL be the build's recorded
source revision, with `-dirty` appended when the build recorded local
modifications. A binary that carries neither a version nor a revision SHALL
refuse to start rather than sign runs with an empty identity.

#### Scenario: A release run is marked with its version
- **WHEN** a runner built for release version `v0.7.0` records a run
- **THEN** the marker line of the report contains `config:v0.7.0`, the
  ledger row's config identity is `v0.7.0`, and the run passport carries
  the same value

#### Scenario: A long version survives the marker
- **WHEN** a runner built for release version `v10.12.3` records a run
- **THEN** the marker contains `config:v10.12.3` unshortened, and parsing
  that marker returns the same identity

#### Scenario: A source build is marked with its revision
- **WHEN** a runner is built from a working tree with uncommitted changes
  and no release version, and it records a run without a configuration
  root in the environment
- **THEN** the marker contains the build's source revision shortened to
  eight characters followed by `-dirty`

#### Scenario: A marker without any identity is refused
- **WHEN** a runner carries neither a release version nor a source revision
- **THEN** it refuses to start and says the build carries no identity

### Requirement: A configuration root in the environment keeps the clone behaviour
When `OFFICE_CONFIG_ROOT` is set, the runner and `run-agent` SHALL read the
office from that directory, SHALL sign runs with that directory's git
commit (with `-dirty` when its tree is not clean), and SHALL build the
result checker from that directory's sources, exactly as before this
change. The payload carried by the binary SHALL NOT be unpacked or read in
that mode. When the variable is unset, the working directory SHALL NOT be
used as an office.

#### Scenario: The wrappers behave as before
- **WHEN** `bin/runner tick` is invoked from a clone
- **THEN** the run reads roles from that clone, its marker carries the
  clone's commit hash, and `${OFFICE_HOME}/office/` is not created

#### Scenario: A hand-built binary without the variable uses its payload
- **WHEN** a binary built from a clone is started from the clone's root
  directory with `OFFICE_CONFIG_ROOT` unset
- **THEN** it reads the office unpacked from its own payload under
  `${OFFICE_HOME}/office/`, not the files of the working directory

### Requirement: The sandbox kit is available from the unpacked office
The unpacked office SHALL contain the sandbox kit and its bake script so
that the sandbox image the runner expects can be baked on a machine without
a clone, and the kit's version SHALL be the one the runner's `sbx` backend
names.

#### Scenario: The image bakes from the unpacked kit
- **WHEN** the bake script is run from
  `${OFFICE_HOME}/office/<version>/sbx-kits/`
- **THEN** it bakes the template the runner's `sbx` backend expects, with
  the same tag the runner names
