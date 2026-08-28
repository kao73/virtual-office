## Context

See `proposal.md` for motivation. The `skills:` mount mechanism (`role.yaml` → adapter copies named `skills/<name>/` directories into the sandbox as Claude Code plugins) has existed and been verified working since stage 1, but sat unused — every role ships `skills: []` today. A live spike (`experiment/skill-question-spike`) exercised it end to end with Superpowers `brainstorming` on `analyst` and produced the findings this design builds on (see `proposal.md` — Why).

## Goals / Non-Goals

**Goals:**
- Make the dormant `skills:` mechanism produce real value for `analyst`, safely, in a way that degrades correctly under headless execution.
- Keep the change's blast radius to `analyst` and its own artifact contract; leave `implementer`'s tool/permission surface untouched.

**Non-Goals:**
- Reworking `implementer` or `reviewer` (tracked as future work, together with `systematic-debugging` for `implementer`).
- Enabling subagent dispatch for any role. Two facts are unverified and security-relevant enough (especially for `reviewer`, which has no broad `Write` by design and by test) to need their own spike first: whether a dispatched subagent inherits the parent role's tool/permission restrictions, and whether its cost rolls into the same `run.log` the adapter reads for `Usage`/budgets.
- Making `docs/changes` (`internal/runner/change.go`, `ChangesDir`) configurable per project. Real, independent gap — no `changes_dir` field exists in `projects.yaml` today — but developed on its own branch off `master`, not as part of this change; this branch rebases onto the result.

## Decisions

**Vendor a pinned copy, not a live reference.** `skills/brainstorming/` is a full copy of Superpowers v6.3.0 (commit `b36e0829c6d0140e93cfef2ca599b1b07d4a7797`), with source recorded in a plain file for humans to read. *Alternative considered:* reference the operator's global plugin cache directly — rejected, the adapter only mounts from the repo's own `skills/`, and a live reference would drift silently whenever the operator's machine updates the plugin. *Alternative considered:* a machine-checked `.source.yaml` with drift detection — rejected per explicit owner decision: nothing consumes it yet, and building the check now is speculative.

**Dispatcher always invokes the skill.** `role.md` instructs `analyst` to call `Skill: office-role-analyst:brainstorming` at the start of every task, not only when ambiguity is already suspected. *Alternative considered:* invoke only when `analyst` would already reach for `needs_human` on its own — rejected by explicit owner choice; the owner wants broader use now and expects to revisit the threshold later against a systematic study of their own usage history (out of scope here).

**Resume-skip is written down, not left emergent.** The spike observed `analyst` naturally skip re-invoking the skill on resume, because the committed plan already answered the question — but one successful run is an observation, not a contract. `role.md` now states the rule explicitly. *Alternative considered:* rely on the emergent behavior — rejected as exactly the kind of thing this project has learned (repeatedly, per stage retros) not to leave to chance.

**Brainstorming's scope is capped to its thinking process.** The dispatcher tells `analyst` to use only classification, clarifying questions, and approach comparison — the outcome always lands in the existing `brief.md`/`design.md`/`tasks.md`, never in `docs/superpowers/specs/`, and `writing-plans` is never invoked as a hand-off. *Alternative considered:* mount `writing-plans` too, for a richer plan — rejected: its plan header hard-requires declaring `subagent-driven-development` or `executing-plans` as a "REQUIRED SUB-SKILL", neither available to `implementer`; its "Execution Handoff" step asks an interactive "which approach?" question nothing in this pipeline can answer; it defaults to saving plans under `docs/superpowers/plans/`, a path `implementer` never reads. *Alternative considered:* mount the entire `superpowers` plugin — rejected: most of it assumes subagent dispatch (`Agent`/`Task` is not in any role's `--tools`) or git branch operations (denied to every role by `defaults.tools.deny`), and a larger vendored surface makes every future version bump more expensive to review.

**The richer plan format is a template change, not a relocation.** `roles/analyst/templates/tasks.md` gains `writing-plans`-style per-task detail (exact files, produced/consumed interfaces, embedded test code, no placeholders), written to the exact same `docs/changes/<KEY>/tasks.md` `implementer` already reads. *Alternative considered:* adopt `writing-plans`'s own save location now, and update `implementer` to match in the same change — rejected: this repo's live EXP pilot runs against `analyst` today, and relocating the plan while explicitly not touching `implementer` (an already-settled decision) would leave `implementer` unable to see any plan at all until a future change lands, a real regression on a real client's project, not a hypothetical one.

**Plan discovery drops the runner-computed pointer.** The `context.md` line naming the plan's exact path is removed; `implementer`'s `role.md` instead says to check the already-provided change directory itself for a committed plan. *Alternative considered:* keep the computed line, or make its target filename configurable (`write_scope.plan_file` or an `artifacts` map in `role.yaml`) — considered seriously across several rounds, ultimately set aside: nothing in this change actually needs the plan to move or be renamed, so solving "what if it moves" now is solving a requirement that does not yet exist. Dropping the line entirely is simpler and asks `implementer` to do something (check a directory it is already pointed at) it is fully equipped to do itself.

**`write_scope` and `change_dir_only` are unchanged — considered for removal, rejected.** Two escalating proposals during design discussion — loosen the write boundary, then remove it entirely and rely on `role.md` prose — were both rejected on the same measured basis: this repository has already found and documented (`docs/notes/followup-network-and-permissions.md`, "Находка 2") that `tools.allow` does not gate `Bash`, meaning `analyst`'s `Bash(*)` could write outside its intended directory (`echo`/`sed` past the `Edit` tool's path scope) with nothing but `change_dir_only`'s post-run git-diff check standing in the way. `analyst`'s own defining constraint — "does not write code" — has no other enforcement mechanism. Removing it would leave that constraint aspirational rather than structural, with a real cost: `reviewer`'s independent review depends on `analyst`'s plan and `implementer`'s code being genuinely separate contributions.

## Risks / Trade-offs

- **[Risk]** Invoking the skill on every task, including trivial ones, adds cost/turns where it may not be needed → **Mitigation**: none applied here; explicitly accepted by the owner as a deliberate choice, to be revisited later against real usage data, not part of this change's scope.
- **[Risk]** Rewording `implementer`'s plan-discovery text could regress a scenario the live EXP pilot depends on → **Mitigation**: existing `implementer` golden cases in `eval-roles` must still pass unmodified; verified as part of this change's tasks before merge.
- **[Risk]** The skill's own reasoning could, on some future task, still attempt something the spike never exercised (e.g., a sequence of one-at-a-time questions, each becoming a separate `Blocked` round-trip) → **Mitigation**: none built now; named explicitly as untested in `proposal.md`'s non-goals rather than silently assumed safe.

## Migration Plan

No data migration. The change takes effect the next time the office config is deployed/pulled onto a machine running `analyst`. Rollback is reverting `roles/analyst/role.yaml`'s `skills:` list to `[]` plus the accompanying `role.md`/template text — immediate and cheap by construction, since nothing about the change alters stored state.

## Open Questions

- The "always invoke" threshold in the dispatcher is a deliberate first cut, not a settled position — the owner intends to revisit it later against a systematic look at their own history of working with Claude to derive better default rules for `analyst`. This can change later without touching this change's spec, approach, or task breakdown: it is a threshold inside one dispatcher instruction, not a structural decision.
