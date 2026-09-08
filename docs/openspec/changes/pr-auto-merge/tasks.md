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

- [ ] 4.1 Replace `project.DefaultBranch` with `project.PRBranch()` at the `MergeCheck`/`OpenPR` call sites in `internal/pipeline/prpass.go`
- [ ] 4.2 Change `prConflict`'s signature to take a `textConflict bool` and word the recorded comment accordingly (real conflict vs. "base advanced, no conflict")
- [ ] 4.3 Call `BaseAdvanced` alongside `MergeCheck` in both `openPR` and `followPR`; route to `prConflict` when either condition holds, for every project regardless of `auto_merge`
- [ ] 4.4 Add the merge-attempt path in `followPR`: when the gate holds and `project.AutoMerge.Enabled`, call `Forge.Merge`; on success behave like today's human-detected `Merged` state; on `ErrRefused` route to the new refusal counter; on any other error, leave the task in place for the next tick (unchanged existing pattern)
- [ ] 4.5 Add `tracker.EventMergeRefused = "merge-refused"` (`internal/tracker/marker.go`, next to `EventPushFailed`/`EventLeaseExpired`) and `limits.max_merge_refusals` in `workflow.yaml`; escalate via the existing `prAnomaly` path once the marker count reaches the limit
- [ ] 4.6 Pipeline tests (`internal/pipeline`): clean gate + `auto_merge.enabled` → `Merge` called, task reaches `pr.merged`; conflict or `BaseAdvanced`-true (both with and without `auto_merge.enabled`) → `prConflict` with the matching wording, attempts unchanged; clean gate + `auto_merge` disabled → task stays put (today's human-wait behavior unaffected); repeated `ErrRefused` → escalates once `max_merge_refusals` is reached, not before

## 5. `roles/implementer/role.md`

- [ ] 5.1 Replace the hardcoded "ветка по умолчанию" / `origin/<ветка по умолчанию>` wording in the "Если ветка не сливается" section with a reference to the base actually named in the task's context
- [ ] 5.2 Broaden that section's trigger description to cover "база продвинулась вперёд, даже без конфликта" alongside the existing conflict case, keeping the same merge → resolve-if-needed → test → `done` procedure

## 6. Documentation

- [ ] 6.1 Rewrite `docs/DESIGN.md` §2.8 per this change's decisions (human merges by default; office merges on an explicit per-project opt-in, by mechanical gate, no new role)
- [ ] 6.2 Update `README.md`'s PR-pass section (the "Сливает человек. Офис за него этого не делает и на этом этапе делать не будет" passage and the graph description) to describe the opt-in instead of stating it as an absolute
- [ ] 6.3 Add a commented `auto_merge` example next to the existing `forge` example in `docs/ONBOARDING.md`'s `projects.local.yaml` walkthrough
- [ ] 6.4 Document `event:merge-refused` and `limits.max_merge_refusals` in `docs/contracts/tracker-protocol.md`, matching the existing `push-failed`/`max_push_failures` entries (table row + prose) — gap surfaced during Task 4 implementation, this file wasn't in the original Task 6 file list

## 7. Live verification

- [ ] 7.1 Live smoke run: an `auto_merge.enabled` project on a real GitHub polygon reaches `Done` with no human comment or click, mirroring the existing PR-pass live checks (`docs/notes/stage-5-live-backlog.md`)
- [ ] 7.2 Live smoke run: a task with an artificially advanced (non-conflicting) base exercises the widened `BaseAdvanced` trigger end-to-end, not just in the unit test
- [ ] 7.3 `go build ./... && go vet ./... && go test ./...` clean
