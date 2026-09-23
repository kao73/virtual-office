## 1. JIRA: new read-only checks

- [x] 1.1 Add `FieldCheck` type and `(*Tracker) CheckFields() ([]FieldCheck, error)` to `internal/tracker/jira/jira.go`: one `GET /field`, matched against `cfg.Fields`'s four `customfield_*` ids and an expected-type table kept next to the `Fields` struct.
- [x] 1.2 Add `(*Tracker) CheckLinkType() error`: one `GET /issueLinkType`, checks `cfg.DependsOnLink` (when non-empty) names an existing type; no-op when empty.
- [x] 1.3 Unit tests for both against the package's existing fake JIRA server, covering: field present with right type, field present with wrong type, field absent, link type present, link type absent, `DependsOnLink` empty (skipped, not failed).
- [x] 1.4 Verify both against the local JIRA Server 8.13 polygon; record the actual `GET /field` and `GET /issueLinkType` response shapes in a doc comment the way `LinkDependsOn` already records its own empirical correction, and adjust the expected-type table if 8.13 differs from the assumed JIRA REST v2 shape.

## 2. `runner doctor` command

- [x] 2.1 Add `cmd/runner/doctor.go`: `doctorCommand(args []string, out io.Writer) error`, `--backend` flag (default `local`), no `--role`, no `--json`.
- [x] 2.2 Tool presence: `git`/`claude` unconditional, `sbx` when `--backend sbx`, `comet` when any project's `Forge != ""`; each via `exec.LookPath`, reported by name.
- [x] 2.3 Credential presence: for each distinct account in `tracker.yaml`'s `Accounts` (`Default` + `Roles`, deduplicated), when at least one project uses `tracker: jira`, check `UserEnv`/`SecretEnv` are set via `os.LookupEnv`; report the variable name only.
- [x] 2.4 JIRA reachability: call `Tracker.CheckAccount()`, `CheckFields()`, `CheckLinkType()` once per distinct JIRA instance; call `Tracker.CheckWorkflow(project, workingStatus)` per project using `tracker: jira`. Skip all JIRA checks and the `tracker.yaml` requirement entirely when no project uses `tracker: jira`.
- [x] 2.5 Sandbox network: call `sbx.BasePolicy{}.Notice()` when `--backend sbx` and the `sbx` tool is present; print as a `warn` finding when non-empty.
- [x] 2.6 Stale office snapshots: when `runner.ResolveOffice(runner.Resolve{}).Source == runner.SourcePayload`, list `${OFFICE_HOME}/office/` entries other than the current identity's directory, sum their size, report as a `warn` finding.
- [x] 2.7 Wire `case "doctor":` into `main.go`'s switch and its `usage` string; return `fmt.Errorf("doctor: %d check(s) failed", n)` when any fatal check failed, `nil` otherwise (fatal = tool/credential/JIRA-reachability failures; `warn` findings never contribute to `n`).

## 3. Tests

- [x] 3.1 `cmd/runner/doctor_test.go`: each check's pass/fail path with fakes/stubs (no live network), asserting per-check lines are printed for every check regardless of earlier failures, and the exit-triggering error's count matches the number of fatal failures.
- [x] 3.2 Scenario test: mock-only `projects.local.yaml` (no `tracker.yaml` on disk) — doctor runs clean without requiring or reading `tracker.yaml`.
- [x] 3.3 Scenario test: `--backend local` — no `sbx` tool check, no sandbox-network finding, even when `sbx` is absent from `PATH`.
- [x] 3.4 Live run against the JIRA Server 8.13 polygon (manual or scripted, matching how `docs/notes/install.md` verified `install`) covering a clean pass and at least one deliberately broken `customfield_*`.

## 4. Docs

- [x] 4.1 README: mention `runner doctor` where `runner init`/`runner version` are already documented.
- [x] 4.2 `docs/notes/install.md`: add the addendum resolving the deferred `office ls`/`office prune` decision (per design.md's Migration Plan — listing now, deletion command later on demand).
