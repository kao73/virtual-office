---
comet_change: config-cleanup
role: verification-report
verify_mode: full
verified_range: 98c0f59..3917b64
date: 2026-09-17
---

# Verification Report: config-cleanup

Full verification (`verify_mode: full` — 23 tasks, 3 delta-spec capabilities,
37 changed files). Range verified: `98c0f59` (start of Build; the three earlier
branch commits are the OpenSpec proposal/design/plan) → `3917b64`
(`phase=verify`). All evidence below was produced fresh in the verify phase on
2026-09-17 unless labelled *build-phase live check*.

## Summary

| Dimension    | Status |
|--------------|--------|
| Completeness | 23/23 tasks.md items checked; 39/39 plan steps checked; 14/14 delta-spec requirements have implementation evidence |
| Correctness  | 31/31 scenarios covered — 29 by named tests (fresh run, `-count=1`, all PASS), 2 by recorded live checks |
| Coherence    | design.md D1–D9 and the Design Doc followed; 2 accepted naming/shape divergences (SUGGESTION), 0 delta-spec ↔ design-doc contradictions |
| Build/tests  | `go build ./... && go vet ./...` exit 0; every package green except `internal/pipeline` (9 pre-existing failures — accepted deviation, see WARNING-1) |

**Final assessment:** no CRITICAL or IMPORTANT issues. One WARNING accepted by
the owner as a deviation (pre-existing master regression outside this change),
two SUGGESTIONs recorded. **Ready for archive.**

## Check items (full verification)

| # | Check | Result | Evidence |
|---|-------|--------|----------|
| 1 | All tasks.md tasks `[x]` | PASS | `grep -c '^- \[x\]' tasks.md` → 23, unchecked → 0 |
| 2 | Implementation matches `design.md` D1–D9 | PASS (2 SUGGESTIONs) | see "Design decisions" below |
| 3 | Implementation matches the Design Doc (`docs/superpowers/specs/2026-09-17-config-cleanup-design.md`) | PASS (same 2 SUGGESTIONs) | code anchors below; `roles/_base/base.yaml` content compared with `98c0f59:projects.yaml` (13 denies, 15 generic hosts kept, 7 EXP hosts moved to the machine file) |
| 4 | All capability spec scenarios pass | PASS | scenario map below; fresh test run: `internal/tracker`, `internal/runner`, `internal/tracker/jira`, `cmd/runner`, `cmd/run-agent`, `internal/pipeline -run 'TestCycle|TestSweep'`, `internal/adapters/...`, `internal/runagent` → all `ok` |
| 5 | proposal.md goals satisfied | PASS | every "What Changes" bullet maps to a landed task (1.x–6.x) and a scenario below; `projects.yaml` absent (`ls projects.yaml` → no such file) |
| 6 | No contradictions between delta specs and design doc | PASS | delta specs unchanged since Design (no incremental spec edits in Build); Design Doc sections map 1:1 to the three capabilities |
| 7 | Design Doc locatable and related | PASS | `.comet.yaml design_doc` → file exists, frontmatter `comet_change: config-cleanup` |

### Build and test evidence

