# Comet Build coordinator checkpoint — role-comet-native-workflow

Plan: docs/superpowers/plans/2026-08-30-role-comet-native-workflow.md
review_mode: standard | tdd_mode: tdd | build_mode: subagent-driven-development

## Progress: 12/16 tasks complete (Tasks 1-12)

## IMPORTANT — out-of-band CLI-contract fix chain (2026-08-31), read before Tasks 13-15

While pre-checking Tasks 13-15's shared fixture recipe against the real
comet CLI (0.4.0-beta.18), found and fixed real production bugs in already-
landed, already-reviewed-clean Tasks 10 and 11:
- roles/implementer/role.md: Builder handoff "checks"/"known_limits" shape
  was wrong (bare strings, rejected by the CLI) -> fixed, commit 80c4970.
- roles/reviewer/role.md: dispatch-verifier/final-result needed an outer
  envelope the CLI requires (bare shapes rejected) -> fixed, commit bbe472c.
  Also: a passing final-result needs an extra `--confirmed` self-confirm
  step to actually reach archive-ready (missing entirely before) -> fixed,
  same commit. Follow-up polish (cwdRef guidance, blocked-vs-fail
  distinction, prose nits) -> commit bcb8dec.
- Plan text for Tasks 13-15's fixture recipe corrected to match (lowercase
  eval-* names, real "A1"-style acceptance ids not "AC1", the envelope
  shapes, the extra confirm step) -> commits f3e608a, be9f613.
- Design doc (Superpowers spec + OpenSpec design.md/proposal.md) corrected
  to document the real, verified contract.
- tasks.md 1.2 and 1.3 both closed (genuinely resolved now).
All fixes independently re-reviewed live against the real CLI (2 review
rounds, opus) and confirmed correct. Full detail in the ledger:
.superpowers/sdd/2026-08-30-role-comet-native-workflow/progress.md
Tip for Task 15: `{"kind":"dispatch-verifier","checks":[]}` (empty array)
is accepted by the real CLI and reaches the pass path — no need to invent
a real check command for the fixture.

## Current
- Task: (about to dispatch) Task 13 (tasks.md 7.1, analyst) — evals/analyst/
- Stage: implementing

## History (condensed — full detail in the ledger above)
- Tasks 1-8: done and clean.
- Tasks 9-11: role rewrites done and clean at review time; Task 10/11 later
  needed the CLI-contract fixes above (found during Task 13 prep, not a
  failure of those reviews — the bug was in unverified design-doc claims
  both reviews correctly checked the diff against, not in the diffs
  themselves relative to their own briefs).
- Task 12: done and clean, 28f2d7c.
