# Verification report: yougile-wiring-and-docs

Full verification, Comet Classic, branch `comet/yougile-wiring-and-docs` (HEAD `de99734`), 2026-09-29.
Verifier ran read-only; no writes to YouGile or any external service.

## Summary

**Verdict: PASS**

| Severity | Count |
|---|---|
| CRITICAL | 0 |
| IMPORTANT | 0 |
| WARNING | 2 |
| SUGGESTION | 4 |

| Dimension | Status |
|---|---|
| Completeness | 16/16 tasks `[x]`; 1/1 ADDED requirement implemented |
| Correctness | 6/6 scenarios covered by tests (plus live doctor evidence) |
| Coherence | design.md decisions followed; Design Doc followed except two recorded departures and two unrecorded doc-level drifts (WARNING) |

## Evidence commands

Gathered by the coordinator in this turn (not re-run):

- `go build ./... && go vet ./... && go test -count=1 ./...` — exit 0, all packages ok.
- `sh scripts/doc-recipe-test.sh` — all five steps passed.
- `comet classic openspec -- validate yougile-wiring-and-docs --strict` — valid.

Run by the verifier:

- `grep -c -- '- \[ \]' docs/openspec/changes/yougile-wiring-and-docs/tasks.md` → `0`.
- `go test -count=1 -run 'YouGile|ThreeTrackers|MockOnly' ./cmd/runner/` → `ok`.
- `go test -count=1 -run 'LoadConfig|FileConfig|ShippedSample|Key' ./internal/tracker/yougile/` → `ok`.
- `go test -count=1 -run AcceptsYouGile ./internal/tracker/` → `ok`.
- `git diff --stat master...HEAD` — 33 files, +3675/−59; code touched: `internal/tracker/config.go`, `internal/tracker/yougile/{config,yougile,task}.go`, `cmd/runner/{office,doctor,init}.go`, `office/payload.go`, `office/tracker-yougile.example.yaml`.

## Checks

### 1. All tasks are `[x]` — PASS

`tasks.md:3-61`: 1.1–1.4, 2.1–2.2, 3.1–3.2, 4.1–4.3, 5.1, 6.1–6.5 all `[x]`; grep for `- [ ]` returns 0.

### 2. Implementation matches design.md decisions — PASS

- Separate `tracker-yougile.yaml`, not a section of `tracker.yaml`: `internal/tracker/yougile/config.go:17-21` (`TrackerFile`), JIRA schema untouched.
- No instance-specific field ids; only `project_id` + `columns`: `config.go:47-52` (`ProjectConfig`).
- One shared `Tracker` for every role (openMock shape): `cmd/runner/office.go:254-282` — `byRole[role] = office` for every role in `workflow.Order()`.
- Opened only when a project declares `yougile`: `office.go:85-86` dispatch per `TrackersInUse`.

### 3. Implementation matches the Design Doc §2–§7 — PASS (with WARNINGs, see Drift)