```
$ go build ./... && go vet ./... && echo BUILD_VET_OK
BUILD_VET_OK
$ gofmt -l .
internal/pipeline/prpass_test.go          # pre-existing on 98c0f59, not touched
$ go test -count=1 ./internal/tracker/ -run 'TestShippedBaseRulesKeepGitRemoteDeny|TestRepoCarriesNoMachineValues|TestRefuseLeftoverOfficeFile|TestLoadProjects|TestTrackersInUse|TestShippedConfigIsValid|TestMergeProjectRules|TestProjectsFor'
ok  	github.com/kao73/virtual-office/internal/tracker	0.492s
$ go test -count=1 ./internal/runner/ -run 'TestUnion|TestLoadRole|TestShippedRolesInheritBaseRules|TestRoleKeeps'
ok  	github.com/kao73/virtual-office/internal/runner	0.344s
$ go test -count=1 ./internal/tracker/jira/ -run 'TestShippedTrackerConfigIsValid'
ok  	github.com/kao73/virtual-office/internal/tracker/jira	0.319s
$ go test -count=1 ./cmd/runner/
ok  	github.com/kao73/virtual-office/cmd/runner	0.919s
$ go test -count=1 ./cmd/run-agent/ -run 'TestDryRun'
ok  	github.com/kao73/virtual-office/cmd/run-agent	8.547s
$ go test -count=1 ./internal/pipeline/ -run 'TestCycle|TestSweep'
ok  	github.com/kao73/virtual-office/internal/pipeline	3.542s
$ go test -count=1 ./internal/adapters/... ./internal/runagent/
ok  	github.com/kao73/virtual-office/internal/adapters/claude	1.789s
ok  	github.com/kao73/virtual-office/internal/runagent	0.700s
```

Full suite (`go test ./...`, build phase exit check): every package `ok` except
`internal/pipeline` — `FAIL` with exactly `TestTickWithLostLeaseOnlyWarns`,
`TestReapReturnsExpiredTask`, `TestReapRemovesSandboxOfDeadRun`,
`TestReapKeepsSandboxOfReclaimedTask`, `TestReapReturnsTaskWhenSandboxSurvives`,
`TestReapCallsHumanAfterStreakOfDeaths`, `TestReapStreakResetsAfterSuccessfulRun`,
`TestReapDoesNotClaimRemovalOfAbsentSandbox`, `TestReapSkipsProjectUnknownToTracker`
(see WARNING-1).

### Security scan

- `TestRepoCarriesNoMachineValues` (fresh PASS) — no absolute paths or
  `customfield_*` in shipped office files; `roles/_base/base.yaml` is inside
  its glob.
- Credentials: `newOffices` reads JIRA accounts only through `jira.LoadConfig`
  → env var names; nothing logged. Live checks used polygon default accounts
  and `gh auth token` exported without echoing.
- `tracker.example.yaml` carries no secrets (names of env vars only).

## Scenario → evidence map

### config-boundary

| Scenario | Evidence |
|---|---|
| A clean clone starts without being edited | *build-phase live check* 5.3 (below): `config_sha: c0d33aa…` without `-dirty`, `git status --porcelain` empty |
| A leftover projects.yaml is refused | `TestRefuseLeftoverOfficeFile`, `TestNewOfficesRefusesLeftoverProjectsYAML`, `TestDryRunProjectFlagRefusesLeftoverProjectsYAML` |
| A minimal entry is enough to run a task | `TestLoadProjects` (`Branch("OFF-12") == "agent/OFF-12"`, `PRBranch() == "master"`) |
| A missing default branch is refused at start-up | `TestLoadProjectsRejectsIncomplete/нет ветки по умолчанию` |
| An explicit branch prefix is honoured | `TestLoadProjectsHonoursBranchPrefix` |
| An unknown key in a project entry is refused | `TestLoadProjectsRejectsIncomplete/неизвестное поле` (names project, key, file) |
| No project at all is refused | `TestLoadProjectsRejectsFileWithoutProjects` (3 sub-cases) |
| auto_merge under defaults is refused | `TestLoadProjectsRejectsProjectKeysUnderDefaults/auto_merge` |
| A relocated project key under defaults is refused | `TestLoadProjectsRejectsProjectKeysUnderDefaults/default_branch`, `/branch_prefix`, `/repo_url` |
| A manual run without a project still carries the denies | `TestDryRunProjectFlagMergesMachineRulesOverBase`; live check 5.3 (`tools.deny` contains `Bash(git *push*)`) |
| A pipeline run carries base, machine and role rules together | `TestLoadRoleUnionsBaseRules` + `TestLoadProjectsLayersDefaultsAndProjectRules` + `TestMergeProjectRulesUnionsWithoutLoss` (compositional) |
| A missing base file is loud | `TestLoadRoleRefusesBaseDirWithoutRules` |
| A fresh copy needs five edits | `TestShippedTrackerConfigIsValid` (`Accounts.Roles` empty, `AlsoAgents` empty, `IssueType == ""`, `DependsOnLink == "Depends"`) |
| The copied header describes the working file | `TestShippedTrackerConfigIsValid` (no "не читает") |
| A fresh instance gains the link type | *build-phase live check* 4.3 (below), run 1 |
| A second run creates nothing | *build-phase live check* 4.3, run 2 |

