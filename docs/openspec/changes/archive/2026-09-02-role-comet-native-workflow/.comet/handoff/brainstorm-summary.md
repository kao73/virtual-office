# Brainstorm Summary

- Change: role-comet-native-workflow
- Date: 2026-08-30

## Confirmed Technical Approach

`analyst`/`implementer`/`reviewer` drive one shared Comet Native change (Shape/Build/Verify) via
the vendored `comet`+`comet-native` skills, replacing analyst's Superpowers skills and
implementer/reviewer's ad hoc mechanisms. Phase-to-role mapping: analyst=Shape, implementer=Build
(keeps its existing one-step-one-commit discipline), reviewer=Verify (dispatches Native's mandated
read-only Verifier — reviewer's own separate role-run process already satisfies this requirement).

Concrete mechanics resolved during design:
- `internal/adapters/claude/adapter.go` gains a new `hooks.pre_tool_use` role.yaml field (list of
  `{matcher, command}`) and a second case in `buildSettings()` that writes a `"PreToolUse"` entry
  into the generated `settings.json`, pointing at the vendored
  `<plugin-dir>/skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root <workdir>`.
- Native change name reuses the existing `safeKey(task.Key)` helper (`internal/runner/change.go:48`)
  under the new root `docs/comet/changes/<name>/`.
- Found and fixed in passing: `internal/pipeline/prpass.go:299` (`prBody`) and
  `internal/runner/input.go:174` still read the retired `docs/changes/<KEY>/brief.md` via
  `ChangeDirRel(task.Key)` — silently empty since `analyst-brainstorming-skill`. Both are
  repointed at `docs/comet/changes/<name>/brief.md`.
- **Correction to the Open-phase design.md**: Archive runs *before* the PR opens (on the task's own
  branch, before any merge), not after merge as originally guessed — `openPR` is already
  deterministic runner code, and an archive commit pushed after merge would mean the runner
  committing to the default branch without human review, violating "the human merges"
  (`docs/DESIGN.md` §2.8). Before-PR keeps the archive commit inside the same PR a human already
  reviews and merges.
- Verify's exact JSON contracts (from `native-verifier-protocol.js`): each check request is
  `{id, name, executable, argv, cwdRef, timeoutMs, repeatable}`; a final verdict is
  `{iteration, attempt, verdict, acceptance:[{id,result,reason}], risks, summary}`.
- `sbx` bootstrap: image is entirely external to this repo (`docs/notes/sbx.md`: ships
  `claude, git, python3, uv`, no local customization hook). Node is very likely already present
  (Claude Code itself ships via npm); the open question narrows to installing the `comet` package
  itself — left as a separate prerequisite-investigation task, not solved in this change.

## Key Trade-offs and Risks

- Comet Native's Shape self-confirmation is emergent model judgment, not an audited feature — the
  office's own `needs_human` outcome (already generalized by `role-external-skills`) remains the
  real safety boundary, not Comet's `--confirmed` flag.
- `comet` is a beta CLI (`0.4.0-beta.18`) with at least one confirmed unconditional bug
  (`workspace prepare/resolve`) — version-pin it, and do not depend on that specific command family
  anywhere in the role pipeline.
- `PreToolUse` guard hook blocks `Edit` on existing files outside `build` phase but does not block
  `Write` of a brand-new file the same way — a known, accepted nuance, not something to fix here.

## Testing Strategy

Golden eval cases under `cmd/eval-roles` for all three roles' new Shape/Build/Verify flow
(extends `evals/analyst/`, `evals/implementer/`, `evals/reviewer/`), plus a unit test covering the
new `docs/comet/changes/<name>/` path resolution in `prBody`/`input.go` (replacing/extending the
existing `ChangeDirRel` coverage).

## Spec Patches

None — `specs/role-native-workflow/spec.md` (Open phase) already covers everything found during
design; `role-external-skills` and `role-sandbox-permissions` are unaffected.
