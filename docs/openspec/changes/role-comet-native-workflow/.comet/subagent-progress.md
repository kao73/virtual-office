# Comet Build coordinator checkpoint — role-comet-native-workflow

Plan: docs/superpowers/plans/2026-08-30-role-comet-native-workflow.md
review_mode: standard | tdd_mode: tdd | build_mode: subagent-driven-development

## Current
- Task: (about to dispatch) Task 9 (tasks.md 4.1) — roles/analyst/{role.yaml,role.md}
- Stage: implementing
- Model: TBD
- IMPORTANT for this dispatch and Tasks 10/11: role.md text must describe
  `<name>` as the SANITIZED Native change name (runner.CometChangeName's
  output — lowercase, `^[a-z][a-z0-9]*(-[a-z0-9]+)*$`), not the raw safeKey/
  task-key. A real tracker key like "OFF-1" becomes "off-1". The plan's own
  prose (Task 4's Interfaces note, ~line 669) says "the same safe key the
  runner already computes" — that's now imprecise post-Task-5-fix; correct
  it when dispatching.

## Progress: 8/16 tasks complete (Tasks 1-8)

## History (condensed — full detail in .superpowers/sdd/2026-08-30-role-comet-native-workflow/progress.md)
- Task 1 (sbx investigation): done, 79fb6b1.
- Task 2 (role.go hooks schema): done, 1bb1533. Review clean.
- Task 3 (adapter PreToolUse wiring): done, 6e3c57c. Review clean.
- Task 4 (change-root helpers): done, 99de3ac. Review clean; naming later corrected (Task 5 fix round 1).
- Task 5 (archive before PR): done after 2 fix rounds (42f35f9, 8dbaf0d, 9228c67), both re-reviewed clean.
  Highest-risk task so far. Design doc + OpenSpec docs corrected (1305646).
- Task 6 (input.go context line): done, d6de935. No risk signals, no reviewer needed.
- Task 7 (vendor skills/comet): done, 1a50671. Verified byte-identical to source directly.
- Task 8 (vendor skills/comet-native): done, a88e20b. Verified byte-identical to source directly.
  tasks.md 3.1 closed.

## Remaining: Tasks 9-16
9-11: role.yaml/role.md rewrites for analyst/implementer/reviewer (each depends on
  Tasks 2,3,7,8, all landed — TestShippedRolesAreValid should now resolve real files).
12: docs/contracts/agent-io.md prose update.
13-15: eval golden-case fixtures (need local `comet` CLI — confirmed installed).
16: paid live regression run — requires explicit user go-ahead before spending money.