### runner-multi-tracker

| Scenario | Evidence |
|---|---|
| The removed flag is rejected | `TestOfficesRejectTrackerFlag`; live check 5.3 (`flag provided but not defined: -tracker`, exit 2, before any config line) |
| Two trackers are served without a flag | `TestOfficesBuildOnePerTracker` + `TestCycleReapsEveryOffice` + `TestEachVisitsEveryOfficeAndJoinsErrors`; live check 5.3 (`runner ls` shows `== трекер jira ==` and `== трекер mock ==`). No test performs a claim in each office within one tick (needs an agent run); the mechanism is covered. |
| A mock-only machine has no tracker file | `TestOfficesMockOnlyNeedsNoTrackerFile` |
| A jira project without the file is refused | `TestOfficesJiraProjectWithoutTrackerFileIsRefused` (also asserts the listing order after `projects.local.yaml`) |
| A tracker sees only its own projects | `TestOfficesBuildOnePerTracker` (`Projects.For(name)`), `TestSweepLeavesForeignProjects` |
| A loop tick covers every office | `TestCycleReapsEveryOffice`, `TestCycleStopsBeforeNextOfficeOnCancel`, `TestEachStopsBetweenOfficesOnCancel`, `TestLoopStopsOnSignalWithoutWaiting`, `TestCycleOrderIsReapThenTickThenCompleteSplits` |
| The board lists tasks per tracker | `TestBoardsListTasksPerTracker`, `TestBoardsProjectFlagPicksTheOwningOffice`; live check 5.3 |
| A tracker that cannot be opened stops the command | `TestOfficesRejectedJiraCredentialRefusesWholeCommand` (httptest 401 on `/myself`, `all == nil`) |
| A failing tracker does not stop the others within one tick | `TestEachVisitsEveryOfficeAndJoinsErrors` (injected error; `jira: …` prefix; both offices visited) |
| A finished folder is swept only by its own office | `TestSweepLeavesForeignProjects` (unchanged pipeline behaviour) + shared `Workspaces` pointer asserted in `TestOfficesBuildOnePerTracker` |
| A role's daily budget counts every tracker | `TestOfficesBuildOnePerTracker` (same `Ledger` pointer across offices) |

### role-sandbox-permissions (MODIFIED)

| Scenario | Evidence |
|---|---|
| Роль без собственных правил наследует итог предыдущих слоёв | `TestLoadRoleWithoutBaseDirHasNoBaseLayer` + `TestMergeProjectRulesUnionsWithoutLoss` |
| Ролевой слой добавляет к унаследованному, не стирая его | `TestLoadRoleUnionsBaseRules`, `TestMergeProjectRulesUnionsWithoutLoss` |
| Новый проект без собственных сетевых правил получает базовый список | `TestShippedRolesInheritBaseRules`, `TestLoadProjectsLayersDefaultsAndProjectRules` |
| Специфика одного проекта не видна другому | `TestLoadProjectsLayersDefaultsAndProjectRules` (`OFF.network` has no `vo-only.test`) |
| Машина добавляет внутренний хост поверх проектных правил | `TestDryRunProjectFlagMergesMachineRulesOverBase` (`machine-only.test` only with `--project`) |
| Машинные умолчания достаются каждому проекту машины | `TestLoadProjectsLayersDefaultsAndProjectRules` |

