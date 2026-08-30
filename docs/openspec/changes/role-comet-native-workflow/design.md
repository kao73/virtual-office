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
  behaves as a standalone, deterministic command - no agent needed. **Correction (Build phase, Task
  5 review, 2026-08-31):** not verified live until Build - the real CLI rejects `--finish` alongside
  `--confirmed` (exit 64), and under `isolation: current` (what these roles use) doesn't need it at
  all; it also does a bare filesystem rename with no git commit under that isolation, which the
  runner now commits itself. See `internal/pipeline/archive.go` and the Superpowers design doc's
  Evidence base item 6 for the corrected contract.
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
- **Archive is runner Go code, not a fourth role — and runs before the PR opens, not after
  merge.** `comet native archive` is deterministic (in the sense of "no judgment call, safe to
  script" - the exact flags needed correction once verified live, see above); adding a role/agent invocation to
  call one deterministic CLI command would cost a full run for no judgment it needs to make, and
  would contradict the existing "route lives in the graph/code" principle already applied to PR
  handling. `internal/pipeline/prpass.go`'s `openPR` is already deterministic runner code that runs
  before the PR exists — Archive attaches there, on the task's own branch. Archiving *after* merge
  was considered and rejected during the Design phase's technical investigation: it would mean the
  runner pushing an unreviewed commit straight to the default branch, which the office's own rule
  that only a human merges (`docs/DESIGN.md` §2.8) forbids. Before the PR, the archive commit is
  just part of the same PR a human already reviews and merges.
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
  this repository's control entirely) - resolve during Design; does not change the specs or task
  breakdown, only the bootstrap task's concrete steps.
