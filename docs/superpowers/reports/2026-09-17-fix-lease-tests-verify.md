---
comet_change: fix-lease-tests
role: verification-report
workflow: hotfix
verify_mode: light
verified_range: f9c3c82..aa60d9c
date: 2026-09-17
---

# Verification Report: fix-lease-tests

Lightweight verification. The scale script proposed `full` only because the
task list has 4 items (> 3); the change is one test file with no delta spec,
no Design Doc and no behaviour change, so `verify_mode` was overridden to
`light` (the mechanism the verify skill provides for exactly this case).
All evidence below was produced fresh in the verify phase.

## Summary

| # | Check | Result | Evidence |
|---|-------|--------|----------|
| 1 | All tasks.md tasks `[x]` | PASS | 4 checked, 0 unchecked |
| 2 | Changed files match tasks.md | PASS | `git diff --stat f9c3c82...HEAD`: `internal/pipeline/pipeline_test.go` (+32/−9 — the `afterLease` helper and nine call sites, tasks 2.1–2.2) plus the change's own artifacts (`proposal.md`, `design.md`, `tasks.md`, `.comet.yaml`, `.openspec.yaml`); nothing else |
| 3 | Build passes | PASS | `go build ./... && go vet ./...` → exit 0 |
| 4 | Related tests pass | PASS | `go test -count=1 ./internal/pipeline/ -run 'TestReap\|TestTickWithLostLease'` → `ok`; full suite `go test -count=1 ./...` → 16/16 packages `ok`, 0 `FAIL` (first fully green run since master `f90fd60`) |
| 5 | No obvious security issues | PASS | added lines contain no secrets, no new I/O, no exec; test-only diff |
| 6 | Code review strategy | SKIPPED by policy | `review_mode: off` (hotfix preset default); the diff is a test-only helper plus nine mechanical substitutions, self-reviewed against the design |

**Result: PASS.** No CRITICAL/IMPORTANT/WARNING findings.

## Root cause and fix (recap)

Master commit `f90fd60` raised the implementer's `timeout_sec` 5400→7200, so
the lease assigned at claim (`timeout_sec + lease_margin_sec 300`, see
`internal/pipeline/pipeline.go`, `claim`) became 7500 s = 2 h 05 m. Nine tests
simulated "lease expired" by advancing the fake clock by a constant
`2 * time.Hour` and therefore found the lease still alive
(`статус "InProgress", ожидался Ready`, `LeaseUntil 14:05` vs clock `14:00`).

Fix (`aa60d9c`): a test helper `(*office).afterLease(t, key)` returns the
task's recorded `LeaseUntil + 1m` (failing loudly on a zero lease); the nine
sites use it instead of the constant. `grep -c 'Add(2 \* time.Hour)'` → 0.

RED (before the edit): `go test ./internal/pipeline/ -run 'TestReap|TestTickWithLostLease' -count=1`
→ 9 `--- FAIL` (`TestTickWithLostLeaseOnlyWarns`, `TestReapReturnsExpiredTask`,
`TestReapRemovesSandboxOfDeadRun`, `TestReapKeepsSandboxOfReclaimedTask`,
`TestReapReturnsTaskWhenSandboxSurvives`, `TestReapCallsHumanAfterStreakOfDeaths`,
`TestReapStreakResetsAfterSuccessfulRun`, `TestReapDoesNotClaimRemovalOfAbsentSandbox`,
`TestReapSkipsProjectUnknownToTracker`).
GREEN (after): the same command → all 9 `--- PASS`, package `ok`.

## Skipped in lightweight mode

Spec scenario coverage, Design Doc consistency and drift detection — not
applicable: the hotfix changes no specification and no production code.

## Notes

- Pre-existing and untouched: `gofmt -l` still flags `internal/pipeline/prpass_test.go`
  (formatting only; outside this hotfix's scope).
- This change resolves the deviation WARNING-1 accepted in
  `docs/superpowers/reports/2026-09-17-config-cleanup-verify.md`.