## Design decisions (design.md D1–D9) — code anchors

| Decision | Anchor |
|---|---|
| D1 `projects.yaml` deleted; shared rules in `roles/_base/base.yaml`; `LoadRole` unions | `roles/_base/base.yaml`; `internal/runner/role.go:31,108,162` (`BaseRulesFile`, `Union`, `loadBaseRules`); `ls projects.yaml` → absent |
| D2 `default_branch` required, `branch_prefix` default `agent/` | `internal/tracker/config.go:39` (`DefaultBranchPrefix`), `LoadProjects` per-entry checks |
| D3 `defaults` allow-list | `config.go:627,632` (`defaultsKeys`, `checkKeys`) |
| D4 N offices walked by an `offices` type | `cmd/runner/offices.go:42,60,74,100` (`each`, `byProject`, `loop`, `cycle`); `office.go:37` constructor; `pipeline.go` has no `Loop`/`tickOnce` (grep count 0) |
| D5 strict build, isolated tick | `office.go` per-tracker loop returns before `all` is allocated; `each` joins with `<name>: ` prefix |
| D6 `tracker.yaml` only under jira | `office.go:91` (`jira.LoadConfig` inside `case "jira"`) |
| D7 `add_link_type` | `scripts/jira-setup.sh:124,146,186` |
| D8 copy-ready example | `tracker.example.yaml`; `TestShippedTrackerConfigIsValid` |
| D9 docs | `docs/DESIGN.md` §2.5/§2.6, `README.md`, `docs/ONBOARDING.md`; `git grep 'tracker jira\|projects\.yaml' -- README.md docs/DESIGN.md docs/ONBOARDING.md | grep -v projects.local.yaml` → only the two leftover-file-refusal sentences |

## Build-phase live checks (recorded)

Both were performed in Build (no tokens spent); the Comet hook keeps this file
verify-only, so their transcripts were held in the build workspace and are
copied here verbatim.

### Task 4.3 — `Depends` link type (`scripts/jira-setup.sh`)

Instance: the usual polygon (`base_url` in `~/.office/tracker.yaml`,
`10.73.10.235`) was unreachable at the time (ssh/HTTP timeouts), so, as
`design.md` "Risks" prescribes, a fresh `bootstrap/jira` container was brought
up locally (`http://localhost:2990/jira`, `admin`/`admin`, empty). Project
`OFF` was created first (ONBOARDING Б2 curl). This exercises the creation
branch on run 1 and idempotence on run 2.

```
before:  ['Blocks', 'Cloners', 'Duplicate', 'Relates']

$ scripts/jira-setup.sh --url "$url" --user admin | sed -n '/^тип связи:/,/^учётки:/p;/depends_on_link/p'
тип связи:
  тип связи Depends заведён
учётки:
  depends_on_link: Depends

$ scripts/jira-setup.sh --url "$url" --user admin | sed -n '/^тип связи:/,/^учётки:/p;/depends_on_link/p'
тип связи:
  тип связи Depends уже есть
учётки:
  depends_on_link: Depends

after:   ['Blocks', 'Cloners', 'Depends', 'Duplicate', 'Relates']
         1 depends on is depended on by      # count, outward, inward
```

After the review fix (`add_link_type` now verifies the POST answer before
printing "заведён"), the sequence was repeated on the same instance: `Depends`
deleted via `DELETE /rest/api/2/issueLinkType/10300`, run 1 → "заведён",
run 2 → "уже есть", final state exactly one `Depends`, `outward = depends on`.
Negative checks: a wrong password makes the script fail at the first API call
without any false "заведён"; a throwaway copy with a bogus POST path shows the
new guard reporting the server's 404 body and exiting 1.

Note: this JIRA 8.13 family renders `outward`/`inward` reversed in the UI
(known finding, compensated in `jira.LinkDependsOn`); the API answer above is
what counts.

