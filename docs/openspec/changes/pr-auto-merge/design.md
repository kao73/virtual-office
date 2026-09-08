## Context

See `proposal.md` — Why, for the motivation. The relevant current-state
facts that shape the approach: `internal/pipeline/prpass.go`'s `PRPass` is
the office's existing role-less, deterministic pass over `Approved` tasks
(`docs/DESIGN.md` §2.1, §2.8); `project.DefaultBranch` (`internal/tracker/config.go`)
is today a single field overloaded across four call sites (task-branch
fork point, agent's diff base, merge-conflict check, PR target); `Forge`
(`internal/forge`) only opens PRs and polls their state, it never merges;
`internal/workspace/merge.go`'s `MergeCheck` only detects text conflicts,
not a base that has simply moved forward without one; and
`roles/implementer/role.md` already has a documented, human-free
merge-conflict-resolution procedure it runs today, just aimed at a
hardcoded "default branch".

Full technical design (exact function signatures, code sketches, event
naming) — `docs/superpowers/specs/2026-09-08-pr-auto-merge-design.md`. This
document stays at the level of the decisions and their rationale.

## Goals / Non-Goals

**Goals:**
- Let a project opt into unattended `Approved → merged` for its tasks,
  without adding a role or reintroducing judgment where the project's own
  philosophy (§2.1) already puts deterministic code.
- Let that same opt-in target an isolated branch instead of the
  repository's real default branch, so the office's own trust in its
  mechanical gate does not have to extend to a client's real production
  branch to be useful.
- Close the specific staleness gap this design surfaced during
  brainstorming: a base that moves forward without a text conflict is
  invisible to today's `MergeCheck`, so a clean-looking merge could still
  land code neither `implementer` nor `reviewer` actually saw.

**Non-Goals:**
- CI status checks or an external review bot as part of the merge gate —
  deliberately deferred a second time (first at `role-comet-native-workflow`
  Task 23); v1's gate is `Approved` plus base currency, nothing else.
- Automating promotion of an integration branch into the real default
  branch — stays a manual, human git operation outside the office.
- A delay or veto window between gate-pass and merge — rejected explicitly;
  once the gate holds, the office merges on the same tick it's next
  evaluated.
- Configurable merge method (squash/rebase) — hardcoded to a merge commit;
  no one asked for the alternative yet.
- Auto-creating a configured target branch that doesn't exist — the office
  fails loudly instead of inventing a long-lived branch a human didn't
  create.

## Decisions

**No new role — extend the existing role-less PR pass.**
Alternative considered: a reviewing/merging agent role that decides when to
merge. Rejected: the actual gate (`Approved` plus base currency) is fully
mechanical, and the project's own §2.1 principle is that the runner's code,
not a prompt, should own transition decisions whenever the decision really
is mechanical. An LLM role here would reintroduce non-determinism exactly
where none is needed.

**Auto-merge config lives in `projects.local.yaml`, not `projects.yaml`.**
Alternative considered: the portable, repo-committed `projects.yaml`, next
to `default_branch`. Rejected: whether a specific instance trusts a
specific real remote enough to merge into it automatically is the same
class of decision as `forge` itself (already machine-level) — not a
portable property of the office.

**Forge-mediated merge (`Forge.Merge()` via the GitHub API), PR stays open
as an audit trail.**
Alternative considered: skip the PR entirely for auto-merge projects and
push a locally computed merge straight to the target branch (uniform with
today's no-forge `prSkipped` path). Rejected: going through the forge keeps
a visible history on GitHub of what got auto-merged and when, and — for
free — lets a human's own branch-protection rules (required checks,
required reviews) block a merge our own v1 gate doesn't know to check,
without any code on our side needing to know about them.

**A new `PRBranch()`, not repurposing `DefaultBranch`.**
`DefaultBranch` is used at four call sites today (task-branch fork point,
agent's `BaseBranch` context, `MergeCheck`, `OpenPR`'s target). Overloading
it directly would silently break the dependency gate (`depends_on`,
`internal/pipeline/deps.go`): a dependent task's branch would keep forking
from the real default branch and never see its prerequisite's work sitting
in the isolated target branch. `PRBranch()` resolves to
`auto_merge.target_branch` when set, `DefaultBranch` otherwise, and replaces
`DefaultBranch` at exactly those four call sites.

**Widened staleness trigger (`BaseAdvanced`) applies to every project, not
only auto-merge ones.**
Alternative considered: gate the wider trigger behind
`auto_merge.enabled`, leaving human-merge projects exactly as they behave
today (only a text conflict returns a task to the implementer). Rejected
by explicit product decision during brainstorming, in favor of one uniform
code path: a human-merge project already benefits from a task being
retested against a genuinely current base before a human ever looks at it,
and the cost — more return-to-implementer cycles on a busy base branch — is
accepted knowingly, not a side effect.

**Bounded, not unbounded, retry on merge refusal.**
When the office's own checks say a merge is clean but the forge refuses it
anyway (for instance, a branch-protection rule the office doesn't inspect),
retrying forever would silently burn cycles without ever telling a human
the real blocker. `max_merge_refusals` mirrors the existing
`max_push_failures`/`max_lease_expiries` pattern (`workflow.yaml`) rather
than inventing a new kind of limit.

**Terminal status is unchanged (`Done`), wording only.**
Alternative considered: a distinct terminal status for "merged into an
integration branch, not yet in the real default branch". Rejected as
premature: the office's job over a task ends where the graph's `pr.merged`
already says it does; promoting the integration branch into the real
default branch is explicitly a human's separate, manual concern (see
Non-Goals), and a second terminal status would only be justified once the
office itself has something to do with that distinction — it doesn't yet.

## Risks / Trade-offs

- **[Risk]** A base that moves forward in a way `git merge-tree` calls
  clean can still be semantically wrong — no text conflict does not mean
  no regression, and nothing in v1 runs the project's own tests against
  the merged result before merging.
  → **Mitigation**: `BaseAdvanced` routes any base movement (conflicting
  or not) back through `implementer`'s existing merge-and-test procedure
  before the office will consider merging again, rather than trusting a
  clean `git merge-tree` alone. Full CI-on-the-merge-result remains future
  work, deliberately deferred (see Non-Goals).

- **[Risk]** Widening the staleness trigger to every project changes
  observable behavior for existing human-merge projects that didn't ask for
  auto-merge at all — more return-to-implementer cycles when their base
  branch is busy.
  → **Mitigation**: accepted deliberately for one uniform code path rather
  than two behaviors to maintain; the returned task is strictly safer
  (retested against a current base) than today's behavior, not merely
  different.

- **[Risk]** An auto-merge project's `target_branch`, being long-lived and
  advancing through automated merges, can silently accumulate a defect from
  one task that later tasks build on before a human ever looks at it.
  → **Mitigation**: contained by design — the blast radius stops at the
  isolated integration branch, never the repository's real default branch,
  and promoting it into that real branch stays an explicit human action
  (see Non-Goals). Not eliminated, only bounded.

- **[Risk]** A forge merge refusal the office's own checks don't explain
  (e.g. an undeclared branch-protection rule) could loop indefinitely
  without a human ever finding out why.
  → **Mitigation**: `max_merge_refusals` bounds it and escalates to a
  human, same pattern as `max_push_failures` today.

## Migration Plan

Additive and opt-in: `auto_merge` absent from a project's configuration
preserves its exact current behavior, with one exception applied uniformly
— the widened `BaseAdvanced` staleness trigger (see Decisions and Risks
above), which is a behavior change accepted by explicit product decision,
not gated behind the opt-in. No data migration; no existing task in flight
needs any special handling, since `Approved` tasks already sit in the exact
state this design starts reading from. Rollback is deleting a project's
`auto_merge` block — this is not implicit, `PRBranch()` falls straight back
to `DefaultBranch`.

## Open Questions

None — every deferred item above (CI/review-bot gating, integration→main
promotion automation, a veto window, configurable merge method, auto-created
target branches) was a decision made and recorded during brainstorming, not
a genuinely open unknown; see Non-Goals and
`docs/superpowers/specs/2026-09-08-pr-auto-merge-design.md` for the
reasoning behind each.
