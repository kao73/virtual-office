## Verification Report: doctor

### Summary

| Dimension    | Status |
|--------------|--------|
| Completeness | 17/17 tasks.md items `[x]`; 7/7 spec requirements implemented |
| Correctness  | 7/7 requirements mapped to code + test with file:line evidence |
| Coherence    | design.md's 4-stage isolation contract and all Decisions followed; one prior text/behavior inconsistency (backend default) found and corrected during final review |

Verification method: this report consolidates evidence from the Build-phase final whole-branch review (opus), the fix wave it triggered (sonnet), and the scoped re-review of that fix wave (sonnet) — each independently traced code rather than trusting prior claims — plus a handful of fresh spot-checks run directly for this report (`tasks.md` checkbox count, `design.md`'s corrected backend-default text, the `agent_owner` field-name fix, presence of the two new backend tests). `verify_mode: full` (17 tasks, 1 capability, 12 files — all three thresholds exceeded).

### Completeness

- `tasks.md`: `grep -c '^- \[x\]'` → 17, `grep -c '^- \[ \]'` → 0. All four groups (JIRA checks, `runner doctor` command, tests, docs) fully checked.
- `specs/office-doctor/spec.md`'s 7 requirements — each has a corresponding implementation and test (see Correctness below). No requirement without code.
- Plan's own "Final verification" checklist (`docs/superpowers/plans/2026-09-23-doctor.md`) — all 3 items checked: full suite, build/vet, spec cross-check.

### Correctness

Requirement → implementation → test mapping, each independently verified during the final review or this report's spot-checks:

1. **Runs every check without side effects** (`spec.md:9`) — `cmd/runner/doctor.go`: every `runner.ResolveOffice` call uses `runner.Resolve{}` (zero value, `Unpack: false`); no `Claim`/`Comment`/`Transition`/`LinkDependsOn`/sbx-policy-write call anywhere in the file (confirmed by the final reviewer's trace). Tests: `TestDoctorMakesNoMutatingRequestsOrWrites` (clone mode, full-tree `filepath.WalkDir` snapshot comparison after the fix wave — was a weak top-level entry count before), `TestDoctorMakesNoWritesInPayloadMode` (payload mode, added in the fix wave to cover `checkStaleOfficeSnapshots`'s tree read).
2. **A broken configuration file isolates only the checks that depend on it** (`spec.md:22`) — `doctor.go`'s 4-stage `doctorCommand`: traced by the coordinator during Task 7/10 review that `findings` is a single slice appended to sequentially, so every early `return concludeExit(out, findings)` (5 exit points, confirmed via `grep -n "return concludeExit"`) still prints everything collected before it. Tests: `TestDoctorFreshOfficeWithoutProjectsFileStillReportsIndependentChecks`, `TestDoctorMalformedTrackerYamlIsolatesOnlyDependentChecks`, `TestDoctorMockOnlyOfficeSkipsJiraEntirelyWithoutTrackerFile`.
3. **Reports tool and credential presence** (`spec.md:50`) — `checkTool`, `checkCredentials` in `doctor.go`. Tests: `TestDoctorReportsToolPresence`, `TestDoctorNamesEachMissingToolWithoutStoppingAtFirst`, `TestCheckCredentialsNamesUnsetVariableWithoutItsValue` (also proves no credential value leaks — traced independently in the final review), `TestCheckCredentialsDedupsSharedAccount`.
4. **Checks JIRA reachability and shape for jira projects** (`spec.md:69`) — `checkAccount`/`checkFields`/`checkLinkType` wrapping `*jira.Tracker`'s `CheckAccount`/`CheckFields`/`CheckLinkType` (the latter two new, added in `internal/tracker/jira/jira.go`, verified against the live JIRA Server 8.13 polygon in Task 13 — clean pass and a deliberately-broken-field isolation run both matched expectations exactly). Field-name bug found in final review (`FieldCheck.Config` said `"owner"`, real YAML key is `agent_owner`) — fixed and spot-checked just now: `grep -n '"agent_owner"'` shows the corrected map/slice keys in both `jira.go` and the doc comment. Tests: `TestCheckAccountWrapsMismatchAsFail`, `TestCheckFieldsNamesMissingFieldByConfigAndID`, `TestCheckLinkTypeFailsWhenAbsent`/`SkippedWhenNotConfigured`.
5. **Reports whether the sandbox network is closed** (`spec.md:91`) — `checkSandboxNetwork` wraps `sbx.BasePolicy{}.Notice()` directly (no reimplementation). Tests: `TestDoctorReportsOpenSandboxNetworkAsWarnNotFail`, `TestDoctorSkipsSandboxNetworkWhenSbxToolMissing`, `TestDoctorSkipsSandboxNetworkOnLocalBackend`.
6. **Reports stale office snapshots without removing them** (`spec.md:103`) — `checkStaleOfficeSnapshots`, read-only (`os.ReadDir` + `filepath.WalkDir` only, no `os.Remove*`/`Rename`/`WriteFile` — grepped clean in Task 6's review). Tests: `TestDoctorReportsStaleSnapshotsInPayloadMode` (asserts directories still exist after the run), `TestDoctorStaleSnapshotsNotApplicableInCloneMode`, `TestDoctorStaleSnapshotsOkWhenOfficeDirMissing`.
7. **The exit code reflects only fatal checks** (`spec.md:114`) — `concludeExit`: counts only `level == "fail"`. Tests: `TestDoctorSingleFatalCredentialFailureAmongPassesExits2`, `TestDoctorOnlyNonFatalFindingsExitsZero`.

Plus the regression the final review specifically demanded a test for: `TestDoctorFullSuccessPathExitsZero` now asserts `"jira:workflow:VO:implementer"` appears in output, closing the gap where deleting the workflow-check wiring left the suite green (reviewer proved this by build-overlay deletion before the fix).

### Coherence

- The 4-stage loading/isolation contract in `docs/superpowers/specs/2026-09-23-doctor-design.md` ("Loading sequence and failure isolation") matches `doctor.go` line for line — confirmed independently by the final reviewer tracing all 5 exit paths.
- The narrow `jiraChecker` interface (excluding `Claim`/`Comment`/`Transition`) is enforced at compile time via `var _ jiraChecker = (*jira.Tracker)(nil)`.
- **One real inconsistency found and resolved during Build-phase final review**: `design.md`'s "Command shape" decision originally claimed doctor's `--backend` default (`local`) matched "the other commands," when every other `runner` subcommand actually defaults to `sbx`. The owner was asked directly (not decided unilaterally) and chose to change doctor's default to `sbx` for consistency. `design.md:54-58` now correctly states this; spot-checked just now (`grep -n "default \`sbx\`"` → present, `default \`local\`` → absent). This was a text/behavior divergence caught and fixed before verify, not an open item now.
- One deliberate, undocumented (in design.md's illustrative pseudocode) implementation improvement, noted by the final reviewer as a strength rather than a defect: the design doc's pseudocode sketch showed `checkWorkflow(trk, key, cfg.StatusMap[...])`, but the actual code passes the raw graph status because `Tracker.CheckWorkflow` already maps it internally via `jiraStatus()` — matching `internal/pipeline/pipeline.go`'s existing call pattern. The design doc's own pseudocode is explicitly marked "illustrative, not final Go" (see design.md, "Loading sequence" section), so this is not treated as a Coherence WARNING — the actual "Decisions" prose doesn't specify this mapping detail, only the illustrative code block does.
- No other design-decision-vs-implementation contradictions found.

### Issues

No CRITICAL or WARNING issues remain open. All findings from the Build-phase final review (4 Important, 1 bonus Minor) were fixed and independently re-verified clean before this verify phase began (see `git log` commits `1ce8464` fix wave, confirmed by scoped re-review). 11 further Minor findings from that same review were triaged as non-blocking and are recorded in project memory / git history, not repeated here since verify's job is spec/design conformance, not a second code-quality pass.

**SUGGESTION** (does not block archive, noted for future awareness): the final reviewer's minors #6–#15 (e.g. `jira:reachability` finding id doesn't actually test network reachability since `jira.Open` makes no network call; two early-return stages skip silently without an explicit `skip:` finding; malformed `projects.local.yaml` untested, only missing-file is; clone mode never lists stale snapshots even though `${OFFICE_HOME}/office/` can hold leftover payload versions) remain as documented, non-blocking observations from the final review — none affect this change's spec conformance.

### Final Assessment

**All checks passed. Ready for archive.**