### Task 5.3 — runner without a flag, `config_sha` without `-dirty`

Environment: this machine's `~/.office/projects.local.yaml` migrated per the
Design Doc (backup at `~/.office/projects.local.yaml.pre-config-cleanup`);
`tracker.yaml` points at the remote polygon `10.73.10.235` (reachable again;
its developer licence expired 16/Sep/26; REST kept answering).

```
$ ./bin/runner ls            # exit 0
конфигурация:
  workflow.yaml          /Users/aleksejkolesnikov/IdeaProjects/virtual-office/workflow.yaml (офис, есть)
  projects.local.yaml    /Users/aleksejkolesnikov/.office/projects.local.yaml (машина, есть)
  tracker.yaml           /Users/aleksejkolesnikov/.office/tracker.yaml (машина, есть)
  budgets.yaml           /Users/aleksejkolesnikov/IdeaProjects/virtual-office/budgets.yaml (офис, есть)
  budgets.yaml           /Users/aleksejkolesnikov/.office/budgets.yaml (машина, нет)
== трекер jira ==
EXP-2      Done         —                        попыток:0                  4д         Трекер личных расходов
EXP-3      Done         —                        попыток:0                  4д         Категории расходов и доходов
EXP-4      Done         —                        попыток:0                  3д         Транзакции (расходы и доходы)
EXP-5      Done         —                        попыток:1                  3д         Бюджеты по категориям
EXP-6      Done         —                        попыток:0                  2д         Дашборд
EXP-7      Done         —                        попыток:0                  3д         Импорт банковской выписки (бонус)
VO: проект описан в projects.local.yaml, но трекер его не знает — пропускаю
== трекер mock ==
задач нет (проекты: OFFICE)
```

No `projects.yaml` line; `tracker.yaml` listed after `projects.local.yaml`;
both trackers served under their headers. `VO` is skipped because that remote
polygon never had a `VO` project (it lived on the old local polygon) — this is
the runner's documented `SkipUnknownProject` behaviour, an instance fact, not a
code finding.

```
$ git status --porcelain      # (empty)
$ ./bin/run-agent --role analyst --workdir /tmp/probe --task /tmp/task.md --backend local --dry-run   # exit 0
config_sha: c0d33aa779bb7eecae53668f832b504adfeaddc9
    "deny": [ "Bash(git *branch*)", "Bash(git *checkout*)", "Bash(git *config*)", "Bash(git *filter-branch*)",
              "Bash(git *filter-repo*)", "Bash(git *push*)", "Bash(git *remote*)", "Bash(git *reset*)",
              "Bash(git *switch*)", "Bash(git *worktree*)", "Bash(rm *--force*)", "Bash(rm *--recursive*)",
              "Bash(rm *-f*)", "Bash(rm *-r*)" ]

$ ./bin/runner tick --tracker jira; echo "код $?"
runner: flag provided but not defined: -tracker
код 2
```

### Task 5.1 — residual `projects.yaml` mentions (fresh, after Task 6)

`git grep -n 'projects\.yaml' -- . ':!docs/notes' ':!docs/openspec/changes/archive' ':!docs/comet' ':!docs/STAGE-*' ':!docs/superpowers' ':!docs/openspec/changes/config-cleanup' | grep -v 'projects\.local\.yaml'`:

- the guard itself and its tests: `internal/tracker/config.go:35` (`OfficeProjectsFile`), `config_test.go`, `boundary_test.go:18`, `cmd/runner/office.go:53`, `cmd/runner/office_test.go`, `cmd/run-agent/main.go:127`, `cmd/run-agent/main_test.go`;
- the two refusal sentences in `README.md:606` and `docs/DESIGN.md:53`;
- history pointers: `roles/_base/base.yaml:11`, `roles/reviewer/role.yaml:65`, `internal/runner/role_test.go:524`;
- reference documents deferred to the follow-up documentation change:
  `docs/contracts/role-sandbox-permissions.md`, `docs/contracts/tracker-protocol.md`;
  `docs/openspec/specs/role-sandbox-permissions/spec.md` (rewritten by the delta merge at archive).

