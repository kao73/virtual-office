---
comet_change: analyst-brainstorming-skill
role: technical-design
canonical_spec: openspec
archived-with: 2026-08-30-analyst-brainstorming-skill
status: final
---

# analyst-brainstorming-skill — Technical Design

This deepens `docs/openspec/changes/analyst-brainstorming-skill/design.md` (the high-level framework and its decisions) into concrete implementation detail. It does not restate or replace the Open-phase decisions; it specifies exactly what `role.yaml`/`role.md` must say and how the two golden cases and one existing eval case must check for it.

## Architecture

`analyst` gains two mounted, vendored Superpowers skills (`brainstorming`, `writing-plans`) via the existing `skills:` mechanism. The dispatcher lives entirely in `role.md` prose — no new Go code, no new runner/adapter mechanism. The two skills run to their real, unmodified conclusion; the only office-side addition is the instruction set that tells `analyst` how to start (always invoke, always treat as architectural), how to skip redundant work on resume, and how to end cleanly when the skill's own process reaches a step the role cannot honor.

## `roles/analyst/role.yaml`

```yaml
skills: [brainstorming, writing-plans]
```

No other field changes. `tools.allow`'s `Edit`/`Write` (already unrestricted, from the separate `remove-role-guards` change) is sufficient — nothing in this design needs a new tool grant.

## `roles/analyst/role.md`

### Removed

The current `## Выход` section's fixed-file prescription — the per-file bullet lists for `brief.md`/`design.md`/`tasks.md` (goal paragraph, `## Критерии приёмки`, `## Что не входит`, `## Разбиение`; `## Что трогаем`, `## Решения`, `## Риски`, `## Чем проверять`; task-list format) — is removed in full. With the dispatcher below invoking `brainstorming` unconditionally at the start of every fresh task, this office-authored structure is never read in practice; keeping it risks silent drift from what the vendored skills actually produce.

The size-threshold guidance ("больше 8 пунктов в tasks.md или больше 4 модулей в design.md — повод спросить про разбиение") is also removed as a numeric rule tied to the old fixed files — `brainstorming`'s own "Understanding the idea" step already covers project decomposition for oversized requests, and duplicating a threshold the office cannot observe (the files' shape is no longer standardized) would be unenforceable prose.

### Added, replacing `## Выход`

The dispatcher instruction, in this order (order matters: an agent following it top to bottom must hit each rule before the situation it governs arises):

1. **Invocation.** At the start of a new task, always invoke `Skill: office-role-analyst:brainstorming` before doing anything else — do not rely on the skill's own description to trigger it (empirically unreliable per `experiment/skill-question-spike`).
2. **Forced classification.** Once `brainstorming` asks for or performs its own spike/bounded/architectural classification, treat the task as **architectural** regardless of what the skill's own criteria would pick. State plainly that this is a deliberate, temporary office rule — not a rediscovery of the skill's own heuristic, and not something the role should second-guess task by task. (This is what keeps every task landing in a real, committed spec + plan; the skill's own lighter paths produce no file at all, which this office cannot yet consume — see design.md's Risks.)
3. **Resume-skip.** Before invoking the skill, check whether prior work already exists for this task — a spec/plan already committed (wherever `brainstorming`/`writing-plans` placed it) that already reflects any human answer received (visible in the task's own conversation history, including this role's own past `artifacts` entries). If so, do not invoke the skill again; verify the existing work still answers the task and proceed straight to the outcome.
4. **Output location.** Let `brainstorming` and `writing-plans` save their spec and plan wherever their own process calls for (their own default paths). Do not redirect, copy, or rename their output into `docs/changes/<KEY>/` or any other office-chosen location.
5. **Artifacts declaration.** Before finishing, list every file actually created or modified during the run — wherever it lives — in `result.json`'s `artifacts` field. This is not optional detail: it is the only mechanism that ties a freely-placed file back to this task for anyone reading the run's report later. On a clean resume-skip (rule 3) where nothing needs to change, this still means listing the already-existing spec/plan, even though this run's own diff touched neither — otherwise a correct resume would report zero artifacts, which is indistinguishable from one that produced nothing at all.
6. **Execution Handoff.** When `writing-plans` reaches its "Execution Handoff" step (offering a choice between `subagent-driven-development` and `executing-plans`), do not answer the question and do not invoke either named skill. This office always executes plans through `implementer`, one task per commit — the question's answer is the same regardless of who is asked, so asking a human via `needs_human` would waste a round-trip on a foreordained answer. The role's work ends once the plan is saved; the plan document — including its "REQUIRED SUB-SKILL" header — is committed exactly as `writing-plans` wrote it, never edited.

### Changed: `## Как коммитить`

Generalize the existing three self-held rules from "the change directory" to "what you actually created": the `git add -A` caution (rule 1: commit what you made, not everything lying around — env/cache directories from tests you ran are not your work regardless of which directory holds them), `done` requires committed work to exist (rule 2, unchanged in substance — "no plan" now means "nothing `brainstorming`/`writing-plans` produced was committed," not specifically an empty `docs/changes/<KEY>/`), and the revert/restore split for own-mistake-vs-uncommitted (rule 3, unchanged). No wording here depends on a fixed directory name once generalized.

