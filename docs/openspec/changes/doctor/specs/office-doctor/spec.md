## Purpose
Gives a new office a single read-only command that surfaces the
misconfigurations someone would otherwise only discover as a side effect of
a real `runner tick`, so standing up an office against a fresh JIRA instance
stops being a trial run with production side effects.

## ADDED Requirements

### Requirement: `runner doctor` runs every check without side effects
`runner doctor` SHALL perform only read operations: it SHALL NOT create,
transition, comment on, or link any tracker task, SHALL NOT change sbx
network policy, and SHALL NOT create, delete, or modify any file under
`${OFFICE_HOME}`. It SHALL accept no `--role` flag and SHALL NOT accept
`--json`; its output SHALL be plain text.

#### Scenario: A run against a live JIRA project leaves it untouched
- **WHEN** `runner doctor` runs against an `${OFFICE_HOME}` whose
  `projects.local.yaml` names a project with `tracker: jira`
- **THEN** no task in that JIRA project changes status, gains a comment, or
  gains a link, and no file under `${OFFICE_HOME}` is created or modified

### Requirement: `runner doctor` reports tool and credential presence
`runner doctor` SHALL check that every external tool the office pipeline
shells out to is resolvable on `PATH`, and that every credential
environment variable named by `tracker.yaml`'s `accounts` (default and any
per-role override) and by each project's role accounts is set to a
non-empty value. It SHALL NOT read or report the value of any credential
variable, only whether it is set.

#### Scenario: A missing tool is named, not just "failed"
- **WHEN** a tool the pipeline depends on is not on `PATH`
- **THEN** `runner doctor` reports that specific tool as missing and does
  not stop at the first missing tool before checking the rest

#### Scenario: An unset credential variable is named by its variable name
- **WHEN** `tracker.yaml`'s `accounts.default.secret_env` names a variable
  that is not set in the environment
- **THEN** `runner doctor` reports that variable name as unset, without
  reporting any variable's value

### Requirement: `runner doctor` checks JIRA reachability and shape for jira projects
For each distinct tracker instance named by a project with `tracker: jira`,
`runner doctor` SHALL check: that the instance is reachable and the
configured account's identity matches what the instance reports for
`/myself`; that each `customfield_*` configured in `tracker.yaml`'s
`fields` exists on the instance and carries the expected type; and that
`depends_on_link` names a link type that exists on the instance. When no
project declares `tracker: jira`, `runner doctor` SHALL skip every JIRA
check and SHALL NOT require `tracker.yaml` to exist.

#### Scenario: A deleted custom field is named, not just "JIRA check failed"
- **WHEN** the instance no longer has a field matching the
  `customfield_*` id configured for `fields.lease_until`
- **THEN** `runner doctor` reports that specific configured field (name and
  configured id) as missing, and other JIRA checks still run

#### Scenario: A mock-only office skips JIRA checks entirely
- **WHEN** every project in `projects.local.yaml` declares `tracker: mock`
  and `${OFFICE_HOME}/tracker.yaml` does not exist
- **THEN** `runner doctor` reports every other check and does not report a
  missing `tracker.yaml` or attempt any network call to a tracker instance

### Requirement: `runner doctor` reports whether the sandbox network is closed
When the `sbx` backend is available, `runner doctor` SHALL report whether
the sandbox network policy is closed (deny-all) or open, using the same
check the pipeline already performs before a sandboxed run. An open network
SHALL be reported as a finding but SHALL NOT by itself cause `runner doctor`
to exit non-zero.

#### Scenario: An open sandbox network is reported without failing the run
- **WHEN** the sandbox network policy allows all hosts
- **THEN** `runner doctor` reports the open policy and, if every other
  check passes, exits `0`

### Requirement: `runner doctor` reports stale office snapshots without removing them
`runner doctor` SHALL list any `${OFFICE_HOME}/office/<version>` directory
other than the one the running binary resolves to, and SHALL report their
count and total size. It SHALL NOT delete or modify any of them.

#### Scenario: Old office snapshots are reported, not removed
- **WHEN** `${OFFICE_HOME}/office/` contains the running binary's version
  plus two older versions
- **THEN** `runner doctor` reports 2 stale snapshots and their combined
  size, and both directories still exist after it exits

### Requirement: The exit code reflects only fatal checks
`runner doctor` SHALL exit `0` when every fatal check passes, regardless of
non-fatal findings (open sandbox network, stale office snapshots). It SHALL
exit `2` when at least one fatal check fails: a missing tool, an unset
credential variable, or — for any project with `tracker: jira` — an
unreachable instance, a mismatched account identity, a missing or
mismatched-type `customfield_*`, or a missing `issueLinkType`.

#### Scenario: A single fatal failure among passing checks still exits 2
- **WHEN** every check passes except one unset credential variable
- **THEN** `runner doctor` prints every check's result and exits `2`

#### Scenario: Only non-fatal findings exit 0
- **WHEN** every fatal check passes and the only findings are an open
  sandbox network and one stale office snapshot
- **THEN** `runner doctor` exits `0`
