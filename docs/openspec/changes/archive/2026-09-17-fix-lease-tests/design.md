## Fix

Add one helper to the test `office` wrapper in `internal/pipeline/pipeline_test.go`:

```go
// afterLease — момент, когда аренда задачи, записанная трекером, уже истекла:
// LeaseUntil плюс минута. Считается от факта, а не от константы: «+2 часа»
// протухли, когда implementer'у подняли timeout_sec (f90fd60) и аренда стала
// 2ч05м — тесты «истёкшей аренды» заходили с ещё живой.
func (o *office) afterLease(t *testing.T, key string) time.Time
```

It reads the task through the existing `o.get(t, key)` and returns
`task.LeaseUntil.Add(time.Minute)`, failing the test if the task carries no
lease (`LeaseUntil.IsZero()`), which would mean the test's own setup is wrong.

Replace every `now.Add(2 * time.Hour)` / `at.Add(2 * time.Hour)` in the nine
tests with `o.afterLease(t, "<key>")` for the task the test just claimed
(`OFF-1` in eight tests; the unknown-project test reads its key the same way).
In the two streak tests the clock is set inside a loop — the helper is called
each iteration after the re-claim, so it follows the latest lease.

Why the task's `LeaseUntil` and not `timeout + margin` recomputed from the role:
the tests assert "reap acts once the recorded lease is over", and the recorded
lease is what the pipeline wrote; recomputing would duplicate the formula the
tests are supposed to exercise. No production code is touched.

Verification: `go test ./internal/pipeline/ -run 'TestReap|TestTickWithLostLease' -count=1`
red before the edit, green after; `go test ./...` fully green afterwards.
