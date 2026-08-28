## Why

The `analyst` role turns a task's description into a plan (`brief.md`/`design.md`/`tasks.md`) that `implementer` executes one commit at a time. The office already has a mechanism to mount external, versioned skills into a role's sandbox (`skills:` in `role.yaml`, adapter-mounted from the repo's own `skills/` directory), verified working since stage 1 — but no role has ever used it. Meanwhile the plans `analyst` writes today are loose checklists, not the detailed, developer-ready plans a strong human analyst would produce.

A live spike (branch `experiment/skill-question-spike`, not merged, three real sandboxed runs, ~$0.72) confirmed that mounting a real Superpowers skill (`brainstorming`) into `analyst` and calling it explicitly is safe for this headless, unattended execution model: when the skill's own instructions require a human's approval, the agent — with no interactive question tool available in headless mode (`AskUserQuestion` is absent from `--tools`) — resolves this by falling back to the office's own native `needs_human`/typed-question protocol, without hanging or fabricating consent. On a later run, after the human answers, the agent does not re-invoke the skill; it recognizes the already-committed plan as sufficient state and proceeds directly to `done`.

This change turns that spike into a supported capability: `analyst` gets `brainstorming` as a pinned, vendored dependency, with an explicit dispatcher telling it when to use it and how to reconcile the skill's own conventions with the office's fixed plan-file contract.

## What Changes

- Vendor Superpowers `brainstorming` v6.3.0 (github.com/obra/superpowers, commit `b36e0829c6d0140e93cfef2ca599b1b07d4a7797`) into `skills/brainstorming/` of this repository, with the source pinned in a plain, human-read record file (no automated drift check).
- `roles/analyst/role.yaml`: add `brainstorming` to `skills:` (currently `[]`).
- `roles/analyst/role.md`: add an explicit dispatcher instruction — always invoke `Skill: office-role-analyst:brainstorming` when starting a new task (auto-discovery by the skill's own description is unreliable, proven by the spike); explicitly skip re-invoking it when a plan is already committed and matches any human answer received; explicitly cap the skill's use to its thinking process (classification, clarifying questions, approach comparison) — the outcome is always written into the existing `brief.md`/`design.md`/`tasks.md` contract, never into `docs/superpowers/specs/`, and `writing-plans` is never invoked.
- `roles/analyst/templates/tasks.md` (and accompanying `role.md` guidance): enrich the per-task format with conventions borrowed from Superpowers `writing-plans` — exact files touched, what a task consumes/produces, embedded test code where useful, no placeholders — without adopting that skill's own execution-handoff or plan-location conventions, which assume capabilities (subagent dispatch, a chosen execution method) `implementer` does not have.
- `roles/implementer/role.md`: replace reliance on a runner-computed "plan file exists" context line with a one-line instruction to check the change directory itself for a committed plan. This removes one runner-side computation without touching any tool permission.
- `evals/analyst/`: add two golden cases — a genuinely ambiguous task that must end in `needs_human` with correctly shaped `questions[]`, and a resume case (plan already committed, human answered) that must end in `done` without a second `Skill` invocation.

## Capabilities

### New Capabilities
- `role-external-skills`: a role can be granted an external, versioned skill through the existing `skills:` mechanism, with an explicit in-prompt dispatcher governing when the skill is invoked and how its output is reconciled with the role's own fixed artifact contract, so that headless execution degrades safely when the skill's own instructions assume a live human participant.

### Modified Capabilities
(none — no existing spec's requirements change; `implementer`'s plan-discovery text changes, but its documented contract — "if the context names a plan, follow it" — does not)

## Impact

- `roles/analyst/role.yaml`, `roles/analyst/role.md`, `roles/analyst/templates/tasks.md` — analyst's skill list, dispatcher, and plan template.
- `roles/implementer/role.md` — plan-discovery wording only; no tool/permission change.
- `skills/brainstorming/` (new, vendored) — the skill's real, pinned content.
- `evals/analyst/<new-case>/` (new) — two golden cases.
- Explicitly **not** touched: `internal/runner/input.go`'s plan-path computation and the `write_scope`/`change_dir_only` guard (both were proposed for removal during design discussion and explicitly rejected — see `design.md`); `implementer`'s and `reviewer`'s tool/permission sets; any other Superpowers skill; subagent dispatch for any role; `projects.yaml`'s hardcoded `docs/changes` convention (tracked as a separate, independent branch to be rebased onto, not part of this change).
