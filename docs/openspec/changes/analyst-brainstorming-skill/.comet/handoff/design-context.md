# Comet Design Handoff

- Change: analyst-brainstorming-skill
- Phase: design
- Mode: compact
- Context hash: 583161a71f71272f17f4d2c4ff0af21a1646136616388c40e8aca7493aa0dd97

Generated-by: comet-handoff.sh

OpenSpec remains the canonical capability spec. This handoff is a deterministic, source-traceable context pack, not an agent-authored summary.

## docs/openspec/changes/analyst-brainstorming-skill/proposal.md

- Source: docs/openspec/changes/analyst-brainstorming-skill/proposal.md
- Lines: 1-38
- SHA256: 8c31c5b97e8a020cd7a001516a1922e6552626f750f51620b5b1ff09f5d732ae

```md
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

```

## docs/openspec/changes/analyst-brainstorming-skill/design.md

- Source: docs/openspec/changes/analyst-brainstorming-skill/design.md
- Lines: 1-43
- SHA256: 145d5d663417007054aae90459aa09820adbb38b6433d1a733194f7c3df75329

```md
## Context

See `proposal.md` for motivation. The `skills:` mount mechanism (`role.yaml` → adapter copies named `skills/<name>/` directories into the sandbox as Claude Code plugins) has existed and been verified working since stage 1, but sat unused — every role ships `skills: []` today. Two live spikes exercised it end to end with Superpowers `brainstorming` and `writing-plans` on `analyst` and produced the findings this design builds on (see `proposal.md` — Why).

## Goals / Non-Goals

**Goals:**
- Make the dormant `skills:` mechanism produce real value for `analyst`, safely, in a way that degrades correctly under headless execution.
- Let `brainstorming`/`writing-plans` run to their natural, unmodified conclusion — their own file locations, their own plan format — rather than reimplementing their conventions inside `role.md`.

**Non-Goals:**
- Reworking `implementer` or `reviewer`, or anything that exists only to serve `implementer` (including `internal/runner/input.go`'s context composition) — tracked as future work, together with `systematic-debugging` for `implementer`. This change is scoped to `analyst` alone, without regard to how `implementer` currently discovers a plan; see `proposal.md` — Impact for the accepted consequence.
- Enabling subagent dispatch, or the `subagent-driven-development`/`executing-plans` skills, for `analyst`. `writing-plans`'s own process names these at its "Execution Handoff" step, but `analyst` is instructed to never act on that step — see Decisions below. Whether any role should ever get subagent dispatch is a separate, unstarted question (two facts are unverified and security-relevant enough — especially for `reviewer` — to need their own spike first: whether a dispatched subagent inherits the parent role's tool/permission restrictions, and whether its cost rolls into the same `run.log` the adapter reads for `Usage`/budgets).
- Making `docs/changes` (`internal/runner/change.go`, `ChangesDir`) configurable per project — a real, independent gap, developed on its own branch off `master`, not part of this change; this branch rebases onto the result.

## Decisions

**Vendor pinned copies, not a live reference.** `skills/brainstorming/` and `skills/writing-plans/` are full copies of Superpowers v6.3.0 (commit `b36e0829c6d0140e93cfef2ca599b1b07d4a7797`), with source recorded in a plain file per skill for humans to read. *Alternative considered:* reference the operator's global plugin cache directly — rejected, the adapter only mounts from the repo's own `skills/`, and a live reference would drift silently whenever the operator's machine updates the plugin. *Alternative considered:* a machine-checked `.source.yaml` with drift detection — rejected per explicit owner decision: nothing consumes it yet, and building the check now is speculative.

**Dispatcher always invokes brainstorming.** `role.md` instructs `analyst` to call `Skill: office-role-analyst:brainstorming` at the start of every task, not only when ambiguity is already suspected. *Alternative considered:* invoke only when `analyst` would already reach for `needs_human` on its own — rejected by explicit owner choice; the owner wants broader use now and expects to revisit the threshold later against a systematic study of their own usage history (out of scope here).

**Both skills run to their real conclusion, including `writing-plans` — not capped to "thinking only."** An earlier draft of this design capped `brainstorming`'s use to its thinking process (classification, questions, approach comparison) and forced the outcome into the office's fixed `brief.md`/`design.md`/`tasks.md`, reasoning that `writing-plans` assumes capabilities `implementer` doesn't have: a chosen execution method, a mandatory plan-document header naming `subagent-driven-development`/`executing-plans`, and an interactive "which approach?" question at its end. A second spike (`experiment/writing-plans-handoff-spike`) showed these are navigable by explicit `role.md` instruction rather than being hard blockers: told plainly never to answer the Execution Handoff question or invoke either named sub-skill, `analyst` classified the task as architectural, ran `brainstorming` through to a written spec, invoked `writing-plans`, and stopped cleanly once the plan was saved — no hang, no attempt to call an unmounted skill, no fabricated answer. The owner's explicit decision, after seeing this: let both skills run for real, output landing wherever their own conventions put it, rather than continuing to reimplement their conventions as prose inside `role.md`. *Alternative considered:* keep the cap, absorb `writing-plans`'s per-task format (files, interfaces, embedded tests) as text in `role.md`'s own `tasks.md` description instead of invoking the skill — rejected once the second spike showed the real skill can be invoked safely; duplicating its conventions in prose would only drift from the vendored source on the next version bump.

**Output location follows the skills' own conventions; `result.json`'s `artifacts` field is the only link back to the task.** `brainstorming` saves an architectural spec to `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md`; `writing-plans` saves its plan to `docs/superpowers/plans/YYYY-MM-DD-<feature-name>.md`. Neither name is keyed by the tracker's task key, unlike `docs/changes/<KEY>/`. The spike's own `result.json` already populated `artifacts` correctly with real paths, without being told to do so for this specific case — the base result-schema prompt (`ResultSpec`, injected into every role) already asks for it in general. `role.md` now makes this explicit rather than relying on that emergent behavior: always list every file actually created or modified, wherever it lives. A later change can use this to teach `implementer` to find a plan (via `internal/tracker/report.go`'s "## Артефакты" section, already carried into the next run's context as "## Переписка в тикете") — but building that reader is out of scope here; see Non-Goals. *Alternative considered:* keep forcing output into `docs/changes/<KEY>/` regardless of what the skills would naturally do — this is in fact what the spike produced without being asked to do otherwise, and would have kept today's `implementer` fully working with zero further change. Rejected by explicit owner decision: this change is scoped to `analyst` "без оглядки на implementer" — implementer's ability to find the result is explicitly not a constraint on this design.

**`writing-plans`'s "Execution Handoff" is neutralized by instruction, never by editing the skill or its output.** The vendored `SKILL.md` files are never modified — editing them would fight every future version bump. Instead `role.md` tells `analyst` directly: this office has exactly one execution method (`implementer`, one task per commit, no subagent dispatch), so the question has one answer regardless of who's asked, and asking a human via `needs_human` would waste a full round-trip on a foreordained answer, every single task. `analyst` does not answer the question, does not invoke `subagent-driven-development` or `executing-plans`, and does not strip the plan document's "REQUIRED SUB-SKILL" header — the file is committed as `writing-plans` produced it, unedited. *Alternative considered:* have `analyst` clean the generated plan file's header before committing — rejected by explicit owner decision, again to avoid the role reaching into content a skill produced by its own convention; the header is inert text `implementer` never reads.

## Risks / Trade-offs

- **[Risk]** `implementer`, unchanged, cannot find a plan that `brainstorming`/`writing-plans` saved outside `docs/changes/<KEY>/` → **Mitigation**: none in this change, by deliberate scope decision. `implementer`'s existing "no plan in context — work as usual" fallback (already how it handles any task without a plan) means this degrades quality, not correctness — no crash, no wrong action, just a task worked from the bare description instead of the richer plan, until a follow-up change teaches `implementer` to read `artifacts` from the conversation history.
- **[Risk]** Invoking `brainstorming` on every task, including trivial ones, adds cost/turns where it may not be needed → **Mitigation**: none applied here; explicitly accepted by the owner as a deliberate choice, to be revisited later against real usage data, not part of this change's scope.
- **[Risk]** `brainstorming`'s own lightweight paths (Bounded, Spike) end without any committed artifact at all — by design, they assume a single continuous agent session that goes straight to implementation or reports a recommendation in chat. `analyst` never implements and has no live chat partner, so a task classified this way could produce nothing for anyone to find → **Mitigation**: none in this change. The owner's explicit, temporary resolution is to treat every task as architectural for now ("с остальными задачами разберемся позже") — how `analyst` should actually behave for genuinely Bounded/Spike-shaped tasks is deferred, named here so it is not silently forgotten.
- **[Risk]** The skill's own reasoning could, on some future task, still attempt something the spikes never exercised (e.g., a sequence of one-at-a-time clarifying questions, each becoming a separate `Blocked` round-trip) → **Mitigation**: none built now; named explicitly here rather than silently assumed safe.

## Migration Plan

No data migration. The change takes effect the next time the office config is deployed/pulled onto a machine running `analyst`. Rollback is reverting `roles/analyst/role.yaml`'s `skills:` list to `[]` plus the accompanying `role.md` text — immediate and cheap by construction, since nothing about the change alters stored state.

## Open Questions

- The "always invoke" threshold in the dispatcher is a deliberate first cut, not a settled position — the owner intends to revisit it later against a systematic look at their own history of working with Claude to derive better default rules for `analyst`.
- What `analyst` should actually do for a task `brainstorming` would classify as Bounded or Spike (rather than treating everything as architectural) is unresolved — named as a Risk above, not solved here.
- How `implementer` eventually reads `artifacts` from the conversation history to find a plan outside `docs/changes/<KEY>/` is unresolved — the mechanism exists (`internal/tracker/report.go`, already carried into context via "Переписка в тикете") but nothing consumes it yet; building that is the next change once `implementer` enters scope.

```

