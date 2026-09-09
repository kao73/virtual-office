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

## Implementation Divergence

Two decisions were made during Build's final whole-branch review — after
this document and the delta spec were written — that this document didn't
originally anticipate. Both are additive safety hardening, not scope
changes to the Goals/Non-Goals above; recorded here per `/comet-verify`'s
spec-drift handling rather than reopening Design.

**Bounded retry on the widened staleness trigger, not only on merge refusal.**
The "Bounded, not unbounded, retry on merge refusal" decision above covers
`max_merge_refusals`, but the *other* new return path — the widened
`BaseAdvanced` staleness trigger (`prConflict`, applies to every project) —
shipped with no bound at all: a busy base could in principle return a task
to the implementer indefinitely, with no counter and no path to a human,
unlike every other recurring non-agent failure in this codebase. Worse, the
existing `max_merge_refusals` counter alone could be defeated by alternating
`merge-refused`/`merge-conflict` markers, each resetting the other's streak.

Fixed with a new `limits.max_pr_returns`, counted by
`tracker.PRReturns(comments, role)` — one `eventStreak` over **both**
`event:merge-conflict` and `event:merge-refused` together, mirroring how
`IdleRuns` already counts two event kinds as one counter for the same
reason: the consequence is the same (the task isn't making forward
progress) and two separate counters would let alternation dodge both.
Escalates via a new `event:pr-returns-exhausted`, kept outside `prEvents`
for the same reason `event:merge-refusals-exhausted` already is — reusing
`event:pr-closed` would corrupt `advancePR`'s open-vs-follow routing for a
PR that isn't actually closed.

**`attemptMerge` verifies the PR URL's repository before merging.**
Not previously decided at all: `Forge.Merge(url)` trusts whatever
repository the URL names, and the URL is read from a ticket comment —
before this change that trust only bought a read (`PRState`); this change
makes it buy an unattended external write. `attemptMerge` now compares the
URL's owner/repo against the project's configured `repo_url`
(`forge.SameRepo`) before calling `Merge`; a mismatch is logged and the
task is left in place, spending no counter. Host is not part of the
comparison (only owner/name), which is an accepted, documented limitation
given this office speaks to a single forge with a single token.

Both are implemented, tested (`TestPRPassStaleReturnsEscalateAtLimit`,
`TestPRPassAlternatingConflictAndRefusalEscalates`,
`TestPRPassAutoMergeRefusesForeignRepo`), and live-verified as part of the
same Task 7 live runs as the rest of this change (they shipped before the
live runs, in the same branch). The delta spec above was not amended with
matching requirements — both are implementation-level safety properties of
the mechanisms the delta spec already describes (the staleness-return
requirement and the merge-attempt requirement), not new user-observable
capabilities in their own right, so no new `### Requirement:` was added.

## pr-converge Findings (PR #9, Round 2)

Found by external review after the branch was already open as PR #9, on top
of everything above. Recorded here for the same reason as the section
above — additive hardening or an explicit accept-and-document decision, not
a scope change.

**GitHub's merge-refusal response doesn't distinguish "not yet" from
"never."** `GitHub.Merge` reads `mergeable_state` before attempting a
merge (a fix already covered by the "Bounded, not unbounded, retry on merge
refusal" decision's *intent*, but not its original *implementation*): the
REST API answers a required-check-still-running PR with the same bare 405 it
gives a permanently blocked one, and the first implementation of this read
counted `blocked`/`unstable`/`behind`/`unknown` as "not a refusal, don't
count it" without any bound at all — trading the original problem (false
escalation before CI finishes) for a new one (a permanently failed required
check, or a review that's never given, hangs forever with no counter and no
path to a human). Fixed with a second, separate, more patient limit,
`limits.max_merge_pending` (`tracker.MergePending`, `event:merge-pending` /
`event:merge-pending-exhausted`) — same shape as `max_merge_refusals`, just
tolerant enough to outlast ordinary CI.

**`SameRepo` verifies repository identity, not PR identity — accepted, not
hardened.** The paragraph above already documents that `attemptMerge`
checks the PR URL's owner/repo before merging, and that this rides on the
same "marker authenticity isn't checked" trust the whole tracker protocol
already accepts. External review sharpened the point: with `auto_merge`,
that acceptance stops being about internal task-state effects (a spoofed
`pr-opened` marker could already mislead `followPR`'s read of `PRState`) and
starts being about an actual privileged GitHub write — the office's token
merging a different, already-mergeable PR in the same repository, if a
ticket comment names one. Owner's call: document it as an explicitly
accepted gap next to `SameRepo` itself (`internal/forge/forge.go`), not
close it in this cycle. Closing it for real would mean verifying the PR's
head ref against `project.Branch(task.Key)` via the forge, which needs a
new `Forge` method — a bigger change than this review round's scope.

**The widened `BaseAdvanced` trigger's cost, quantified against
`max_pr_returns`.** The "Widened staleness trigger" decision above already
accepts the cost of extra return-to-implementer cycles on every project.
What it didn't spell out: on a human-merge project, "PR open, waiting for a
human" is an expected, possibly hours-long state, not a stuck one — and
`max_pr_returns` counts `BaseAdvanced` returns the same way whether
`auto_merge` is on or not. A busy base can rack up three such returns well
within a human's normal response time, escalating to `Blocked` with
"PR-проход не сходится" wording that reads as a malfunction when nothing
actually is one. Owner's call: document as accepted (README.md,
`docs/contracts/tracker-protocol.md`), not special-case the counting by
`auto_merge.enabled` — keeping the one-code-path decision this section
already made, at the cost this section already named, just now with the
specific failure mode spelled out.
