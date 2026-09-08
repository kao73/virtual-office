## 1. Config: `auto_merge` and `PRBranch()`

- [x] 1.1 Add `AutoMerge` struct (`Enabled`, `TargetBranch`) and `Project.PRBranch()` to `internal/tracker/config.go`
- [x] 1.2 Wire `auto_merge` into `machineProject`, `machineKeys`, and the `LoadProjects` assembly (`Project{ ... AutoMerge: local.AutoMerge ... }`)
- [x] 1.3 Add validation: `auto_merge.enabled=true` with `Forge == ""` is a load error naming the project
- [x] 1.4 Unit tests (`internal/tracker/config_test.go`): `PRBranch()` falls back to `DefaultBranch` when `TargetBranch` is empty and wins when set; `auto_merge` without `forge` fails to load; `auto_merge` key in the office half (`projects.yaml`) is rejected the same way `forge` is today

## 2. Workspace: `BaseAdvanced` and `PRBranch()` threading

- [x] 2.1 Add `Manager.BaseAdvanced(repo, branch, base string) (bool, error)` to `internal/workspace/merge.go` (`git merge-base --is-ancestor`)
- [x] 2.2 Unit tests: base already an ancestor (`false`), base has advanced (`true`), missing ref (error path, mirrors `MergeCheck`'s existing coverage)
- [x] 2.3 Replace `project.DefaultBranch` with `project.PRBranch()` at the task-branch fork point (`internal/workspace/workspace.go:402`)
- [x] 2.4 Replace `project.DefaultBranch` with `project.PRBranch()` in the agent's `BaseBranch` context (`internal/pipeline/pipeline.go:483`)

## 3. Forge: `Merge()`

- [x] 3.1 Add `Merge(url string) error` to the `Forge` interface (`internal/forge/forge.go`), documented as reusing `ErrRefused` for the forge's own final refusal
- [x] 3.2 Implement `GitHub.Merge` (`internal/forge/github.go`): `PUT /repos/{owner}/{repo}/pulls/{number}/merge`, hardcoded `merge_method: "merge"`, via the existing `do()` helper
- [x] 3.3 Unit tests (`internal/forge/github_test.go`, fake HTTP server pattern already used for `OpenPR`/`PRState`): 200 success, 405/409 → `ErrRefused`, transport failure → plain error

## 4. PR pass: gate, widened staleness trigger, merge

- [x] 4.1 Replace `project.DefaultBranch` with `project.PRBranch()` at the `MergeCheck`/`OpenPR` call sites in `internal/pipeline/prpass.go`
- [x] 4.2 Change `prConflict`'s signature to take a `textConflict bool` and word the recorded comment accordingly (real conflict vs. "base advanced, no conflict")
- [x] 4.3 Call `BaseAdvanced` alongside `MergeCheck` in both `openPR` and `followPR`; route to `prConflict` when either condition holds, for every project regardless of `auto_merge`
- [x] 4.4 Add the merge-attempt path in `followPR`: when the gate holds and `project.AutoMerge.Enabled`, call `Forge.Merge`; on success behave like today's human-detected `Merged` state; on `ErrRefused` route to the new refusal counter; on any other error, leave the task in place for the next tick (unchanged existing pattern)
- [x] 4.5 Add `tracker.EventMergeRefused = "merge-refused"` (`internal/tracker/marker.go`, next to `EventPushFailed`/`EventLeaseExpired`) and `limits.max_merge_refusals` in `workflow.yaml`; escalate via a dedicated `EventMergeRefusalsExhausted` marker (mirrors `EventPushFailuresExhausted`) once the count reaches the limit — **not** via `prAnomaly`/`EventPRClosed` as originally planned: task-review round 1 found that reusing `EventPRClosed` corrupts the `pr-opened`/`pr-closed` state family `advancePR` routes on (the PR isn't actually closed, only refused), stranding a human's fix in a reopen loop; fixed by keeping the escalation marker outside that family, per `docs/superpowers/plans/2026-09-08-pr-auto-merge.md` Task 4 (implementation note added there too)
- [x] 4.6 Pipeline tests (`internal/pipeline`): clean gate + `auto_merge.enabled` → `Merge` called, task reaches `pr.merged`; conflict or `BaseAdvanced`-true (both with and without `auto_merge.enabled`) → `prConflict` with the matching wording, attempts unchanged; clean gate + `auto_merge` disabled → task stays put (today's human-wait behavior unaffected); repeated `ErrRefused` → escalates once `max_merge_refusals` is reached, not before; auto-merge-enabled + dirty gate → `Merge` never called (safety-gate negative test, added in the fix round)

## 5. `roles/implementer/role.md`

- [x] 5.1 Replace the hardcoded "ветка по умолчанию" / `origin/<ветка по умолчанию>` wording in the "Если ветка не сливается" section with a reference to the base actually named in the task's context
- [x] 5.2 Broaden that section's trigger description to cover "база продвинулась вперёд, даже без конфликта" alongside the existing conflict case, keeping the same merge → resolve-if-needed → test → `done` procedure

## 6. Documentation

- [x] 6.1 Rewrite `docs/DESIGN.md` §2.8 per this change's decisions (human merges by default; office merges on an explicit per-project opt-in, by mechanical gate, no new role)
- [x] 6.2 Update `README.md`'s PR-pass section (the "Сливает человек. Офис за него этого не делает и на этом этапе делать не будет" passage and the graph description) to describe the opt-in instead of stating it as an absolute
- [x] 6.3 Add a commented `auto_merge` example next to the existing `forge` example in `docs/ONBOARDING.md`'s `projects.local.yaml` walkthrough
- [x] 6.4 Document `event:merge-refused`, `event:merge-refusals-exhausted`, and `limits.max_merge_refusals` in `docs/contracts/tracker-protocol.md`, matching the existing `push-failed`/`push-failures-exhausted`/`max_push_failures` entries (table rows + prose) — gap surfaced during Task 4 implementation and its fix round, this file wasn't in the original Task 6 file list

## Addendum: final whole-branch review + fix wave (after Task 6, before Task 7)

Not a numbered task — Tasks 1-6 were code-complete, so the SDD process's final
whole-branch review ran before Task 7 (which has no source diff). The review
found one Critical gap the plan itself never covered: the widened conflict/
base-advanced return (`prConflict`, deliberately applied to every project) had
no attempt bound and no path to a human, unlike every sibling counter in this
codebase — plus an alternation gap where `merge-refused`/`merge-conflict`
markers could reset each other's counters and defeat `max_merge_refusals`
forever. Fixed with a new combined `limits.max_pr_returns` counter
(`tracker.PRReturns`, mirrors `IdleRuns`' "one counter, two event kinds"
pattern) plus a same-shape dedicated `event:pr-returns-exhausted` marker kept
outside the `pr-opened`/`pr-closed` routing family. Also fixed in the same
wave: `attemptMerge` now verifies the PR URL's repo matches the project's own
before merging (previously trusted whatever repo a ticket-comment URL named);
several stale "офис не сливает" / "ветка по умолчанию" wording spots left over
from Task 4's original design; one gofmt regression from Task 1; a missing
git-stderr detail in `BaseAdvanced`'s error path; one typo.

Commits: `db1b976` (the counter), `c6ae1ac` (repo check + ticket-text fixes),
`4a5e2b4` (wording/gofmt/stderr/typo), `acf2635` (docs). Reviewed clean by a
scoped re-review of the whole wave — see `.superpowers/sdd/2026-09-08-pr-auto-merge/progress.md`
for full findings, verification detail, and two parked (non-blocking)
residual observations: `followPR` reads foreign PR state before the new repo
guard (read-only, low severity), and this change's own delta spec
(`specs/pipeline-pr-auto-merge/spec.md`) doesn't yet mention
`limits.max_pr_returns` — worth reconciling before archiving.

## 7. Live verification

- [ ] 7.1 Live smoke run: an `auto_merge.enabled` project on a real GitHub polygon reaches `Done` with no human comment or click, mirroring the existing PR-pass live checks (`docs/notes/stage-5-live-backlog.md`)
- [ ] 7.2 Live smoke run: a task with an artificially advanced (non-conflicting) base exercises the widened `BaseAdvanced` trigger end-to-end, not just in the unit test
- [ ] 7.3 `go build ./... && go vet ./... && go test ./...` clean
