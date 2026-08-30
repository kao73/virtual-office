# Comet Design Handoff

- Change: role-comet-native-workflow
- Phase: design
- Mode: compact
- Context hash: 4c79157f1cf4713149ffecb46d8099aecaaebe9a02ae265280ad79dbf3b04756

Generated-by: comet-handoff.sh

OpenSpec remains the canonical capability spec. This handoff is a deterministic, source-traceable context pack, not an agent-authored summary.

## docs/openspec/changes/role-comet-native-workflow/proposal.md

- Source: docs/openspec/changes/role-comet-native-workflow/proposal.md
- Lines: 1-66
- SHA256: 1ad85897f3351dbc758b421050c11ced70a62c71ec758da4269ba2463551799a

```md
## Why

`analyst`, `implementer`, and `reviewer` currently plan, build, and review a task through three
unrelated, ad hoc mechanisms: `analyst` runs vendored Superpowers skills (`brainstorming` +
`writing-plans`) that save a spec/plan wherever their own logic picks; `implementer` follows a
free-form `tasks.md`/`design.md` the analyst wrote, with no machine-checked structure; `reviewer`
re-derives what to check by reading the diff. None of the three share a resumable, file-based state
machine, and the client project accumulates no growing, always-current specification of its own
behavior — each task's planning artifact is a one-off file nobody merges anywhere.

Two live headless experiments (documented in this change's design) showed Comet Native — the same
`comet` CLI already used to develop this repository itself, in its `native` (not `classic`) mode —
resolves this cleanly: a shared, git-resumable four-phase state machine (`Shape → Build → Verify →
Archive`) that already maps almost one-to-one onto our three roles, degrades gracefully with no
interactive human present, and merges a per-capability spec into the client project's own
always-current `specs/` tree on Archive.

## What Changes

- `analyst` drives Comet Native's **Shape** phase instead of `brainstorming`/`writing-plans`,
  producing `docs/comet/changes/<name>/{brief.md, specs/<capability>/spec.md}` in the client
  project instead of choosing its own spec/plan location.
- `implementer` drives **Build**: reads the brief and target spec instead of `tasks.md`/`design.md`,
  keeps its existing "one step, one commit" discipline layered on top, and submits a Builder handoff
  to advance to Verify.
- `reviewer` drives **Verify**: dispatches the Runtime-mandated read-only Verifier role against the
  acceptance items instead of freely re-deriving what to check from the diff.
- **Archive becomes a deterministic runner step**, not a fourth role: after the human merges the
  role's PR, the runner (not an agent) runs `comet native archive --confirmed --finish keep`,
  syncing the delta capability spec into the client project's canonical `specs/`.
- `internal/adapters/claude/adapter.go` gains a second hook type (`PreToolUse`, alongside the
  existing `Stop` hook) so Comet's own guard hook (`comet-hook-router.mjs`) can technically enforce
  phase-scoped write permissions — turning "analyst does not write code" / "reviewer does not edit"
  from role.md convention into a hook-enforced boundary, verified directly against a synthetic
  PreToolUse payload during this change's design work.
- `skills/comet/` and `skills/comet-native/` are vendored into the repo (same unmodified-copy
  pattern as `skills/brainstorming` and `skills/writing-plans`) and mounted on all three roles via
  `role.yaml`'s `skills:` field.
- `docs/changes/<KEY>/` retires for tasks going through these three roles, replaced by
  `docs/comet/changes/<name>/`.

## Capabilities

### New Capabilities
- `role-native-workflow`: the shared Comet Native change that `analyst`/`implementer`/`reviewer`
  drive across Shape/Build/Verify, the phase-to-role mapping, the PreToolUse guard-hook enforcement,
  and the deterministic runner-side Archive step.

### Modified Capabilities
(none — `role-external-skills`'s existing requirements already generically cover a role using a
mounted external skill, including Comet's own Shape-phase confirmation gate; `role-sandbox-permissions`
governs a different, orthogonal mechanism (`tools`/`network` layering, not hooks) and is unaffected.)

## Impact

- Code: `internal/adapters/claude/adapter.go` (hook plumbing), a new deterministic archive step in
  the runner (exact call site TBD in design — before/after the existing PR-merge detection).
- Roles: `roles/analyst/{role.yaml,role.md}`, `roles/implementer/{role.yaml,role.md}`,
  `roles/reviewer/{role.yaml,role.md}`.
- New vendored assets: `skills/comet/`, `skills/comet-native/`.
- Docs/contracts: `docs/contracts/agent-io.md` (artifact location note).
- External dependency: the `comet` npm CLI (Node 22+) must become available inside the role
  sandbox (`sbx`/`local` backends) — bootstrap ownership is a design-time open question, not yet
  confirmed to be inside this repo's control.
- Out of scope: Comet Classic/OpenSpec for the role pipeline (stays reserved for this repo's own
  meta-development), the runner↔agent contract shape, `workflow.yaml`, a fourth "archivist" role.

```

