---
comet_change: role-comet-native-workflow
role: technical-design
canonical_spec: openspec
archived-with: 2026-09-02-role-comet-native-workflow
status: final
---

# role-comet-native-workflow — Technical Design

This deepens `docs/openspec/changes/role-comet-native-workflow/design.md` (the high-level
framework and its decisions) into concrete implementation detail. It does not restate or replace
the Open-phase decisions — with one correction carried back into that file during this phase (see
below) — it specifies exactly what `internal/adapters/claude/adapter.go`, the three roles'
`role.yaml`/`role.md`, and the runner's PR pass must say and do.

## Evidence base

Two live headless experiments (`claude --print`, restricted `--tools` with no `AskUserQuestion`,
the vendored `comet`+`comet-native` skills mounted as a plugin) grounded every claim below:

1. A fresh repo with no `.comet/config.yaml` resolves `/comet` to **Native**, not Classic (both get
   enabled; Native is the default for new, non-interactive projects).
2. Native ran Shape→Build fully headless across two separate tasks with zero `AskUserQuestion`
   attempts and zero stalls.
3. On a trivial task, the model self-confirmed Shape (`comet native next <change> --confirmed`)
   with an explicit, auditable rationale written into `brief.md`'s Decisions section.
4. On a deliberately ambiguous, irreversible-looking task (log cleanup with an unstated retention
   period, against real tracked files — the model verified this itself via `git ls-files`), the
   model neither blindly self-confirmed nor blocked/stalled: it made the destructive path opt-in
   (`--apply` required, dry-run default), exposed the missing parameter as a flag, never ran the
   destructive path even during its own verification, and explicitly flagged the retention policy
   as needing review "before the first `--apply` run in production" while still shipping tested,
   working code.
