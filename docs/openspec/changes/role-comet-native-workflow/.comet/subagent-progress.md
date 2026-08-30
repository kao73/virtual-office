# Comet Build coordinator checkpoint — role-comet-native-workflow

Plan: docs/superpowers/plans/2026-08-30-role-comet-native-workflow.md
review_mode: standard | tdd_mode: tdd | build_mode: subagent-driven-development

## Current
- Task: (about to dispatch) Task 6 (tasks.md — none, internal/runner/input.go) — dual-root context line
- Stage: implementing
- Model: TBD

## Carry-forward for Tasks 9-11 (IMPORTANT, do not lose this)
role.md text for analyst/implementer/reviewer must describe <name> as the
SANITIZED Native change name (runner.CometChangeName's output: lowercase,
`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`), NOT "the same safe key the runner already
computes" verbatim as the plan's own prose currently says (plan line ~669,
Task 4's Interfaces note) — a real tracker key like "OFF-1" becomes "off-1".
Found during Task 5's re-review (out-of-scope observation).

## History
- Task 1 (sbx investigation): done. Commit 79fb6b1. Review: not needed (no risk signals).
- Task 2 (role.go hooks schema): done. Commit 1bb1533. Review clean.
- Task 3 (adapter PreToolUse wiring): done. Commit 6e3c57c. Review clean.
- Task 4 (change-root helpers): done. Commit 99de3ac. Review clean (0 findings)
  — but see Task 5: CometChangeName's output format later corrected (fix
  round 1, commit 42f35f9) to be Native-CLI-compatible (lowercase/sanitized).
- Task 5 (archive before PR): done after 2 fix rounds, both re-reviewed
  clean. Implementer c0001af. Fix round 1: 42f35f9 (runner naming fix) +
  8dbaf0d (pipeline CLI-contract fix) — corrected 4 Critical findings the
  original review caught by testing the real installed comet CLI live
  (JSON envelope shape, phase-vs-stage, --finish rejected, invalid change
  names) plus Important findings (self-commit needed under isolation:
  current — user chose "archive.go commits it itself" over changing the
  roles' isolation mode; non-busy Ensure failure was aborting the whole PR
  pass). Fix round 2: 9228c67 — scoped an unscoped `git add -A` the fix
  brief itself had prescribed (coordinator's own mistake), found by the
  round-1 re-review. Design doc + OpenSpec design.md/proposal.md corrected
  to match the real CLI contract (commit 1305646). tasks.md 1.3 and 5.1 both
  closed. This was the highest-risk task so far — worth the extra rounds.
