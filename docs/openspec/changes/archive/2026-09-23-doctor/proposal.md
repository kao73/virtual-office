## Why

Today the checks that would tell a new user their office is misconfigured
either run inline as a side effect of a real cycle (`Tracker.CheckAccount`
during `office.go`'s startup, `Tracker.CheckWorkflow` only once a claim
candidate exists in `pipeline.go`), live in a separate tool the user has to
know to run by hand (`sbx policy check network`), or don't exist at all
(nothing today confirms the four `customfield_*` in `tracker.yaml` actually
exist on the instance and carry the expected type, or that `depends_on_link`
names a real `issueLinkType`). The roadmap agreed on 2026-09-17 named this
gap explicitly as the item after `install` and `user-guide`: "the manual
checks that actually hurt" for someone standing up their own office against
their own JIRA. `install` (PR #11) and `user-guide` (PR #18) are merged and
`v0.1.0` is tagged; this is that next item.

## What Changes

- **New `runner doctor` subcommand.** Read-only: it must not mutate tracker
  state, sbx policy, or the filesystem beyond reading it. No `--role`, no
  lease or claim side effects, no `--json` (no other `runner` subcommand has
  one; the machine-readable channel stays the exit code, matching `ls`,
  `ledger`, `version`, `offices`).
- **Checks in this change:**
  - Required external tools are on `PATH`.
  - Credential environment variables named by `tracker.yaml` and by the
    projects' role accounts are set (existence only, not value — the same
    boundary `run-agent --dry-run` already draws).
  - JIRA, live: account identity and workflow self-entry reuse the existing
    `Tracker.CheckAccount` / `Tracker.CheckWorkflow`. New: each configured
    `customfield_*` in `tracker.yaml` exists on the instance and carries the
    expected type (`GET /field`), and `depends_on_link` names a real
    `issueLinkType` (`GET /issueLinkType`). Skipped for projects whose
    `tracker` is `mock`.
  - sbx network policy: a thin wrapper around the existing
    `sbx policy check network` that reports whether the sandbox network is
    closed, using the same non-fatal framing `docs/notes/sbx.md` already
    describes for the runner's own network-open warning.
  - Stale `${OFFICE_HOME}/office/<hash8>` directories: report-only finding
    (count, total size) — no deletion. The `runner office ls/prune`
    question `docs/notes/install.md` flagged as "decide together with
    doctor" is resolved in `design.md` as a decision, not necessarily
    shipped as code in this change.
- **Not in this change:** sbx image build/freshness, GitHub push-token
  rights (`forge`), `--json` output, and (unless design.md decides
  otherwise) the `office prune` deletion command itself.
- **Exit code:** `0` when every check passes, `2` (`exitInfra`, the same
  code every other `runner` subcommand uses for an infrastructure failure)
  when at least one check fails. A non-fatal finding (stale office
  directories, open sandbox network) prints but does not affect the exit
  code.

## Capabilities

### New Capabilities
- `office-doctor`: the `runner doctor` subcommand — a read-only preflight
  that surfaces machine- and instance-level misconfiguration (tools,
  credentials, JIRA reachability and shape, sandbox network policy, stale
  office snapshots) before a real `tick`/`loop` cycle would discover the
  same problem as a side effect.

### Modified Capabilities
- (none) — `runner-multi-tracker` keeps every requirement as written: doctor
  reads `projects.local.yaml` and `tracker.yaml` the same way the pipeline
  already does and adds no new configuration surface or tracker-selection
  behavior.

## Impact

- **New**: `cmd/runner/doctor.go` (subcommand, dispatched from
  `cmd/runner/main.go`'s switch) and its test.
- `internal/tracker/jira`: two new read-only calls (field existence/type via
  `GET /field`, link type existence via `GET /issueLinkType`), exposed
  alongside the existing `CheckAccount`/`Whoami`/`CheckWorkflow` — JIRA-
  specific, not part of the generic `tracker.Tracker` interface, since a
  `mock` project has no equivalent to check.
- `internal/runner` or `internal/office` (wherever `${OFFICE_HOME}/office/`
  is enumerated today, per `office-distribution`): a read-only listing used
  to report stale versions.
- Docs: README's install section gains a `runner doctor` mention;
  `docs/notes/install.md` gets an addendum resolving the deferred
  `office ls/prune` decision.