### Unchanged

`## Вход`, `## Исходы`, `## Когда задачу вернул разработчик` — none of these reference the fixed-file contract or `write_scope`/guards; they stand as-is.

## Vendoring

```
skills/brainstorming/          # full copy, Superpowers v6.3.0
skills/brainstorming/SOURCE.md
skills/writing-plans/          # full copy, Superpowers v6.3.0
skills/writing-plans/SOURCE.md
```

`SOURCE.md` (identical shape for both skills):

```markdown
# Source

- Repository: https://github.com/obra/superpowers
- Tag: v6.3.0
- Commit: b36e0829c6d0140e93cfef2ca599b1b07d4a7797
```

No drift-check script, no version pin beyond this file — an explicit owner decision from the Open phase (design.md, "Vendor pinned copies, not a live reference").

## Testing Strategy

### Update `evals/analyst/capability-basic-plan`

This existing case's task ("add `Shout`, write the plan") is Bounded by `brainstorming`'s own criteria, but under the forced-architectural rule it now goes through the full path and lands in `docs/superpowers/`, not `docs/changes/_manual/`. Update `expect.yaml`:

```yaml
role: analyst
checks:
  - kind: outcome
    expect: done
    next_owner: implementer
  - kind: diff_scope
    allow: ["docs/superpowers/**"]
  - kind: fixture_tests
    command: >-
      find docs/superpowers/specs -name '*.md' | grep -q . &&
      find docs/superpowers/plans -name '*.md' | grep -q .
```

This keeps the case's original purpose (a plain, unambiguous plan-writing task) as a canary that the new, forced-architectural path still produces real, findable output — not a specific claim about file naming, which is now the skills' own concern.

### New golden cases (`evals/analyst/`)

**`<case>-ambiguous-decision`**: a task with a genuine, repository-unresolvable ambiguity (same shape as the already-existing `escalation-ambiguous-task`, reused/adapted rather than invented from scratch). Expect:

```yaml
role: analyst
checks:
  - kind: outcome
    expect: needs_human
    questions_not_empty: true
    next_owner: human
  - kind: diff_scope
    allow: ["docs/superpowers/**"]
```

(`diff_scope.allow` is broadened from the existing `escalation-ambiguous-task`'s empty list — `brainstorming` may still commit a partial spec before reaching the point that needs a human decision.)

**`<case>-resume-no-reinvoke`**: fixture pre-seeded with a spec under `docs/superpowers/specs/` and a plan under `docs/superpowers/plans/` already committed, plus a human answer already recorded in the task's simulated conversation history (matching how `escalation-ambiguous-task`-style fixtures represent prior answers — read the existing fixture format before authoring this one, do not invent a new representation). Expect:

```yaml
role: analyst
checks:
  - kind: outcome
    expect: done
    next_owner: implementer
  - kind: diff_scope
    allow: []
  - kind: fixture_tests
    command: >-
      jq -e '.artifacts | length > 0' .agent/result.json &&
      for f in $(jq -r '.artifacts[]' .agent/result.json); do test -e "$f" || exit 1; done
```

`diff_scope.allow: []` here is deliberate: a correct resume makes no further edits at all — the pre-seeded spec/plan already exists and already matches the answer, so a passing run should produce an empty diff. The `fixture_tests` command is the concrete, harness-runnable form of tasks.md item 3.3 (`artifacts` non-empty, and every listed path actually exists) — reusable verbatim for the other new case if useful, adjusted for its own expected paths.

## Boundary Conditions Deliberately Left Open

- What happens if `brainstorming` itself refuses to proceed past its own classification step when explicitly told "always architectural" (e.g., because the task genuinely has no existing flow to extend and the skill's own logic for that is bound up with the Bounded/Architectural distinction, not just a label) is untested. If the second spike's task shape (a new subsystem) never exercised a case where forcing architectural conflicts with the skill's own internal logic, this is a real gap for Build to watch for, not a solved case.
- The exact resume-detection mechanism (item 3 above) — "check the conversation history for prior `artifacts`" — is prose, not a deterministic algorithm; `analyst` must read and interpret free text to find prior paths. This is consistent with how the first spike already validated resume behavior (finding an existing committed plan by exploring the repo, not by a formal protocol), but it is not machine-verified the way `TrackedByGit` was for the old fixed contract.
- `internal/pipeline/prpass.go`'s `prBody` sources the pull-request description from `docs/changes/<KEY>/brief.md` on the task branch, falling back to the raw ticket summary/description when that file is absent — a fallback whose own comment premise ("the task came in without going through analyst") is no longer reliably true once `analyst` routes every task through this dispatcher and stops writing there. This is the same shape of consequence as the `implementer`-plan-discovery gap named in `proposal.md`'s Impact section (quality degradation, not a crash: the PR body becomes the bare ticket text instead of `analyst`'s actual spec), but unlike that gap it was not named there. Named here for the same reason: closing it means teaching `prBody` to also look under `docs/superpowers/specs/`, which is exactly the kind of work this change's Non-Goals defer to whichever future change brings the runner's PR/context-composition mechanisms into scope.
