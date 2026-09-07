## 1. Tracker: read `depends_on` back on JIRA

- [x] 1.1 Add `DependsOn []string` to `tracker.TaskRef`; copy it in
      `Task.Ref()` alongside the other mirrored fields.
- [x] 1.2 Parse `fields["issuelinks"]` in `jira.toTask`: an entry whose
      `type.name` matches `cfg.DependsOnLink` and carries `outwardIssue`
      contributes that issue's key to `Task.DependsOn`; an entry carrying
      `inwardIssue` instead (the reverse relation) is not read.
- [x] 1.3 Add `"issuelinks"` to `jira.searchFields()` so `ListReady`/`List`
      results (which go through `search()`, not `Get()`) carry `DependsOn`
      too — without this, the gate would see an empty `DependsOn` on every
      candidate even after 1.2.
- [x] 1.4 Unit tests for the parsing in 1.2/1.3: link present with matching
      type, link with a non-matching `type.name` (ignored), multiple links
      at once, `inwardIssue`-only entry (not read), and a `TaskRef` from
      `search()` carrying the same `DependsOn` a `Get()` would.
- [x] 1.5 Update the `Task.DependsOn` doc comment (`internal/tracker/tracker.go`)
      to drop the now-stale claim that JIRA never reads this back.

## 2. Live verification on the JIRA polygon

- [x] 2.1 Verify the read direction empirically, independent of what the
      code under test expects: throwaway pair linked by the reference
      `Blocks` type, confirm via `GET /issue` which side carries
      `outwardIssue` vs `inwardIssue` and that it matches what the JIRA UI
      shows ("A blocks B"). Delete the throwaway pair after.
- [x] 2.2 Repeat on a pair linked through the real `DependsOnLink`
      config/`LinkDependsOn`, confirm the parsing from 1.2 reads the
      dependency in the expected direction. Delete the pair after.

## 3. Dependency-status helper

- [x] 3.1 Add `internal/pipeline/deps.go` with a helper that, given a
      `TaskRef` and a `map[string]TaskRef` for its project plus
      `Workflow.IsTerminal`, returns the dependencies that are not yet
      terminal (including a marker for a dependency key absent from the
      map — treated as unresolved, not satisfied).
- [x] 3.2 Unit tests: all dependencies terminal (none unmet), one
      non-terminal dependency (returned), dependency key missing from the
      map (returned as unmet, not silently dropped), no `DependsOn` at all
      (none unmet, no lookup needed).

## 4. Gate in `claim()`

- [ ] 4.1 In `internal/pipeline/pipeline.go`, `claim()`: after
      `ListReady`, when there are candidates, fetch
      `List(project, o.Workflow.Statuses)` once and build the lookup map;
      skip a candidate with unmet dependencies (via the task 3 helper)
      the same way an exhausted-attempts candidate is skipped, with a log
      line naming what it waits on.
- [ ] 4.2 Pipeline tests (`internal/pipeline/pipeline_test.go`, fake
      tracker): candidate with an unresolved dependency is not claimed;
      the same candidate is claimed once the dependency reaches a
      terminal status; a candidate with a dependency key not present in
      the tracker stays blocked; the same behavior holds for both
      `analyst`- and `implementer`-flow candidates, with no special-casing
      needed for `reviewer`.

## 5. Visibility in `runner ls`

- [ ] 5.1 Extend `printBoard` (`cmd/runner/board.go`) to reuse the same
      `List` call already made for the board and show, for a row with
      unmet dependencies, the blocking task's key and current status
      (e.g. "ждёт: EXP-5 (Review)").
- [ ] 5.2 Test or manually verify the rendered output for a blocked row
      and a row with no dependencies (column stays empty).

## 6. `roles/analyst/role.md`

- [ ] 6.1 Add the `depends_on` criterion next to the existing `split`
      outcome description: a dependency is set only when the dependent
      child's work cannot begin before the other child's code is merged
      (shared schema/migration/model/interface), not for a merely
      convenient ordering.

## 7. Optional cleanup (not blocking)

- [ ] 7.1 Make `linkChildren` (`internal/pipeline/splits.go`) check
      `Get(key).DependsOn` before calling `LinkDependsOn`, now that the
      read is trustworthy on JIRA too, instead of relying on server-side
      idempotency of `POST /issueLink` every cycle.

## 8. Verification

- [ ] 8.1 `go build ./... && go vet ./... && go test ./...` green after
      each task and at the end.
- [ ] 8.2 Live end-to-end check on the polygon: a split with a real
      dependency between two children shows the dependent one withheld
      from `claim()` and `ls` reporting what it waits on, then picked up
      once the dependency reaches `Done`.