## docs/openspec/changes/role-comet-native-workflow/design.md

- Source: docs/openspec/changes/role-comet-native-workflow/design.md
- Lines: 1-82
- SHA256: 4f80f44c04d810f331d5f4d8bc49fd290b02a6fb6fba90cf1fd6c46575429adc

[TRUNCATED]

```md
## Context

See `proposal.md` - Why. Two live headless experiments (documented in the follow-up Superpowers
Design Doc from the Design phase) established the technical facts this design relies on:

- Comet Native (not Classic) is the workflow that fits a headless, no-interactive-human role run:
  it degraded gracefully with zero `AskUserQuestion` attempts across both experiments, and on an
  ambiguous, irreversible-looking task it defused the ambiguity into a safe default (dry-run,
  opt-in destructive flag) rather than blocking or guessing.
- Comet's `--confirmed` phase-advance flags are CLI-trust-based (no independent verification a
  human actually answered), but the `PreToolUse` guard hook (`comet-hook-router.mjs`) does
  technically block an `Edit` on an existing file outside the `build` phase (verified directly with
  a synthetic payload; a `Write` of a brand-new file was not blocked the same way).
- `comet native archive --confirmed --finish keep|merge|push|pull-request` is documented and
  behaves as a standalone, deterministic command - no agent needed.
- `comet init --platform claude --workflow native --scope project --yes` produces a minimal
  footprint (`.claude/{rules,skills,settings.local.json}`, `.comet/`, `docs/comet/`); the wide
  multi-platform footprint seen in early exploration came from the unscoped `comet workflow
  resolve --activate` entry point, not from Comet Native itself.

## Goals / Non-Goals

**Goals:**
- One shared, resumable state machine across all three roles for one task.
- Archive stays deterministic runner code (consistent with the existing PR-merge handling
  described in `docs/DESIGN.md` §2.8 - "the route lives in the graph/code, not the agent").
- Technical (not just textual) enforcement of "analyst does not write code" / "reviewer does not
  edit", reusing Comet's own guard hook.

**Non-Goals:**
- Building or customizing the `sbx` sandbox image in this change - Comet CLI availability inside
  that backend is an open dependency question for the Design phase, not solved here.
- Changing `internal/adapters/claude/adapter.go`'s existing `Stop`-hook mechanism - this design
  only adds a second, independent hook type alongside it.
- Any change to how `implementer` commits (kept: one plan step, one commit).

## Decisions

- **Comet Native, not Classic, for the role pipeline.** Classic's decision-point protocol assumes
  a live chat turn (`AskUserQuestion` or "ask and wait for the reply in the conversation") with no
  headless fallback; Native's Shape confirmation is a CLI flag an agent can pass on its own
  judgment, and both experiments showed that judgment lands close to what the office already wants
  (self-resolve the trivial, defuse the risky, never fabricate-and-proceed on something genuinely
  irreversible). Classic remains this repository's own meta-development workflow only.
- **Archive is runner Go code, not a fourth role.** `comet native archive` is documented as
  deterministic; adding a role/agent invocation to call one deterministic CLI command would cost a
  full run for no judgment it needs to make, and would contradict the existing "route lives in the
  graph/code" principle already applied to PR handling.
- **One change, one design/plan, not phased by role.** `analyst` switching output format from
  `tasks.md`/`design.md` to `brief.md`/`spec.md` is a breaking change to `implementer`'s input
  regardless of whether `implementer` itself adopts Native's CLI-driven Build mechanics - the two
  cannot be rolled out independently by role boundary.
- **Vendor `comet` + `comet-native` the same way as `brainstorming`/`writing-plans`** (unmodified
  copy into `skills/`, mounted via `role.yaml`'s `skills:` field) rather than depending on a
  machine-global Comet installation reaching into the sandbox some other way.
- **Add a second hook type (`PreToolUse`) to the adapter rather than inferring it from the skills
  list.** Matches the existing pattern where `role.yaml` declares hooks explicitly (`hooks.stop`)
  instead of the adapter guessing intent from which skills are mounted.

## Risks / Trade-offs

- [Comet Native's Shape self-confirmation is emergent model judgment, not an audited feature] →
  Mitigation: keep the office's own `needs_human` outcome as the actual escalation path (Comet's
  `[blocking]` marker in `brief.md` maps onto a typed question, per `role-external-skills`'s
  existing "Human-approval fallback" requirement); do not rely on Comet's own gate as the safety
  boundary.
- [`comet` CLI is a beta package (`0.4.0-beta.18`) with at least one confirmed bug in this exact
  version - `comet classic workspace prepare/resolve` fails unconditionally] → Mitigation: pin the
  exact version vendored/installed, and do not depend on `workspace prepare/resolve` anywhere in
  the role pipeline design (this change's own Open phase already worked around it via
  `state init --isolation branch`, unrelated to the role pipeline itself but evidence the CLI has
  rough edges to route around, not trust blindly).
- [Verifier check-dispatch JSON schema is not yet fully understood - a live experiment hit `Native
  Runtime check 0 fields are invalid` and ran out of turn budget before resolving it] → Mitigation:
  resolve the exact schema during the Design phase's technical investigation, before writing
  `reviewer`'s role.md instructions.

## Open Questions

- Where/how the `sbx` sandbox image is customized to add Node + `comet` (or whether that is outside

```

