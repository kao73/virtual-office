## Why

Nine tests in `internal/pipeline` fail on `master` since commit `f90fd60`
(2026-09-14): `TestTickWithLostLeaseOnlyWarns` and eight `TestReap*` tests
(`ReturnsExpiredTask`, `RemovesSandboxOfDeadRun`, `KeepsSandboxOfReclaimedTask`,
`ReturnsTaskWhenSandboxSurvives`, `CallsHumanAfterStreakOfDeaths`,
`StreakResetsAfterSuccessfulRun`, `DoesNotClaimRemovalOfAbsentSandbox`,
`SkipsProjectUnknownToTracker`). Reproduced on `master` (`8e30e8e`) and on
every commit of `comet/config-cleanup`; the full suite is red for a reason
unrelated to any pipeline behaviour.

## Root cause

The pipeline assigns a lease of `role.Limits.TimeoutSec + workflow.LeaseMargin()`
(`internal/pipeline/pipeline.go:297`). `f90fd60` raised the implementer's
`timeout_sec` from 5400 to 7200 (`roles/implementer/role.yaml`), so with
`lease_margin_sec: 300` the lease is now 7500 s = 2 h 05 m. The nine tests
simulate an expired lease by advancing the fake clock by a hard-coded
`2 * time.Hour` (`pipeline_test.go`, nine sites) — 5 minutes short of the
lease, so `Reap`/`Tick` see a live lease and the tests fail with
`статус "InProgress", ожидался Ready` / `аренда не снята`. The constant
encoded the old lease (1 h 35 m) with margin and silently went stale.

## Fix goal

Make the nine tests derive "the lease has expired" from the lease the tracker
actually recorded for the claimed task (`LeaseUntil` plus a minute), so a
future change to a role's `timeout_sec` or the workflow's `lease_margin_sec`
cannot break them again. Test-only change; no pipeline behaviour changes; no
spec changes.