## Issues

### CRITICAL
None.

### IMPORTANT
None.

### WARNING

**WARNING-1 — accepted deviation (owner decision, 2026-09-17): 9 pre-existing
failing tests in `internal/pipeline`.** `TestTickWithLostLeaseOnlyWarns` and
eight `TestReap*` tests fail identically on the base commit `98c0f59`
(reproduced in a detached worktree) and are unrelated to this change's diff
(the pipeline edit here is the deletion of `Office.Loop`/`tickOnce` and the
`cycle` test rewrite; `-run 'TestCycle|TestSweep'` passes). Root cause: master
commit `f90fd60` raised the implementer's `timeout_sec` 5400→7200, so the
lease is `timeout + lease_margin_sec` = 7500 s (2 h 05 m) while those tests
advance the clock by a hard `+2h`. Fixing it means editing `pipeline_test.go`
outside this change's specs and against its Global Constraint ("the pipeline
package does not change"), so the owner chose **accept as deviation**; a
separate hotfix should make those tests derive the advance from the role's
lease instead of a constant. Impact scope: test suite signal only; no runtime
behaviour. Also pre-existing: `gofmt -l` flags `internal/pipeline/prpass_test.go`.

### SUGGESTION

**SUGGESTION-1 — constructor name.** `design.md` D4 and the Design Doc name the
constructor `offices()`; it is implemented as `newOffices()` because Go's
single package namespace cannot hold both `type offices` and `func offices`
(coordinator ruling during Build). The high-level decision (one `pipeline.Office`
per tracker, walked by an `offices` type) is intact. The design documents are
marked superseded by the main spec at archive; no edit in verify.

**SUGGESTION-2 — `offices` struct shape and test fixture.** The Design Doc's
sketch lists fields `list`, `out`; the implementation adds `workspaces
*workspace.Manager` so `worktree rm` can list folders before it knows the
owning office (documented in the plan as an accepted deviation). The
`twoOffices` test fixture carries a `Workspaces` the plan omitted (a nil
manager panicked in `PRPass`). Both are implementation details below the
decision level.

## Follow-ups (not blocking archive)

1. Hotfix change: `internal/pipeline` lease tests → derive the clock advance
   from the role's lease (WARNING-1).
2. Owner decision: `runner worktree rm KEY --force` for a folder whose project
   was removed from `projects.local.yaml` now refuses at `byProject` before
   `--force` is consulted (old code tolerated it via `ErrNotFound`). Either
   accept or let `--force` proceed with a nil task.
3. Documentation follow-up change (already planned in proposal.md): `CONFIG.md`,
   `projects.local.example.yaml`, `bootstrap/` scheduler examples,
   `docs/contracts/*.md` still mentioning `projects.yaml`.
4. Cosmetic: `TrackersInUse()` computed twice in `newOffices`; `tick` helper vs
   `tickCommand` closure; `newOffices` length (an `openTracker` extraction);
   `add_link_type`'s GET failure falls through to the POST (which then trips
   `set -e`).
5. The local `bootstrap/jira` container (`office-jira-software`, port 2990)
   brought up for Task 4.3 is still running; `docker compose down -v` in
   `bootstrap/jira` removes it and its two volumes.

## Review trail (Build)

Per-task reviews under `review_mode: standard`: Tasks 1, 2, 3, 4, 5 (risk
signals hit) each reviewed, Tasks 1/3/4/5 each closed after one fix round with
a scoped re-review; Task 6 (docs only) had no risk signal. Final whole-branch
review (opus): "Ready to merge — Yes", 0 Critical/Important, 4 Minors fixed in
one wave (`e25320e`, `ebf6b61`) and re-reviewed clean.
