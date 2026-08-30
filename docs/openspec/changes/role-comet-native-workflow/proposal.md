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
- **Archive becomes a deterministic runner step**, not a fourth role: the runner (not an agent) runs
  `comet native archive --confirmed` (superseding this line's original "after merge"/`--finish keep`
  wording — superseded already during Design, see `design.md`'s Decisions, and the exact flags
  corrected during Build after live verification, see `design.md`'s Context correction), syncing the
  delta capability spec into the client project's canonical `specs/`.
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
