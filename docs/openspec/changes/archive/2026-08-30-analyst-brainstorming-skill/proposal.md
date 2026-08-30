## Why

The `analyst` role turns a task's description into a plan that `implementer` executes one commit at a time. The office already has a mechanism to mount external, versioned skills into a role's sandbox (`skills:` in `role.yaml`, adapter-mounted from the repo's own `skills/` directory), verified working since stage 1 — but no role has ever used it. Meanwhile the plans `analyst` writes today are loose checklists, not the detailed, developer-ready plans a strong human analyst would produce.

Two live spikes confirmed this is workable for headless, unattended execution:

- `experiment/skill-question-spike` (three runs, ~$0.72): mounting Superpowers `brainstorming` and calling it explicitly is safe — when the skill's own instructions require a human's approval, the agent, with no interactive question tool available (`AskUserQuestion` is absent from `--tools`), falls back to the office's own native `needs_human`/typed-question protocol, without hanging or fabricating consent. On resume, after the human answers, the agent recognizes the already-committed work as sufficient state and does not re-invoke the skill.
- `experiment/writing-plans-handoff-spike` (one run, $0.43, 22 steps): mounting `writing-plans` alongside `brainstorming` and inviting the full architectural path — including `writing-plans`'s own mandatory "Execution Handoff" question, which asks which of two subagent-execution approaches to use — the agent, told explicitly never to answer it or invoke either named sub-skill, cleanly stopped after saving its plan. Neither skill was edited or worked around; both ran to their natural, unmodified conclusion.

This change turns both spikes into a supported capability: `analyst` gets `brainstorming` and `writing-plans` as pinned, vendored dependencies, with an explicit dispatcher governing how to invoke them and how to end a run once they are done.

## What Changes

- Vendor Superpowers `brainstorming` and `writing-plans`, both v6.3.0 (github.com/obra/superpowers, commit `b36e0829c6d0140e93cfef2ca599b1b07d4a7797`), into `skills/brainstorming/` and `skills/writing-plans/` of this repository, with the source pinned in a plain, human-read record file per skill (no automated drift check).
- `roles/analyst/role.yaml`: add `brainstorming` and `writing-plans` to `skills:` (currently `[]`).
- `roles/analyst/role.md`:
  - add an explicit dispatcher instruction — always invoke `Skill: office-role-analyst:brainstorming` when starting a new task (auto-discovery by the skill's own description is unreliable, proven by the first spike); explicitly skip re-invoking it when prior work (a committed spec/plan, wherever it was saved) already reflects any human answer received;
  - let `brainstorming`/`writing-plans` save their output wherever their own conventions call for (their own default paths), rather than redirecting it into the office's `docs/changes/<KEY>/` contract;
  - require every file the role actually creates or modifies, wherever it lands, to be named in `result.json`'s `artifacts` field — this is the only thing that ties a freely-placed file back to its task, so it cannot be left implicit;
  - add an explicit instruction for `writing-plans`'s mandatory "Execution Handoff" step: never answer it and never invoke either sub-skill it names (`subagent-driven-development`, `executing-plans`) — this office always executes plans through `implementer`, one task per commit, regardless of what a human might otherwise choose; the role's work ends once the plan is saved.
- `evals/analyst/`: add two golden cases — a genuinely ambiguous task that must end in `needs_human` with correctly shaped `questions[]`, and a resume case (relevant work already exists from a prior run, human answered) that must end in `done` without a second `Skill` invocation.

## Capabilities

### New Capabilities
- `role-external-skills`: a role can be granted external, versioned skills through the existing `skills:` mechanism, with an explicit in-prompt dispatcher governing when each skill is invoked and how the role concludes its run once a skill's own process finishes or reaches a step the role cannot honor (a live-human question, a hand-off to a capability the role doesn't have), so that headless execution degrades safely instead of hanging or improvising.

### Modified Capabilities
(none)

## Impact

- `roles/analyst/role.yaml`, `roles/analyst/role.md` — analyst's skill list and dispatcher.
- `skills/brainstorming/`, `skills/writing-plans/` (new, vendored) — both skills' real, pinned content.
- `evals/analyst/<new-case>/` (new) — two golden cases.
- Explicitly **not** touched, by deliberate scope decision, not as a side effect: `implementer`'s role and tool/permission set, `reviewer`'s role, `internal/runner/input.go`'s context composition (including the runner-computed "План: `<path>`" line), any other Superpowers skill, subagent dispatch for any role, `projects.yaml`'s hardcoded `docs/changes` convention (tracked as a separate, independent branch to be rebased onto, not part of this change).
- `write_scope`/`change_dir_only` no longer exist in the codebase at all, as of a separate, later change (`remove-role-guards`, merged before this branch's rebase, for reasons unrelated to this change). Analyst's write boundary — to the extent it still writes into `docs/changes/<KEY>/` for tasks that don't reach `brainstorming`'s architectural path — is a `role.md` convention only, already in place.
- Known, accepted consequence of not touching `implementer`: for a task where `analyst`'s plan lands via `brainstorming`/`writing-plans` outside `docs/changes/<KEY>/`, today's unchanged `implementer` will not find it and will fall back to its existing "no plan in context — work as usual" behavior (already how it handles any task without a plan). This is a quality gap, not a break — `implementer` has no fixed-path assumption to violate, and nothing crashes. Closing this gap (teaching `implementer` to read `artifacts` from the conversation history) is deferred to the change that brings `implementer` into scope.
