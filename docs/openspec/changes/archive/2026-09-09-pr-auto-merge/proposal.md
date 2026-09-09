## Why

Today every task's pull request sits in `Approved` waiting for a human to
click merge on GitHub, for an unbounded amount of time — `docs/DESIGN.md`
§2.8 says plainly "человек сливает... авто-слияния нет и на этом этапе не
будет". That stalls the whole conveyor whenever the human isn't watching,
which contradicts the project's own stated intent that a human should
participate at fixed points, not on every step (`README.md`). Auto-merge was
already considered and deliberately deferred once, during
`role-comet-native-workflow` (Task 23) — but what changed since then is not
new information about the risk, it's the accumulating cost of the status
quo: real pipeline throughput lost to indefinite human wait.

## What Changes

- New per-project config, `auto_merge.enabled` / `auto_merge.target_branch`,
  in `projects.local.yaml` (machine-level, alongside `forge`) — auto-merge
  is opt-in per project, off by default.
- The office (no new role — deterministic code, same as the rest of the PR
  pass) merges an `Approved` task's pull request automatically once a
  mechanical gate holds: `Approved` is already true on entry, plus the task
  branch's base must still be current (no text conflict, and the target
  branch hasn't advanced since the task branch forked). No waiting window
  beyond that.
- New `Project.PRBranch()` resolves the branch task branches fork from and
  the PR pass targets: `auto_merge.target_branch` when set, `default_branch`
  otherwise — so an auto-merge project can land work in an isolated
  integration branch instead of the repository's real default branch,
  without losing the dependency gate (`depends_on`) that relies on a child
  task's branch actually containing its parent's merged work.
- New `Forge.Merge(url) error` method; GitHub implementation via
  `PUT /repos/{owner}/{repo}/pulls/{number}/merge`.
- **Behavioral change, not API-breaking**: the existing conflict-return
  trigger widens from "the task branch text-conflicts with its base" to
  "the base has advanced since the task branch forked, conflict or not" —
  for **every** project, not only auto-merge ones. An unchanged human-merge
  project will see more return-to-implementer cycles when its base branch
  is busy; the returned task is retested against the current base either
  way, which the current implementer procedure already does for the
  conflict case.
- New bounded limit `max_merge_refusals` (`workflow.yaml`), mirroring the
  existing `max_push_failures`/`max_lease_expiries` pattern, escalating to
  a human when the forge keeps refusing a merge the office believes is
  clean.
- `roles/implementer/role.md`: stop hardcoding "the default branch" in the
  merge-conflict procedure (it must merge whatever base the task actually
  targets, not always `default_branch`); widen that section's trigger
  description to match the point above.
- `docs/DESIGN.md` §2.8 rewritten: human merges by default; the office
  merges on an explicit per-project opt-in, by mechanical gate, without a
  new role.

## Capabilities

### New Capabilities
- `pipeline-pr-auto-merge`: office-driven, opt-in automatic merge of an
  `Approved` task's pull request once a mechanical gate holds (review
  already approved, base still current), with a configurable merge target
  branch decoupled from the repository's real default branch.

### Modified Capabilities
(none — the current human-merge PR pass behavior described in
`docs/DESIGN.md` §2.8 was never captured as an `openspec/specs/*`
capability; there is nothing under `docs/openspec/specs/` to file a delta
spec against.)

## Impact

- `internal/tracker/config.go` — new `AutoMerge` struct, `Project.PRBranch()`,
  config validation (`auto_merge.enabled` without `forge` is a load error).
- `internal/workspace/{workspace,merge}.go` — new `BaseAdvanced`; `PRBranch()`
  threaded into task worktree creation.
- `internal/pipeline/{pipeline,prpass}.go` — agent `BaseBranch` uses
  `PRBranch()`; `followPR`/`openPR` gate and call `Merge()`; `prConflict`
  gains the widened trigger and an accurate message depending on which
  condition fired.
- `internal/forge/{forge,github}.go` — `Merge()` on the interface and the
  GitHub implementation, reusing the existing 4xx/`ErrRefused` classification
  in `do()`.
- `roles/implementer/role.md`, `workflow.yaml` (`limits.max_merge_refusals`),
  `docs/DESIGN.md` §2.8.
- Deliberately not touched: no new role; no CI or external review-bot
  integration as a merge gate; no automated integration-branch→main
  promotion; no configurable merge method (hardcoded to a merge commit).
  Full rationale for each — `docs/superpowers/specs/2026-09-08-pr-auto-merge-design.md`.