5. `comet native next ... --confirmed` succeeds regardless of whether a human was actually
   involved — a trust-based CLI flag, not independently verified. The office's own `needs_human`
   outcome (already generalized by `specs/role-external-skills/spec.md`'s "Human-approval
   fallback" requirement) is the real safety boundary, not this flag.
6. `comet native archive <name> --confirmed --finish merge|push|pull-request|keep` is a standalone,
   documented-as-deterministic command. **Correction (Build phase, Task 5 review, 2026-08-31):**
   this claim was never verified live during design — it was wrong. Verified against the actually
   installed CLI: `--finish` is rejected outright alongside `--confirmed` (`--finish is only valid
   with --dry-run`, exit 64) and is not required at all under `isolation: current` (what these
   roles use). The real call is a single `comet native archive <name> --confirmed`. Separately,
   `comet native status --json`'s payload is wrapped in an envelope (`{command, exitCode, data:
   {phase, loop: {stage}}}`) — `"archive-ready"` (bullet 4/§Runner below) is `data.loop.stage`, not
   `data.phase` (whose enum is only `shape|build|verify|archive`). And under `isolation: current`,
   `archive --confirmed` does a bare filesystem rename with no git commit — the runner must commit
   the rename itself before pushing. See the corrected §Runner below and
   `internal/pipeline/archive.go`'s doc comments for the fixed contract.
7. `comet init --platform claude --workflow native --scope project --yes` produces a minimal
   footprint (`.claude/{rules,skills,settings.local.json}`, `.comet/`, `docs/comet/`); the wide
   multi-platform footprint seen in earlier, unscoped testing (`comet workflow resolve --activate`)
   was this developer's own machine leaking in, not a property of Comet Native itself.
8. The `PreToolUse` guard hook (`comet-hook-router.mjs`, inside the `comet` skill's `scripts/`
   folder) technically blocks `Edit` on an existing tracked file when the bound change is not in
   `phase: build` — confirmed by piping a synthetic PreToolUse JSON payload directly into the
   script, which returned exit 2 with `"Native change <name> is in shape; implementation writes
   are only allowed in Build"`. A `Write` of a brand-new file is not blocked the same way — accepted
   as a known nuance (Comet's own Shape artifacts are written this way), not something to fix.
9. Exact JSON contracts for Build→Verify, read from the installed package's compiled source
   (`@rpamis/comet/dist/domains/comet-native/native-verifier-protocol.js`) since the CLI's own
   `--help` text under-specifies them:
   - A check request (used in both `dispatch-verifier` and a Verifier's `request-checks`):
     `{id, name, executable, argv, cwdRef, timeoutMs, repeatable}` — exact keys, no extras.
   - A Verifier `final-result`: `{iteration, attempt, verdict: "pass"|"fail"|"blocked",
     acceptance: [{id, result: "passed"|"failed"|"blocked", reason}], risks: string[], summary}`.
   **Correction (Build phase, Task 11 fix, 2026-08-31):** these two shapes are correct as the
   *inner* payload, but were never verified against a live `comet native next --runner-input`
   call, and it turns out both need an outer envelope the CLI actually requires: a check plan is
   `{"kind": "dispatch-verifier", "checks": [...]}`, not a bare array (`comet native next` rejects
   a bare array with "Native Runner input must be an object"); a `final-result` is `{"kind":
   "verifier-response", "response": {"kind": "final-result", "result": {...the shape above...}}}`,
   not the bare inner object (rejected with "Native Runner input kind is invalid"). Also newly
   found: a passing `final-result` (all acceptance items `passed`) moves the change to status
   `await-user`, not directly to `archive-ready` — one more self-confirm call is required
   (`comet native next <name> --summary "..." --confirmed`, the same trust-based flag named in
   Evidence base item 5) before `archive-ready` is actually reached. A failing/blocked verdict
   needs no such step; Native routes back to `build`/`repairing` on its own. See
   `roles/reviewer/role.md`'s corrected Verify-dispatch section for the fixed contract.
   **Stale as of `0.4.0-beta.20` (plan Task 18):** this exact flag was split — `--confirmed`
   now means only Shape self-confirmation, and this specific gate needs `--accept-result`
   instead. `roles/reviewer/role.md` was updated; this Evidence-base paragraph is left as an
   accurate account of what `0.4.0-beta.18` (item 10 below) actually did, not of current behavior.
10. `comet` is `@rpamis/comet@0.4.0-beta.18` (npm, MIT, Node 22+, no daemon). A confirmed,
    unconditional bug in this exact version: `comet classic workspace prepare|resolve` fails with
    `"Classic command project context is unavailable"` (root cause: a missing
    `withClassicCommandContext(...)` wrapper around `classicCommandProjectRoot()` in the compiled
    bundle — see project memory `reference_comet_workspace_bug_existing_change`). Irrelevant to the
    role pipeline's own runtime (it never touches Classic), but evidence this beta CLI needs
    version-pinning and should not be trusted blindly.

## Architecture

```
tracker status:   Ready ──────▶ In Progress ──────▶ Review ─────────────────────▶ Done
role:              analyst        implementer         reviewer     (runner, no agent)
comet native phase: shape          build                verify   ─▶ archive ─▶ PR opens ─▶ human merges
```

Archive is not a fourth tracker status or role — it is the runner's own deterministic step,
sitting between `reviewer` finishing Verify and the PR that carries the merge to `Done` (see
below for why it must come *before* the PR, not after the human merges it).

One Comet Native change per task lives at `docs/comet/changes/<name>/` in the **client** project's
own worktree, where `<name>` is the existing `safeKey(task.Key)` helper's output
(`internal/runner/change.go:48`) reused under this new root instead of the retired
`docs/changes/<KEY>/`. Each role's session runs `comet native status <name> --json` to find the
change already bound to its task (created by `analyst` on first touch) and drives exactly the
phase(s) assigned to it; the git-committed `comet-state.yaml` is what makes this resumable across
truncated runs and across roles, the same way `STATE.md`/committed plan files already do today.
No new runner↔agent channel: this is entirely internal to the role's own working directory, same
as `.agent/task.md`/`.agent/context.md`/`.agent/result.json` today.

**Archive is deterministic runner code, not a role**, and runs on the task's branch *before* the
PR opens — see the `internal/pipeline/prpass.go` section below for why this replaces the
Open-phase design.md's original "archive after merge" guess.

**Phase-scoped writes are hook-enforced.** Comet's own `PreToolUse` guard hook, wired by the
adapter (see below), turns "analyst does not write code" / "reviewer does not edit" from
`role.md`-only convention into a technical boundary — on top of, not instead of, the existing
textual convention (defense in depth; the hook only catches `Edit` of existing files, not `Write`
of new ones, so `role.md` text remains load-bearing too).

## `internal/adapters/claude/adapter.go`

### New role.yaml field: `hooks.pre_tool_use`

Mirrors the existing `hooks.stop` shape (a role-declared list, adapter resolves paths — no
skill-name inference):

```yaml
hooks:
  stop:
    - hooks/require-result.sh
  pre_tool_use:
    - matcher: "Write|Edit"
      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root "$WORKDIR"
```

The `command` path is resolved relative to the role's mounted plugin dir (the same
`buildPlugin()`-produced tree that already carries `<pluginDir>/skills/comet/scripts/
comet-hook-router.mjs` because `role.Skills` includes `comet`); `$WORKDIR` is substituted by the
adapter from the `workdir` it already has in `Build()`. A role that declares `hooks.pre_tool_use`
without mounting the referenced skill is a config error the loader should reject at role-load time,
the same way `docs/contracts/agent-io.md` already requires the file-result hook to have its
validator binary available.

### `buildSettings()` change

Currently `s.Hooks` is unconditionally set to `map[string][]hookMatcher{"Stop": {...}}`
(`adapter.go` ~342-377). This becomes a second, independent case keyed by hook type instead of a
single hardcoded key:

```go
type hookMatcher struct {
    Matcher string        `json:"matcher,omitempty"` // new field; empty = no matcher (Stop's current behavior)
    Hooks   []hookCommand `json:"hooks"`
}
```

`buildSettings()` populates `s.Hooks["Stop"]` exactly as today (unchanged), and additionally
populates `s.Hooks["PreToolUse"]` from `role.HookFiles().PreToolUse` (or an equivalent accessor) —
one `hookMatcher` per declared `{matcher, command}` pair, each carrying its own `Matcher` string.
`copyHooks()` needs no change: the script it copies for `PreToolUse` already lives inside the
skill's own plugin-mounted tree (via `buildPlugin()`), not the role's `hooks/` directory the way
`require-result.sh` does — so the new hook type's "copy" step is really just referencing a path
`buildPlugin()` already materializes, not a new file-copy operation.

## `roles/analyst` (drives Shape)

### `role.yaml`

```yaml
skills: [comet, comet-native]   # was: [brainstorming, writing-plans]
hooks:
  stop:
    - hooks/require-result.sh
  pre_tool_use:
    - matcher: "Write|Edit"
      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root "$WORKDIR"
```

### `role.md`: dispatcher replaces the `analyst-brainstorming-skill` one

Removed: the whole current "Диспетчер скиллов" section (invocation, forced classification, no
visual companion, resume-skip, output location, artifacts, Execution Handoff) — it governed
`brainstorming`/`writing-plans`, which are no longer mounted.

Added, in its place:

1. **Resolve or create the change.** Compute `<name>` from the task key the same way the runner
   does; run `comet native status <name> --json`. No active change → create one
   (`comet native new <name> --isolation current`, since the role already runs inside the runner's
   own branch/worktree — Comet's own workspace isolation is a no-op here, the runner already
   isolated the task). An active change already in `build`/`verify`/`archive` phase → this task
   already passed Shape; treat as resume-skip equivalent to today's rule (verify the existing
   brief/spec still answers the task, do not re-Shape).
