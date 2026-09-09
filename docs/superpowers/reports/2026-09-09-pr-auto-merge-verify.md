# Verification report: pr-auto-merge

Comet Classic full workflow, full verification mode (`comet state scale`: 26
tasks > 3, 21 changed files > 8 — thresholds exceeded; delta spec is a single
new capability).

## Summary

| Dimension | Status |
|---|---|
| Completeness | 26/26 `tasks.md` tasks; 5/5 requirements of the `pipeline-pr-auto-merge` capability covered |
| Correctness | Every requirement implemented and covered by unit/behavioral tests (re-run fresh, all green); the two most safety-critical scenarios (unattended merge, widened staleness trigger) additionally confirmed live on the `EXP` GitHub-backed polygon |
| Coherence | Implementation matches every decision in `design.md`; one real spec-drift item found (two safety mechanisms added during the final whole-branch review were never fed back into `design.md`/the delta spec) — resolved with the project owner during this verify pass via an "Implementation Divergence" section in `design.md`, not a spec change |

## Completeness

**Tasks.** All 26 `tasks.md` items are checked `[x]`. Base ref `b14e8eb`
(merge-base with `master`, recorded in the plan's frontmatter) through
`ed1f0bf` (this verify pass's divergence note): 6 build tasks, one per-task
fix round (Task 4 — a real routing-corruption bug found and fixed), a final
whole-branch review, one fix wave (4 commits) closing a Critical
unbounded-loop finding plus 7 Important/Minor findings, a scoped re-review,
and live verification (Task 7) on `EXP-9`/`EXP-10`.

**Requirement coverage.** `specs/pipeline-pr-auto-merge/spec.md` (new
capability) — 5 ADDED Requirements, addressed individually below.

## Correctness

### ADDED: Auto-merge is off by default and requires a forge

Implementation: `internal/tracker/config.go:477` (`AutoMerge` struct),
`:546` (`Project.PRBranch()`, falls back to `DefaultBranch` when
`TargetBranch` is empty), `:747` (`project.AutoMerge.Enabled &&
project.Forge == ""` → load error naming the project).

- **Scenario "Auto-merge omitted"** — `TestPRPassHumanMergeUnaffectedByGate`
  (fresh run: PASS) — a project with `AutoMerge` zero-valued behaves exactly
  as before, `Merge` never called.
- **Scenario "Auto-merge enabled without a forge"** —
  `TestLoadProjectsRejectsIncomplete`'s `"auto_merge без forge"` case (fresh
  run: PASS).

### ADDED: Office merges an approved task's pull request without a human click

Implementation: `internal/pipeline/prpass.go:355` (`attemptMerge`), called
from `followPR` once `MergeCheck`/`BaseAdvanced` are both clean and
`project.AutoMerge.Enabled`.

- **Scenario "Clean auto-merge task reaches its terminal status unattended"**
  — `TestPRPassAutoMergeMergesCleanGate` (fresh run: PASS). **Additionally
  live-verified**: `EXP-9` on `kao73/expense-tracker` (polygon), PR #17,
  merged by the office (`event:merged` recorded by the office account,
  `admin`, role `office`) with the Jira human-flag field staying `null`
  throughout — no human comment or click at any point (2026-09-09).

### ADDED: Merge target branch is configurable per project

Implementation: `Project.PRBranch()` (above) threaded into
`internal/workspace/workspace.go`'s `addWorktree` (task-branch fork point)
and `internal/pipeline/prpass.go`'s `openPR`/`followPR` (PR target).

- **Scenario "Task branches fork from the configured target branch"** —
  `TestLoadProjectsCarriesAutoMerge`/`TestPRBranch` (fresh run: PASS) at the
  unit level. **Live-verified**: `agent/EXP-9` and `agent/EXP-10` confirmed
  forked from `office-integration` via `git merge-base --is-ancestor`; PR
  #17/#18 both show `baseRefName: office-integration`, not `main`.
- **Scenario "Configured target branch does not exist yet"** — no dedicated
  test (this is an emergent property of *absent* code, not a feature to
  unit-test); verified structurally instead — the final whole-branch review
  grepped every line touched by this change for branch-creation calls
  (`git branch`/`checkout -b`/`update-ref`/`CreateRef`/`push .*refs`) and
  found none. The office has no code path that could create
  `auto_merge.target_branch`.

### ADDED: A stale base returns the task to the implementer, conflict or not

Implementation: `internal/pipeline/prpass.go:244` (`prConflict`, takes
`textConflict bool`, called from both `openPR` and `followPR` whenever
`merge.Conflict || advanced`, unconditionally — not gated behind
`AutoMerge.Enabled`).

- **Scenario "Base advanced with a text conflict"** —
  `TestPRPassConflictReturnsWork`/`TestPRPassReusesPullRequestAfterConflict`
  (fresh run: PASS).
- **Scenario "Base advanced without a text conflict"** —
  `TestPRPassAdvancedBaseTriggersReturnWithoutConflict` (fresh run: PASS).
  **Live-verified**: `EXP-10`, after PR #18 opened, an independent
  non-conflicting commit was pushed directly to `office-integration`; the
  next tick recorded `event:merge-conflict` with the advanced-base wording
  ("База office-integration продвинулась вперёд… конфликта нет, но
  контекст мог устареть") and returned the task to `Ready`; the same tick's
  implementer run correctly merged the base per the updated
  `roles/implementer/role.md:121` procedure and continued without
  confusion.
- **Scenario "Base has not moved"** — covered by
  `TestPRPassAutoMergeMergesCleanGate`/`TestPRPassHumanMergeUnaffectedByGate`
  (the negative case: gate clean, no return).

### ADDED: Repeated merge refusal escalates to a human instead of looping

Implementation: `internal/pipeline/prpass.go:392` (`mergeRefused`),
`internal/tracker/config.go:189` (`Limits.MaxMergeRefusals`).

- **Scenario "Refusal count reaches the configured limit"** —
  `TestPRPassMergeRefusalEscalatesAtLimit` (fresh run: PASS) — three
  consecutive refusals escalate on the third, not before.
- **Scenario "A single refusal, then success"** — same test's rounds 1-2
  assert the task stays at `Approved` with no human flag.

## Coherence

**Design adherence.** Every decision in `design.md`'s Decisions section is
reflected in the shipped code: no new role (`attemptMerge`/`prConflict` are
plain functions, not agent-dispatched); `auto_merge` lives only in
`projects.local.yaml`/`machineProject` (`internal/tracker/config.go`, not
`projects.yaml`); `Forge.Merge()` goes through the real forge API, PR stays
open as an audit trail; `PRBranch()` is a new method, not a repurposed
`DefaultBranch`; the widened `BaseAdvanced` trigger applies unconditionally,
confirmed by `TestPRPassAdvancedBaseTriggersReturnWithoutConflict`'s project
having `AutoMerge` zero-valued; `max_merge_refusals` mirrors
`max_push_failures`; terminal status is unchanged (`Done`).

**Spec drift found and resolved.** The final whole-branch review (after
`design.md`/the delta spec were written) added two safety mechanisms that
neither document described:

1. `limits.max_pr_returns` / `tracker.PRReturns` (`internal/tracker/marker.go:314`,
   `EventPRReturnsExhausted` at `:117`) — bounds the widened staleness
   trigger, which had shipped with no limit at all, and closes a gap where
   `max_merge_refusals` alone could be defeated by alternating
   `merge-refused`/`merge-conflict` markers.
2. `forge.SameRepo` (`internal/forge/forge.go:107`), called from
   `attemptMerge` before `impl.Merge` — verifies the PR URL's repository
   matches the project's configured `repo_url`.

Per `/comet-verify`'s spec-drift handling, presented to the project owner as
a single-select decision (append an Implementation Divergence section to
`design.md` / return to Build to update Design + delta spec via
brainstorming / accept and mark superseded-by-main-spec at archive). Owner
chose the first option. `design.md`'s "Implementation Divergence" section
(commit `ed1f0bf`) now records both, with the reasoning for why they don't
need new `### Requirement:` entries in the delta spec (both are
implementation-level safety properties of requirements the spec already
states, not new user-observable capabilities).

**Deferred, non-blocking (SUGGESTION-level, already parked during Build's
final review and re-confirmed here, none affect correctness):**
- `internal/workspace/workspace.go`'s `addWorktree` doc comment still says
  "от ветки по умолчанию" while the code already forks from `PRBranch()`.
- `internal/tracker/marker.go`'s `EventMerged` doc comment and
  `docs/contracts/tracker-protocol.md`'s `merged` table row still say "слит
  человеком" — true only for the default (non-auto-merge) case now.
- `internal/pipeline/prpass.go`'s `followPR` reads `impl.PRState(url)`
  before the `SameRepo` guard (the guard only lives in `attemptMerge`,
  right before the actual write) — read-only exposure, not a write path.
- Two bookkeeping inaccuracies in `tasks.md`: item 3.3 still names
  `internal/forge/github_test.go` (the plan's own Global Constraints
  already flagged this file doesn't exist; tests actually live in
  `forge_test.go`).

## Build/test evidence

Fresh run at HEAD (`ed1f0bf`), same as recorded for the Build-phase guard:

```
go build ./... && go vet ./... && go test ./...
```

Clean — every package `ok`, no vet warnings. `gofmt -l .` reports exactly
one file, `internal/pipeline/prpass_test.go` — a known, verified-harmless
false positive (gofmt wants to normalize a `'` character inside a Russian
comment into a Unicode `'`, which would corrupt the comment).

## Final Assessment

No CRITICAL or WARNING issues open. All 5 requirements implemented, tested,
and (for the two most safety-critical) live-verified. The one real
coherence gap found during this verify pass — implementation ahead of
`design.md`/the delta spec — was resolved with the project owner during
this pass, not deferred. Remaining items are SUGGESTION-level documentation
staleness with no effect on correctness or safety. **Ready for archive.**