Full source: docs/openspec/changes/role-comet-native-workflow/design.md

## docs/openspec/changes/role-comet-native-workflow/tasks.md

- Source: docs/openspec/changes/role-comet-native-workflow/tasks.md
- Lines: 1-32
- SHA256: c06504cc504b625d9cdebec098d62ff77b4e194a79ba875ff5aa1e5dee4ee64a

```md
## 1. Dependency and bootstrap investigation

- [ ] 1.1 Determine where/how the `sbx` sandbox image is customized (or confirm it is outside this repo's control) and how Node 22+ / `comet` would reach it
- [ ] 1.2 Confirm the exact `--runner-input dispatch-verifier` check schema Comet Native's Build→Verify handoff expects (blocked on `Native Runtime check 0 fields are invalid` during design-time experiments)
- [ ] 1.3 Decide the archive `--finish` mode and its call site relative to the existing PR-merge detection in the runner

## 2. Adapter changes

- [ ] 2.1 Add `PreToolUse` hook support to `internal/adapters/claude/adapter.go`'s `buildSettings`, alongside the existing `Stop` hook
- [ ] 2.2 Wire the vendored `comet-hook-router.mjs` path (from the mounted `comet` skill's plugin dir) into the generated settings for roles that declare it

## 3. Vendor Comet skills

- [ ] 3.1 Vendor `skills/comet/` and `skills/comet-native/` (unmodified copies, `.source.yaml` pin, same pattern as `skills/brainstorming`/`skills/writing-plans`)

## 4. Role updates

- [ ] 4.1 `roles/analyst`: mount `comet`/`comet-native`, rewrite `role.md`'s skill dispatcher for the Shape phase, drop `brainstorming`/`writing-plans`
- [ ] 4.2 `roles/implementer`: mount `comet`/`comet-native`, rewrite `role.md` to read `brief.md`/`spec.md` instead of `tasks.md`/`design.md`, keep one-step-one-commit discipline
- [ ] 4.3 `roles/reviewer`: mount `comet`/`comet-native`, rewrite `role.md` to dispatch the mandated read-only Verifier and mark acceptance items

## 5. Runner archive step

- [ ] 5.1 Implement the deterministic post-merge `comet native archive --confirmed --finish keep` call in the runner

## 6. Contracts and docs

- [ ] 6.1 Update `docs/contracts/agent-io.md` to note `docs/comet/changes/<name>/` replacing `docs/changes/<KEY>/` for these three roles

## 7. Verification

- [ ] 7.1 Extend `evals/` with golden cases exercising the new Shape/Build/Verify flow through `cmd/eval-roles`

```