- §2 connection file: schema `config.go:34-52`; missing file points at sample and `runner init` `config.go:61-64`; strict decoding `KnownFields(true)` and empty doc tolerated `config.go:70-75`; joined errors `config.go:76-78`; ru. host refusal with attachment hint and `https://yougile.com` `config.go:86-90`; empty `api_key_env` `config.go:91-93`; exactly one project `config.go:94-110`; `project_id`/`columns` `config.go:97-104`; `Tracker(key)` with empty-env error naming the variable `config.go:125-145`.
- §3 `Config.Key`: diff in `internal/tracker/yougile/yougile.go` and `task.go`; tests `TestKeyNamesTheProjectForTheRunner` (`yougile_test.go:601`), `TestKeyDefaultsToProjectID` (`yougile_test.go:631`).
- §4 wiring: `internal/tracker/config.go` trackers list includes `yougile` (`TestLoadProjectsAcceptsYouGile`, `config_test.go:996`); `office.go:85-86` case; `youGileFile` key match / two projects `office.go:212-228`; column coverage `office.go:234-247`; `Open` → `Whoami` wrapped as "учётка офиса" → `Logf` → `accounts=[email]+also_agents` `office.go:254-282`; `init.go` sample + `payload.go` embed; `scripts/doc-recipe-test.sh:50,59` checks it.
- §5 doctor: `cmd/runner/doctor.go:543-603`, ids listed in header `doctor.go:16-17`; cascade `config → skip:yougile`, `cred → skip:yougile-checks`, `open → skip:yougile-checks`, `account` no cascade — matches the table. Only `Open`/`Whoami` (reads) are called.
- §6 docs: `docs/reference/yougile-requirements.md` (account, `api_key_env`, project/columns + curl, `base_url`, Blocked `:226`, archived cards `:233`, doctor table `:247`); `docs/guide/project-setup.md:117-131,157`, `docs/guide/machine-setup.md:91-99`, `docs/reference/configuration.md:4,67,102,113`, `README.md:36,95`, `docs/ONBOARDING.md:30-37`.
- §7 tests: all listed test groups exist (see scenario table and test names below).
- Recorded departures (not counted as drift): Logf prints without a second `yougile: ` prefix (`office.go:271`, `tasks.md:65-68`, pinned in `office_test.go` `TestOpenYouGileAccountsLogAndRoles`, asserts no `yougile: yougile:`); JIRA stage extracted into `doctorJira` (`doctor.go:466-474`, `tasks.md:69-70`, test `TestDoctorBrokenJiraDoesNotHideYouGile`).

### 4. Scenario coverage — PASS

See the table below. Every scenario has at least one automated test; live `runner doctor` output under the office account is recorded in `tasks.md:77-84`.

### 5. proposal.md goals — PASS

- `yougile` accepted in `trackers` — `internal/tracker/config.go` (+ `TestLoadProjectsAcceptsYouGile`).
- `tracker-yougile.yaml` + `office/tracker-yougile.example.yaml` — present; `TestShippedSampleLoads` (`config_test.go:167`).
- `openYouGile` — `office.go:254`.
- `docs/reference/yougile-requirements.md` + guide/reference pages — present (Check 3).
- Minimal doctor stage — `doctor.go:547`.
- `Config.Key` — Check 3 §3.
- README/ONBOARDING mention `yougile` — `README.md:95`, `ONBOARDING.md:30`.
- Carried-over items:
  - API-key user's email in `Accounts` — `office.go:273`; `TestOpenYouGileAccountsLogAndRoles`, `TestOpenYouGileOfficeAccountIsNotHuman`.
  - `Logf` routed to runner output — `office.go:271`; `TestOpenYouGileAccountsLogAndRoles`.
  - `ru.yougile.com` refused with hint — `config.go:86-90`; `TestLoadConfigRuHostExplainsAttachments`, `TestLoadConfigRejectsBrokenFile/ru.*`.
  - Blocked: move out of graph, do not archive — `yougile-requirements.md:226-231`.
  - `runner ls` lists archived cards — `yougile-requirements.md:233-238`.
- Account provisioning (3.1/3.2) done by the owner — `tasks.md:71-84`.

### 6. Delta spec vs Design Doc consistency — PASS with WARNINGs

No contradiction between `specs/runner-multi-tracker/spec.md` and the Design Doc: one project per runner, key match, ru. refusal, office account as agent, file opened only when used — all appear in both. Build-time additions to docs and doctor behaviour are not reflected in the Design Doc (W1, W2).

### 7. Design Doc exists with frontmatter — PASS

`docs/superpowers/specs/2026-09-29-yougile-wiring-and-docs-design.md:1-5`: `comet_change: yougile-wiring-and-docs`, `role: technical-design`, `canonical_spec: openspec`.

## Scenario coverage table