## docs/openspec/changes/analyst-brainstorming-skill/tasks.md

- Source: docs/openspec/changes/analyst-brainstorming-skill/tasks.md
- Lines: 1-25
- SHA256: 7feecefd214b8008696d27c900cbdf3dd0fe316f6b940af0b8e81e2247ecf36d

```md
## 1. Vendor the skills

- [ ] 1.1 Copy Superpowers `brainstorming` v6.3.0 (github.com/obra/superpowers, commit `b36e0829c6d0140e93cfef2ca599b1b07d4a7797`) into `skills/brainstorming/`, matching the copy already validated on `experiment/skill-question-spike`
- [ ] 1.2 Copy Superpowers `writing-plans`, same repo/tag/commit, into `skills/writing-plans/`, matching the copy already validated on `experiment/writing-plans-handoff-spike`
- [ ] 1.3 Add a plain source-record file per skill (e.g. `skills/brainstorming/SOURCE.md`, `skills/writing-plans/SOURCE.md`) naming the repo, tag, and commit — informational only, no automated check

## 2. Analyst role changes

- [ ] 2.1 `roles/analyst/role.yaml`: set `skills: [brainstorming, writing-plans]`
- [ ] 2.2 `roles/analyst/role.md`: add the dispatcher — always invoke `Skill: office-role-analyst:brainstorming` at the start of a new task
- [ ] 2.3 `roles/analyst/role.md`: add the resume-skip clause — do not re-invoke the skill when prior work (a committed spec/plan, wherever it was saved, or the role's own past `artifacts` visible in the tracker history) already reflects any human answer received
- [ ] 2.4 `roles/analyst/role.md`: remove the "Три файла в каталоге изменения" instruction as the mandatory output location — let `brainstorming`/`writing-plans` save wherever their own conventions call for
- [ ] 2.5 `roles/analyst/role.md`: add the artifacts rule — every file actually created or modified, wherever it lands, must be named in `result.json`'s `artifacts` field
- [ ] 2.6 `roles/analyst/role.md`: add the Execution Handoff rule — when `writing-plans` asks which execution approach to use, never answer and never invoke `subagent-driven-development` or `executing-plans`; the role's work ends once the plan is saved, unedited

## 3. Eval coverage

- [ ] 3.1 Add `evals/analyst/<case>-ambiguous-decision/`: a task with a genuine, repository-unresolvable ambiguity; expect `needs_human` with a correctly shaped `questions[]`
- [ ] 3.2 Add `evals/analyst/<case>-resume-no-reinvoke/`: a fixture with prior work already present (spec/plan committed wherever `brainstorming`/`writing-plans` would have put it) and a human answer already recorded; expect `done` without a second `Skill` invocation
- [ ] 3.3 Confirm `result.json`'s `artifacts` field names the real files in both new cases — this is now the only link between a freely-placed file and its task

## 4. Verification

- [ ] 4.1 Run the two new `analyst` golden cases from section 3; confirm they pass
- [ ] 4.2 Run `go test ./...`

```