## docs/openspec/changes/role-comet-native-workflow/specs/role-native-workflow/spec.md

- Source: docs/openspec/changes/role-comet-native-workflow/specs/role-native-workflow/spec.md
- Lines: 1-52
- SHA256: 62dfea7d3bcb020836f48273d49367f7ddc7484e80fa9a47d92cc75d064940a0

```md
## Purpose
Gives `analyst`, `implementer`, and `reviewer` one shared, git-resumable Comet Native change
(Shape/Build/Verify/Archive) to drive a task through, in place of each role's previously unrelated
planning/build/review mechanism, with Archive left to deterministic runner code rather than a role.

## ADDED Requirements

### Requirement: Phase-to-role boundary
Each role SHALL drive exactly the Comet Native phase(s) assigned to it and SHALL end its run at
that phase's boundary without performing the work of another role's phase.

#### Scenario: analyst stops at Shape confirmation
- **WHEN** `analyst` confirms Shape for a task's Comet Native change
- **THEN** the run ends with `outcome: done` and `next_owner: implementer` without writing any
  implementation code

#### Scenario: implementer does not redo Shape
- **WHEN** `implementer` picks up a task whose Comet Native change is already in the `build` phase
- **THEN** `implementer` reads the existing brief and target spec as given and does not reopen or
  re-litigate Shape decisions already recorded there

### Requirement: Archive is deterministic runner code, not a role
The system SHALL execute Comet Native's Archive step (`comet native archive --confirmed --finish
keep`) as part of the runner's own deterministic post-merge processing, without dispatching any
role or agent to perform it.

#### Scenario: Archive runs after human PR merge with no agent invocation
- **WHEN** the runner detects that a task's pull request has been merged
- **THEN** the runner itself calls `comet native archive` for that task's change, and no role's
  `.agent/task.md` is ever generated for the purpose of archiving

### Requirement: Cross-run resumability through git-committed state
A role resuming a task already in progress SHALL continue the same Comet Native change from its
committed `comet-state.yaml` rather than re-deriving progress from the working tree or starting
over.

#### Scenario: Truncated Build run resumes from the same change
- **WHEN** `implementer`'s previous run on a task was truncated mid-Build without submitting a
  Builder handoff
- **THEN** the next `implementer` run reads the same `docs/comet/changes/<name>/comet-state.yaml`,
  continues the same change, and does not create a second Native change for the task

### Requirement: Phase-scoped writes are hook-enforced, not convention-only
When a role's session attempts to edit an existing implementation file while its Comet Native
change is not in the `build` phase, Comet's guard hook (wired as a `PreToolUse` hook by the
adapter) SHALL block the edit, independent of what the role's own `role.md` instructions say.

#### Scenario: An edit attempt outside Build is rejected
- **WHEN** a role's session issues an `Edit` tool call against a tracked implementation file while
  the session's bound Comet Native change is in `shape`, `verify`, or `archive` phase
- **THEN** the `PreToolUse` hook denies the tool call with a non-zero exit and a message naming the
  current phase, before the file is modified

```
