# Verification report: yougile-adapter-core

- Date: 2026-09-28
- Branch: `comet/yougile-adapter-core`, range `a73fc33..HEAD`: 17 files, +6397/−32
- verify_mode: full (17 tasks, 16+ changed files)
- Result: **PASS**, after one repair round (`verify_failures: 1`)

## Evidence (fresh runs)

| Check | Command | Result |
|---|---|---|
| Build | `go build ./...` | exit 0 |
| Vet | `go vet ./...` | exit 0 |
| Tests | `go test -count=1 ./...` | exit 0, 21 packages ok |
| Race | `go test -count=1 -race ./internal/tracker/yougile/` | ok |
| Live smoke | `go test -tags yougile_live -run TestLive ./internal/tracker/yougile/` against the office-polygon sandbox, with owner approval | PASS (2.83s). Comment text reaches the server verbatim; `"lease": null` clears the lease |
| OpenSpec | `comet classic openspec -- validate yougile-adapter-core --strict` | valid |
| Secrets | grep for hardcoded keys in `internal/tracker/yougile/` | none; the API key never appears in errors (`TestCallErrorNeverLeaksAPIKey`) |

## Full verification checklist

An independent verifier ran the `openspec-verify-change` semantics.

| # | Check | Result |
|---|---|---|
| 1 | All tasks.md tasks `[x]` | PASS, 17/17 |
| 2 | Matches `design.md` decisions | PASS. The idempotency-key scope differs; this is plan departure #6, now recorded in the Design Doc §11 |
| 3 | Matches the Design Doc | PASS. The 13 plan departures are recorded in Design Doc §11 "Implementation Divergence" |
| 4 | Every spec scenario has an asserting test | PASS. All 11 scenarios are mapped to tests (see below) |
| 5 | Proposal goals met, non-goals respected | PASS. All 14 methods are present. `cmd/`, `internal/tracker/config.go` and `tracker.go` are untouched. No `LinkDependsOn` or attachments. No full `tracker.Tracker` assertion; the `coreTracker` subset is asserted in `contract_test.go` |
| 6 | Delta spec and Design Doc agree | PASS after repair. The drift on the FindByMarker-labels and claim-race amendments was resolved by the owner choosing option A (Design Doc §11) |
| 7 | Design Doc locatable | PASS: `docs/superpowers/specs/2026-09-28-yougile-adapter-core-design.md` |

Scenario → test:

| Scenario | Test(s) |
|---|---|
| Transition moves to the column | `TestTransitionMovesColumnOnly` |
| ListReady matches the configured column | `TestListReadyFiltersByColumnServerSide` |
| Overwritten claimant is told it lost | `TestClaimLostWhenOverwrittenAfterWrite`, which now also asserts that the winner stays owner |
| Claiming a live-owned lease fails | `TestClaimRefusesLiveLease` |
| Renew extends a live lease | `TestRenewExtendsOwnLiveLease` |
| Renew of an expired lease fails | `TestRenewRefusesExpiredLease` |
| Release keeps status | `TestReleaseClearsLeaseKeepsStatusAndCounters` |
| Repeated create returns the original | `TestCreateTaskRepeatedReturnsSameTask` |
| Comment round-trips verbatim | `TestCommentRoundTripsAndIsAttributedToOffice` |
| Labeled task found by marker | `TestFindByMarkerFindsLabeledTask` |
| Office account identifiable | `TestCommentRoundTripsAndIsAttributedToOffice`, `TestGetReadsCommentsOldestFirstWithAuthorEmails` |

## Repair round 1

- Spec drift: the Design Doc did not reflect the two Build-time delta-spec
  amendments. The owner chose option A, and §11 "Implementation Divergence"
  was appended in `6f4d1df`.
- WARNING: the overwritten-claim test did not assert the recorded owner.
  The fix is test-only, `827b8e1`, with RED/GREEN evidence. The adapter
  already behaved correctly.

## Code review

- Build phase (review_mode standard): per-task reviews on the risk tasks
  (1, 3, 5, 6, 8) and a final whole-branch review with no Critical or
  Important findings.
- Accepted Minor findings are listed in tasks.md under "Build review
  notes". The most significant is un-namespaced top-level keys in
  `apiData`: one card with a foreign `apiData` shape stalls its column's
  queue. The owner should decide this before `yougile-wiring-and-docs`.
- Verify: the lightweight review dedups with the build-phase final review.
  The only diff added after that review is the test-only fix above and
  documentation.

## Items for the owner

- Build reworded the claim-race spec scenario by coordinator ruling. The
  mirror race stays open, the same limitation as `jira.Claim`.
- The `apiData` namespacing decision is due before `yougile-wiring-and-docs`.
- The main-tree batch file `.comet/batches/yougile-tracker-adapter.json`
  still says status-as-sticker, while the implementation is
  status-as-column.