2. **Investigate, then Shape.** Follow `comet-native`'s own Shape/clarification protocol
   unmodified — investigate facts, write `brief.md`/`specs/<capability>/spec.md`.
3. **Confirmation boundary.** When Native's own protocol reaches its Shape confirmation point: if
   every remaining open item was resolved by a safe engineering default (mirroring what both
   experiments actually did — self-confirm only when nothing genuinely irreversible/ambiguous is
   left unresolved), call `comet native next <name> --summary "..." --confirmed`. If a real
   `[blocking]` item remains that cannot be defused this way, do **not** call `--confirmed`: end
   the run with `outcome: needs_human`, one `questions[]` entry per remaining blocking line (`id`
   generated the same way today's `Q1`/`Q2` scheme works, `text` from the blocking line, `options`
   when Native's own note names discrete choices), `next_owner: human`. This is a direct
   application of `specs/role-external-skills/spec.md`'s existing "Human-approval fallback"
   requirement to Native's specific gate, not a new requirement.
4. **Resume with an answer.** On the next run, the human's answer arrives in context the same way
   it already does for any `needs_human` round-trip. Apply it into `brief.md`'s Decisions, remove
   the resolved `[blocking]` line, and continue Shape from there — do not restart the change.
5. **Artifacts.** List `brief.md` and every `specs/<capability>/spec.md` actually written in
   `result.json`'s `artifacts`, per the existing generic requirement.
6. **Outcome.** Shape confirmed → `done`, `next_owner: implementer` (unchanged shape from today).

### `role.md`: `## Как коммитить` — unchanged in substance

The three self-held rules (commit only what you made; `done` requires committed work; revert vs.
restore) already generalized past a fixed directory name during `analyst-brainstorming-skill` —
nothing here depends on `docs/changes/<KEY>/` specifically, so no further change needed.

## `roles/implementer` (drives Build)

### `role.yaml`

```yaml
skills: [comet, comet-native]   # was: []
hooks:
  stop:
    - hooks/require-result.sh
  pre_tool_use:
    - matcher: "Write|Edit"
      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root "$WORKDIR"
```

### `role.md`: input changes, working style mostly does not

Removed: the "Если есть план" section's `tasks.md`/`design.md` reading and checkbox-marking
instructions.

Added, replacing it:

1. **Read the change instead of a plan file.** `comet native status <name> --json` (phase
   expected: `build`); read `brief.md`, `specs/<capability>/spec.md`, and the acceptance list from
   the JSON. Treat this exactly as today's role.md treats "проверенные решения" from a
   `design.md` — trust it, only push back on an actual mismatch with the real code.
2. **Keep one-step-one-commit, redefined against acceptance items, not checkboxes.** Native does
   not hand implementer a checklist to tick — this office's own convention of small, reviewable
   commits continues on top of it, organized around the acceptance items rather than `tasks.md`
   lines. Where no such natural breakdown exists (a genuinely small/atomic task), today's existing
   "Плана в контексте нет — работай как обычно" fallback already covers exactly this shape of
   work and needs no new text.
3. **Submit the Builder handoff to advance to Verify.** Build a `builder-handoff` JSON
   (`{kind: "builder-handoff", summary, addressed_acceptance_ids, checks, known_limits}`, per the
   CLI's own `--help`) via a `Bash` heredoc (mirrors both experiments, which used this path rather
   than the scoped `Write` tool — `implementer`'s `Bash(*)` already covers it) into a temp file,
   then `comet native next <name> --runner-input <file>`. `outcome: done`, `next_owner: reviewer`.
4. **Mismatch handling unchanged.** Spec disagrees with reality → `done`, `next_owner: analyst`,
   explaining the discrepancy — same shape as today's "план разошёлся с кодом" rule, just against
   `brief.md`/`spec.md` instead of `tasks.md`/`design.md`.

### `role.md`: truncated-run and non-merging-branch sections — unchanged in substance

Both already describe git-level recovery (uncommitted work, merge conflicts) that has nothing to
do with the plan file's format; Native's own git-committed `comet-state.yaml` only reinforces the
existing "check `git status`/`git diff` before assuming nothing happened" guidance.

## `roles/reviewer` (drives Verify)

### `role.yaml`

```yaml
skills: [comet, comet-native]   # was: []
hooks:
  stop:
    - hooks/require-result.sh
  pre_tool_use:
    - matcher: "Write|Edit"
      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root "$WORKDIR"
```

No change to `tools.allow`/`tools.deny` — reviewer's existing `Bash(*)` already covers writing the
temporary `--runner-input` JSON files the same way `implementer` does; its scoped, single-path
`Write`/`Edit` (result file only) is untouched and remains the real boundary against editing code.

### `role.md`: new Verify-dispatch section, replacing ad hoc diff-reading

1. **Read the change.** `comet native status <name> --json` (phase expected: `verify`); the
   Runtime's `continuation` after the Builder handoff already requests `dispatch-verifier`.
2. **Build and submit the check plan.** Discover the project's real checks the same way
   `implementer` already does (`.pre-commit-config.yaml`, `Makefile` lint targets, the test
   runner) and express each as `{id, name, executable, argv, cwdRef, timeoutMs, repeatable}`
   (exact keys — an extra or missing field is rejected). Submit via
   `comet native next <name> --runner-input <dispatch-verifier.json>`; the Runtime executes them,
   not `reviewer` directly, and returns results in the next `continuation`.
3. **Judge every acceptance item.** Using the Runtime's check results plus the actual diff/code
   (unchanged: reviewer still reads and reasons, it just reports against acceptance IDs now
   instead of free-form review notes), submit a `final-result`
   (`{iteration, attempt, verdict, acceptance: [{id, result, reason}], risks, summary}`) via the
   same `--runner-input` mechanism. Every acceptance ID must get exactly one verdict.
4. **Outcome.** All acceptance items pass → `done`, `next_owner: none` — Verify hands off to the
   human via the existing PR flow, not back to a role; Native's own state moves to `archive-ready`,
   which the runner's PR pass reads before opening the PR (see below). Any item fails/blocked →
   `done`, `next_owner: implementer`, same as today's "found problems" outcome, with the specific
   failed acceptance IDs and reasons in `details_md`.

## Runner: `internal/pipeline/prpass.go` and `internal/runner/input.go`

Two independent fixes to the same underlying drift, found during this phase's investigation:

- **`prpass.go:299` (`prBody`)** currently falls back to `ChangeDirRel(task.Key)` (`docs/changes/
  <KEY>/brief.md`) for the PR description, which `analyst` stopped writing to after
  `analyst-brainstorming-skill`. Repoint at `docs/comet/changes/<name>/brief.md` (same `safeKey`
  helper, new root) as the primary source; fall back to the raw ticket text only when neither the
  old nor new path exists, matching the existing fallback's intent.
- **`input.go:174`** (the "Каталог изменения: `<path>`" context line implementer/reviewer receive)
  has the identical drift — same fix, same helper reuse.
- **Archive attaches to `prpass.go`'s `openPR` (`prpass.go:76`), before it runs**, on the task's
  own branch: once Native's state shows `data.loop.stage == "archive-ready"` (i.e., `reviewer`
  submitted a passing `final-result`), the pass runs `comet native archive <name> --confirmed`
  before calling `openPR`. This corrects the Open-phase `design.md`'s original "archive after
  merge" guess — archiving after merge would mean the runner pushing an unreviewed commit straight
  to the default branch, which `docs/DESIGN.md` §2.8's "the human merges" rule forbids. Before the
  PR opens, the archive commit is simply part of the same PR a human already reviews and merges.
  **Correction (Build phase, Task 5 review, 2026-08-31):** the paragraph above originally read
  `--confirmed --finish keep`, on the untested assumption that `--finish keep` "avoids Comet's own
  git operations conflicting with the office's PR flow." That's wrong on two counts, both found by
  running the real CLI: `--finish` is rejected together with `--confirmed` at all (exit 64), and
  under `isolation: current` — the isolation these roles actually use, since the runner has already
  isolated the task with its own worktree — `archive --confirmed` never runs a git operation of its
  own regardless of `--finish`; it does a bare filesystem rename of the change directory. So there
  is no Comet-vs-office git conflict to avoid, but there is a gap the original text didn't
  anticipate: nothing commits the rename. The runner (`archiveIfReady`) now stages and commits it
  itself — `git add -A && git commit` in the task's worktree, right after a successful `archive
  --confirmed` and before `Workspaces.Push` — using the same worktree `work()` already committed
  the role's own output in. This keeps the "archive commit rides in the same PR a human reviews"
  property the original design wanted; it just needed the runner, not Comet, to make the commit.

## Vendoring

```
skills/comet/           # full copy, @rpamis/comet CLI's shipped skill bundle
skills/comet/.source.yaml
skills/comet-native/    # full copy
skills/comet-native/.source.yaml
```

`.source.yaml` (same shape as `skills/brainstorming`/`skills/writing-plans`'s, per
`chore(skills): rename SOURCE.md to .source.yaml`):

```yaml
repository: https://github.com/rpamis/comet
tag: <exact npm version tag, e.g. v0.4.0-beta.18>
commit: <resolved commit for that tag>
```

No drift-check script, consistent with the existing precedent for vendored skills in this repo —
but unlike `brainstorming`/`writing-plans` (pure markdown, no runtime dependency), this vendored
copy is inert without the `comet` npm package also present on `PATH` inside the role's sandbox;
that dependency is tracked separately (tasks.md item 1.1), not solved by vendoring the skill files.

## Testing Strategy

Extend `evals/analyst/`, `evals/implementer/`, `evals/reviewer/` with golden cases exercising the
new flow through `cmd/eval-roles`, mirroring the existing fixture shapes (task.md + expect.yaml +
fixture repo):

- `analyst`: a basic-plan case (Shape completes, `done`/`next_owner: implementer`,
  `docs/comet/changes/**` in `diff_scope`) and an ambiguous-decision case
  (`needs_human`/`questions_not_empty`), reusing the existing ambiguous-task fixture shape.
- `implementer`: a basic-build case reading a pre-seeded `brief.md`/`spec.md` fixture instead of
  `tasks.md`/`design.md`, expecting `done`/`next_owner: reviewer` and a Builder handoff having been
  submitted (checked via `comet-state.yaml`'s `loop.stage`).
- `reviewer`: a spot-defect case verifying a `final-result` with a `failed` acceptance ID routes
  back to `implementer`, and a clean case verifying `next_owner: none` with Native state at
  `archive-ready`.
- A unit test in `internal/pipeline` (or wherever `prpass_test.go` already lives) covering
  `prBody`'s new `docs/comet/changes/<name>/brief.md` path resolution and its fallback ordering,
  replacing/extending the existing `ChangeDirRel`-based coverage.

## Boundary Conditions Deliberately Left Open

- **`sbx` bootstrap.** ~~Whether/how the `comet` npm package reaches the sandbox image is explicitly
  not solved here~~ — **Resolved, plan Task 17** (added after the original 16 tasks and their final
  review closed, at the user's explicit request). `sbx kit` (experimental but present in `sbx`
  v0.38.0) bakes `comet` — and, as insurance for a possible future switch to Comet Classic,
  `openspec` — into a pinned local sandbox template (`bootstrap/sbx-kits/`), applied via
  `internal/backends/sbx.Template`/`createArgs`. Confirmed live and empirically that `comet native`
  needs neither the npm package's other nine bundled skills nor `openspec` to function — see
  `docs/notes/sbx.md`'s "`comet` CLI bootstrap" section for the full recipe and evidence.
- **`Write` vs. `Edit` asymmetry in the guard hook.** Confirmed directly: a brand-new file write is
  not blocked outside `build` phase, only editing an existing tracked file is. This is accepted as
  Comet's own design (its Shape phase legitimately needs unrestricted `Write` for its own
  artifacts) — `role.md` text remains the only boundary against, say, `analyst` writing a new
  implementation file via `Write` instead of `Edit`. Not a gap this change closes; named so Build
  does not mistake the hook for a complete technical boundary.
- **Verifier-as-subagent nuance.** Native's own protocol says "start a fresh read-only Verifier
  subagent, or a new Agent task separate from the Builder session" — `reviewer`'s entire role-run
  already is a separate process/session from `implementer`, satisfying this without `reviewer`
  needing to spawn any further internal subagent. Untested directly (neither experiment reached
  Verify with a real Verifier dispatch to completion — experiment 2 hit the check-schema error
  before resolving it); the schema fix above should unblock this in Build, but the actual end-to-end
  Verify loop remains unverified by a live run until then.
- **Beta CLI risk carried forward.** `comet classic workspace prepare/resolve`'s unconditional
  failure is irrelevant to this design (Classic is never invoked by the role pipeline), but it is
  evidence this exact CLI version has real rough edges; pin the version vendored/installed and
  re-verify against any newer release before upgrading it.

## Post-Build final-review findings (2026-08-31)

All 16 tasks landed and were individually reviewed clean — but each review was scoped to its own
task's diff, and a mandatory whole-branch final review (before Task 16's live run) caught several
things no single task's diff could reveal, all fixed and independently live-re-verified afterward:

- **The `PreToolUse` guard hook was never actually executable.** `skills/comet/scripts/
  comet-hook-router.mjs` shipped mode 644 (matching the upstream npm package itself, not a
  vendoring mistake) — the adapter invokes it directly rather than via `node <path>`, so it exited
  126 ("permission denied"), which Claude Code's hook mechanism treats as a non-blocking error.
  The "technical, not just textual, enforcement" this whole design is built around had never once
  fired. Fixed: `chmod +x` on vendoring, plus an exec-bit check in `role.go`'s `hooks.pre_tool_use`
  validation mirroring the existing `Stop`-hook check (a role declaring a non-executable hook script
  now fails at load time instead of shipping a dead guard).
- **`.comet/` was never git-tracked or excluded.** `comet native new` writes `.comet/config.yaml`,
  `.comet/current-change.json`, and `.comet/runtime/**` with no `.gitignore` of its own. Left as-is:
  a fresh/rebuilt worktree or second runner host could never resume a change (`comet native status`
  needs `config.yaml`); every Comet-driven worktree stayed permanently "dirty" and unreclaimable by
  `sweepWorktrees`; the guard hook (above) silently went neutral wherever `current-change.json` was
  missing. Fixed: `internal/runner.ExcludeCometRuntime` extends the existing `ExcludeAgentDir`
  `info/exclude` mechanism to exclude the two machine-local paths, while `config.yaml` stays a
  normal trackable path that `analyst` now explicitly commits on first `comet native new`.
- **Activating the guard hook (first fix above) exposed a third bug**: with no `hook.allow_paths`
  configured, the guard can't distinguish a role's own mandatory `.agent/result.json`/`STATE.md`
  write from a real code edit. Outside `build` phase this either hard-blocks the write (breaking
  `analyst`'s `needs_human` escalation outright) or — worse — silently succeeds while reverting the
  change's phase backward (`verify`→`build`, or on `capability-clean-verify`'s archive-phase case,
  `archive`→`build` while also deleting `verification.md`). Found and fixed in three rounds as it
  kept surfacing in one more place each time: `analyst`'s own `role.md` now appends `hook:
  allow_paths: [.agent, STATE.md]` to `.comet/config.yaml` once per project (checked on both the
  new-change and resume branches); all four pre-seeded `evals/*` golden-case fixtures — which bypass
  the live `analyst` run that would otherwise add this — got the identical block added directly to
  their fixture `.comet/config.yaml`. Verified live across every reachable phase (`shape`, `build`,
  `verify`, `verify`/`await-user`, `archive`) with negative controls proving the corruption is real
  without the fix. This remains a `role.md`-text contract with no technical check that analyst
  actually adds it on a fresh project, consistent with this repository's existing "role.md decides,
  no technical enforcement" convention elsewhere — Task 16's live run is what actually exercises
  this end-to-end with a real model, not just simulated-compliant CLI calls.
- **`archiveIfReady` trusted `comet native archive --confirmed`'s exit 0 with no postcondition
  check.** Live-verified: in a worktree missing `.comet/runtime/**` state, the archive command can
  print a recovery message and exit 0 while actually *reverting* the change's phase
  (`archive`/`archive-ready` → `verify`) instead of archiving — and the old code committed and
  pushed that regression as a successful archive. Fixed: `archiveIfReady` now verifies the archive
  destination actually exists and the source directory is actually gone before committing/pushing;
  otherwise it logs the mismatch and skips, non-blocking, same as every other failure path in that
  function.
- Plus smaller fixes: two `evals/analyst/` and one `evals/implementer/` golden case needed
  `diff_scope.allow` widened for paths a compliant run genuinely touches; `evals/implementer/
  capability-reads-brief-and-spec` needed the same seeded-runtime-state fix Task 15 already applied
  to `evals/reviewer/capability-spot-defect` (a fresh materialized checkout's first `comet native
  next` call silently no-ops without local execution history) and a corrected (previously inverted)
  `fixture_tests` assertion.

Full detail, every live-CLI reproduction, and every review round's findings are in
`.superpowers/sdd/2026-08-30-role-comet-native-workflow/progress.md`.
