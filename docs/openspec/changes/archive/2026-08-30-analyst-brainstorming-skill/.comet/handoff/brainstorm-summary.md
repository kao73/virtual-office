# Brainstorm Summary

- Change: analyst-brainstorming-skill
- Date: 2026-08-30

## Confirmed Technical Approach

1. `roles/analyst/role.yaml`: `skills: [brainstorming, writing-plans]`.
2. `roles/analyst/role.md`: replace the fixed-file `## Выход` prescription with a dispatcher instruction: always invoke `brainstorming`; always classify the task as architectural regardless of what the skill itself would pick (closes the Bounded/Spike no-artifact risk for now, as a stated temporary rule, not a heuristic); skip re-invocation on resume when prior work (visible via the role's own past `artifacts` in the tracker history) already reflects a received human answer; let `brainstorming`/`writing-plans` save output wherever their own conventions call for, never redirected into `docs/changes/<KEY>/`; declare every actually-created/modified file in `result.json`'s `artifacts` field; never answer `writing-plans`'s "Execution Handoff" question and never invoke `subagent-driven-development`/`executing-plans` — the role's work ends once the plan is saved, unedited (including its "REQUIRED SUB-SKILL" header). `## Как коммитить` generalizes from "the change directory" to "whatever you actually created."
3. Vendor `skills/brainstorming/` and `skills/writing-plans/` (full copies, Superpowers v6.3.0, commit `b36e0829c6d0140e93cfef2ca599b1b07d4a7797`), each with a `SOURCE.md` naming repo/tag/commit.

## Key Trade-offs and Risks

- `implementer`, unchanged, cannot find a plan `brainstorming`/`writing-plans` saved outside `docs/changes/<KEY>/` — degrades to its existing "no plan — work as usual" fallback, not a crash; deferred to a future change scoped to `implementer`.
- Forcing "always architectural" classification is an explicit, temporary override, not a rediscovered heuristic — real Bounded/Spike handling is deferred.
- Cost/turns: both skills invoked on every task, including trivial ones — accepted, to be revisited against real usage data later.

## Testing Strategy

- Update the existing `evals/analyst/capability-basic-plan` (a Bounded-shaped task by brainstorming's own criteria) to check `diff_scope.allow: ["docs/superpowers/**"]` and glob-based `fixture_tests` (existence under `docs/superpowers/specs/` and `docs/superpowers/plans/`) instead of the old fixed `docs/changes/_manual/*.md` paths.
- Add two new golden cases per tasks.md section 3, same glob-based checking style, plus a `fixture_tests` command that parses `.agent/result.json` and asserts `artifacts` is non-empty and names real, existing paths (no new checker kind needed — `fixture_tests` already runs an arbitrary shell command).

## Spec Patches

None — `specs/role-external-skills/spec.md` from the Open phase already describes exactly what this design implements; no missing acceptance scenarios found.