## docs/openspec/changes/analyst-brainstorming-skill/specs/role-external-skills/spec.md

- Source: docs/openspec/changes/analyst-brainstorming-skill/specs/role-external-skills/spec.md
- Lines: 1-33
- SHA256: 11919edb742c9028d5043fbf743f292bf68e6aa586d8db57545d46ca639b3fbc

```md
## Purpose

Lets a headless office role draw on external, versioned skills' expertise while still terminating safely and deterministically when those skills' own instructions assume a live human participant, or a hand-off to a capability, the role does not have.

## ADDED Requirements

### Requirement: Human-approval fallback
When a role's mounted external skill reaches a point in its own instructions that requires live human approval or an interactive answer, and no interactive question tool is available to the run, the role SHALL end the run through the office's own `needs_human` outcome with a typed, answerable question, rather than stalling, erroring uncontrolled, or fabricating consent.

#### Scenario: Skill hits its own approval gate on a genuinely ambiguous task
- **WHEN** `analyst` runs a task whose correct handling depends on a decision the repository cannot resolve on its own, with the external skill invoked
- **THEN** the run ends with `outcome: needs_human` and a `questions[]` entry shaped as `{id, text, options[]}`, answerable by the existing tracker comment protocol

### Requirement: No redundant re-invocation on resume
When a role resumes a task after a human has answered a question a mounted skill's process raised, and prior work already reflects that answer, the role SHALL NOT invoke the skill's process a second time for the same decision.

#### Scenario: Resuming with already-satisfied prior work
- **WHEN** `analyst` runs again on a task whose spec/plan is already committed — wherever the skill's own conventions placed it — and matches the human's answer
- **THEN** the run completes with `outcome: done` and does not call the skill a second time

### Requirement: Every produced artifact is declared, regardless of location
A role using a mounted skill's own file-location conventions SHALL name every file it actually creates or modifies in `result.json`'s `artifacts` field, so that wherever a skill chooses to save its output, that output does not become untraceable to the task that produced it.

#### Scenario: Skill saves output to its own default location
- **WHEN** a mounted skill saves a spec or plan to a location the office's own fixed contract does not name (e.g. `docs/superpowers/specs/...`, `docs/superpowers/plans/...`)
- **THEN** `result.json`'s `artifacts` field lists that exact path

### Requirement: Hand-off steps the role cannot honor are neutralized by instruction, not by editing the skill
When a mounted skill's own process ends with a step that assumes a capability or a live participant the role does not have (an interactive execution choice, a required sub-skill the role isn't granted), the role SHALL follow an explicit dispatcher instruction that ends its run without acting on that step, and SHALL NOT modify the skill's own files or the content it produces to work around the step.

#### Scenario: A skill's terminal step names a capability the role doesn't have
- **WHEN** `writing-plans` reaches its "Execution Handoff" step, naming `subagent-driven-development` or `executing-plans`
- **THEN** `analyst` does not answer the question, does not invoke either named skill, and finishes its run with the plan already saved — unedited, including any header text the skill wrote

```
