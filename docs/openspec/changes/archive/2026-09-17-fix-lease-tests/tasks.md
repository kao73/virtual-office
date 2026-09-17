## 1. Reproduce and record

- [x] 1.1 Run `go test ./internal/pipeline/ -run 'TestReap|TestTickWithLostLease' -count=1` on the current tree and record the nine failures (RED evidence)

## 2. Fix the tests

- [x] 2.1 Add `(*office).afterLease(t, key)` to `internal/pipeline/pipeline_test.go` next to `get`, returning `LeaseUntil + 1m` and failing on a zero lease
- [x] 2.2 Replace the nine `Add(2 * time.Hour)` clock advances with `o.afterLease(t, <key>)`; keep each test's comments truthful
- [x] 2.3 `gofmt -l internal/pipeline` clean; `go test ./internal/pipeline/ -run 'TestReap|TestTickWithLostLease' -count=1` green (GREEN evidence); `go test ./...` green