| Scenario (spec.md) | Tests | Other evidence |
|---|---|---|
| A jira-and-mock-only machine has no YouGile file (`:12`) | `cmd/runner/office_test.go` `TestOfficesWithoutYouGileNeverTouchItsFile`; `doctor_test.go` `TestDoctorWithoutYouGileProjectsSkipsStage` | — |
| A yougile project without the file is refused (`:18`) | `office_test.go` `TestOfficesYouGileProjectWithoutFileIsRefused`; `config_test.go` `TestLoadConfigMissingFilePointsAtSample`; `doctor_test.go` `TestDoctorYouGileMissingFileSkipsDependentChecks` | — |
| All three trackers are served together without a flag (`:23`) | `office_test.go` `TestOfficesServeAllThreeTrackers` (builds jira, mock, yougile offices from `newOffices(flags("tick"))`, sources list names the file) | — |
| A second YouGile project is refused (`:30`) | `office_test.go` `TestOpenYouGileRefusals/два yougile-проекта` (projects.local.yaml side); `config_test.go` `TestLoadConfigRejectsBrokenFile/два проекта` (file side) | — |
| The ru. host is refused with a hint (`:37`) | `config_test.go` `TestLoadConfigRejectsBrokenFile/ru.`, `.../ru. со слешем и в верхнем регистре`, `TestLoadConfigRuHostExplainsAttachments` | — |
| The office's own account is not a human (`:42`) | `office_test.go` `TestOpenYouGileOfficeAccountIsNotHuman` (office email reply ignored, human reply recognised), `TestOpenYouGileAccountsLogAndRoles` | live `yougile:account ok учётка офиса: kao@simbirsoft.com` (`tasks.md:83`) |

## Findings

### CRITICAL
None.

### IMPORTANT
None.

### WARNING

- **W1 — Design Doc §5 does not describe the graph-unread `warn` on `config:tracker-yougile.yaml`.** Implementation reports `warn` and continues when the office workflow cannot be read (`cmd/runner/doctor.go:563-567`); the reference doc documents it (`docs/reference/yougile-requirements.md:255,260-263`) and it surfaced live (`tasks.md:86-87`), but Design Doc §5's table lists only `fail → skip:yougile`, and the Build notes do not record this as a departure. Recommendation: add the warn row/sentence to Design Doc §5.
- **W2 — Design Doc §6 does not reflect documentation added after provisioning.** `a9b7926` added to `yougile-requirements.md`: creating the office user (`:109`), adding it to the project as `worker` (`:132-149`), an API recipe for creating project/board/columns (`:194-212`; §6 says only "read the ids with curl"), a distinct `api_key_env` when a personal `YOUGILE_API_KEY` exists (`:45-48`), the fresh-`OFFICE_HOME` warn (`:260`), and a rate-limit section (`:240`). The Build notes mention these (`tasks.md:88-90`), but the Design Doc still describes the smaller scope. Recommendation: extend Design Doc §6 (or §8) with these items.

### SUGGESTION

- **S1** — No unit test pins the graph-unread `warn` branch of the YouGile doctor stage (`doctor.go:566`); only live evidence covers it. Add a `TestDoctorYouGile…` case with an unresolvable office.
- **S2** — Design Doc §4 step 6 and §7 still show/assert the `"yougile: "+f` prefix. Recorded in Build notes, so not drift, but aligning the Design Doc text would remove the need to cross-reference.
- **S3** — `design.md:73-76` (Risks) still says no dedicated YouGile account exists; it now does (`tasks.md:71-76`). Optional wording update.
- **S4** — Scenario "All three trackers are served together" is covered at office-assembly level (`newOffices(flags("tick"))`), not by a full `runner tick` pass; acceptable given `runner-multi-tracker` already tests per-office tick dispatch, but a live `tick` belongs to `yougile-live-validation`.

## Drift assessment

- Code vs delta spec: no drift.
- Code vs design.md: no drift.
- Code vs Design Doc: two recorded departures (Logf prefix, `doctorJira` extraction) — accepted. One unrecorded behavioural addition (graph-unread warn, W1).
- Docs vs Design Doc: unrecorded scope growth of the reference doc after provisioning (W2).
- Neither drift contradicts the delta spec; both are documentation-of-design gaps, not correctness failures. Ready for archive after (optionally) updating the Design Doc.

## Drift resolution

Owner chose option A (2026-09-29): W1 and W2, plus the Logf-prefix and
stale-risk suggestions, are recorded in the Design Doc §9 "Implementation
Divergence". No implementation change.
