# office-install Specification

## Purpose
Gets the office onto a machine that has neither a clone of this repository
nor a Go toolchain: a release with one archive per platform and an install
script that places the binaries, plus `runner init` to lay out
`${OFFICE_HOME}` with copy-ready samples, and `runner version` to say what
is installed.

## Requirements

### Requirement: `runner init` lays out the office home without touching what exists
`runner init` SHALL create `${OFFICE_HOME}` (default `~/.office`) when it
is missing and SHALL place two samples there — `projects.local.example.yaml`
and `tracker.example.yaml` — each only when a file of that name does not
already exist. It SHALL never create, overwrite or rename
`projects.local.yaml`, `tracker.yaml` or `budgets.yaml`, and SHALL print
what it created, what it left alone, and the next step: copy each sample to
its working name and edit it. The samples SHALL be copy-ready in the sense
already required of `tracker.example.yaml`: the projects sample SHALL load
after editing only the project key, `repo_url`, `default_branch` and
`tracker`.

#### Scenario: A fresh machine gets a home and two samples
- **WHEN** `${OFFICE_HOME}` does not exist and `runner init` runs
- **THEN** afterwards the directory exists and contains exactly
  `projects.local.example.yaml` and `tracker.example.yaml`, and the output
  names both as created and says to copy them to `projects.local.yaml` and
  `tracker.yaml`

#### Scenario: A configured home is left untouched
- **WHEN** `${OFFICE_HOME}` contains `projects.local.yaml`, `tracker.yaml`
  and an edited `projects.local.example.yaml`, and `runner init` runs
- **THEN** none of the three files changes, the output says the samples
  already exist, and the exit code is zero

#### Scenario: The projects sample works with four edits
- **WHEN** `projects.local.example.yaml` is copied to
  `projects.local.yaml` and only the project key, `repo_url`,
  `default_branch` and `tracker` are changed
- **THEN** the runner starts and lists that project

### Requirement: `runner version` reports what is installed
`runner version` SHALL print the runner's identity (release version, or
source revision with `-dirty` when applicable) and the directory the office
of that identity is unpacked to, without unpacking it and without opening
any configuration file.

#### Scenario: A release runner names its version and office directory
- **WHEN** a runner of release version `v0.7.0` runs `version` on a home
  where nothing has been unpacked yet
- **THEN** it prints `v0.7.0` and `${OFFICE_HOME}/office/v0.7.0`, and that
  directory still does not exist afterwards

### Requirement: A release provides one archive per platform and a checksum file
A tagged release SHALL publish, as GitHub Release assets, an archive for
each of darwin/arm64, linux/amd64 and linux/arm64 containing `runner` and
`run-agent`, a checksum file covering every archive, and `install.sh`.
Every binary in a release SHALL carry the release version as its identity,
and every runner SHALL carry the office payload and the result checkers
required for its platform. A snapshot build from an untagged tree SHALL
produce the same set of artifacts with a snapshot identity, so the release
can be verified before a tag exists.

#### Scenario: A snapshot build yields three runners with their checkers
- **WHEN** a snapshot release is built from the tree
- **THEN** three archives exist, the darwin/arm64 runner carries checkers
  for darwin/arm64 and linux/arm64, the linux/amd64 runner carries one for
  linux/amd64, the linux/arm64 runner carries one for linux/arm64, and
  each `runner version` prints the snapshot identity

#### Scenario: A tag triggers the release
- **WHEN** a tag matching `v*` is pushed
- **THEN** a workflow builds the artifacts and attaches them to the GitHub
  Release for that tag

### Requirement: `install.sh` places the binaries without a clone or Go
`install.sh` SHALL detect the host platform, refuse an unsupported one by
name, download the matching archive and the checksum file for the requested
version (latest by default), verify the archive against the checksum before
unpacking, and place `runner` and `run-agent` in `${OFFICE_HOME}/bin`
(default `~/.office/bin`), creating the directory when missing and
replacing binaries of an earlier version in place. It SHALL end by saying
where the binaries are, whether that directory is on `PATH`, and that the
next step is `runner init`. It SHALL need only `sh`, `curl` (or `wget`),
`tar` and a checksum tool. It SHALL accept a local directory of release
artifacts in place of the download so that a snapshot build can be
installed and verified offline.

#### Scenario: Install on a supported platform from a release
- **WHEN** `install.sh` runs on darwin/arm64 with no Go and no clone
  present
- **THEN** afterwards `${OFFICE_HOME}/bin/runner` and
  `${OFFICE_HOME}/bin/run-agent` exist and are executable,
  `runner version` prints the release version, and the script's last lines
  name the directory and `runner init`

#### Scenario: Install from a local snapshot
- **WHEN** `install.sh` is pointed at a directory holding a snapshot's
  archives and checksum file
- **THEN** it installs from there without any network access, and
  `runner version` prints the snapshot identity

#### Scenario: A checksum mismatch aborts the install
- **WHEN** the downloaded archive does not match the checksum file
- **THEN** nothing is placed in `${OFFICE_HOME}/bin`, and the script exits
  non-zero naming the mismatch

#### Scenario: An unsupported platform is refused by name
- **WHEN** `install.sh` runs on darwin/amd64
- **THEN** it exits non-zero, names `darwin/amd64` as unsupported, and
  lists the supported platforms

#### Scenario: Updating replaces the binaries and nothing else
- **WHEN** `install.sh` runs on a machine where an earlier version is
  installed and `${OFFICE_HOME}` holds `projects.local.yaml`, `tracker.yaml`
  and an unpacked `office/<old>/`
- **THEN** only the two binaries change; the configuration files and the
  old unpacked office remain as they were
