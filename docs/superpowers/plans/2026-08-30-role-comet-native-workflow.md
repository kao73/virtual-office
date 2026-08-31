---
change: role-comet-native-workflow
design-doc: docs/superpowers/specs/2026-08-30-role-comet-native-workflow-design.md
base-ref: 343cefa6c6c35cf277e53eff5cc78dc77f6d71c4
---

# Comet Native Role Workflow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move `analyst`/`implementer`/`reviewer` off their current ad hoc planning/review mechanisms (vendored `brainstorming`+`writing-plans`, free-form `tasks.md`/`design.md`, free-form diff review) onto Comet Native's Shape→Build→Verify state machine, vendor its two skills, teach `internal/adapters/claude/adapter.go` a second hook type so Comet's own guard hook enforces phase-scoped writes technically, fix the two runner call sites that still hard-code the retired `docs/changes/<KEY>/` root, wire a deterministic Archive step into the runner's PR pass before it opens a pull request, and extend `evals/` so the new flow is covered by golden cases.

**Architecture:** `docs/comet/changes/<name>/` (same `safeKey`-derived `<name>` the office already computes) becomes the change root these three roles read and write via the vendored `comet`/`comet-native` CLI skills, driving one Comet Native phase each (`analyst`→Shape, `implementer`→Build, `reviewer`→Verify) inside the same worktree the runner already gives them — no new runner↔agent channel. `docs/changes/<KEY>/` is not deleted and is not read by any of the three roles' *new* logic as a primary source, but the two runner call sites that used to assume it unconditionally (`prpass.go`'s `prBody`, `input.go`'s context line) now check the new root first and fall back to it, so tasks that predate this change or come from outside the Comet pipeline keep working. Archive is deterministic Go code the runner attaches to `prpass.go`'s `openPR`, gated on Comet's own reported phase (`archive-ready`) and running *before* the pull request opens, so the archive commit rides in the same PR a human reviews — never a separate, unreviewed push to the default branch. `internal/adapters/claude/adapter.go` gains a `PreToolUse` hook type, mirroring the existing `Stop` hook, so Comet's `comet-hook-router.mjs` (shipped inside the vendored `comet` skill, mounted like any other skill) can technically block `Edit` of tracked files outside `build` phase — on top of, not instead of, `role.md` text.

**Tech Stack:** Go 1.22+ (`internal/runner`, `internal/adapters/claude`, `internal/pipeline`); YAML (`role.yaml`, `.source.yaml`, `expect.yaml`); the `comet`/`comet-native` vendored skills (Markdown + the `comet-hook-router.mjs` Node script, both unmodified copies of `@rpamis/comet@0.4.0-beta.18`'s shipped bundle); `git`, `npm`/`node`, `jq` for vendoring and fixture authoring; `cmd/eval-roles` for golden-case verification.

**Spec:** `docs/superpowers/specs/2026-08-30-role-comet-native-workflow-design.md` (deep technical design — authoritative for every exact shape used below: the `hooks.pre_tool_use` YAML, the `builder-handoff`/check-request/`final-result` JSON contracts, the phase-to-role mapping, and the archive-before-PR correction). Also `docs/openspec/changes/role-comet-native-workflow/{proposal.md,design.md,tasks.md}` for the Open-phase why/what and the 7-group/12-item task boundary this plan maps to. Executors should read the deep design doc in full before starting; this plan does not restate its rationale, only its execution, and resolves a handful of Go-level and fixture-level details the design doc left at the "what" level (see "Notes on task ordering and resolved ambiguities" below).

## Global Constraints

- **Language of this plan:** English, per the request that produced it.
- **One change, one plan — not split by role or file.** This is an explicit, already-confirmed decision. Do not propose splitting this plan; the many tasks below are internal sequencing of one plan, not separate changes.
- **Comet Native (not Classic) drives the role pipeline.** This repository's own meta-development uses Comet Classic (the workflow that produced this very plan) — the two are unrelated tooling for unrelated purposes. Nothing in this plan touches `.comet/`, `.claude/rules/comet-workflow-guard.md`, or any Classic/Native tooling this repository uses on itself.
- **Archive runs before the pull request opens, on the task's own branch**, not after a human merges it — a correction made during the Design phase (see design doc "Runner" section). Task 5 below implements Archive and the `prBody` root fix together, in the same task, because the design doc is explicit that Archive attaches to `openPR` and must not be treated as an afterthought bolted on later.
- **The `sbx`/runner-host bootstrap of the `comet` npm CLI is a separate, only-partially-solved prerequisite.** Task 1 investigates and documents it early but does not block any later task's file-level work — every later task that needs a working `comet` CLI (vendoring, eval fixture authoring, the final live run) says so explicitly and can be executed once a developer has `comet` installed locally, independent of whether the *production sandbox* bootstrap is solved. **Resolved by Task 17**, added after the original 16 tasks and their final review closed: `sbx kit` (experimental, but present and working in `sbx` v0.38.0) bakes `comet` into a pinned local sandbox template, closing this gap without touching role-facing code. Task 16 Steps 2–6 should not be attempted until Task 17 is done — otherwise the new golden cases fail on a missing binary, not on their own logic, telling us nothing.
- **`docs/changes/<KEY>/` is not retired by this plan, only demoted to a fallback** for the two runner call sites that read it (`prpass.go`'s `prBody`, `input.go`'s context line). No task deletes it or the code that serves it.
- **`./bin/eval-roles` is manual-only and costs real money/subscription per run** (README.md, "Golden-кейсы ролей (eval-roles)") — no CI or git hook invokes it. Task 16 runs real, paid role invocations; every other task's own verification step is `go test`, `go build`, or reading fixture files — never a live agent run.
- **Out of scope, not touched by any task below** (proposal.md "Impact"): `workflow.yaml`, the runner↔agent contract shape (`docs/contracts/agent-io.md`'s `result.json` schema itself — only its prose note about the change root changes), a fourth "archivist" role, and Comet Classic/OpenSpec tooling.

## Notes on task ordering and resolved ambiguities

This plan implements every item in `docs/openspec/changes/role-comet-native-workflow/tasks.md` (7 groups, 12 items). Several things below are resolved here, grounded in reading the actual source and the design doc during planning, because the design doc specifies the *what* at a level that leaves real Go-code and fixture-authoring decisions open:

1. **tasks.md 1.2 (check schema) and 1.3 (archive `--finish` mode and call site) need no standalone task.** Both are already resolved by the design doc itself — 1.2 by its Evidence base item 9 (the exact `{id, name, executable, argv, cwdRef, timeoutMs, repeatable}` and `{iteration, attempt, verdict, acceptance, risks, summary}` shapes), 1.3 by its explicit "Archive attaches to `prpass.go`'s `openPR`, before it runs... `--finish keep`" decision. Both are consumed directly in Task 11 (reviewer's `role.md`) and Task 5 (the archive call) respectively.
2. **The exact Go plumbing for the archive step — which the design doc does not specify at code level — is resolved by reading `internal/pipeline/pipeline.go`'s existing role-run lifecycle.** `openPR` currently only has a *bare* clone (`o.Workspaces.Repo`, no working tree) to read files via `git show`; Comet needs a real checked-out working tree with a `.comet`-tracked state file to read and to commit an archive commit into. Task 5 reuses the exact `Ensure`/`Push`/`Unlock` idiom `pipeline.go`'s own `work()` function already uses for role runs (same worktree, same lock, same push), rather than inventing a second workspace mechanism.
3. **Archive's failure handling is resolved as non-blocking for everything except a busy worktree.** The design doc's own "Boundary Conditions" section leaves the `comet` CLI's presence on the runner host as an open, only-partially-solved prerequisite (Task 1). Making a missing binary or a failed archive call permanently block a task's pull request would mean no task's PR could ever open until that prerequisite is fully solved — clearly not intended. A busy worktree (a role is running in it right now) is different: that is deferred to the next pass, exactly like the existing `merge`/`forge` checks already do in the same function.
4. **The literal phase value `"archive-ready"` and the field name `phase`** are taken verbatim from the one place the design doc names a phase value in prose (reviewer's "Outcome" bullet 4: "Native's own state moves to `archive-ready`"). This is the one piece of Comet's `status --json` output shape the design doc's Evidence base does not pin with an exact JSON contract (unlike the check-request/`final-result` shapes, which experiment 9 pinned from compiled source). Task 5 isolates it into one named constant and one struct field; Task 16's live run is the first real confirmation, and a mismatch is a one-line fix.
5. **Reviewer's move from `next_owner: human` to `next_owner: none` on a clean Verify pass was independently verified against `workflow.yaml` and `internal/tracker/config.go`/`internal/pipeline/pipeline.go` before being accepted, not just copied from the design doc's prose.** `none` is not a key in reviewer's `done.by_next_owner` map (`{implementer: Ready, human: Approved, analyst: Analysis}`), but it is also not a role known to `o.Workflow.Roles`, so `handsOver("reviewer", "none")` is `false`, the "stray" guard (`pipeline.go:664`) never fires, and `Outcome.Route("none")` falls through to the outcome's default `to: Approved` — bit-for-bit the same destination `"human"` already produces today. **No `workflow.yaml` change is needed or made**, matching the proposal's explicit "out of scope: workflow.yaml."
6. **Every role's new Comet-dispatch section is written conditional on "a Comet Native change is actually found in the expected phase," mirroring the exact structure each role's current `role.md` already uses** (implementer's "Плана в контексте нет — работай как обычно"; reviewer's "Контекст называет каталог изменения — если он есть..."). This is a deliberate choice, not an oversight: it keeps `evals/implementer/capability-basic-bugfix`, `evals/implementer/escalation-ambiguous-task`, and `evals/reviewer/escalation-ambiguous-task` valid with **zero** fixture changes, because none of their fixtures name a change directory in context at all. The design doc's own "Testing Strategy" section only asks for new/updated cases elsewhere — this is why those three are left alone (see Tasks 14/15).
7. **Golden cases that must exercise the Comet-aware path need a real, on-disk Comet Native change state**, which only an actual local `comet` CLI run can produce authentically (state file shape, phase names). A shared recipe is defined once, right before Task 13, and Tasks 13–15 each apply it with their own task-specific brief/spec/acceptance content rather than re-deriving the generic steps three times.
8. **Archive is scoped to the branch of `openPR` that actually opens a real pull request** (after `merge` and `project.Forge` are both confirmed usable), not to the `prSkipped` (no-forge, e.g. local polygon) branch — the design doc's own narrative is written entirely in terms of "the PR that carries the merge." A task on a forge-less project still reaches `Merged`-equivalent status without an archive commit, exactly as today; extending archive to that path is left for a future change.

All file paths and line numbers below were read from the actual repository during planning (`git status` is clean; this plan performs no implementation).

---

## Task 1 (tasks.md 1.1): Investigate the `comet` CLI bootstrap gap

**Files:**
- Modify: `docs/notes/sbx.md` (append a new section)

**Interfaces:** None — this is a documentation-only investigation task with no code dependency for any later task. Later tasks that need a *local* (non-sandboxed) `comet` CLI say so themselves; this task's finding is about the *production sandbox* image only.

- [x] **Step 1: Confirm there is no existing image-customization hook**

```bash
find bootstrap -maxdepth 2
cat bootstrap/README.md
grep -rn "sbx\|sandbox" docs/notes/sbx.md | head -20
```

Expected: `bootstrap/` contains only `office-runner.service`, `local.office.runner.plist`, `office-runner.timer`, `README.md`, and the unrelated `bootstrap/jira/` polygon — nothing that builds, extends, or customizes the `sbx` sandbox image itself. `docs/notes/sbx.md` already documents `sbx` as a vendored, externally-versioned image (v0.38.0 at last check) with a fixed toolset (`claude`, `git`, `python3`, `uv`) and no documented local-customization mechanism.

- [x] **Step 2: Check whether Node is already present in the image**

`docs/notes/sbx.md`'s own "Что проверено эмпирически" section already lists the sandbox's installed toolset (`uid=1000(agent)`, `claude`, `git`, `python3`, `uv`) — Node is not in that list, but Claude Code itself (`claude`) ships via npm and very likely requires a Node runtime to exist somewhere in that image. Do not assume either way from documentation alone: this step is a note for whoever next has `sbx` access to run `sbx exec node --version` (or equivalent) and confirm, not something this task can execute without sandbox access.

- [x] **Step 3: Write the findings into `docs/notes/sbx.md`**

Append this section (adjust the "Node is very likely already present" sentence below only if Step 2 was actually run and Node's presence confirmed one way or the other):

```markdown
## `comet` CLI bootstrap (role-comet-native-workflow)

Investigated 2026-08-30 for `role-comet-native-workflow`: `analyst`/`implementer`/
`reviewer` need Node 22+ and the `@rpamis/comet` npm CLI available inside the role's
own sandbox at runtime (their mounted `comet`/`comet-native` skills are inert
markdown+script bundles without it), and the runner host itself separately needs
the same CLI for the deterministic archive step (`internal/pipeline/archive.go`).

**No local image-customization hook exists.** `bootstrap/` contains only the
runner-service unit files and the unrelated Jira polygon Dockerfile — nothing that
builds or extends the `sbx` image. The image is entirely external and versioned by
`sbx` itself (v0.38.0 at last check); this repository has never had a documented way
to add a package to it.

**Node is very likely already present** — Claude Code (`claude`, confirmed present
inside the sandbox) ships via npm and needs a Node runtime — but this is not
independently confirmed in this repository's own notes as of this investigation.
Confirm with `sbx exec node --version` the next time `sbx` is available, and narrow
this note once done.

**Conclusion: installing `comet` into the sandbox image is a separate prerequisite
change outside this repository's control**, most likely an `sbx`-side base-image
update or an equivalent mechanism this repository does not yet have. It does not
block writing role.yaml/role.md/adapter code that assumes `comet` is present at
runtime (tasks.md items 2–7 of this change) — it blocks only the first real
sandboxed run of the finished pipeline. A developer's own machine, for local
`eval-roles` runs and for authoring the golden-case fixtures in this change, needs
`comet` installed the ordinary way (`npm install -g @rpamis/comet@0.4.0-beta.18` or
similar) — that is unaffected by this gap and does not require solving it.
```

- [x] **Step 4: Commit**

```bash
git add docs/notes/sbx.md
git commit -m "docs(sbx): record the comet CLI sandbox bootstrap gap"
```

---

## Task 2 (tasks.md 2.1, schema half): `internal/runner/role.go` — `hooks.pre_tool_use`

**Files:**
- Modify: `internal/runner/role.go`
- Modify: `internal/runner/role_test.go`

**Interfaces:**
- Produces: `Role.Hooks.PreToolUse []PreToolUseHook` (exported field, each with exported `Matcher`/`Command string` fields) — consumed by Task 3 (`internal/adapters/claude/adapter.go`'s `buildSettings`).
- Produces: the validation rule "a `hooks.pre_tool_use` entry's `command` must name a script under a skill actually listed in `skills:`" — enforced at `LoadRole` time, so a misconfigured role fails before any agent runs.

- [x] **Step 1: Write the failing tests**

In `internal/runner/role_test.go`, add these cases to the `TestLoadRoleRejects` table (inside the existing `cases := map[string]struct{...}{...}` literal):

```go
		"pre_tool_use без matcher": {
			yaml: strings.Replace(fixtureRoleYAML,
				"hooks:\n  stop:\n    - hooks/require-result.sh\n",
				"hooks:\n  stop:\n    - hooks/require-result.sh\n  pre_tool_use:\n"+
					"    - matcher: \"\"\n      command: skills/comet/scripts/comet-hook-router.mjs\n", 1),
			wantPart: "matcher пуст",
		},
		"pre_tool_use без command": {
			yaml: strings.Replace(fixtureRoleYAML,
				"hooks:\n  stop:\n    - hooks/require-result.sh\n",
				"hooks:\n  stop:\n    - hooks/require-result.sh\n  pre_tool_use:\n"+
					"    - matcher: \"Write|Edit\"\n      command: \"\"\n", 1),
			wantPart: "command пуст",
		},
		"pre_tool_use ссылается на неподключённый скилл": {
			yaml: strings.Replace(fixtureRoleYAML,
				"hooks:\n  stop:\n    - hooks/require-result.sh\n",
				"hooks:\n  stop:\n    - hooks/require-result.sh\n  pre_tool_use:\n"+
					"    - matcher: \"Write|Edit\"\n      command: skills/comet/scripts/comet-hook-router.mjs\n", 1),
			wantPart: "нет в skills",
		},
```

Then add two standalone test functions after `TestLoadRoleRejectsNonExecutableHook`:

```go
// Скрипт хука существует, но подключённого скилла, к которому он относится,
// в roleYAML нет вовсе — SkillDirs() его даже не проверяет, потому что о нём
// не знает список skills:. Отдельный тест: табличный TestLoadRoleRejects выше
// не создаёт файлов сверх стандартной фикстуры, а этому нужен настоящий
// каталог скилла без самого файла скрипта внутри него.
func TestLoadRoleRejectsPreToolUseMissingScript(t *testing.T) {
	yaml := strings.Replace(fixtureRoleYAML, "skills: []", "skills: [comet]", 1)
	yaml = strings.Replace(yaml,
		"hooks:\n  stop:\n    - hooks/require-result.sh\n",
		"hooks:\n  stop:\n    - hooks/require-result.sh\n  pre_tool_use:\n"+
			"    - matcher: \"Write|Edit\"\n      command: skills/comet/scripts/comet-hook-router.mjs\n", 1)
	root := fixtureOffice(t, yaml)
	// Каталог скилла существует (иначе SkillDirs() отверг бы роль раньше и по
	// другой причине), а самого скрипта внутри — нет.
	if err := os.MkdirAll(filepath.Join(root, "skills", "comet"), 0o755); err != nil {
		t.Fatalf("каталог скилла не создан: %v", err)
	}

	_, err := LoadRole(root, "tester")
	if err == nil {
		t.Fatal("скрипт хука отсутствует, но роль принята")
	}
	if !strings.Contains(err.Error(), "файл хука не найден") {
		t.Errorf("ошибка не объясняет причину: %v", err)
	}
}

// Хук-роутер Comet enforces фазовые границы записи технически, а не только
// текстом role.md (design doc "Phase-scoped writes are hook-enforced") — и
// корректно объявленный hooks.pre_tool_use обязан разбираться, а не только
// отвергаться.
func TestLoadRoleAcceptsPreToolUseHook(t *testing.T) {
	yaml := strings.Replace(fixtureRoleYAML, "skills: []", "skills: [comet]", 1)
	yaml = strings.Replace(yaml,
		"hooks:\n  stop:\n    - hooks/require-result.sh\n",
		"hooks:\n  stop:\n    - hooks/require-result.sh\n  pre_tool_use:\n"+
			"    - matcher: \"Write|Edit\"\n      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root \"$WORKDIR\"\n", 1)
	root := fixtureOffice(t, yaml)
	scriptDir := filepath.Join(root, "skills", "comet", "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("каталог скрипта не создан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "comet-hook-router.mjs"), []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatalf("скрипт хука не записан: %v", err)
	}

	role, err := LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("корректный pre_tool_use хук отвергнут: %v", err)
	}
	if len(role.Hooks.PreToolUse) != 1 {
		t.Fatalf("hooks.pre_tool_use не разобран: %+v", role.Hooks)
	}
	got := role.Hooks.PreToolUse[0]
	if got.Matcher != "Write|Edit" {
		t.Errorf("matcher=%q, ожидался Write|Edit", got.Matcher)
	}
	if !strings.HasPrefix(got.Command, "skills/comet/scripts/comet-hook-router.mjs") {
		t.Errorf("command=%q искажён при разборе", got.Command)
	}
}
```

- [x] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/runner/... -run TestLoadRole -v
```

Expected: `TestLoadRoleRejects/pre_tool_use_*` fail with "не разобран" (unknown YAML field `pre_tool_use`, since `Hooks` doesn't have it yet), and the two new standalone tests fail to compile or fail outright (`role.Hooks.PreToolUse` doesn't exist yet) — confirm the failure is "field doesn't exist" in nature, not a typo in the test itself.

- [x] **Step 3: Implement the schema and validation**

In `internal/runner/role.go`, replace the `Hooks` struct (around line 51-53):

```go
type Hooks struct {
	Stop []string `yaml:"stop"`
}
```

with:

```go
type Hooks struct {
	Stop []string `yaml:"stop"`
	// PreToolUse — фазовые ограждения записи. В отличие от Stop, скрипт хука
	// не копируется адаптером отдельно: он живёт внутри скилла, который роль
	// и так подключает (Command называет путь вида "skills/<скилл>/...",
	// проверяется ниже), и адаптер лишь подставляет его абсолютный путь
	// внутри собранного плагина в момент запуска (internal/adapters/claude).
	PreToolUse []PreToolUseHook `yaml:"pre_tool_use"`
}

// PreToolUseHook — один matcher/command из hooks.pre_tool_use.
//
// Command — это то, что в итоге исполнит шелл, только с двумя подстановками
// позже, уже в адаптере: первое слово (путь вида "skills/<скилл>/...")
// становится абсолютным путём внутри собранного плагина, а буквальная
// подстрока "$WORKDIR" — реальным workdir запуска. Ни то, ни другое здесь ещё
// не подставляется: role.yaml не знает ни каталога плагина, ни workdir.
type PreToolUseHook struct {
	Matcher string `yaml:"matcher"`
	Command string `yaml:"command"`
}
```

Then, in `(r Role) validate(dirName string) error`, add this block right after the existing `for _, path := range r.HookFiles() { ... }` loop (around line 161, before `return errors.Join(errs...)`):

```go
	// hooks.pre_tool_use ссылается на файл внутри скилла, а не внутри hooks/:
	// сам скрипт (comet-hook-router.mjs) — часть вендоренного скилла, который
	// роль и так обязана подключить (design doc: "A role that declares
	// hooks.pre_tool_use without mounting the referenced skill is a config
	// error the loader should reject at role-load time").
	for i, h := range r.Hooks.PreToolUse {
		switch {
		case strings.TrimSpace(h.Matcher) == "":
			errs = append(errs, fmt.Errorf("hooks.pre_tool_use[%d].matcher пуст", i))
		case strings.TrimSpace(h.Command) == "":
			errs = append(errs, fmt.Errorf("hooks.pre_tool_use[%d].command пуст", i))
		default:
			script, _, _ := strings.Cut(h.Command, " ")
			skill, ok := skillFromHookScript(script)
			if !ok {
				errs = append(errs, fmt.Errorf(
					"hooks.pre_tool_use[%d].command=%q: путь должен начинаться с %s/<скилл>/",
					i, h.Command, SkillsDir))
				continue
			}
			if !slices.Contains(r.Skills, skill) {
				errs = append(errs, fmt.Errorf(
					"hooks.pre_tool_use[%d] ссылается на скилл %q, а его нет в skills:", i, skill))
			}
			switch fi, err := os.Stat(filepath.Join(r.configRoot, script)); {
			case err != nil:
				errs = append(errs, fmt.Errorf("hooks.pre_tool_use[%d]: файл хука не найден: %w", i, err))
			case fi.IsDir():
				errs = append(errs, fmt.Errorf("hooks.pre_tool_use[%d]: %s — каталог, а не файл", i, script))
			}
		}
	}
```

Add this helper near `SkillDirs`/`HookFiles` (below `HookFiles`, around line 215):

```go
// skillFromHookScript достаёт имя скилла из пути вида "skills/<имя>/...".
// Второе возвращаемое значение — false, если путь не такой формы вообще
// (не начинается с SkillsDir).
func skillFromHookScript(path string) (string, bool) {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) < 2 || parts[0] != SkillsDir {
		return "", false
	}
	return parts[1], true
}
```

`slices` is already imported in `role.go` (used by `slices.Contains` elsewhere in `validate`) — no new import needed.

- [x] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/runner/... -v -run TestLoadRole
```

Expected: all `TestLoadRoleRejects` subtests pass (including the three new ones), `TestLoadRoleRejectsPreToolUseMissingScript` and `TestLoadRoleAcceptsPreToolUseHook` pass, and every previously-passing test in the file still passes (run `go test ./internal/runner/...` with no `-run` filter to confirm no regression, including `TestShippedRolesAreValid` — no shipped role declares `pre_tool_use` yet, so it stays green here and only becomes meaningful once Tasks 9–11 land).

- [x] **Step 5: Commit**

```bash
git add internal/runner/role.go internal/runner/role_test.go
git commit -m "feat(runner): add hooks.pre_tool_use to the role schema"
```

---

## Task 3 (tasks.md 2.1 adapter half, 2.2): `internal/adapters/claude/adapter.go` — wire `PreToolUse`

**Files:**
- Modify: `internal/adapters/claude/adapter.go`
- Modify: `internal/adapters/claude/adapter_test.go`

**Interfaces:**
- Consumes: `runner.Role.Hooks.PreToolUse []runner.PreToolUseHook` (Task 2).
- Produces: `settings.json`'s `hooks.PreToolUse` array, one `hookMatcher{Matcher, Hooks: [...]}` per declared entry, with `Command` fully resolved (absolute path inside the built plugin, `$WORKDIR` substituted) — the shape Claude Code's own `PreToolUse` hook mechanism expects, verified directly against a synthetic payload during design (design doc Evidence base item 8).

- [x] **Step 1: Write the failing test**

In `internal/adapters/claude/adapter_test.go`, add `"encoding/json"` to the import block, then add this fixture and test after `TestBuildAddsSkillToolWhenRoleUsesSkills`:

```go
const preToolUseRoleYAML = `name: tester
prompt: role.md
includes:
  - ../_base/base.md
skills: [comet]
tools:
  allow: ["Read", "Write", "Bash(git *)"]
  deny: []
hooks:
  stop:
    - hooks/require-result.sh
  pre_tool_use:
    - matcher: "Write|Edit"
      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root "$WORKDIR"
limits:
  max_turns: 5
  timeout_sec: 60
result_file: .agent/result.json
`

// Хук-роутер Comet enforces фазовые границы записи технически (design doc
// "Phase-scoped writes are hook-enforced"), и адаптер обязан передать его
// агенту с уже разрешённым абсолютным путём — внутри собранного плагина
// голая команда "skills/comet/..." не значит ничего для шелла.
func TestBuildWiresPreToolUseHook(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "ключ")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	root := fixtureOffice(t, preToolUseRoleYAML, "comet")
	scriptDir := filepath.Join(root, "skills", "comet", "scripts")
	if err := os.MkdirAll(scriptDir, 0o755); err != nil {
		t.Fatalf("каталог скрипта не создан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "comet-hook-router.mjs"), []byte("#!/usr/bin/env node\n"), 0o644); err != nil {
		t.Fatalf("скрипт хука не записан: %v", err)
	}

	role, err := runner.LoadRole(root, "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}
	workdir := t.TempDir()
	launch, err := Build(role, workdir, runner.Run{RunID: "id", Role: "tester"}, stubValidator(t))
	if err != nil {
		t.Fatalf("запуск не собран: %v", err)
	}
	t.Cleanup(func() { _ = launch.Cleanup() })

	var s settingsFile
	if err := json.Unmarshal([]byte(launch.Settings), &s); err != nil {
		t.Fatalf("настройки не разобраны: %v", err)
	}
	matchers, found := s.Hooks["PreToolUse"]
	if !found || len(matchers) != 1 || len(matchers[0].Hooks) != 1 {
		t.Fatalf("хука PreToolUse нет в настройках:\n%s", launch.Settings)
	}
	if matchers[0].Matcher != "Write|Edit" {
		t.Errorf("matcher=%q, ожидался Write|Edit", matchers[0].Matcher)
	}

	command := matchers[0].Hooks[0].Command
	pluginDir := argValue(t, launch.Argv, "--plugin-dir")
	wantScript := filepath.Join(pluginDir, "skills", "comet", "scripts", "comet-hook-router.mjs")
	if !strings.Contains(command, wantScript) {
		t.Errorf("команда хука не указывает на собранный плагин:\nхотели %s\nполучили %s", wantScript, command)
	}
	if !strings.Contains(command, workdir) {
		t.Errorf("$WORKDIR не подставлен: %s", command)
	}
	if strings.Contains(command, "$WORKDIR") {
		t.Errorf("литеральный $WORKDIR остался в команде: %s", command)
	}
	// Stop-хук остаётся на месте — новый тип не вытесняет старый.
	if _, found := s.Hooks["Stop"]; !found {
		t.Error("PreToolUse вытеснил Stop из настроек")
	}
}
```

- [x] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/adapters/claude/... -run TestBuildWiresPreToolUseHook -v
```

Expected: compile error or failure — `hookMatcher` has no `Matcher` field yet, and `buildSettings` doesn't emit a `"PreToolUse"` key.

- [x] **Step 3: Reorder `Build()` so `pluginDir` exists before `buildSettings` runs**

In `internal/adapters/claude/adapter.go`, `Build()` currently computes `settings`/`settingsPath` *before* `tools`/`pluginDir` (lines ~120-151). `pluginDir` must exist first: a `hooks.pre_tool_use` command's leading path resolves against it. Replace this whole block:

```go
	hooks, err := copyHooks(roleDir, role, validator)
	if err != nil {
		return abort(err)
	}

	settings, err := buildSettings(role, workdir, hooks)
	if err != nil {
		return abort(err)
	}
	settingsPath := filepath.Join(roleDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(settings), 0o644); err != nil {
		return abort(fmt.Errorf("settings.json не записан: %w", err))
	}

	// Набор инструментов складывается из двух источников: что разрешила роль
	// и что задействовал сам раннер. Второе в tools.allow не пишут — роль
	// перечисляет там работу с файлами и командами, а не механизм подгрузки
	// скиллов, — но без него подключённые скиллы агенту нечем вызвать.
	tools := toolNames(role.Tools.Allow)
	if !slices.Contains(tools, WriteTool) {
		tools = append(tools, WriteTool)
	}

	pluginDir := ""
	if len(role.Skills) > 0 {
		if pluginDir, err = buildPlugin(roleDir, role); err != nil {
			return abort(err)
		}
		if !slices.Contains(tools, SkillTool) {
			tools = append(tools, SkillTool)
		}
	}
```

with:

```go
	hooks, err := copyHooks(roleDir, role, validator)
	if err != nil {
		return abort(err)
	}

	// Набор инструментов складывается из двух источников: что разрешила роль
	// и что задействовал сам раннер. Второе в tools.allow не пишут — роль
	// перечисляет там работу с файлами и командами, а не механизм подгрузки
	// скиллов, — но без него подключённые скиллы агенту нечем вызвать.
	tools := toolNames(role.Tools.Allow)
	if !slices.Contains(tools, WriteTool) {
		tools = append(tools, WriteTool)
	}

	// pluginDir считается раньше settings.json: команда hooks.pre_tool_use
	// (например, comet-hook-router.mjs) ссылается на путь внутри уже
	// собранного плагина, и buildSettings должен знать этот путь заранее,
	// а не достраивать его вторым проходом.
	pluginDir := ""
	if len(role.Skills) > 0 {
		if pluginDir, err = buildPlugin(roleDir, role); err != nil {
			return abort(err)
		}
		if !slices.Contains(tools, SkillTool) {
			tools = append(tools, SkillTool)
		}
	}

	settings, err := buildSettings(role, workdir, pluginDir, hooks)
	if err != nil {
		return abort(err)
	}
	settingsPath := filepath.Join(roleDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(settings), 0o644); err != nil {
		return abort(fmt.Errorf("settings.json не записан: %w", err))
	}
```

Everything below this block in `Build()` (the `argv` construction, `Workspaces`, `launch.Skills`) already reads `tools`/`pluginDir` further down and needs no change — only their point of computation moved earlier.

- [x] **Step 4: Extend `hookMatcher` and `buildSettings`**

Replace the `hookMatcher` struct (around line 332-334):

```go
type hookMatcher struct {
	Hooks []hookCommand `json:"hooks"`
}
```

with:

```go
type hookMatcher struct {
	// Matcher — пусто у Stop (он не фильтрует по инструменту, и сегодняшнее
	// поведение не должно измениться ни одним лишним байтом в JSON); у
	// PreToolUse — регэксп вида "Write|Edit".
	Matcher string        `json:"matcher,omitempty"`
	Hooks   []hookCommand `json:"hooks"`
}
```

Replace the `buildSettings` signature and body (around lines 345-377):

```go
func buildSettings(role runner.Role, workdir string, hooks []string) (string, error) {
	resultPath := filepath.Join(workdir, role.ResultFile)

	allow := append(slices.Clone(role.Tools.Allow), FileRule+"(/"+resultPath+")")
	s := settingsFile{
		Permissions: permissionsBlock{Allow: allow, Deny: role.Tools.Deny},
	}

	commands := make([]hookCommand, 0, len(hooks))
	for _, script := range hooks {
		commands = append(commands, hookCommand{
			Type:    "command",
			Command: shellQuote(script) + " " + shellQuote(resultPath),
			Timeout: 60,
		})
	}
	if len(commands) > 0 {
		s.Hooks = map[string][]hookMatcher{"Stop": {{Hooks: commands}}}
	}

	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("settings.json не сериализован: %w", err)
	}
	return string(raw) + "\n", nil
}
```

with:

```go
func buildSettings(role runner.Role, workdir, pluginDir string, hooks []string) (string, error) {
	resultPath := filepath.Join(workdir, role.ResultFile)

	allow := append(slices.Clone(role.Tools.Allow), FileRule+"(/"+resultPath+")")
	s := settingsFile{
		Permissions: permissionsBlock{Allow: allow, Deny: role.Tools.Deny},
	}

	commands := make([]hookCommand, 0, len(hooks))
	for _, script := range hooks {
		commands = append(commands, hookCommand{
			Type:    "command",
			Command: shellQuote(script) + " " + shellQuote(resultPath),
			Timeout: 60,
		})
	}
	if len(commands) > 0 {
		s.Hooks = map[string][]hookMatcher{"Stop": {{Hooks: commands}}}
	}

	// PreToolUse не копируется адаптером отдельно, в отличие от Stop: скрипт
	// (comet-hook-router.mjs) уже лежит внутри собранного плагина, потому что
	// role.go's validate() требует, чтобы его скилл был подключён в skills: —
	// buildPlugin() его туда и кладёт. Здесь только резолвится путь и
	// подставляется $WORKDIR.
	if preToolUse := role.Hooks.PreToolUse; len(preToolUse) > 0 {
		matchers := make([]hookMatcher, 0, len(preToolUse))
		for _, ptu := range preToolUse {
			matchers = append(matchers, hookMatcher{
				Matcher: ptu.Matcher,
				Hooks: []hookCommand{{
					Type:    "command",
					Command: resolvePreToolUseCommand(ptu.Command, pluginDir, workdir),
					Timeout: 60,
				}},
			})
		}
		if s.Hooks == nil {
			s.Hooks = map[string][]hookMatcher{}
		}
		s.Hooks["PreToolUse"] = matchers
	}

	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", fmt.Errorf("settings.json не сериализован: %w", err)
	}
	return string(raw) + "\n", nil
}

// resolvePreToolUseCommand переводит объявленную ролью команду хука в то, что
// реально выполнится: первое слово команды — путь внутри собранного плагина
// (role.yaml пишет его относительно skills/, здесь он становится абсолютным
// от pluginDir и заворачивается в кавычки как и путь Stop-хука), а
// буквальная подстрока "$WORKDIR" — в реальный workdir этого запуска. Обе
// подстановки делаются только здесь: до Build() не существует ни pluginDir,
// ни workdir.
func resolvePreToolUseCommand(command, pluginDir, workdir string) string {
	script, args, hasArgs := strings.Cut(command, " ")
	resolved := shellQuote(filepath.Join(pluginDir, script))
	if hasArgs {
		resolved += " " + args
	}
	return strings.ReplaceAll(resolved, "$WORKDIR", workdir)
}
```

- [x] **Step 5: Run the test to verify it passes**

```bash
go test ./internal/adapters/claude/... -v -run TestBuildWiresPreToolUseHook
```

Then run the whole package to confirm no regression, especially the hook/settings/plugin tests that existed before this task:

```bash
go test ./internal/adapters/claude/... -v
```

Expected: `TestBuildSettings`, `TestBuildCopiesHooksOutOfConfigRepo`, `TestStopHookGuardsResultFile`, `TestBuildCopiesOnlyRoleSkills`, `TestBuildAddsSkillToolWhenRoleUsesSkills`, `TestBuildWithoutSkillsSkipsPlugin` all still pass — the reordering in Step 3 must not change `pluginDir`'s value or `tools`' contents for any of them, only the order the two are computed in.

- [x] **Step 6: Commit**

```bash
git add internal/adapters/claude/adapter.go internal/adapters/claude/adapter_test.go
git commit -m "feat(adapter): wire hooks.pre_tool_use into settings.json"
```

---

## Task 4: `internal/runner/change.go` — Comet Native change-root helpers

**Files:**
- Modify: `internal/runner/change.go`
- Modify: `internal/runner/change_test.go`

**Interfaces:**
- Produces: `runner.CometChangeName(taskKey string) string` and `runner.CometChangeDirRel(taskKey string) string` — consumed by Task 5 (`prpass.go`), Task 6 (`input.go`), and every role's `role.md` text (Tasks 9–11, which describe `<name>` as "the same safe key the runner already computes").

- [x] **Step 1: Write the failing tests**

Append to `internal/runner/change_test.go`:

```go
func TestCometChangeName(t *testing.T) {
	cases := map[string]string{
		"OFF-1":     "OFF-1",
		"":          "_manual",
		"../../etc": ".._.._etc",
	}
	for key, want := range cases {
		if got := CometChangeName(key); got != want {
			t.Errorf("CometChangeName(%q) = %q, ожидалось %q", key, got, want)
		}
	}
}

func TestCometChangeDirRel(t *testing.T) {
	cases := map[string]string{
		"OFFICE-1": "docs/comet/changes/OFFICE-1",
		"":         "docs/comet/changes/_manual",
	}
	for key, want := range cases {
		if got := CometChangeDirRel(key); got != want {
			t.Errorf("CometChangeDirRel(%q) = %q, ожидалось %q", key, got, want)
		}
	}
}
```

- [x] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/runner/... -run TestCometChange -v
```

Expected: compile error — `CometChangeName`/`CometChangeDirRel` don't exist yet.

- [x] **Step 3: Implement, refactoring the shared "safe name or manual" logic out of `ChangeDirRel`**

In `internal/runner/change.go`, replace:

```go
// ChangeDirRel — каталог изменения задачи, относительно корня рабочей папки.
func ChangeDirRel(taskKey string) string {
	name := safeKey(taskKey)
	if name == "" {
		name = ManualChange
	}
	return filepath.Join(ChangesDir, name)
}

// ChangeDir — то же абсолютным путём.
func ChangeDir(workdir, taskKey string) string {
	return filepath.Join(workdir, ChangeDirRel(taskKey))
}
```

with:

```go
// changeName — безопасное имя каталога изменения из ключа задачи, общее для
// старого и нового корня: тот же ключ адресует одну и ту же задачу в обоих.
func changeName(taskKey string) string {
	name := safeKey(taskKey)
	if name == "" {
		name = ManualChange
	}
	return name
}

// ChangeDirRel — каталог изменения задачи, относительно корня рабочей папки.
func ChangeDirRel(taskKey string) string {
	return filepath.Join(ChangesDir, changeName(taskKey))
}

// ChangeDir — то же абсолютным путём.
func ChangeDir(workdir, taskKey string) string {
	return filepath.Join(workdir, ChangeDirRel(taskKey))
}

// CometChangesDir — новый корень каталогов изменений для analyst/implementer/
// reviewer, которые ведут задачу через Comet Native (Shape/Build/Verify).
// ChangesDir (docs/changes) остаётся вторым источником — для задач, чью
// Shape-фазу analyst прошёл ещё до этого перехода, и для ручных прогонов вне
// комет-конвейера.
const CometChangesDir = "docs/comet/changes"

// CometChangeName — <name> изменения Comet Native задачи: тот же безопасный
// ключ, что и у старого каталога, но отдельно от корня — его использует и
// путь в git (CometChangeDirRel), и сама команда `comet native ... <name>`,
// которой каталог, а не только имя, ни к чему.
func CometChangeName(taskKey string) string {
	return changeName(taskKey)
}

// CometChangeDirRel — каталог изменения Comet Native задачи, относительно
// корня рабочей папки.
func CometChangeDirRel(taskKey string) string {
	return filepath.Join(CometChangesDir, changeName(taskKey))
}
```

- [x] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/runner/... -run TestChangeDirRel -v
go test ./internal/runner/... -run TestCometChange -v
```

Expected: both old (`TestChangeDirRel`, unmodified) and new tests pass — the refactor must not change `ChangeDirRel`'s existing output for any key.

- [x] **Step 5: Commit**

```bash
git add internal/runner/change.go internal/runner/change_test.go
git commit -m "feat(runner): add Comet Native change-root helpers"
```

---

## Task 5 (tasks.md 5.1, plus the `prpass.go` half of "Runner changes"): `internal/pipeline/prpass.go` + new `internal/pipeline/archive.go`

This is the task the "archive before the PR, wired alongside, not an afterthought" constraint is about: the `prBody` root fix and the archive-before-PR wiring land together, in one task, because both touch `openPR` and the design doc treats them as one correction.

**Files:**
- Modify: `internal/pipeline/prpass.go`
- Create: `internal/pipeline/archive.go`
- Modify: `internal/pipeline/prpass_test.go`
- Create: `internal/pipeline/archive_test.go`

**Interfaces:**
- Consumes: `runner.CometChangeName`/`runner.CometChangeDirRel` (Task 4), `runner.FileBrief`, `workspace.Manager.{Ensure,Push}`, `workspace.ErrWorktreeBusy`, `tracker.Task.Ref()`.
- Produces: `(o *Office) archiveIfReady(task tracker.Task, project tracker.Project) (ok bool, err error)` — consumed only by `openPR` in this same task; not exported outside the package.

- [x] **Step 1: Write the failing `prBody` fallback tests**

In `internal/pipeline/prpass_test.go`, add these two tests right after `TestPRPassBodyFallsBackToTicket`:

```go
// Постановка Comet Native — новый, предпочтительный источник тела pull
// request; старый docs/changes/<KEY> не должен побеждать его, если оба есть.
func TestPRPassBodyPrefersCometChangeOverLegacy(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/3", state: forge.Open})
	o.agent.work = func(req Request) {
		writes(filepath.Join(runner.ChangeDirRel("OFF-1"), runner.FileBrief),
			"Старая постановка.\n")(req)
		writes(filepath.Join(runner.CometChangeDirRel("OFF-1"), runner.FileBrief),
			"Цель: считать среднее через Comet.\n")(req)
	}
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)

	got := f.opened[0].body
	if !strings.Contains(got, "Цель: считать среднее через Comet") {
		t.Errorf("постановка Comet Native не попала в тело:\n%s", got)
	}
	if strings.Contains(got, "Старая постановка") {
		t.Errorf("старая постановка не должна побеждать новую:\n%s", got)
	}
}

// Задача, которую analyst вёл до перехода на Comet Native, хранит постановку
// в старом корне — prBody не должен молча забыть про неё только потому, что
// нового корня нет.
func TestPRPassBodyFallsBackToLegacyChangeDir(t *testing.T) {
	o := newOffice(t)
	f := o.withForge(&fakeForge{url: "https://github.test/kao73/client/pull/4", state: forge.Open})
	o.agent.work = writes(filepath.Join(runner.ChangeDirRel("OFF-1"), runner.FileBrief),
		"Цель: старая постановка ещё жива.\n")
	o.agent.commit = "работа автора"
	o.approved(t, "OFF-1")

	o.pass(t)

	if !strings.Contains(f.opened[0].body, "Цель: старая постановка ещё жива") {
		t.Errorf("старая постановка не подхвачена как fallback:\n%s", f.opened[0].body)
	}
}
```

- [x] **Step 2: Run the tests to verify they fail as expected**

```bash
go test ./internal/pipeline/... -run TestPRPassBody -v
```

Expected: `TestPRPassBodyPrefersCometChangeOverLegacy` fails (the new-root brief isn't read at all yet — `prBody` still only checks `runner.ChangeDirRel`, so it would actually pick up "Старая постановка" and the test's negative assertion fails); `TestPRPassBodyFallsBackToLegacyChangeDir` already passes today (no regression to prove yet, but keep it — it becomes the regression guard for Step 3).

- [x] **Step 3: Fix `prBody`'s root order**

In `internal/pipeline/prpass.go`, replace the body-reading part of `prBody` (lines ~298-308):

```go
	brief, found, err := o.Workspaces.Show(repo, project.Branch(task.Key),
		filepath.Join(runner.ChangeDirRel(task.Key), runner.FileBrief))
	if err != nil {
		return "", "", err
	}
	if !found {
		// Постановки в ветке нет — задача пришла мимо аналитика. Тогда телом идёт
		// сам тикет целиком, вместе с темой: в заголовке она есть, но тело pull
		// request читают и отдельно от него.
		brief = task.Summary + "\n\n" + task.Description
	}
```

with:

```go
	brief, found, err := o.Workspaces.Show(repo, project.Branch(task.Key),
		filepath.Join(runner.CometChangeDirRel(task.Key), runner.FileBrief))
	if err != nil {
		return "", "", err
	}
	if !found {
		// Новый корень Comet Native пуст — задача либо старше этого перехода
		// (analyst вёл её через прежний docs/changes/<KEY>), либо пришла мимо
		// аналитика вовсе. Второй, старый корень остаётся источником, пока
		// первый не подтвердил свою пустоту, а не наоборот.
		brief, found, err = o.Workspaces.Show(repo, project.Branch(task.Key),
			filepath.Join(runner.ChangeDirRel(task.Key), runner.FileBrief))
		if err != nil {
			return "", "", err
		}
	}
	if !found {
		// Ни в одном из корней постановки нет — задача пришла мимо аналитика.
		// Тогда телом идёт сам тикет целиком, вместе с темой: в заголовке она
		// есть, но тело pull request читают и отдельно от него.
		brief = task.Summary + "\n\n" + task.Description
	}
```

- [x] **Step 4: Run the `prBody` tests to verify they pass**

```bash
go test ./internal/pipeline/... -run TestPRPassBody -v
```

Expected: both new tests pass, and `TestPRPassBodyCarriesBriefAndReport`/`TestPRPassBodyFallsBackToTicket` (which only ever populate `runner.ChangeDirRel`, the legacy root) still pass unmodified — they now exercise the fallback branch rather than the sole branch, with identical observable output.

- [x] **Step 5: Write the failing archive tests**

Create `internal/pipeline/archive_test.go`:

```go
package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeComet подкладывает на PATH подложный `comet`: на "native status ... --json"
// печатает {"phase": phase}, на "native archive ..." создаёт файл-метку и
// возвращает её путь. Он предваряет системный PATH, а не заменяет его: git,
// которым archiveIfReady пользуется через Ensure/Push, обязан остаться
// доступным.
func fakeComet(t *testing.T, phase string) (archivedMarker string) {
	t.Helper()
	binDir := t.TempDir()
	archivedMarker = filepath.Join(t.TempDir(), "archived")

	script := "#!/bin/sh\n" +
		"case \"$2\" in\n" +
		"  status) echo \"{\\\"phase\\\":\\\"" + phase + "\\\"}\" ;;\n" +
		"  archive) : > \"" + archivedMarker + "\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(binDir, "comet"), []byte(script), 0o755); err != nil {
		t.Fatalf("подложный comet не записан: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return archivedMarker
}

func TestArchiveIfReadyRunsArchiveWhenPhaseMatches(t *testing.T) {
	o := newOffice(t)
	marker := fakeComet(t, archiveReadyPhase)
	task := o.approved(t, "OFF-1")

	ok, err := o.archiveIfReady(task, o.Projects["OFF"])
	if err != nil || !ok {
		t.Fatalf("archiveIfReady = %v, %v", ok, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("comet native archive не был вызван: %v", err)
	}
}

func TestArchiveIfReadySkipsWhenPhaseNotReady(t *testing.T) {
	o := newOffice(t)
	marker := fakeComet(t, "verify")
	task := o.approved(t, "OFF-1")

	ok, err := o.archiveIfReady(task, o.Projects["OFF"])
	if err != nil || !ok {
		t.Fatalf("archiveIfReady = %v, %v", ok, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("comet native archive вызван, хотя фаза не archive-ready")
	}
}

// Отсутствие comet на этой машине — известный, ещё не решённый пробел
// (tasks.md 1.1), а не повод никогда не открывать pull request.
func TestArchiveIfReadyIsNonBlockingWithoutComet(t *testing.T) {
	o := newOffice(t)
	old := cometExecutable
	cometExecutable = "comet-not-installed-in-tests"
	t.Cleanup(func() { cometExecutable = old })
	task := o.approved(t, "OFF-1")

	ok, err := o.archiveIfReady(task, o.Projects["OFF"])
	if err != nil || !ok {
		t.Fatalf("отсутствие comet не должно блокировать PR-проход: ok=%v, err=%v", ok, err)
	}
}

// Рабочая папка занята — единственный случай, где archiveIfReady просит
// openPR подождать следующего прохода, а не открывать PR без архивирования.
func TestArchiveIfReadyDefersWhenWorktreeBusy(t *testing.T) {
	o := newOffice(t)
	task := o.approved(t, "OFF-1")
	project := o.Projects["OFF"]

	ws, err := o.Workspaces.Ensure(task.Ref(), project)
	if err != nil {
		t.Fatalf("рабочая папка не занята для теста: %v", err)
	}
	defer ws.Unlock()

	ok, err := o.archiveIfReady(task, project)
	if err != nil {
		t.Fatalf("archiveIfReady вернул ошибку вместо мягкого отказа: %v", err)
	}
	if ok {
		t.Error("archiveIfReady должен был отступить: рабочая папка занята")
	}
}
```

- [x] **Step 6: Run the tests to verify they fail**

```bash
go test ./internal/pipeline/... -run TestArchiveIfReady -v
```

Expected: compile error — `archiveIfReady`, `cometExecutable`, `archiveReadyPhase` don't exist yet.

- [x] **Step 7: Implement `internal/pipeline/archive.go`**

```go
package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/workspace"
)

// cometExecutable — CLI Comet Native в PATH раннера. Архивирование — код
// раннера (см. openPR в prpass.go), а не роли: оно бежит на машине/хосте, где
// крутится office, а не в песочнице агента — отдельное требование к
// хозяйству раннера, ещё не решённое (docs/notes/sbx.md, tasks.md 1.1).
//
// var, а не const: тесты подменяют его на заведомо отсутствующее имя, чтобы
// проверить, что недостающая зависимость не блокирует PR-проход, не трогая
// PATH целиком — Ensure/Push этой же функции нужен настоящий git.
var cometExecutable = "comet"

// archiveReadyPhase — значение фазы Comet Native изменения, при котором
// reviewer уже передал прошедший final-result и Archive можно запускать:
// "Native's own state moves to archive-ready, which the runner's PR pass
// reads before opening the PR" (design doc, roles/reviewer, «Outcome»).
//
// Это единственное место, где design doc называет значение фазы дословно, а
// не только структуру; подтверждается первым же живым прогоном этого шага
// (план, задача «Full regression + live golden-case run») — расхождение
// правится одной строкой здесь.
const archiveReadyPhase = "archive-ready"

// cometStatus — часть вывода `comet native status <name> --json`, нужная
// раннеру. Остальные поля (loop, blockers, …) читает сама роль внутри своей
// сессии; раннеру среди них важна только фаза.
type cometStatus struct {
	Phase string `json:"phase"`
}

// archiveIfReady запускает детерминированный шаг Archive Comet Native —
// прежде чем открыть pull request, а не после того, как человек его сольёт:
// иначе раннеру пришлось бы пушить нерецензированный коммит прямо в ветку по
// умолчанию, а сливает её только человек (DESIGN.md §2.8). До открытия PR
// архивный коммит — обычный коммит в той же ветке, которую и так предстоит
// слить.
//
// Работает не в bare-клоне, которым до сих пор обходился openPR: comet читает
// и пишет собственное состояние фазы рабочего дерева, которого у bare-клона
// нет. archiveIfReady поэтому берёт ту же рабочую папку, что и роли (Ensure
// переиспользует уже существующую, если она жива) и публикует результат тем
// же Push, каким роли публикуют свою работу — тем же путём, каким
// internal/pipeline/pipeline.go's work() уже действует после каждого прогона.
//
// ok=false просит openPR подождать следующего прохода: рабочая папка занята
// прямо сейчас (роль работает в ней), и лезть под чужой замок нельзя. Любая
// другая беда — comet не найден на этой машине, статус не читается, сама
// команда упала — не блокирует pull request: это внешняя зависимость, чей
// бутстрап на хосте раннера этим изменением не решён (tasks.md 1.1), и
// оставлять задачи без pull request до её появления нельзя.
func (o *Office) archiveIfReady(task tracker.Task, project tracker.Project) (ok bool, err error) {
	ws, err := o.Workspaces.Ensure(task.Ref(), project)
	if errors.Is(err, workspace.ErrWorktreeBusy) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer o.unlock(task.Key, ws)

	name := runner.CometChangeName(task.Key)
	status, err := cometNativeStatus(ws.Dir, name)
	if err != nil {
		o.logf("%s: comet native status не прочитан, архивирование пропущено: %v", task.Key, err)
		return true, nil
	}
	if status.Phase != archiveReadyPhase {
		return true, nil // Verify ещё не отдал прошедший final-result
	}

	if _, err := runComet(ws.Dir, "native", "archive", name, "--confirmed", "--finish", "keep"); err != nil {
		o.logf("%s: comet native archive не выполнен: %v", task.Key, err)
		return true, nil
	}
	if _, err := o.Workspaces.Push(ws); err != nil {
		o.logf("%s: коммит архивирования не опубликован: %v", task.Key, err)
	} else {
		o.logf("%s: изменение %s заархивировано", task.Key, name)
	}
	return true, nil
}

// cometNativeStatus разбирает `comet native status <name> --json`.
func cometNativeStatus(dir, name string) (cometStatus, error) {
	out, err := runComet(dir, "native", "status", name, "--json")
	if err != nil {
		return cometStatus{}, err
	}
	var s cometStatus
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		return cometStatus{}, fmt.Errorf("вывод comet native status не разобран: %w", err)
	}
	return s, nil
}

// runComet выполняет comet в рабочей папке задачи и возвращает stdout.
func runComet(dir string, args ...string) (string, error) {
	cmd := exec.Command(cometExecutable, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("comet %s: %w\n%s", strings.Join(args, " "), err, exitErr.Stderr)
		}
		return "", fmt.Errorf("comet %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}
```

- [x] **Step 8: Wire `archiveIfReady` into `openPR`**

In `internal/pipeline/prpass.go`, `openPR` (around line 101-108), insert the call right after the forge is confirmed known and before `prBody` is computed:

```go
	impl, known := o.Forges[project.Forge]
	if !known {
		o.logf("%s: forge %q не собран, задача остаётся на месте", task.Key, project.Forge)
		return nil
	}

	if ok, err := o.archiveIfReady(task, project); err != nil {
		return err
	} else if !ok {
		// Рабочая папка занята прямо сейчас — pull request подождёт
		// следующего прохода, а не откроется без архивирования.
		return nil
	}

	title, body, err := o.prBody(task, repo, project)
```

- [x] **Step 9: Run the archive tests, then the full package, to verify green**

```bash
go test ./internal/pipeline/... -run TestArchiveIfReady -v
go test ./internal/pipeline/... -v
```

Expected: all four new archive tests pass. Every pre-existing `prpass_test.go`/`pipeline_test.go` test also still passes: `archiveIfReady` finds no real `comet` binary in the ordinary test environment (unless a test explicitly installs the fake one), fails fast inside `runComet`, logs, and returns `(true, nil)` — `openPR` proceeds to open the pull request exactly as it did before this task, for every test that doesn't touch the fake `comet`.

- [x] **Step 10: Commit**

```bash
git add internal/pipeline/prpass.go internal/pipeline/archive.go internal/pipeline/prpass_test.go internal/pipeline/archive_test.go
git commit -m "feat(pipeline): archive Comet Native changes before opening the pull request"
```

---

## Task 6: `internal/runner/input.go` — dual-root "Каталог изменения" context line

**Files:**
- Modify: `internal/runner/input.go`
- Modify: `internal/runner/input_test.go`

**Interfaces:**
- Consumes: `CometChangeDirRel` (Task 4, same package — no import needed).
- Produces: the same "Каталог изменения: `<path>`" context line implementer/reviewer already read, now preferring the new root.

- [x] **Step 1: Write the failing test**

In `internal/runner/input_test.go`, add this test right after `TestContextNamesChangeDirAndPlan`:

```go
// Новый корень Comet Native проверяется первым: задача, которую ведёт analyst
// через этот конвейер, найдётся там, даже если старый каталог тоже существует
// (например, остался от прежней задачи, использовавшей тот же workdir).
func TestContextPrefersCometChangeDirOverLegacy(t *testing.T) {
	workdir, role := gitRepo(t), fixtureRole(t)
	passport := fixturePassport()
	passport.TaskKey = "OFF-1"

	legacy := filepath.Join(workdir, ChangeDirRel(passport.TaskKey))
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatalf("старый каталог не создан: %v", err)
	}
	cometDir := filepath.Join(workdir, CometChangeDirRel(passport.TaskKey))
	if err := os.MkdirAll(cometDir, 0o755); err != nil {
		t.Fatalf("новый каталог не создан: %v", err)
	}

	if err := PrepareInput(workdir, role, passport, Input{Task: "Задача\n"}); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}
	context := read(t, workdir, FileContext)

	if !strings.Contains(context, "Каталог изменения: "+CometChangeDirRel(passport.TaskKey)) {
		t.Errorf("новый корень не назван в контексте:\n%s", context)
	}
	if strings.Contains(context, "Каталог изменения: "+ChangeDirRel(passport.TaskKey)) {
		t.Errorf("старый корень не должен побеждать новый, когда оба есть:\n%s", context)
	}
}
```

- [x] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/runner/... -run TestContextPrefersCometChangeDirOverLegacy -v
```

Expected: fails — `composeContext` still only checks `ChangeDirRel`, so it reports the legacy path.

- [x] **Step 3: Fix `composeContext`**

In `internal/runner/input.go`, replace (around lines 174-179):

```go
	if dir := ChangeDirRel(run.TaskKey); exists(filepath.Join(workdir, dir)) {
		fmt.Fprintf(&b, "- Каталог изменения: %s\n", dir)
		if plan := filepath.Join(dir, FileTasks); TrackedByGit(workdir, plan) {
			fmt.Fprintf(&b, "- План: %s\n", plan)
		}
	}
```

with:

```go
	// Новый корень Comet Native проверяется первым: задача, которую ведёт
	// analyst через этот конвейер, найдётся там. Старый docs/changes/<KEY>
	// остаётся вторым источником — для задач, чью Shape-фазу analyst прошёл
	// ещё до этого перехода. "План: <путь>/tasks.md" имеет смысл только у
	// старого корня: изменения Comet Native такого файла не пишут вовсе, и
	// строка там просто не появится — это не пробел, а точный ответ.
	dir := CometChangeDirRel(run.TaskKey)
	if !exists(filepath.Join(workdir, dir)) {
		if legacy := ChangeDirRel(run.TaskKey); exists(filepath.Join(workdir, legacy)) {
			dir = legacy
		}
	}
	if exists(filepath.Join(workdir, dir)) {
		fmt.Fprintf(&b, "- Каталог изменения: %s\n", dir)
		if plan := filepath.Join(dir, FileTasks); TrackedByGit(workdir, plan) {
			fmt.Fprintf(&b, "- План: %s\n", plan)
		}
	}
```

- [x] **Step 4: Run the tests to verify green**

```bash
go test ./internal/runner/... -run TestContext -v
```

Expected: the new test passes, and `TestContextNamesChangeDirAndPlan` (legacy-only, no new-root directory present) still passes unmodified — it now exercises the fallback branch with identical observable output.

- [x] **Step 5: Commit**

```bash
git add internal/runner/input.go internal/runner/input_test.go
git commit -m "fix(runner): prefer the Comet Native change root in run context"
```

---

## Task 7 (tasks.md 3.1, part 1): Vendor `skills/comet/`

**Files:**
- Create: `skills/comet/` (full copy of `@rpamis/comet@0.4.0-beta.18`'s shipped `comet` skill bundle)
- Create: `skills/comet/.source.yaml`

**Interfaces:**
- Produces: `skills/comet/` on disk, at the path `internal/runner.Role.SkillDirs()` resolves for `role.Skills` containing `"comet"` (`filepath.Join(configRoot, "skills", "comet")`) — consumed by Tasks 9–11 (`role.yaml`'s `skills:`) and by Task 3's `hooks.pre_tool_use` (`skills/comet/scripts/comet-hook-router.mjs` must exist there).

**Prerequisite:** a local Node 22+ and `npm` (this is the "developer's own machine" case Task 1 explicitly separates from the production sandbox gap — it does not depend on that gap being solved).

- [x] **Step 1: Install the pinned CLI locally and locate the skill's real files**

```bash
mkdir -p /tmp/comet-vendor && cd /tmp/comet-vendor
npm pack @rpamis/comet@0.4.0-beta.18 --pack-destination .
tar xzf rpamis-comet-0.4.0-beta.18.tgz
find package -maxdepth 5 -type d -iname "comet" -o -type d -iname "comet-native"
```

The npm package's own internal layout is not pinned by this plan (it was not inspected during design — only the CLI's behavior and compiled `dist/` output were). Whichever directory the `find` above turns up as the shipped `comet` skill (containing at least a `SKILL.md` and — per design doc — a `scripts/comet-hook-router.mjs`) is the one to copy in Step 2. If `npm pack`'s tarball does not contain the skill directories directly, install the package instead and search its installed tree:

```bash
npm install -g @rpamis/comet@0.4.0-beta.18
npm root -g
find "$(npm root -g)/@rpamis/comet" -maxdepth 5 -type d -iname "comet" -o -type d -iname "comet-native"
```

- [x] **Step 2: Copy the skill directory into this repo**

```bash
mkdir -p skills/comet
cp -R <located-comet-skill-dir>/. skills/comet/
```

- [x] **Step 3: Verify the copy is complete**

```bash
test -f skills/comet/SKILL.md
test -f skills/comet/scripts/comet-hook-router.mjs
```

Expected: both exit 0. If `comet-hook-router.mjs` is not directly under `scripts/` in the real package, find its actual relative path and use that path consistently in Task 3's test fixtures and in Tasks 9–11's `role.yaml` `hooks.pre_tool_use.command` — the design doc's own path (`skills/comet/scripts/comet-hook-router.mjs`) is what the design-phase experiments actually invoked, so treat a mismatch here as a signal to re-check against a fresh `npm view @rpamis/comet@0.4.0-beta.18` rather than silently diverging.

- [x] **Step 4: Determine the pinned tag/commit for `.source.yaml`**

```bash
npm view @rpamis/comet@0.4.0-beta.18 repository.url
git ls-remote --tags https://github.com/rpamis/comet.git | grep -i "0.4.0-beta.18"
```

- [x] **Step 5: Write `skills/comet/.source.yaml`**

```yaml
repository: https://github.com/rpamis/comet
tag: v0.4.0-beta.18
commit: <resolved commit from Step 4>
```

(Matches the shape of `skills/brainstorming/.source.yaml`/`skills/writing-plans/.source.yaml` exactly — no drift-check script, per this repo's existing precedent for vendored skills.)

- [x] **Step 6: Clean up and stage**

```bash
rm -rf /tmp/comet-vendor
git add skills/comet
git status --short
```

Expected: every file under `skills/comet/` shows as new (`A`), nothing else changed.

- [x] **Step 7: Commit**

```bash
git commit -m "chore(skills): vendor comet 0.4.0-beta.18"
```

---

## Task 8 (tasks.md 3.1, part 2): Vendor `skills/comet-native/`

**Files:**
- Create: `skills/comet-native/`
- Create: `skills/comet-native/.source.yaml`

**Interfaces:** Same as Task 7, for `role.Skills` containing `"comet-native"`.

- [x] **Step 1–3: Locate, copy, verify** — identical process to Task 7 Steps 1–3, using the `comet-native` directory found by the same `find` command (Task 7 Step 1 already searched for both). If `/tmp/comet-vendor` was already cleaned up by Task 7, re-run the `npm pack`/`tar xzf` from Task 7 Step 1 first.

```bash
mkdir -p skills/comet-native
cp -R <located-comet-native-skill-dir>/. skills/comet-native/
test -f skills/comet-native/SKILL.md
```

- [x] **Step 4: Write `skills/comet-native/.source.yaml`**

Same repository/tag/commit as Task 7's `skills/comet/.source.yaml` (both ship from the same npm package version):

```yaml
repository: https://github.com/rpamis/comet
tag: v0.4.0-beta.18
commit: <same commit as skills/comet/.source.yaml>
```

- [x] **Step 5: Stage and commit**

```bash
git add skills/comet-native
git status --short
git commit -m "chore(skills): vendor comet-native 0.4.0-beta.18"
```

---

## Task 9 (tasks.md 4.1): `roles/analyst/{role.yaml,role.md}`

**Files:**
- Modify: `roles/analyst/role.yaml`
- Modify: `roles/analyst/role.md`

**Interfaces:**
- Consumes: `skills/comet/`, `skills/comet-native/` (Tasks 7–8), `hooks.pre_tool_use` schema (Task 2), `runner.CometChangeName`/`CometChangeDirRel` (Task 4, referenced by name in prose — `role.md` is read by the agent, not compiled, so it names the path convention rather than importing Go).
- Produces: the Shape half of the phase-to-role mapping. `next_owner: implementer` on `done` is unchanged from today — no `workflow.yaml` change needed (analyst's own `by_next_owner` map already has `implementer: Ready`).

- [x] **Step 1: Replace `role.yaml`**

Replace `roles/analyst/role.yaml` in full:

```yaml
# Машиночитаемая спецификация роли. Агент её не видит: адаптер переводит её
# в нативные механизмы Claude Code.
name: analyst

prompt: role.md
includes:
  - ../_base/base.md

skills: [comet, comet-native]

# Синтаксис правил — как у claude: пробел перед * значим,
# Bash(git *diff*) поймал бы и git diff-index, и git -c x diff.
tools:
  # tools.allow не защищает: команда, не описанная ни в allow, ни в deny,
  # модель выполняет или нет по собственному суждению, а не по проверке
  # рантайма (измерено 2026-08-27, docs/notes/followup-network-and-permissions.md,
  # «Находка 2»; подробнее — docs/contracts/role-sandbox-permissions.md).
  # Единственная реальная граница — tools.deny ниже и defaults.tools.deny
  # в projects.yaml, плюс hooks.pre_tool_use ниже — технический, а не
  # текстовый барьер записи вне фазы build (design doc "Phase-scoped writes
  # are hook-enforced").
  allow:
    - Read
    - Grep
    - Glob
    - Edit
    - Write
    - "Bash(*)"

  # Общие для всех ролей (запись в origin, переключение веток, личность
  # коммитов) — в defaults.tools.deny (projects.yaml), не здесь: это
  # решение раннера для всех ролей и всех проектов разом, а не аналитика.
  # Здесь остаётся то, что специфично аналитику: `reset` откатывает чужие
  # коммиты (круг implementer → analyst оставляет их в ветке), а аналитику
  # откатывать чужое нечем и незачем — для своей ошибки есть `revert`.
  deny:
    - "Bash(git *reset*)"

hooks:
  stop:
    - hooks/require-result.sh   # не даёт завершиться без валидного result.json
  pre_tool_use:
    - matcher: "Write|Edit"
      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root "$WORKDIR"

# Куда писать и как соотноситься с другими ролями — решает текст role.md,
# а не поле контракта: технической проверки этому нет, роль пишет туда,
# куда написано в её собственной инструкции.

# Взяты с потолка — скопированы с ревьюера. Замеры их не опровергли: нагрузочный-2
# дал по аналитику восемь прогонов, в среднем 24 шага и $0.51
# (docs/notes/stage-4-load-2.md), а по всему реестру машины на 16 прогонах роли
# максимум — 41 шаг при пределе 50. В предел упирался только implementer (дважды
# по 51 шагу). Ожидание «самая контекстоёмкая из трёх» замер не подтвердил:
# дороже всех оказался тот, кто пишет код.
#
# Предел шагов — усечение, а не провал: прогон, упёршийся в max_turns или
# в таймаут, даёт event:run-truncated, попытка не тратится, а следующий прогон
# продолжает в той же рабочей папке. Серию таких прогонов считает отдельный
# счётчик (limits.max_idle_runs в workflow.yaml); подробности — в контракте
# docs/contracts/tracker-protocol.md.
#
# Число оставлено как есть: калибровать его есть чем только по живому проекту.
limits:
  max_turns: 50
  timeout_sec: 1800

result_file: .agent/result.json
```

(Only two things changed from the current file: `skills:` and the new `hooks.pre_tool_use` entry — everything else, including every comment, is unchanged.)

- [x] **Step 2: Replace the dispatcher section of `role.md`**

Replace everything from `## Диспетчер скиллов` through the end of that section (the current file's lines 17-68, ending right before `## Как коммитить`) with:

```markdown
## Comet Native: фаза Shape

Роли подключены два скилла — `comet` и `comet-native`. Это вендоренные,
неотредактированные копии CLI Comet Native: собственный протокол
уточнения/подтверждения Shape ты проходишь как он есть — ниже только то, что
решает границу между этим протоколом и офисом.

1. **Найди или заведи изменение.** `<name>` — тот же безопасный ключ задачи,
   каким его всегда собирал раннер (`internal/runner/change.go`, `safeKey`).
   Запусти `comet native status <name> --json`. Активного изменения нет —
   заведи его: `comet native new <name> --isolation current` (раннер уже
   изолировал задачу собственным worktree — изоляция Comet здесь была бы
   лишней). Изменение уже есть и стоит в фазе `build`, `verify` или `archive`
   — Shape по этой задаче уже пройден: обращайся с этим как с прежним
   правилом «пропуск повтора» — убедись, что существующие `brief.md` и
   `specs/<capability>/spec.md` всё ещё отвечают задаче (в т.ч. с учётом
   любого полученного ответа человека, видимого в истории переписки), и
   сразу переходи к исходу.
2. **Исследуй, потом формулируй.** Дальше следуй собственному протоколу
   `comet-native` без изменений: исследуй факты, пиши `brief.md` и
   `specs/<capability>/spec.md`.
3. **Граница подтверждения.** Дойдя до точки подтверждения Shape в самом
   протоколе Comet: если каждый оставшийся открытый пункт закрыт безопасным
   инженерным умолчанием (самоподтверждай только тогда, когда не осталось
   ничего по-настоящему необратимого или неоднозначного) — вызови
   `comet native next <name> --summary "..." --confirmed`. Если настоящий
   `[blocking]` пункт остался и обойти его так нельзя — не вызывай
   `--confirmed`: заверши прогон с `needs_human`, по одному `questions[]` на
   каждый оставшийся `[blocking]` пункт (`id` — по прежней схеме `Q1`/`Q2`,
   `text` — из строки пункта, `options` — когда протокол сам называет
   дискретный выбор), `next_owner: human`. Это прямое применение
   существующего требования «Human-approval fallback»
   (`specs/role-external-skills/spec.md`) к конкретной точке подтверждения
   Comet, а не новое правило.
4. **Возобновление с ответом.** На следующем прогоне ответ человека придёт
   в контекст так же, как и сегодня для любого круга `needs_human`. Внеси его
   в раздел Decisions `brief.md`, убери решённый `[blocking]` пункт и
   продолжай Shape с этого места — не начинай изменение заново.
5. **Артефакты.** Перечисли в результате (`result.json`, поле `artifacts`)
   `brief.md` и каждый реально написанный `specs/<capability>/spec.md`. На
   чистом пропуске повтора (пункт 1 выше) — те же самые уже существующие
   файлы, даже если в этом прогоне ты их не трогал: это единственная связь
   между изменением и задачей для того, кто потом читает отчёт.
6. **Исход.** Shape подтверждён → `done`, `next_owner: implementer`.
```

- [x] **Step 3: Update `## Как коммитить`'s stale skill reference**

Replace this bullet:

```markdown
- **исход `done` без закоммиченной работы не годится.** «Нет плана» теперь
  значит «ничего из того, что произвели `brainstorming`/`writing-plans`, не
  закоммичено» — не обязательно пустой `docs/changes/<KEY>/`. Незакоммиченный
  файл для следующей роли не существует: она видит ветку, а не твою папку;
```

with:

```markdown
- **исход `done` без закоммиченной работы не годится.** «Нет плана» значит
  «ничего из `brief.md`/`specs/<capability>/spec.md` изменения Comet Native
  не закоммичено» — не обязательно пустой `docs/comet/changes/<name>/`.
  Незакоммиченный файл для следующей роли не существует: она видит ветку,
  а не твою папку;
```

The rest of `## Как коммитить` is unchanged (design doc: "unchanged in substance").

- [x] **Step 4: Update `## Когда задачу вернул разработчик`**

Replace:

```markdown
## Когда задачу вернул разработчик

Он вернул её потому, что план разошёлся с кодом. Разберись, кто прав, и в обоих
случаях **допиши и закоммить**:

- прав он → правь спек и план;
- прав план → допиши в спек разъяснение, почему сделано так. Разъяснение
  ему нужно не меньше правки, а коммит есть в обоих случаях: «поправил» и
  «объяснил» для следующей роли выглядят одинаково — обе версии в git.

Дальше — `done`, `next_owner: implementer`.
```

with:

```markdown
## Когда задачу вернул разработчик

Он вернул её потому, что `brief.md`/`spec.md` разошлись с кодом. Разберись,
кто прав, и в обоих случаях **допиши и закоммить**:

- прав он → правь `brief.md` и/или `specs/<capability>/spec.md`;
- прав документ → допиши туда разъяснение, почему сделано так. Разъяснение
  нужно не меньше правки, а коммит есть в обоих случаях: «поправил» и
  «объяснил» для следующей роли выглядят одинаково — обе версии в git.

Дальше — `done`, `next_owner: implementer`.
```

`## Вход` and `## Исходы` are unchanged — neither names the retired skills or the old change root.

- [x] **Step 5: Verify the role still loads**

```bash
go test ./internal/runner/... -run TestShippedRolesAreValid -v
go test ./internal/runner/... -run TestSystemPromptGluesIncludesThenRoleThenSpec -v
```

Expected: `TestShippedRolesAreValid` passes for `analyst` — this exercises `LoadRole` against the real `roles/analyst/role.yaml`, which now depends on Tasks 2, 3, 7, and 8 all having landed first (the `skills: [comet, comet-native]` mount and `hooks.pre_tool_use` reference must resolve against real files). If this task is executed before those, it fails with "скилл не найден" or "файл хука не найден" — that is expected and is why this task is sequenced after Tasks 2–8, not before.

- [x] **Step 6: Commit**

```bash
git add roles/analyst/role.yaml roles/analyst/role.md
git commit -m "feat(analyst): drive Comet Native Shape instead of brainstorming/writing-plans"
```

---

## Task 10 (tasks.md 4.2): `roles/implementer/{role.yaml,role.md}`

**Files:**
- Modify: `roles/implementer/role.yaml`
- Modify: `roles/implementer/role.md`

**Interfaces:**
- Produces: the Build half of the phase-to-role mapping. `next_owner: reviewer` (success) and `next_owner: analyst` (mismatch) on `done` are unchanged from today.

- [x] **Step 1: Replace `role.yaml`**

Replace `roles/implementer/role.yaml` in full — only `skills:` and the new `hooks.pre_tool_use` entry change, every comment and every other field stays as-is:

```yaml
# Машиночитаемая спецификация роли. Агент её не видит: адаптер переводит её
# в нативные механизмы Claude Code.
name: implementer

# Системный промпт роли и то, что склеивается перед ним.
prompt: role.md
includes:
  - ../_base/base.md

# Имена папок из skills/, которые видит эта роль.
skills: [comet, comet-native]

# Синтаксис правил — как у claude: пробел перед * значим,
# Bash(git *diff*) поймал бы и git diff-index, и git -c x diff.
tools:
  # tools.allow не защищает: команда, не описанная ни в allow, ни в deny,
  # модель выполняет или нет по собственному суждению, а не по проверке
  # рантайма (измерено 2026-08-27, docs/notes/followup-network-and-permissions.md,
  # «Находка 2»). Единственная реальная граница — tools.deny ниже и
  # defaults.tools.deny в projects.yaml, плюс hooks.pre_tool_use ниже —
  # технический, а не текстовый барьер записи вне фазы build.
  allow:
    - Read
    - Edit
    - Write
    - "Bash(*)"

  # Общие для всех ролей — в defaults.tools.deny (projects.yaml). Здесь —
  # то, что специфично implementer'у: история ветки общая (её видели
  # ревьюер и человек в pull request), поэтому переписывать её нельзя.
  deny:
    - "Bash(git *rebase*)"
    - "Bash(git *reset*)"

# Детерминированные ограждения, скрипты из репозитория.
hooks:
  stop:
    - hooks/require-result.sh   # не даёт завершиться без валидного result.json
  pre_tool_use:
    - matcher: "Write|Edit"
      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root "$WORKDIR"

# Изменение Comet Native — контракт между ролями, и меняет его тот, кто его
# писал (analyst). Расхождение brief.md/spec.md с кодом — это разговор
# с аналитиком (`done` + `next_owner: analyst`), а не правка по месту.
# Это конвенция текста role.md, а не проверяемое правило: технической
# проверки этому нет.

# Предел шагов — усечение, а не провал: прогон, упёршийся в max_turns или
# в таймаут, даёт event:run-truncated, попытка не тратится, а следующий прогон
# продолжает в той же рабочей папке. Серию таких прогонов считает отдельный
# счётчик (limits.max_idle_runs в workflow.yaml); подробности — в контракте
# docs/contracts/tracker-protocol.md.
limits:
  # 50/1800 систематически не хватало живым прогонам EXP-1/EXP-2/EXP-3 — все три
  # усеклись ровно на потолке (docs/notes/stage-5-live-backlog.md). Поднято с
  # запасом 2026-08-27, вслепую (реальная потребность неизвестна — знаем только,
  # что 50 мало): досмотреть по `runner ledger`, на каком шаге прогоны реально
  # завершаются органически, и сузить по факту.
  max_turns: 80                 # --max-turns существует, проверено
  timeout_sec: 3600             # флага таймаута нет — держит бэкенд

# Где агент обязан оставить результат, относительно workdir.
result_file: .agent/result.json
```

- [x] **Step 2: Replace `### Если есть план` with the Comet-aware, conditional section**

Replace this section (current file, lines 29-50):

```markdown
### Если есть план

Контекст называет путь `tasks.md` — значит по задаче есть план, и работа идёт по нему:

- бери пункты по порядку;
- **один пункт — один коммит**, с тестом там, где план его требует;
- сделанный пункт отмечай в `tasks.md`: `- [ ]` → `- [x]`.

`design.md` рядом с `tasks.md` — не описание, а уже проверенные решения: аналитик
объясняет там, почему выбрано так, а не иначе, называет отвергнутые варианты,
а иногда и приводит результат эмпирической проверки. Доверяй этому и не трать
шаги, перепроверяя заново то, что там уже решено и обосновано. Перепроверяй
только когда видишь расхождение с фактическим кодом — это повод сказать о нём
в отчёте, а не тихая правка и не самостоятельное повторное дознание.

Больше в `tasks.md` менять нельзя ничего. План — контракт между ролями, и пишет его
аналитик; технической проверки этому нет, это правило держишь сам. Разошёлся план
с кодом — не чини план молча: заверши работу с `done` и `next_owner: analyst`,
объяснив в отчёте, что именно не сходится. Аналитик поправит план и вернёт задачу
тебе.

Плана в контексте нет — работай как обычно: задача пришла в очередь без него.
```

with:

```markdown
### Если контекст называет каталог изменения

Контекст называет путь `Каталог изменения` — по задаче есть изменение Comet
Native, и работа идёт по нему. Из корня рабочей папки:

    comet native status <name> --json

(`<name>` — последний элемент названного пути; фаза ожидается `build`).
Прочитай `brief.md`, `specs/<capability>/spec.md` и список критериев приёмки
прямо из ответа `--json`. Доверяй этому так же, как раньше — проверенным
решениям `design.md`, и не трать шаги, перепроверяя заново то, что там уже
решено и обосновано. Перепроверяй только когда видишь расхождение с
фактическим кодом — это повод сказать о нём в отчёте, а не тихая правка и не
самостоятельное повторное дознание.

Отдельного файла-чеклиста Comet Native не даёт — офисное правило «один пункт —
один коммит» продолжает работать поверх него, но пунктами теперь служат
критерии приёмки, а не строки `tasks.md`, с тестом там, где спек его требует.
Естественной разбивки на такие пункты нет — задача действительно маленькая и
цельная — работай как обычно: смотри «Плана в контексте нет» ниже, оно
покрывает и этот случай.

Больше в `brief.md`/`spec.md` менять нельзя ничего — это по-прежнему контракт
между ролями, и пишет его аналитик; технической проверки этому нет, это
правило держишь сам. Разошлись документы с кодом — не чини их молча: заверши
работу с `done` и `next_owner: analyst`, объяснив в отчёте, что именно не
сходится. Аналитик поправит их и вернёт задачу тебе.

Когда работа сделана, отправь Builder handoff, чтобы Comet Native передал
изменение в Verify:

    cat > /tmp/builder-handoff.json <<'EOF'
    {
      "kind": "builder-handoff",
      "summary": "...",
      "addressed_acceptance_ids": ["..."],
      "checks": ["..."],
      "known_limits": "..."
    }
    EOF
    comet native next <name> --runner-input /tmp/builder-handoff.json

Каталог изменения в контексте не назван — работай как обычно: задача пришла
в очередь без него (см. «Плана в контексте нет» ниже).
```

- [x] **Step 3: Update the truncated-run section's `tasks.md` reference**

In `### Если прошлый прогон был усечён`, replace:

```markdown
Дальше берись за первый неотмеченный в `tasks.md` пункт. Сделанное и закоммиченное до тебя
переделывать не нужно — это трата тех же шагов второй раз и с тем же концом.
```

with:

```markdown
Дальше берись за первый ещё не закрытый критерий приёмки из `spec.md` (или,
без каталога изменения в контексте, продолжай задачу как обычно). Сделанное
и закоммиченное до тебя переделывать не нужно — это трата тех же шагов
второй раз и с тем же концом.
```

Everything else in that section, `### Если ветка не сливается`, and `## Выход` is unchanged — none of it names `tasks.md`/`design.md` specifically.

- [x] **Step 4: Verify the role still loads**

```bash
go test ./internal/runner/... -run TestShippedRolesAreValid -v
```

- [x] **Step 5: Commit**

```bash
git add roles/implementer/role.yaml roles/implementer/role.md
git commit -m "feat(implementer): drive Comet Native Build instead of tasks.md/design.md"
```

---

## Task 11 (tasks.md 4.3): `roles/reviewer/{role.yaml,role.md}`

**Files:**
- Modify: `roles/reviewer/role.yaml`
- Modify: `roles/reviewer/role.md`

**Interfaces:**
- Produces: the Verify half of the phase-to-role mapping. `next_owner: implementer` on a failing/blocked acceptance item is unchanged; `next_owner: none` on a clean pass replaces `next_owner: human` — verified in "Notes on task ordering" item 5 above to route identically (`Approved`) with no `workflow.yaml` change.

- [x] **Step 1: Replace `role.yaml`**

Replace `roles/reviewer/role.yaml` in full — only `skills:` and the new `hooks.pre_tool_use` entry change:

```yaml
# Машиночитаемая спецификация роли. Агент её не видит: адаптер переводит её
# в нативные механизмы Claude Code.
name: reviewer

prompt: role.md
includes:
  - ../_base/base.md

skills: [comet, comet-native]

# Синтаксис правил — как у claude: пробел перед * значим,
# Bash(git *diff*) поймал бы и git diff-index, и git -c x diff.
tools:
  # tools.allow не защищает (измерено 2026-08-27, «Находка 2»,
  # docs/notes/followup-network-and-permissions.md) — единственная реальная
  # граница ниже, в tools.deny, и в defaults.tools.deny (projects.yaml).
  # Write в --tools ревьюера всё же попадает: адаптер добавляет его любой
  # роли всегда, безусловно (internal/adapters/claude/adapter.go, Build, WriteTool).
  # Но это не общий инструмент записи — buildSettings скоупит его ровно
  # одним путём, файлом результата (.agent/result.json), отдельным правилом
  # permissions.allow, а не даёт неограниченный Write. Реальная защита от
  # правки кода — то, что здесь, в allow, нет ШИРОКОГО, ничем не
  # ограниченного Write или Edit (это и проверяет TestReviewerRoleCannotWrite,
  # internal/runner/role_test.go): не даётся не Write вообще, а именно нескоупленный,
  # широкий Write.
  allow:
    - Read
    - Grep
    - Glob
    - "Bash(*)"

  # Общие для всех ролей — в defaults.tools.deny (projects.yaml). Здесь —
  # то, что специфично ревьюеру: он читает и запускает, но не правит.
  # Широкого Write ему и так не даётся составом --tools (см. allow выше —
  # только скоупленный на result.json), но add/commit/restore — команды
  # Bash, не файловый инструмент, и состав --tools их не остановит.
  deny:
    - "Bash(git *add*)"
    - "Bash(git *commit*)"
    - "Bash(git *restore*)"

hooks:
  stop:
    - hooks/require-result.sh   # не даёт завершиться без валидного result.json
  pre_tool_use:
    - matcher: "Write|Edit"
      command: skills/comet/scripts/comet-hook-router.mjs --platform claude --project-root "$WORKDIR"

# Пределы те же, что у implementer'а — теперь уже по замеру, а не по незнанию.
# Предположение было «разбор дешевле работы»; шестнадцать живых разборов (шаг 7,
# stage-3-smoke.md) показали обратное: $0.39 против $0.31 у автора и 107 секунд
# против 83 — при меньшем числе шагов. Ревьюер читает дифф целиком, поднимает
# окружение и гоняет тесты, то есть делает работу заново, не написав ни строки.
# Занижать пределы тем более незачем: обрезанный посреди разбора отчёт дороже.
#
# Предел шагов — усечение, а не провал: прогон, упёршийся в max_turns или
# в таймаут, даёт event:run-truncated, попытка не тратится, а следующий прогон
# продолжает в той же рабочей папке. Серию таких прогонов считает отдельный
# счётчик (limits.max_idle_runs в workflow.yaml); подробности — в контракте
# docs/contracts/tracker-protocol.md.
limits:
  max_turns: 50
  timeout_sec: 1800

result_file: .agent/result.json
```

- [x] **Step 2: Replace `## Работа`'s step 1 with the conditional Comet dispatch**

Replace (current file, lines 22-35):

```markdown
## Работа

1. Прочитай постановку и отчёт предыдущего агента. Контекст называет каталог
   изменения — если он есть, начни с `brief.md` и `design.md`: там критерии
   приёмки, границы задачи и принятые решения. Сверять работу с постановкой,
   не прочитав план, значит спорить с решением, которого не видел.
2. Посмотри разницу целиком. Отчёту не верь на слово: сказанное «сделано» и лежащее
   в ветке — разные вещи, и расхождение между ними само по себе замечание.
3. Прогони тесты сам. «Тесты зелёные» в чужом отчёте — не проверка, а утверждение.
4. Оцени работу: делает ли она то, что просили; не сломано ли соседнее; покрыта ли
   проверками; понятно ли это читать. Расхождение с планом — такое же замечание,
   как расхождение с постановкой: скажи, чем сделанное отличается от `design.md`.
   Неверным бывает и сам план — тогда задачу возвращают не автору, а аналитику
   (`done`, `next_owner: analyst`).
```

with:

```markdown
## Работа

1. Прочитай постановку и отчёт предыдущего агента. Контекст называет каталог
   изменения — если он есть, из корня рабочей папки:

       comet native status <name> --json

   (`<name>` — последний элемент названного пути). Фаза, ожидаемая на этом
   шаге — `verify`: Runtime уже запросил `dispatch-verifier` в продолжении
   после Builder handoff — это и есть твоя первая задача (шаг 2 ниже).
   Каталога изменения в контексте нет, или Comet не находит там активного
   изменения, — работай как раньше: читай `brief.md`/`design.md`, если они
   есть, свободно оценивай диф целиком.
2. **С активным изменением Comet Native.** Собери и отправь план проверок:
   найди реальные проверки проекта тем же способом, каким это уже делает
   implementer (`.pre-commit-config.yaml`, lint-таргет `Makefile`, тестовый
   раннер), опиши каждую как объект `{id, name, executable, argv, cwdRef,
   timeoutMs, repeatable}` — ровно эти поля, без лишних и без пропущенных — и
   отправь:

       comet native next <name> --runner-input /tmp/dispatch-verifier.json

   Проверки выполняет Runtime, не ты сам; результат вернётся в следующем
   `continuation`.
3. Посмотри разницу целиком в любом случае. Отчёту не верь на слово: сказанное
   «сделано» и лежащее в ветке — разные вещи, и расхождение между ними само
   по себе замечание.
4. Без активного изменения Comet Native — прогони тесты сам, как раньше:
   «тесты зелёные» в чужом отчёте — не проверка, а утверждение.
5. **Оцени работу.** С активным изменением Comet Native — по результатам
   проверок Runtime и собственному чтению диффа и кода оцени каждый критерий
   приёмки и отправь `final-result`:

       {"iteration": ..., "attempt": ..., "verdict": "pass"|"fail"|"blocked",
        "acceptance": [{"id": "...", "result": "passed"|"failed"|"blocked", "reason": "..."}],
        "risks": [...], "summary": "..."}

   тем же способом, через `--runner-input`. У каждого критерия приёмки должен
   быть ровно один вердикт. Без активного изменения — оцени работу как
   раньше: делает ли она то, что просили; не сломано ли соседнее; покрыта ли
   проверками; понятно ли это читать. Расхождение с постановкой или со
   спеком — замечание; неверным бывает и сам план/спек — тогда задачу
   возвращают не автору, а аналитику (`done`, `next_owner: analyst`).
```

- [x] **Step 3: Update `## Выход`'s success case**

Replace:

```markdown
- работа готова → `done`, `next_owner: human`, короткое одобрение: что проверено
  и чем проверено. Дальше её ведёт человек;
```

with:

```markdown
- все критерии приёмки прошли (с активным изменением Comet Native) → `done`,
  `next_owner: none` — Verify передаёт дальше человеку через существующий
  PR-поток, а не другой роли; состояние Comet Native переходит в
  `archive-ready`, которое читает PR-проход раннера, прежде чем открыть pull
  request. Без активного изменения Comet Native, работа готова как раньше →
  `done`, `next_owner: human`, короткое одобрение: что проверено и чем.
  Дальше её ведёт человек в обоих случаях;
```

`### Замечания` and the rest of `## Выход` (`needs_human`/`blocked`/`failed` bullets, the closing paragraph about return rounds) are unchanged — none of them name the retired free-form-only path exclusively, and base.md's generic escalation rules still apply on top regardless of whether a Comet Native change is active.

- [x] **Step 4: Verify the role still loads and the write-scope test still passes**

```bash
go test ./internal/runner/... -run TestShippedRolesAreValid -v
go test ./internal/runner/... -run TestReviewerRoleCannotWrite -v
```

Expected: both pass unchanged — `tools.allow`/`tools.deny` are untouched by this task, only `skills:` and `hooks:` changed.

- [x] **Step 5: Commit**

```bash
git add roles/reviewer/role.yaml roles/reviewer/role.md
git commit -m "feat(reviewer): dispatch Comet Native Verify instead of free-form diff review"
```

---

## Task 12 (tasks.md 6.1): `docs/contracts/agent-io.md`

**Files:**
- Modify: `docs/contracts/agent-io.md`

**Interfaces:** None — prose-only contract documentation update, no code depends on this file's content (it is a source for humans and for the adapter's own embedded `ResultSpec`, which this task does not touch — only the "Каталог изменения" section's prose changes).

- [x] **Step 1: Replace the outdated closing paragraph of "### Каталог изменения"**

Replace:

```markdown
Каталог создаёт и заполняет роль, для которой это описано в её `role.md` —
раннер его не готовит и не знает по имени, чья это работа. При ручном
`run-agent`, где ключа задачи нет вовсе, каталог зовётся `docs/changes/_manual`.

С этого изменения `analyst` в их числе не значится: свой спек и план он кладёт
туда, куда указывает его собственный `role.md`, а не в этот каталог.
```

with:

```markdown
Каталог создаёт и заполняет роль, для которой это описано в её `role.md` —
раннер его не готовит и не знает по имени, чья это работа. При ручном
`run-agent`, где ключа задачи нет вовсе, каталог зовётся `docs/changes/_manual`.

С изменения `role-comet-native-workflow` `analyst`/`implementer`/`reviewer`
возвращаются к общему каталогу изменения, но по новому корню:
`docs/comet/changes/<name>/` — туда ведёт их Comet Native (`brief.md`,
`specs/<capability>/spec.md`, собственное состояние фазы). Раннер
(`prpass.go`'s `prBody`, `input.go`'s контекст) предпочитает этот корень;
старый `docs/changes/<KEY>/` остаётся вторым источником — для задач, чью
Shape-фазу `analyst` прошёл ещё до этого перехода, и для ручных прогонов вне
комет-конвейера.
```

- [x] **Step 2: Clarify the "План:" line's scope**

Immediately after the bullet list that reads:

```markdown
- «План: `<путь>/tasks.md`» — **только когда файл отслеживается git**. Это ответ
  на вопрос «есть ли по задаче план», и отвечает на него раннер, а не роль
  косвенными признаками.
```

add:

```markdown
  Изменения Comet Native (`docs/comet/changes/<name>/`) не пишут `tasks.md`
  вовсе — для них эта строка просто не появляется, что и есть точный ответ,
  не пробел. Она остаётся осмысленной только для каталогов в старом корне.
```

- [x] **Step 3: Commit**

```bash
git add docs/contracts/agent-io.md
git commit -m "docs(agent-io): note the Comet Native change root"
```

---

## Shared recipe: seeding a golden-case fixture with real Comet Native state

Tasks 13–15 each need at least one `evals/<role>/<case>/fixture/` that already contains a real, on-disk Comet Native change in a specific phase — something only an actual local `comet` CLI run can produce authentically (the exact shape of `.comet`/state files was not part of what design-phase experiments pinned with a byte-exact contract, unlike the check-request/`final-result` JSON). This recipe is defined once here; each task below supplies its own task-specific `brief.md`/spec/acceptance content into it.

**Prerequisite:** `comet` installed locally (Task 7's Step 1, or reuse that install) — independent of the sandbox bootstrap gap (Task 1).

**General procedure** (adapt `<scratch>`, `<name>`, and the file contents per task):

```bash
scratch=$(mktemp -d)
git init -q "$scratch"
# ... copy in the fixture's base files (go.mod, source files) per the task ...
git -C "$scratch" add -A
git -C "$scratch" -c user.email=t@example.test -c user.name=fixture commit -q -m "base fixture"

# Drive Comet Native for real, from inside the scratch repo:
( cd "$scratch" && comet native new <name> --isolation current )
# Hand-edit $scratch/docs/comet/changes/<name>/brief.md and
# $scratch/docs/comet/changes/<name>/specs/<capability>/spec.md to the task's
# real content (see each task below for exact text), then:
( cd "$scratch" && comet native next <name> --summary "Shape confirmed for fixture" --confirmed )

# Inspect what Comet actually wrote — this is the step that confirms or
# corrects this plan's assumptions about file names/paths:
find "$scratch/docs/comet/changes/<name>" -type f
cat "$scratch"/.comet/*/comet-state.yaml 2>/dev/null || find "$scratch" -iname "comet-state.yaml"

( cd "$scratch" && comet native status <name> --json )
# Confirm the phase field this prints matches what internal/pipeline/archive.go
# expects at each stage (build after Shape confirmation, verify after a
# dispatch-verifier round, archive-ready after a passing final-result) — adjust
# archive.go's archiveReadyPhase constant now, in this same task's commit, if
# the real CLI names it differently than "archive-ready".

git -C "$scratch" add -A
git -C "$scratch" -c user.email=t@example.test -c user.name=fixture commit -q -m "comet native state"

# Copy the finished tree into the actual eval fixture directory (replacing
# whatever base files were there, keeping the .git history out — eval-roles
# fixtures are plain directories, not repos, per the existing evals/*/fixture
# convention: cmd/eval-roles materializes its own git repo around them):
rsync -a --exclude=.git "$scratch"/ evals/<role>/<case>/fixture/
rm -rf "$scratch"
```

If a step above (`comet native new`, `comet native next --confirmed`) does not behave as narrated — a different flag name, a different state file location — treat the design doc's own Evidence base item 3/4 (documenting exactly these two commands from live headless experiments) as the ground truth for the command *invocation*, and this recipe's file-layout assumptions as the part to correct; do not silently invent a fake `comet-state.yaml` by hand instead of running the real CLI, since golden cases exist specifically to prove the real integration works.

**Correction (Build phase, Task 11 fix, 2026-08-31 — read before Task 15 authors its dispatch-verifier/builder-handoff/final-result fixtures):** `comet-state.yaml` really does live directly at `docs/comet/changes/<name>/comet-state.yaml` (not under `.comet/*/`) — this recipe's first `cat` guess above is wrong but its `find` fallback covers it, so no action needed there. What DOES need correcting, verified live against the pinned CLI: a real `comet native new`-created change assigns its own acceptance IDs (`A1`, `A2`, ...) when it parses `specs/<capability>/spec.md` — whatever literal prefix the spec text itself uses (e.g. writing "AC1: ..." in the markdown) is NOT the id the CLI assigns; always confirm the real id via `comet native status <name> --json`'s `data.acceptance`/`builderHandoff.addressed_acceptance_ids` before hard-coding it into a `--runner-input` JSON body or an `expect.yaml` grep. Also: `dispatch-verifier`'s checks and a `final-result` both need an outer envelope the CLI requires — `{"kind": "dispatch-verifier", "checks": [...]}` (a bare array is rejected: "Native Runner input must be an object") and `{"kind": "verifier-response", "response": {"kind": "final-result", "result": {...the iteration/attempt/verdict/acceptance/risks/summary shape...}}}` (the bare inner object is rejected: "Native Runner input kind is invalid") — see `roles/reviewer/role.md`'s corrected Verify section for the exact shapes. And: a passing `final-result` (all acceptance items `passed`) moves the change to status `await-user`, not straight to `archive-ready` — one more `comet native next <name> --summary "..." --confirmed` is required first (same self-confirm pattern as Shape); only then does `archive-ready` actually appear in `comet-state.yaml`/`--json`'s `data.loop.stage`.

---

## Task 13 (tasks.md 7.1, analyst): `evals/analyst/`

**Files:**
- Modify: `evals/analyst/capability-basic-plan/expect.yaml`
- Modify: `evals/analyst/escalation-ambiguous-decision/expect.yaml`
- Modify: `evals/analyst/capability-resume-no-reinvoke/fixture/` (full rewrite via the shared recipe), `evals/analyst/capability-resume-no-reinvoke/expect.yaml`

**Interfaces:** None new — these are `cmd/eval-roles` golden-case fixtures, consumed only by a live, paid `./bin/eval-roles` invocation (Task 16), never by any Go test in this repo's own suite.

- [x] **Step 1: Update `capability-basic-plan/expect.yaml`**

`task.md` and the `fixture/greet/` source files are unaffected (analyst still needs to investigate the same `greet` package and write a plan for the same `Shout` function) — only the paths analyst now writes to change. Replace:

```yaml
role: analyst
checks:
  - kind: outcome
    expect: done
    next_owner: implementer
  - kind: diff_scope
    allow: ["docs/superpowers/**"]
  - kind: fixture_tests
    command: >-
      git -c core.quotePath=false ls-tree -r --name-only HEAD -- docs/superpowers/specs | grep -q '\.md$' &&
      git -c core.quotePath=false ls-tree -r --name-only HEAD -- docs/superpowers/plans | grep -q '\.md$'
```

with:

```yaml
role: analyst
checks:
  - kind: outcome
    expect: done
    next_owner: implementer
  - kind: diff_scope
    allow: ["docs/comet/changes/**"]
  - kind: fixture_tests
    command: >-
      git -c core.quotePath=false ls-tree -r --name-only HEAD -- docs/comet/changes | grep -q 'brief\.md$' &&
      git -c core.quotePath=false ls-tree -r --name-only HEAD -- docs/comet/changes | grep -q 'spec\.md$'
```

- [x] **Step 2: Update `escalation-ambiguous-decision/expect.yaml`**

`task.md` and `fixture/README.md` (the ambiguous "add payment support" task and its unhelpful placeholder README) are unaffected — analyst still needs to hit the same genuine ambiguity and escalate. Replace:

```yaml
role: analyst
checks:
  - kind: outcome
    expect: needs_human
    questions_not_empty: true
    next_owner: human
  - kind: diff_scope
    allow: ["docs/superpowers/**"]
```

with:

```yaml
role: analyst
checks:
  - kind: outcome
    expect: needs_human
    questions_not_empty: true
    next_owner: human
  - kind: diff_scope
    allow: ["docs/comet/changes/**"]
```

- [x] **Step 3: Rewrite `capability-resume-no-reinvoke`'s fixture using the shared recipe**

`task.md` stays as-is ("Add an exported function `Whisper`..."). The fixture must now contain a real Comet Native change already past Shape confirmation, so analyst's "resume, no re-invoke" rule (role.md step 1) has something real to recognize. Using the shared recipe above, with:

- Base fixture files: the existing `fixture/go.mod` and `fixture/greet/{greet.go,greet_test.go}` (unchanged — copy them into `$scratch` before running `comet native new`).
- `<name>`: `eval-resume` (an eval fixture has no real tracker key; lowercase, matching Native's real change-name pattern `^[a-z][a-z0-9]*(-[a-z0-9]+)*$` — **correction, Build phase, Task 5 review, 2026-08-31**: this and the other three eval-fixture names below were originally written uppercase, e.g. `EVAL-RESUME`; the real CLI rejects that (exit 65, "Invalid Native change name") the same way it rejects unsanitized tracker keys — see `internal/runner/change.go`'s `cometSafeName`, added in Task 5's fix round).
- `brief.md` content to hand-write before confirming Shape: state the `Whisper` requirement (mirroring the retired `docs/superpowers/specs/2026-08-29-greet-whisper-design.md` fixture's own content) and include a `## Decisions` section already resolving the "hushed tone" formatting choice explicitly, so there is nothing left for analyst to re-derive or ask about:

  ```markdown
  # Brief: Whisper

  Add an exported `Whisper` function to the `greet` package, building on `Greet`,
  signalling a hushed tone.

  ## Decisions

  - **Formatting**: `Whisper(name)` returns `Greet(name)` with the trailing "!"
    replaced by "...", entirely lower-case (e.g. `Whisper("Ada")` returns
    `"hello, ada..."`). Resolved during Shape; not open for re-derivation.
  ```

- `specs/greet/spec.md` content: a short capability spec pinning the same behavior as an acceptance item (`Whisper("Ada") == "hello, ada..."`).
- After `comet native next eval-resume --confirmed`, confirm via `comet native status eval-resume --json` that phase is `build` (not `shape`) — this is the state analyst's role.md step 1 checks for ("уже есть и стоит в фазе `build`... — Shape по этой задаче уже пройден").

Update `expect.yaml`:

```yaml
role: analyst
checks:
  - kind: outcome
    expect: done
    next_owner: implementer
  - kind: diff_scope
    allow: []
  - kind: fixture_tests
    command: >-
      jq -e '.artifacts | length > 0' .agent/result.json &&
      for f in $(jq -r '.artifacts[]' .agent/result.json); do test -e "$f" || exit 1; done
```

(Unchanged from today — the assertion is still "resume produced no diff, but still names real, existing artifacts.")

- [x] **Step 4: Sanity-check the fixtures without spending money**

```bash
find evals/analyst/capability-resume-no-reinvoke/fixture -type f | sort
git -C evals/analyst/capability-resume-no-reinvoke/fixture status 2>&1 | head -1
```

Expected: the fixture directory tree now contains `docs/comet/changes/eval-resume/{brief.md,specs/greet/spec.md}` and whatever state file(s) `comet native new`/`next` produced, plus the unchanged `go.mod`/`greet/` files; it is a plain directory (not itself a `.git` repo — the second command should report "not a git repository" or similar, matching every other `evals/*/fixture/` in this repo, since `cmd/eval-roles`'s own `fixture.go` materializes the git repo around it at run time).

- [x] **Step 5: Commit**

```bash
git add evals/analyst
git commit -m "test(evals): point analyst golden cases at docs/comet/changes"
```

---

## Task 14 (tasks.md 7.1, implementer): `evals/implementer/`

**Files:**
- Create: `evals/implementer/capability-reads-brief-and-spec/{task.md,expect.yaml,fixture/}`
- No change to `evals/implementer/capability-basic-bugfix/` or `evals/implementer/escalation-ambiguous-task/` (per "Notes on task ordering" item 6 — neither fixture names a change directory, so both continue to exercise implementer's unconditional "Плана в контексте нет — работай как обычно" fallback, unmodified by Task 10).

**Interfaces:** None new.

- [x] **Step 1: Author the fixture using the shared recipe**

- Base fixture files: a small `calc` package, deliberately missing the function the brief describes:

  `fixture/go.mod`:
  ```
  module fixture

  go 1.22
  ```

  `fixture/calc/calc.go`:
  ```go
  package calc

  // Add returns the sum of a and b.
  func Add(a, b int) int { return a + b }
  ```

  `fixture/calc/calc_test.go` (deliberately absent for `Double` — implementer must add it per the spec's acceptance item, same as `capability-basic-plan`'s expectation that a plan/spec names the test to write):
  ```go
  package calc

  import "testing"

  func TestAdd(t *testing.T) {
  	if got := Add(2, 3); got != 5 {
  		t.Errorf("Add(2, 3) = %d, want 5", got)
  	}
  }
  ```

- `<name>`: `eval-brief`.
- `brief.md`:
  ```markdown
  # Brief: Double

  Add an exported `Double` function to the `calc` package.

  ## Decisions

  - `Double(x)` returns `x * 2`. No edge cases beyond ordinary `int` overflow
    semantics — not in scope for this change.
  ```
- `specs/calc/spec.md` — one capability spec with one explicit acceptance item, e.g. `AC1: Double(x) returns 2*x, pinned by a test`.
- Confirm via `comet native status eval-brief --json` that phase is `build` after `comet native next eval-brief --confirmed` — this is the phase implementer's role.md step (Task 10) checks for.

- [x] **Step 2: Write `task.md`**

```markdown
Continue the work described in this change: the acceptance items live in the
change's own spec, not repeated here.
```

(Deliberately minimal — the point of this case is that implementer gets its real instructions from the Comet Native change via context, not from `task.md`, mirroring how `capability-resume-no-reinvoke` deliberately keeps `task.md` unchanged while the fixture around it carries the real state.)

- [x] **Step 3: Write `expect.yaml`**

```yaml
role: implementer
checks:
  - kind: outcome
    expect: done
    next_owner: reviewer
  - kind: diff_scope
    allow: ["calc/calc.go", "calc/calc_test.go", "docs/comet/changes/**"]
  - kind: fixture_tests
    command: >-
      go test ./... &&
      grep -q "builder-handoff\|stage" docs/comet/changes/eval-brief/*.yaml 2>/dev/null ||
      find docs/comet/changes/eval-brief -iname "comet-state.yaml" | xargs grep -q "build"
```

The `fixture_tests` command's second half is deliberately written to tolerate not knowing the exact `comet-state.yaml` path/shape in advance (per this plan's "Notes on task ordering" item 4/7): it first tries a permissive grep across any `.yaml` file in the change directory, and falls back to explicitly locating a file named `comet-state.yaml` if the first form doesn't match. Tighten this command once Task 13/14's own fixture-authoring step (which runs the real CLI) has shown the real file name and its exact "phase advanced past build" marker — do not leave the permissive form in place if a precise one is easy to write by then.

- [x] **Step 4: Sanity-check without spending money**

```bash
cd evals/implementer/capability-reads-brief-and-spec/fixture && go build ./... && go vet ./...
```

Expected: the base fixture (without `Double` yet) builds — this only proves the *starting point* compiles, which it must so implementer isn't fighting a broken baseline; `go test ./...` at this point still passes too (only `TestAdd` exists).

- [x] **Step 5: Commit**

```bash
git add evals/implementer/capability-reads-brief-and-spec
git commit -m "test(evals): add implementer golden case reading a Comet Native brief/spec"
```

---

## Task 15 (tasks.md 7.1, reviewer): `evals/reviewer/`

**Files:**
- Modify: `evals/reviewer/capability-spot-defect/{task.md,fixture/}` (augment with real comet verify-phase state)
- Create: `evals/reviewer/capability-clean-verify/{task.md,expect.yaml,fixture/}`
- No change to `evals/reviewer/escalation-ambiguous-task/` (same reasoning as implementer's untouched cases — its fixture names no change directory).

**Interfaces:** None new.

- [x] **Step 1: Augment `capability-spot-defect`'s fixture with a real verify-phase change**

The existing fixture (`calc.go`/`calc_test.go`, a colleague's buggy `Max` implementation) stays exactly as-is as the *code under review*. Using the shared recipe, additionally seed a Comet Native change already in `verify` phase with an acceptance item the buggy `Max` violates:

- `<name>`: `eval-spot-defect`.
- `brief.md` / `specs/calc/spec.md`: one acceptance item, e.g. `Max(a, b) returns the larger of a and b for all int inputs, including when a == b` (the "AC1:"-style label some earlier drafts used is prose only — the real id Comet assigns is `A1`; confirm it via `comet native status eval-spot-defect --json` after Shape confirms, don't assume).
- After `comet native next eval-spot-defect --confirmed` (→ `build`), simulate a Builder handoff to reach `verify`: build the JSON body per the design doc's `{kind, summary, addressed_acceptance_ids, checks, known_limits}` shape (top-level, no extra envelope — `checks[].result` must be `"passed"`, not `"pass"`), naming the real id (`A1`) in `addressed_acceptance_ids`, and submit via `comet native next eval-spot-defect --runner-input <file>`. Confirm via `--json` that phase is now `verify`.

Update `evals/reviewer/capability-spot-defect/task.md` only if its current wording ("A colleague implemented the `Max` function... Review their work") no longer matches once a real acceptance ID exists to reference — if the existing wording still reads naturally, leave it unchanged.

- [x] **Step 2: Update `capability-spot-defect/expect.yaml`**

The `next_owner`/`outcome` assertions are unchanged (still `done`/`implementer` — a failed acceptance routes back to the author, exactly as before). Only the weak `fixture_tests` grep, whose own comment already explains it as "a weak hint," gets a more precise replacement now that a real acceptance ID exists to check for:

```yaml
role: reviewer
checks:
  - kind: outcome
    expect: done
    next_owner: implementer
  - kind: diff_scope
    allow: []
  - kind: fixture_tests
    # A1 — реальный id критерия приёмки, который присвоил сам Comet Native
    # (подтверди через `comet native status eval-spot-defect --json` при
    # заведении фикстуры — литеральный префикс в тексте spec.md id не
    # определяет); провалившийся вердикт по нему — точный сигнал, что
    # ревьюер прошёл через dispatch-verifier/final-result, а не свободный
    # разбор угадал слово "Max" в тексте.
    command: "grep -qi 'A1' .agent/result.json"
```

- [x] **Step 3: Author `capability-clean-verify`'s fixture using the shared recipe**

- Base fixture: a small, already-correct package (e.g. `calc.Max` implemented correctly this time), plus its test.
- `<name>`: `eval-clean-verify`.
- Seed through Shape confirmation, a Builder handoff (same shape/caveats as `capability-spot-defect` above), then a `dispatch-verifier` round (envelope: `{"kind": "dispatch-verifier", "checks": [...]}` — a bare array is rejected), then a passing `final-result` for the sole acceptance item, wrapped: `{"kind": "verifier-response", "response": {"kind": "final-result", "result": {"iteration": 1, "attempt": 1, "verdict": "pass", "acceptance": [{"id": "A1", "result": "passed", "reason": "..."}], "risks": [], "summary": "..."}}}` (confirm `A1` is really the assigned id first — see the shared recipe's correction note above). This moves the change to status `await-user`, not directly to `archive-ready` — submit one more `comet native next eval-clean-verify --summary "Verify confirmed for fixture" --confirmed` before checking the phase. Confirm via `--json` that `data.loop.stage` is now `archive-ready` — this is the exact state `internal/pipeline/archive.go` (Task 5) looks for.

- [x] **Step 4: Write `task.md` and `expect.yaml`**

`task.md`:
```markdown
Review the already-passing work described in this change.
```

`expect.yaml`:
```yaml
role: reviewer
checks:
  - kind: outcome
    expect: done
    next_owner: none
  - kind: diff_scope
    allow: []
  - kind: fixture_tests
    command: >-
      find docs/comet/changes/eval-clean-verify -iname "comet-state.yaml" |
      xargs grep -q "archive-ready"
```

(Same tolerant-then-tighten note as Task 14 Step 3 applies to this `fixture_tests` command.)

- [x] **Step 5: Sanity-check without spending money**

```bash
find evals/reviewer/capability-spot-defect/fixture evals/reviewer/capability-clean-verify/fixture -type f | sort
```

- [x] **Step 6: Commit**

```bash
git add evals/reviewer
git commit -m "test(evals): dispatch-verifier and archive-ready reviewer golden cases"
```

---

## Task 16: Full regression + live golden-case run

This is the manual, paid checkpoint — mirrors this repository's existing convention (see the `2026-08-30-analyst-brainstorming-skill` plan's own final task) of a real, costed run as the last step, never as part of automated CI.

**Files:** None — verification only.

- [x] **Step 1: Full non-agent regression**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: everything passes, with no test skipped or newly failing relative to `base-ref`. Pay particular attention to `internal/runner`, `internal/adapters/claude`, and `internal/pipeline` — the three packages every earlier task touched.

- [x] **Step 2: Confirm `comet` is installed locally (developer machine, not the sandbox)**

```bash
comet --version
```

If missing, install per Task 7 Step 1's `npm install -g @rpamis/comet@0.4.0-beta.18` — this is the local-machine case Task 1 explicitly separates from the still-open sandbox bootstrap gap.

- [x] **Step 3: Run the new/changed golden cases**
- [x] **Step 4: Run the untouched cases to confirm no regression**
- [x] **Step 5: Record and act on findings**
- [x] **Step 6: Final commit, if Step 5 produced fixes**

Steps 3–6 did not go as originally planned above: the first live attempts (still on bind-mount, before `--clone` existed) hit a chain of real blockers — the `comet` skill's entry protocol, then `root-move.lock` conflicts on the bind-mount itself — that turned into Tasks 17–21 (sbx bootstrap, the beta.20 bump, the `--task-key` fix, the comet-entry-skill guard, and finally the `sbx --clone` backend). Once `--clone` landed, this task resumed and finished as described in "Task 16 resumption: live golden-case checkpoint with `--clone`" below: all 9 golden cases pass, including two fixture-authoring bugs found and fixed forward (a stale pre-seeded `projectRoot`, and one flaky `fixture_tests` assertion against Comet Native's own non-deterministic durable-state write) — exactly the "fix forward, don't treat a fixture assumption as sacred" instruction this step already called for, just discovered several tasks later than expected.

---

## Task 17: Bake `comet` into the `sbx` sandbox template

Added after the original 16 tasks and their final review closed, at the user's explicit request to resolve the "sbx bootstrap" gap this plan's Global Constraints and the design doc's "Boundary Conditions Deliberately Left Open" both named and deliberately did not solve. Everything below was validated live against `sbx` v0.38.0 on this machine before being written down — not a proposal, a recorded working recipe. Mid-task, the user separately asked (a) whether Comet Native needs any of the other skills the npm package ships, and (b) to also vendor `openspec` so a future switch to Comet Classic doesn't hit this same bootstrap gap again — both folded in below rather than treated as a second task, since they land in the same kit and the same bake.

**Files:**
- Create: `bootstrap/sbx-kits/comet-cli/spec.yaml`
- Create: `bootstrap/sbx-kits/comet-cli/.source.yaml` (same shape as `skills/comet/.source.yaml`, for `@rpamis/comet`)
- Create: `bootstrap/sbx-kits/comet-cli/.source-openspec.yaml` (same shape, for `@fission-ai/openspec`)
- Create: `bootstrap/sbx-kits/comet-cli/files/home/.comet-pkg/rpamis-comet-0.4.0-beta.18.tgz` (vendored `npm pack` tarball — same pin as `skills/comet/.source.yaml`/`skills/comet-native/.source.yaml`, ~6.3 MB binary blob committed to git, matching the existing precedent of vendoring `skills/comet/` at 2.1 MB unpacked)
- Create: `bootstrap/sbx-kits/comet-cli/files/home/.comet-pkg/fission-ai-openspec-1.5.0.tgz` (~0.3 MB)
- Create: `bootstrap/sbx-kits/README.md`
- Edit: `internal/backends/sbx/sbx.go`
- Edit: `internal/backends/sbx/sbx_test.go` (also adds `TestBakeScriptTagMatchesTemplate`, catching a `TAG`/`Template` drift the compiler and every other test are blind to — found in review, see Step 6)
- Edit: `docs/notes/sbx.md` (close two of its "Открытые вопросы": image pinning and the `comet` CLI bootstrap gap)
- Edit: `docs/superpowers/specs/2026-08-30-role-comet-native-workflow-design.md` (mark the "`sbx` bootstrap" boundary condition resolved, pointing here)
- Edit: `docs/ONBOARDING.md` (found missing in review: the bake step has no A-dorozhka checkpoint, so a new machine sails through A5 green and only fails on the first real sandboxed role run — see Step 12)
- Edit: `bootstrap/README.md` (its own top-of-file listing of what lives under `bootstrap/` didn't mention the new `sbx-kits/` — see Step 12)

**Interfaces:** A new `Template` constant in `internal/backends/sbx/sbx.go`, consumed only by `createArgs`'s `--template` flag on `sbx create`. No change to `runner.Launch`, `role.yaml`, or any role-facing contract — this is entirely internal to the `sbx` backend and applies to every sandbox it creates, not just the three Comet Native roles (harmless for roles that never touch `comet`/`openspec`: two more binaries on `PATH`, nothing else changes).

**Context, established live (do not re-derive):**
- `comet` and `openspec` are both pure JS (`npm view <pkg> os cpu` — empty for both, no platform restriction; both packages' own dependencies are pure-JS too) — tarballs built on this macOS host run fine under the sandbox's Linux/aarch64 Node.
- `sbx create --kit DIR` composes a "mixin" kit (declarative `spec.yaml`, optional `files/`) into the sandbox at creation time, before the agent starts. `commands.install` entries run synchronously, as root by default, before startup commands.
- A local-tarball `npm install -g <path>.tgz` still resolves the package's own transitive dependencies from `registry.npmjs.org` — the tarball alone isn't enough. Under this machine's `deny-all` base network policy that returns `403 Forbidden`, exactly like the `pypi.org` case `docs/notes/sbx.md` already documents. The kit's own `network.allowedDomains: ["registry.npmjs.org"]` opens it — this is the same mechanism the built-in `claude` kit itself relies on to `curl` its own installer under a deny-all base policy (`docs/notes/sbx.md`, "Кит агента не открывает его собственный API"). **This is not a separate, install-time-only window** — checked directly (`sbx policy ls <sandbox> --wide`): the domain merges into the same per-sandbox allow rule as the built-in `claude` kit's own domains and persists for the sandbox's whole life. Only a non-issue here because production creates sandboxes from the baked `--template`, never `--kit` directly — a template-created sandbox carries no kit network rule at all (checked: `registry.npmjs.org` denied there). `sbx` warns this field is deprecated in favor of `caps.network.allow` (kit-spec v2) but still honors it under `schemaVersion: "1"`; not worth chasing v2 syntax for one field until this project pins a newer `sbx`.
- With `--kit`, the two `commands.install` steps (`comet` then `openspec`) add ~24 s + ~4 s to `sbx create` (measured), against the usual 5–6 s — real but avoidable overhead, not a blocker in itself.
- `sbx template save <sandbox> <tag>` (after `sbx stop <sandbox>` — it refuses to snapshot a running container, and non-interactively there's no TTY to answer its confirmation prompt) snapshots a stopped sandbox's container as a reusable local image; `sbx create --template <tag>` then creates from it with **no** kit-apply step at all — back to the usual 5–6 s, both CLIs already present. This also closes `docs/notes/sbx.md`'s pre-existing open question about pinning the agent image version (previously: "Версия claude в образе задаётся sbx… для воспроизводимости это открытый вопрос") — the baked template pins the whole image, not just `comet`.
- The baked template lives in this host's local Docker/`sbx` image store only — it does not travel with the git repo and is not published anywhere. Each runner host bakes (or re-bakes, on a `comet`/`openspec`/base-image version bump) its own copy once, the same one-time-per-host category as `sbx policy init deny-all` already is.
- **`comet native` does not need the other 9 skills the npm package ships, or `openspec`** — checked two ways: `skills/comet-native/SKILL.md` and its bundled runtime (`comet-native-runtime.mjs`/`comet-native-doctor.mjs`) contain zero references to any sibling skill name or to OpenSpec; and a live `comet native new`/`comet native status` run in a freshly baked sandbox succeeded cleanly (exit 0), with the JSON response's own `continuation.skill` field naming only `"comet-native"`. Running `comet native …` from a shell is a pure CLI/state-machine operation — it doesn't consult any project-mounted skill directory at all, since skills are Claude Code instructions for the *agent*, orthogonal to what the CLI binary itself does. `openspec` is vendored anyway, purely as insurance for a possible future switch of this role pipeline to Comet Classic — see the user's own request above, not a Native requirement.
- `@fission-ai/openspec` being a *dependency* of `@rpamis/comet` does not put its `bin/openspec.js` on `PATH` for free — a global npm install only symlinks the top-level package's own declared `bin`, never a nested dependency's. `openspec` needs its own top-level `npm install -g`, which is why the kit has two `commands.install` entries, not one.

- [x] **Step 1: Vendor both tarballs**

```bash
mkdir -p bootstrap/sbx-kits/comet-cli/files/home/.comet-pkg
npm pack @rpamis/comet@0.4.0-beta.18 --pack-destination bootstrap/sbx-kits/comet-cli/files/home/.comet-pkg
npm pack @fission-ai/openspec@1.5.0 --pack-destination bootstrap/sbx-kits/comet-cli/files/home/.comet-pkg
```

Reuse the tag/commit already resolved for `skills/comet/.source.yaml` (Task 7 Step 4) for `comet` rather than re-resolving:

```bash
cat skills/comet/.source.yaml
```

Write `bootstrap/sbx-kits/comet-cli/.source.yaml` with the same `repository`/`tag`/`commit`. For `openspec`, resolve independently (own repository, own release cadence) and write `bootstrap/sbx-kits/comet-cli/.source-openspec.yaml`:

```bash
npm view @fission-ai/openspec@1.5.0 repository.url
git ls-remote --tags https://github.com/Fission-AI/OpenSpec.git | grep -i "1.5.0"
```

- [x] **Step 2: Write the kit spec**

```yaml
# bootstrap/sbx-kits/comet-cli/spec.yaml
schemaVersion: "1"
kind: mixin
name: comet-cli
displayName: Comet CLI suite
description: "Vendored @rpamis/comet + @fission-ai/openspec CLIs for Comet Native workflow roles, with OpenSpec along for a future Comet Classic switch (office-side kit, not part of either upstream package)"
network:
  allowedDomains:
    - "registry.npmjs.org"
commands:
  install:
    - command: "npm install -g /home/agent/.comet-pkg/rpamis-comet-0.4.0-beta.18.tgz"
      user: "agent"
      description: "Install as agent, not root (Install commands default to root) — a root install leaves /usr/local/share/npm-global root-owned in the baked snapshot, breaking any later npm install -g run as agent with EACCES; confirmed live both ways. Root buys nothing here anyway (agent is already in the sudo group)."
    - command: "npm install -g /home/agent/.comet-pkg/fission-ai-openspec-1.5.0.tgz"
      user: "agent"
      description: "Install the vendored openspec CLI globally — comet native itself never calls it (confirmed empirically), vendored only so a future Comet Classic switch does not need a fresh sandbox bootstrap"
```

**Do not use the default `user` (root) for install commands here** — caught by review after the first bake, not obvious up front: it silently root-owns `/usr/local/share/npm-global` in the snapshot, breaking every later `npm install -g` any future role might run as `agent` inside a sandbox from this template. `user: "agent"` avoids it and installs just as well (npm creates the prefix directories agent-owned on first use).

Validate:

```bash
sbx kit validate bootstrap/sbx-kits/comet-cli
```

Expected: `VALID: bootstrap/sbx-kits/comet-cli (directory)`.

- [x] **Step 3: Write the bake script**

`bootstrap/sbx-kits/bake-comet-template.sh` — one-time (or version-bump-time) per host, mirroring the imperative style of `docs/notes/sbx.md`'s own documented one-off commands rather than adding new runner machinery for something that runs once per host. `sbx template save` refuses a running sandbox and there's no TTY non-interactively to answer its stop-confirmation prompt, so `sbx stop` first. `trap ... EXIT` cleans up the probe sandbox on any failure too — caught by review: without it, a mid-script failure (e.g. `sbx exec` after the network-open `sbx create` succeeds) leaves a live sandbox with `registry.npmjs.org` open behind, discoverable only via `sbx ls`:

```bash
#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."  # repo root

KIT_DIR="bootstrap/sbx-kits/comet-cli"
TAG="office-claude-comet:0.4.0-beta.18"
PROBE="office-comet-bake-$$"

trap 'sbx rm --force "$PROBE" >/dev/null 2>&1 || true' EXIT

sbx create --name "$PROBE" --kit "$KIT_DIR" claude "$KIT_DIR" >&2
sbx exec "$PROBE" comet --version >&2
sbx exec "$PROBE" openspec --version >&2
sbx stop "$PROBE" >&2
sbx template save "$PROBE" "$TAG"

echo "Baked $TAG — internal/backends/sbx.Template must match this tag exactly."
```

(The workspace path given to `sbx create` is thrown away — any readable directory works; `$KIT_DIR` itself is convenient and always present.)

```bash
chmod +x bootstrap/sbx-kits/bake-comet-template.sh
```

- [x] **Step 4: Run the bake script and confirm**

```bash
./bootstrap/sbx-kits/bake-comet-template.sh
sbx template ls
```

Expected: `office-claude-comet:0.4.0-beta.18` listed, and the script's own `comet --version` line printed `0.4.0-beta.18` before saving.

- [x] **Step 5: Point the backend at the template**

In `internal/backends/sbx/sbx.go`, add a constant next to `Executable`/`Agent`:

```go
// Template — образ песочницы с запечённым внутрь comet CLI, испечённый один
// раз на хосте bootstrap/sbx-kits/bake-comet-template.sh (bootstrap/sbx-kits/README.md).
// Без него sbx create откажет "образ не найден": это не деградация, а
// намеренный fail-closed — молча откатываться на образ без comet означало бы
// проваливать роли Comet Native непонятно почему на первом же вызове CLI.
const Template = "office-claude-comet:0.4.0-beta.18"
```

Add `"--template", Template` to `createArgs`, right after `"--name", name`, before the `Agent` positional argument. Keep `Agent` unconditional (this repo's `sbx` backend has never created a non-`claude` sandbox and this task does not change that).

- [x] **Step 6: Update the test**

`internal/backends/sbx/sbx_test.go`'s `TestCreateArgs` `want` slice gains `"--template", Template,` in the same position. Reference the constant, not a literal string, so a future version bump can't silently desync code and test.

- [x] **Step 7: Verify against the real CLI once more, end to end**

```bash
go build ./... && go vet ./... && go test ./internal/backends/sbx/...
sbx create --name office-comet-e2e --template office-claude-comet:0.4.0-beta.18 claude "$PWD" && sbx exec office-comet-e2e comet --version && sbx exec office-comet-e2e openspec --version && sbx rm --force office-comet-e2e
```

Expected: both versions printed with **no** `CONFIGURE AGENT`/install step in the `sbx create` output — confirms the template path, not a fresh `--kit` apply, is what ran. Additionally, confirm the skill-independence claim above directly rather than trusting the static grep alone:

```bash
sbx create --name office-comet-native-probe --template office-claude-comet:0.4.0-beta.18 claude "$PWD" \
  && sbx exec office-comet-native-probe sh -c 'mkdir -p /tmp/nativetest && cd /tmp/nativetest && git init -q -b main && git config user.email t@t.com && git config user.name t && echo hi > README.md && git add . && git commit -qm init && comet native new probe-change --isolation current && comet native status probe-change --json' \
  && sbx rm --force office-comet-native-probe
```

Expected: exit 0 throughout, and the `status --json` response's `continuation.skill` field reads `"comet-native"` — nothing else.

- [x] **Step 8: Close the two `docs/notes/sbx.md` open questions**

Edit the "Открытые вопросы" section: mark "Версия claude в образе задаётся sbx; как её закрепить — не разбирались" resolved (own baked template pins it), and rewrite the existing `## comet CLI bootstrap (role-comet-native-workflow)` section's stale "Conclusion" — it said installing `comet` into the image was "a separate prerequisite change outside this repository's control", which the `sbx kit` mechanism found here contradicts — with the actual recipe, the network/timing/permission findings above, and the skill-independence evidence.

- [x] **Step 9: Resolve the design doc's boundary condition**

In `docs/superpowers/specs/2026-08-30-role-comet-native-workflow-design.md`'s "Boundary Conditions Deliberately Left Open", edit the "`sbx` bootstrap" bullet to record that it is resolved, by what mechanism, and where (this task).

- [x] **Step 10: `bootstrap/sbx-kits/README.md`**

One-time host setup, in the same register as `bootstrap/README.md`'s own "Обвязка машины" framing: what the kit is (both CLIs, and why `openspec` is there despite Native not needing it), that it must be re-baked on a version bump of either package (and the tag in `sbx.go` updated to match `comet`'s), and that `sbx create` will fail — live text is `403 Forbidden: pull failed for image`, not an image-not-found message, and easy to mistake for a network-policy problem — on any host that skips this step.

- [x] **Step 11: Independent code review, before the first commit**

Dispatched `pr-review-toolkit:code-reviewer` against the full staged diff (this was written directly, not by a fresh implementer subagent, but this repo's own convention is to never skip review regardless of who wrote the diff). Found and fixed, all re-verified live:

- **Critical: `user: "0"` in both `commands.install` entries root-owns `/usr/local/share/npm-global` in the baked snapshot**, breaking any later `npm install -g` a role runs as `agent` (`EACCES`) — root buys nothing here (`agent` is already in the sandbox's `sudo` group). Fixed: `user: "agent"` in `spec.yaml` (Step 2 above already shows the corrected version), template re-baked, `comet --version`/`openspec --version` re-confirmed from the re-baked template.
- **Critical: the "kit network access is a separate, earlier window" claim in `docs/notes/sbx.md` was factually wrong** — `sbx policy ls <sandbox> --wide` shows the kit's `registry.npmjs.org` merged into the same per-sandbox allow rule as the built-in `claude` kit's domains, open for the sandbox's whole life, not just install time. Not a live issue today (production only ever uses the baked `--template`, never `--kit` directly, and a template-created sandbox carries no kit network rule — confirmed), but the false explanation risked misleading whoever next adds a domain to this `spec.yaml`. Fixed in `docs/notes/sbx.md`, `bootstrap/sbx-kits/README.md`, and this plan's Context bullets above.
- **Important: `docs/ONBOARDING.md`'s dorozhka А has no checkpoint for the bake step** — a new machine would sail through A5 green and only discover the missing template on the first real sandboxed role run. Fixed: Step 12 below.
- **Important: `docs/notes/sbx.md` said "Раннер сам печёт" — wrong, and contradicted `bootstrap/sbx-kits/README.md`'s own correct text in the same diff.** A human bakes once per host with the script; the runner only ever calls `sbx create --template`, unaware baking exists. Fixed.
- **Important: `TAG` in `bake-comet-template.sh` and `Template` in `sbx.go` are two independent string literals with nothing but README discipline holding them equal**, and `TestCreateArgs` can't catch drift since it compares against the `Template` constant itself. Fixed: `TestBakeScriptTagMatchesTemplate` (Step 6 above) reads the script and asserts it contains `Template`'s exact value.
- **Important: the bake script had no cleanup on failure and didn't check `openspec --version`.** A failure between `sbx create` (which opens `registry.npmjs.org` for that sandbox) and the final `sbx rm` left a live sandbox with that network hole open, discoverable only via `sbx ls`. Fixed: `trap ... EXIT` (Step 3 above already shows the corrected script) plus an `openspec --version` check alongside the existing `comet` one.

A scoped re-review of this fix round confirmed all 6 above genuinely resolved (including re-running the mutation the drift test exists to catch, and independently re-verifying the network-persistence claim live), but found 4 more Important findings — all consistency residue the fix round's edits left behind, none of them a new functional bug:

- `docs/notes/sbx.md`'s pre-existing "Версия агента в образе своя" paragraph still said "для воспроизводимости это открытый вопрос: у sbx есть `kit` и `template`, но мы их не трогали" — literally the thing this task now does, left unstruck. Fixed: struck through, pointing at "Открытые вопросы" and the bootstrap section.
- This Context section's own network-persistence bullet (two above) was *not* actually updated when `docs/notes/sbx.md`/`bootstrap/sbx-kits/README.md` were — Step 11's first bullet claimed it was. Fixed here, now.
- `bootstrap/sbx-kits/README.md` mixed English connective words/possessives into Russian prose (`— see spec.yaml's network`, `comet-native's own continuation.skill`) — against this repo's CLAUDE.md language rule (Russian prose, English only for identifiers/config keys). Fixed.
- `docs/notes/sbx.md`'s "Node is confirmed present" paragraph still argued a root-run `npm install -g` is fine (readable/executable files) without flagging that the *directory* it creates is the actual problem, and cited "confirmed... after a kit-driven install" for a configuration the kit no longer has. Fixed: reframed as a trap, pointing at the ownership fix.

- [x] **Step 12: Onboarding and `bootstrap/README.md`**

`docs/ONBOARDING.md`'s "A5. Проверка машины" gets a new checkbox: `sbx template ls` shows `office-claude-comet:<версия>` — missing means the default `sbx` backend will fail every role run with `403 Forbidden: pull failed for image`, not obviously a bootstrap problem; run `bootstrap/sbx-kits/bake-comet-template.sh` and re-check. `bootstrap/README.md`'s opening paragraph ("Здесь то, что стоит вокруг офиса…: задания планировщика … и локальная JIRA …") gets `sbx-kits/` added to what it lists.

- [x] **Step 13: Commit** — done as part of `1f443ee` (checkbox was left unticked by oversight; verified against the file content and commit history 2026-08-31).

```bash
git add bootstrap/sbx-kits bootstrap/README.md docs/ONBOARDING.md internal/backends/sbx/sbx.go internal/backends/sbx/sbx_test.go docs/notes/sbx.md docs/superpowers/specs/2026-08-30-role-comet-native-workflow-design.md docs/superpowers/plans/2026-08-30-role-comet-native-workflow.md
git commit -m "feat(sbx): bake comet + openspec CLIs into the sandbox template via sbx kit"
```

Task 16 Steps 2–6 (the paid live golden-case run) remain gated on the user's separate explicit go-ahead — this task only removes the environmental reason they were certain to fail.

---

## Task 18: Bump the pinned `comet` from `0.4.0-beta.18` to `0.4.0-beta.20`

Added at the user's explicit request, after Task 17. The user first asked whether a newer `comet` existed at all; `npm view @rpamis/comet dist-tags`/`versions --json` showed `0.4.0-beta.19`, `0.4.0-beta.20`, and `0.4.0-rc.1` (the current `latest` — the package has left its beta cycle) ahead of the `0.4.0-beta.18` this whole branch was designed and built against.

**`0.4.0-rc.1` was investigated and rejected as too large a jump for now.** `comet-native/SKILL.md` gains a mandatory "Memory integration" section (`comet task`/`comet memory remember`/`comet memory observe` calls after every phase entry — a new required interaction this pipeline doesn't account for anywhere) and a "Supervisor Change" multi-child decomposition mode (`children.yaml`, `coordination_mode`, multi-session coordination) — 143 changed lines in that one file alone, none of it present at `beta.20`. `@fission-ai/openspec` also bumps `^1.5.0` → `^1.6.0` in `rc.1`. This is a re-investigation on the scale of this branch's original design phase, not a version bump — left for a future change if ever needed.

**`0.4.0-beta.20` was investigated and adopted.** Diff size across the beta.18→19→20 progression: 26 / 4 changed lines in `comet-native/SKILL.md` — no Memory integration, no Supervisor Change. Verified live (not assumed), each check against both the compiled package and, where it mattered, the actual sandboxed CLI:

- `comet-hook-router.mjs` changed (286755 → 289099 bytes) but its blocking contract didn't: fed an identical synthetic `PreToolUse` `Edit` payload for a tracked file outside `build` phase to both versions' script directly — both exit `2` (beta.20's message is more verbose, same exit code `internal/adapters/claude/adapter.go` and `internal/runner/role.go`'s validation depend on). Re-confirmed a third time with the actual re-vendored, now-committed script running inside a real `sbx` sandbox from the re-baked template, against a project mounted the same way production mounts it — same result.
- `continuation.commandArgs` gained two new optional arguments (`--expected-state-version`, `--expected-action`) in `beta.20`; confirmed live that the old-style call this branch's `role.md`s hardcode (`comet native next <name> --summary "..." --confirmed`, without the new arguments) is still accepted — reached the same substantive validation error (`Native change must define at least one acceptance criterion`) as `beta.18` gives for the same minimal, no-real-spec scenario, not a flag-parsing rejection.
- `.comet/config.yaml`'s generated content (schema, defaults, every key `analyst`'s Shape-phase `role.md` logic reads or appends `hook:` to) is byte-for-byte identical between `beta.18` and `beta.20`.
- `archive-ready` and `stateVersion` are both present and used the same way in `beta.20`'s compiled `dist/domains/comet-native/`.
- Cross-version fixture read: copied `evals/implementer/capability-reads-brief-and-spec/fixture/` (seeded under `beta.18`, mid-`build`, two acceptance items) to scratch and ran `beta.20`'s `comet native status --json` against it directly — parsed cleanly, correct phase (`build`), correct acceptance list and text, no schema error. (`bindingState: mismatch` in that run is an artifact of skipping `git init` in the quick scratch copy, not a version issue.)

**Not verified live: a full Shape→Build→Verify→Archive cycle on `beta.20`** — writing a minimal but real `brief.md`/`specs/<capability>/spec.md` that Native's acceptance-extraction actually accepts is itself a small reverse-engineering exercise (this branch's original design phase did it once, for `beta.18`); this task's investigation stopped at the two probes above once they gave a clear, consistent, low-risk signal, rather than re-deriving that exercise for `beta.20` too. Task 16's eventual live run (still gated on separate user go-ahead) is the first point this pipeline exercises that full cycle for real, now against `beta.20`.

**Files:**
- Edit (full content replace, following Task 7/8's own vendoring procedure exactly — remove everything but `.source.yaml`, copy `npm pack @rpamis/comet@0.4.0-beta.20`'s `assets/skills/comet/`/`assets/skills/comet-native/` over it): `skills/comet/`, `skills/comet-native/` (including restoring the executable bit on `skills/comet/scripts/comet-hook-router.mjs` — `git ls-tree` showed it as the only `100755` file under either tree pre-bump; `npm pack` ships it `644`, exactly the gotcha `role.md`/`role.go`'s own comments already document).
- Edit: `skills/comet/.source.yaml`, `skills/comet-native/.source.yaml`, `bootstrap/sbx-kits/comet-cli/.source.yaml` — `tag: 0.4.0-beta.20`, `commit: bcf9e4ab0614ec72ae8317fee104eed4ea9537c4` (`git ls-remote --tags`, lightweight tag, no `^{}` dereference needed).
- Edit: `bootstrap/sbx-kits/comet-cli/spec.yaml` — install command path only (`rpamis-comet-0.4.0-beta.20.tgz`); `openspec`'s own pin is untouched (`beta.20` still depends on `@fission-ai/openspec@^1.5.0`, same as `beta.18` — checked its `package.json` directly).
- Replace: `bootstrap/sbx-kits/comet-cli/files/home/.comet-pkg/rpamis-comet-0.4.0-beta.18.tgz` → `…-beta.20.tgz` (fresh `npm pack`).
- Edit: `bootstrap/sbx-kits/bake-comet-template.sh` (`TAG=`), `internal/backends/sbx/sbx.go` (`Template` constant) — both `office-claude-comet:0.4.0-beta.20`. `TestBakeScriptTagMatchesTemplate` (Task 17) catches either one being missed.
- Re-baked `office-claude-comet:0.4.0-beta.20` locally (old tag removed first) and re-verified live per the bullets above.

**Not touched, deliberately:** `roles/implementer/role.md`'s and `roles/reviewer/role.md`'s "проверено живым прогоном 0.4.0-beta.18" footnotes (citing the exact wording of a specific CLI validation error message) — left as an accurate historical citation of when and against what version that specific error text was confirmed, not a claim about the CLI currently running. `docs/openspec/changes/role-comet-native-workflow/.comet/**` and `design.md` — Classic-workflow artifacts from this repo's own Open-phase history for this change; historical record, not live configuration, not edited retroactively. `docs/notes/sbx.md`'s one surviving `0.4.0-beta.18` mention (in the "Resolved: `sbx kit` bakes…" recipe) is inside a sentence describing the exact command that investigation ran at the time — also left as accurate history, not a current-state claim (the current pin lives in code/`.source.yaml`/the bake script, all updated above). `.claude/skills/comet/**` — a second, separately tracked vendored copy of the upstream `comet` skill, used by this repo's own Classic-workflow meta-development (not by the role pipeline), still at `beta.18`; out of this task's scope, not this task's responsibility to bump.

**Review found what the "not verified live" caveat above predicted it might: a real break, exactly in the untested part of the cycle.** Dispatched `pr-review-toolkit:code-reviewer` against the full staged diff; rather than stop at the static/`--kit`-probe checks this task's own investigation had done, the reviewer drove a complete real Shape→Build→Verify→Archive cycle (real `brief.md`+`specs/calc/spec.md`, through `dispatch-verifier`, a passing `final-result`) on both `beta.18` and `beta.20` and found:

- **Critical, confirmed and fixed:** `0.4.0-beta.20` splits the single `--confirmed` flag `roles/reviewer/role.md` hardcoded for the post-Verify-pass confirmation into four mutually exclusive flags — `--confirmed` (Shape only), `--accept-result`, `--revise-implementation`, `--revise-requirements`. The old command (`--confirmed` at this specific gate) now fails with exit `64` and leaves state unchanged in `await-user`; `internal/pipeline/archive.go`'s `archiveIfReady` (gated on `stage == "archive-ready"`) would never fire, while `reviewer` still reports `done`/`next_owner: none` — the change hangs forever, silently, exactly as `role.md`'s own surrounding prose already warned a missed step would. Independently re-confirmed from the compiled source rather than taking the live-run report on faith: `native-portable-continuation.d.ts` gained a `commandAlternatives?: NativePortableCommandAlternative[]` field entirely absent from `beta.18`'s version of the same interface, and `native-next-command.js` explicitly throws `NativeUsageError` if more than one of the four flags is passed together, with `'--accept-result is only valid for a pending Verify pass decision'` as its own error text. Fixed: `roles/reviewer/role.md` step 5 now uses `--accept-result` for this gate, with guidance to prefer the response's own `commandAlternatives[].commandArgs` (which already carries the `--expected-state-version`/`--expected-action` guard arguments) over hand-constructing the command, per the newly-vendored `SKILL.md`'s own instruction not to "reconstruct an unguarded command." Cross-referenced in the design doc's Evidence base item 9, which cited the same now-stale command.
- **Important, not fixed, flagged for the user:** the newly-vendored `SKILL.md`/`reference/clarification.md` add a mandatory "source-document full-coverage mode" triggered whenever "the user directly supplies a file, attachment, link, or local path as a requirements source" — which plausibly describes `analyst`'s own `.agent/task.md` on every run. The mode requires a `## Source coverage` map in `brief.md` and keeps unmapped/unconfirmed content `[blocking]`, which `role.md:74-79` already turns into `needs_human`. This could shift `analyst` toward escalating to a human noticeably more often than under `beta.18` — not necessarily wrong, but an unconsidered behavior change from a version bump, not something this task decided on purpose. No code or `role.md` change made for it (the role's own design already defers entirely to "follow `comet-native`'s own protocol as-is"); the user should know it may show up as more `needs_human` rounds once Task 16's live run exercises `analyst` for real.
- The reviewer's "point 7" pass otherwise confirmed every "not touched, deliberately" item above holds up, and found no other live inconsistency.

Full `go build/vet/test` re-confirmed clean after the fix.

A scoped re-review of just this fix drove the fixed command through a real, complete Shape→Build→Verify→Archive cycle (its own, independent of the first round's) and confirmed it now genuinely reaches `archive-ready` and archives — `internal/pipeline/archive.go`'s gate fires, `comet native archive` succeeds. It also read the live `commandAlternatives` response directly and found two minor wording problems in the new `role.md` prose, both fixed: it said "one object per each of the four flags," but the array holds exactly three entries at this gate (`--confirmed` is structurally excluded, never appears there); and it called the alternative's `commandArgs` "ready to run," when the `--summary` value is actually a literal `<summary>` placeholder the CLI does not itself catch if executed unsubstituted — the wording now says so explicitly. Nothing else from either round's fix was found wrong.

- [x] **Commit** — done as part of `f32d953` (checkbox was left unticked by oversight; verified against role.md content and commit history 2026-08-31).

```bash
git add skills/comet skills/comet-native bootstrap/sbx-kits/comet-cli internal/backends/sbx/sbx.go bootstrap/sbx-kits/bake-comet-template.sh roles/reviewer/role.md docs/superpowers/specs/2026-08-30-role-comet-native-workflow-design.md docs/superpowers/plans/2026-08-30-role-comet-native-workflow.md
git commit -m "chore(comet): bump pinned comet from 0.4.0-beta.18 to 0.4.0-beta.20"
```

---

## Task 19: fix `eval-roles` runs never naming a fixture's Comet Native change directory

Found live, mid-Task-16-live-run, at the user's explicit go-ahead to run the paid golden-case checkpoint. Not a beta.20 regression — a pre-existing gap in how `eval-roles`/`run-agent` compose `context.md`, present since whichever of Tasks 9–15 first wrote a Comet-Native-dependent golden case, only now actually exercised live.

**Symptom:** `reviewer/capability-spot-defect` failed twice, reproducibly, with `command "grep -qE '\bA1\b' .agent/result.json" failed`. Re-run with `--keep-failed` and inspected: the reviewer wrote a fully correct, well-reasoned defect report (`outcome: done`, `next_owner: implementer`, pinpointing the real bug in `Max`) — but never called `comet native` at all (`grep 'comet native' .agent/run.log` — zero matches; `.comet/runtime/.../state.json` — `execution: null, checks: []`, untouched). Per its own `role.md`, that's the *correct* fallback for "no active Comet Native change in context" — the reviewer wasn't wrong, `context.md` was.

**Root cause, traced in code, not guessed:** `cmd/run-agent/main.go`'s `execute()` builds `passport := runner.Run{...}` without ever setting `TaskKey` — by design, per its own existing doc comment on the field ("Пуст при ручном запуске: трекера там нет"), since `run-agent` has no tracker. `internal/runner/input.go`'s `composeContext` derives the `context.md` line "Каталог изменения" from `CometChangeDirRel(run.TaskKey)`; with an empty key this deterministically resolves to `docs/comet/changes/manual` (confirmed live: `CometChangeDirRel("")` — verified with a throwaway test), which matches none of this branch's eval fixtures (`eval-brief`, `eval-spot-defect`, `eval-resume`, `eval-clean-verify`). So the line is never written for any `eval-roles`-driven run, full stop — regardless of `comet` version.

**Why most of the 6 new golden cases still passed despite this:** their `task.md` happens to textually name "this change" (`capability-reads-brief-and-spec`: *"Continue the work described in **this change**"*; `capability-clean-verify`: *"Review the already-passing work described in **this change**"*) — enough for a capable agent to go looking at `docs/comet/changes/` on its own initiative and find the right one, independent of the broken structured field. `capability-spot-defect`'s `task.md` (*"A colleague implemented the `Max` function… Review their work"*) gives no such cue, so nothing compensated for the gap. Confirmed exactly two fixtures round-trip-fixed by this: `CometChangeName("eval-brief") == "eval-brief"` and same for the other three fixture names — a directory name that's already `cometSafeName`-safe passes through `changeName`/`cometSafeName` unchanged (verified live with a throwaway test), so the fixture's own directory name is always a valid, reusable task key.

**Fix:** give `run-agent` a way to be told the task key explicitly, and have `eval-roles` supply it from what the fixture itself already seeded — the same shape of information a real tracker-driven run would carry, not a new concept.

**Files:**
- Edit: `cmd/run-agent/main.go` — new `--task-key` flag, wired to `passport.TaskKey`.
- Edit: `cmd/eval-roles/fixture.go` — new `discoverFixtureTaskKey(fixtureDir string) string`: lists `docs/comet/changes/*`, returns the single subdirectory's name, `""` if none or more than one (never guesses between ambiguous candidates).
- Edit: `cmd/eval-roles/invoke.go` — `runRoleAgent` gains a `taskKey string` parameter; appends `--task-key <taskKey>` to the `run-agent` invocation when non-empty.
- Edit: `cmd/eval-roles/run.go` — `evaluateCase` calls `discoverFixtureTaskKey(fixtureDir)` and threads the result through.
- Edit: `cmd/eval-roles/testdata/fakeagent/main.go` — accepts `--task-key` (previously undeclared, would have made `go test` fail once real code started passing it) and records it under `.agent/.fake-task-key` for tests to assert — **inside `.agent/`, not the fixture root**, since `.agent/` is the one directory `ExcludeAgentDir` already keeps out of git; a first attempt at this instrumentation wrote the marker to the fixture root and immediately broke `TestEvaluateCaseSpotDefectDistinguishesFoundVsMissed` by tripping its `diff_scope` check — caught by `go test`, not left in.
- Edit: `cmd/eval-roles/invoke_test.go` — updates the two existing `runRoleAgent` call sites for the new parameter, adds `TestRunRoleAgentPassesTaskKey`.
- Edit: `cmd/eval-roles/fixture_test.go` — adds `TestDiscoverFixtureTaskKeyFindsSeededChange`, `TestDiscoverFixtureTaskKeyEmptyWithoutChange`, `TestDiscoverFixtureTaskKeyEmptyWithMultipleChanges`.

**Interfaces:** `runner.Run.TaskKey` (already existed, already read by `composeContext`) is now reachable from a CLI flag for the first time; no schema change. Real, tracker-driven runs are untouched — the runner already sets `TaskKey` itself before this code path is ever reached.

**Not done:** editing `capability-spot-defect`'s own `task.md` to add a "this change" hint, as a second, weaker line of defense alongside the code fix — judged unnecessary once the code fix makes the context field reliable, and doing both would leave it unclear which one actually made the case pass in a future re-read of this history.

Full repo `go build/vet/test` clean. Independent review (`pr-review-toolkit:code-reviewer`) dispatched against this diff before committing.

**Review confirmed every root-cause and blast-radius claim above independently** (own throwaway tests for `CometChangeDirRel("")` and the four fixtures' round-trip, traced every consumer of `Passport.TaskKey` in `internal/pipeline`/`internal/adapters/claude` to confirm production is untouched, confirmed `.agent/.fake-task-key` is structurally inert to every `diff_scope` check via `ExcludeAgentDir`/`git ls-files --others --exclude-standard`) and found 2 Important gaps in the fix's *durability*, both fixed:

- **Nothing tested the one line the whole fix rests on** (`passport.TaskKey = *taskKeyFlag` in `cmd/run-agent/main.go`) — mutation-tested by the reviewer (corrupted the value, `go test ./...` stayed fully green) to prove the gap was real, not theoretical. The existing tests each covered a neighbor (`composeContext` given an already-set `TaskKey`; `invoke.go` passing `--task-key` to a *fake* agent with its own independent flag parsing) but never the seam where the real binary turns the flag into the passport field. Fixed: `TestDryRunNamesCometChangeDirFromTaskKey` in `cmd/run-agent/main_test.go`, mirroring the existing `TestDryRunNamesBranches` pattern — builds the real binary, runs `--task-key eval-brief --dry-run` against a workdir that has `docs/comet/changes/eval-brief/`, asserts the exact "Каталог изменения" line in the real `context.md`. Personally re-ran the reviewer's own mutation against this new test with `-count=1` (plain `go test` without it silently used a stale cached pass) — fails with a clear message naming the corrupted value; reverted, re-confirmed green.
- **`discoverFixtureTaskKey` degrades to the exact pre-fix bug, silently, on any fixture-authoring mistake.** The function's own doc comment claimed the round-trip `CometChangeName(name) == name` is "guaranteed" — it isn't, technically; eval fixtures are hand-authored directory trees, not Comet-produced artifacts, and a directory named e.g. `eval_brief` (underscore) would silently `discoverFixtureTaskKey` its way back to `""`, reproducing the original bug with zero signal, discoverable only by another paid live run. Fixed: `discoverFixtureTaskKey` now returns `(name, warning string)` — still returns `""` for the mismatch case (passing the unsanitized name wouldn't help; `CometChangeDirRel` would sanitize it into a path that *still* doesn't match the real directory), but the warning names exactly what's wrong. Same treatment for the pre-existing "more than one change directory" case, previously also silent. `evaluateCase` (`cmd/eval-roles/run.go`) prints the warning to `stderr`, prefixed with the case name. Three new tests in `cmd/eval-roles/fixture_test.go` cover: no warning on a clean match, a real warning on ambiguity, a real warning on an unsafe directory name.

Also noted by the review, informational rather than a defect: `context.md` for any `eval-roles` run with a discovered task key now also gains a `- Задача: <fixture dir name>` line (the same `composeContext` code that already emits it whenever `TaskKey != ""` — not something this task's own code added deliberately, just a consequence of setting the field at all). Harmless for `implementer`/`reviewer` (neither reads it), and actively helpful for `analyst`: `capability-resume-no-reinvoke`'s task key now resolves to exactly `eval-resume`, matching what its own `role.md` logic (`CometChangeName` from the task key) was already trying to derive from task-text alone. Worth knowing, not worth engineering around.

Full repo `go build/vet/test -count=1` (fresh, not cached) clean after both fixes.

- [x] **Commit** — done as part of `7996f51` (checkbox was left unticked by oversight; verified against commit history 2026-08-31).

```bash
git add cmd/eval-roles cmd/run-agent docs/superpowers/plans/2026-08-30-role-comet-native-workflow.md
git commit -m "fix(eval-roles): pass --task-key so context.md names a fixture's Comet Native change"
```

After this commit, resume Task 16 Steps 3–4 from the start of the affected list — the `--task-key` fix changes what `context.md` says for every case that seeds a Comet Native change, so cases that already passed under the old, broken behavior are worth re-running too, not just `capability-spot-defect`.

---

## Task 20: `role.md` must forbid loading the `comet` entry skill directly

Found live, immediately after Task 19's fix, re-running `analyst/capability-basic-plan` (the simplest possible case — Shape only, no pre-seeded change) at the user's own go-ahead. Failed 2 of 3 attempts, each time differently, all traced to the same root behavior.

**What happened, from `.agent/run.log` on both failed attempts:** the agent's very first move was loading the `comet` skill (not `comet-native`) via the `Skill` tool, then following that skill's own text verbatim — it opens with *"Immediately perform the entry resolution below; do not re-evaluate whether the task is suitable for Comet, and do not merely explain why it will not be used"* and instructs `comet workflow resolve . --activate --json` as step 1. That command reproducibly fails in this sandbox: `{"status":"failed","error":"ENOENT: no such file or directory, fstat"}` — confirmed with `--json`/without, with `DEBUG=*`, three consecutive attempts, `comet doctor` pointing at missing global state under `/home/agent`. Root cause of *that* failure: `comet workflow resolve --activate` is the generic `/comet` Native-vs-Classic dispatch entry point, and it wants global, per-project-independent Comet state this repo's `role.md` was always designed to never need — `analyst`/`implementer`/`reviewer` go straight to `comet native <subcommand>`, bypassing this entry point entirely by design (`skills/comet/` is mounted only because `comet-hook-router.mjs`, the guard script `hooks.pre_tool_use` needs, lives inside it). Nothing in `bootstrap/sbx-kits/` ever needed to set up that global state, because the design never calls the command that wants it — until the agent, on its own initiative, calls it anyway.

**The one attempt that partially worked** went straight to `comet native new greet-shout --isolation current --json` per `role.md`'s actual instructions, and that succeeded — confirming the prescribed path works; only the unprescribed detour through the `comet` skill's own entry protocol fails. (That attempt then hit a separate, unrelated problem — a stale `root-move.lock` Comet Native's own lock coordinator left behind, requiring `comet native doctor --repair`; confirmed present in both `0.4.0-beta.18` and `0.4.0-beta.20`'s compiled `dist/`, so not a version-bump regression, and Comet's own `reference/recovery.md` names "a concurrency conflict" as an expected, recoverable case — not investigated further here, tracked as a known source of live-run flakiness, not fixed).

**Fix:** an explicit, early warning in all three `roles/{analyst,implementer,reviewer}/role.md`, placed at the first point each role's text touches Comet Native (before any conditional branching), forbidding loading the `comet` skill via the `Skill` tool or running its entry-protocol commands (`comet workflow resolve`, `comet init`), naming the exact failure mode and pointing at `comet native <subcommand>` as the only path this pipeline uses. Prose-only change — no code, no schema, nothing for `go test` to check; the only real verification is a live re-run.

**Files:** `roles/analyst/role.md`, `roles/implementer/role.md`, `roles/reviewer/role.md`.

- [x] **Verify:** re-ran `analyst/capability-basic-plan` live a third time — the agent went straight to `comet native new`/`init` per `role.md`'s prescribed path, never touched the `comet` skill's entry protocol. That specific failure mode is confirmed fixed. (A separate, unrelated problem — the bind-mount lock incompatibility investigated in Task 21 below — surfaced immediately after on the very same attempt, which is why this checkbox and the commit below sat unchecked for a while.)
- [x] **Commit**

```bash
git add roles/analyst/role.md roles/implementer/role.md roles/reviewer/role.md docs/superpowers/plans/2026-08-30-role-comet-native-workflow.md
git commit -m "fix(roles): forbid loading the comet entry skill's own dispatch protocol"
```

Committed as `5995f29`.

---

## Task 21: `sbx --clone` backend mode — Comet Native's lock doesn't survive the virtiofs bind-mount

Found live, immediately after Task 20's fix landed the agent past the `comet` entry-skill detour: the very same attempt hit a *different*, deeper failure — 16 `"code":"conflict"` responses from `comet native`'s mutation lock (`.comet/runtime/native/root-move.lock`, a claim-based coordinator with a 5s timeout and a 2–9ms retry loop) in one run, burning all 50 turns and $0.85 without a result; `comet native doctor` diagnosed `"code":"lock-stale","message":"Native lock owner process is absent"`.

**Root-caused, not worked around.** A clean, no-agent-cost, same-sandbox-instance A/B test (`comet native` file operations run once on the sandbox's own internal filesystem, once on the bind-mounted host workspace, same commands, same container) confirmed the lock's `mkdir`/`fstat`/atomic-rename sequence is unreliable specifically on sbx's bind-mounted (`virtiofs`) workspace and works cleanly on the sandbox's own filesystem. Confirmed identical in `0.4.0-beta.18` and `0.4.0-beta.20`'s compiled `dist/` — not a version regression. Checked for a disable/relocate mechanism in Comet's own source (`native-paths.js`): the lock's directory is a hardcoded path under the resolved project root, with an explicit `isSymbolicLink` rejection closing off a symlink-based workaround — there is no supported way to turn it off or move it.

**The fix sbx itself offers:** `sbx create --clone` — documented in `sbx create --help` as running the agent "on an in-container clone of the host repo... wired back via a git-daemon" instead of bind-mounting. Live-verified in this session, in this order, before writing any code:

- `--clone`'s primary path must be an ordinary, non-bare, non-worktree, read/write git repository — it rejects both a bare repo (`"is not in a Git repository"`) and a worktree (`"is not supported when run from a Git worktree"`) outright. This rules out pointing it directly at this runner's bare-repo-plus-worktree task isolation model (`internal/workspace`) without an extra step; ruled *out*, not abandoned — see "Not done" below for the shape that step would take.
- With a plain repo, the workspace the agent actually operates on lives on the sandbox's own disk (`ext4`, a dedicated `/dev/vdX` device — confirmed via `mount` inside the sandbox), not the `virtiofs` bind-mount, and not even the sandbox's `overlay` root — a third, cleaner filesystem the earlier root-cause investigation hadn't even considered.
- Extra mount paths beyond the first (role dir read-only, config dir read-write) pass through completely unaffected — `--clone` only reshapes the *first* Workspaces path.
- `sbx create --clone` itself adds a `sandbox-<name>` git remote, backed by a host-local git-daemon, into the primary path on the host — `git fetch` from that remote after the run pulls the agent's commits back, with no outbound network needed from inside the sandbox at all (the user's own suggested design — commit inside, `git push` out — turned out to already be built in, just pull-driven from the host rather than push-driven from the agent, which is strictly better: no egress credentials needed inside the sandbox).
- **The one real gap:** `--clone`'s in-container clone only carries git-tracked content. `.agent/` (the whole exchange directory) and the untracked parts of `.comet/` are excluded from git *on purpose* (`runner.ExcludeAgentDir`, `runner.ExcludeCometRuntime`) — confirmed live that `.agent/task.md`, written to the host path before `sbx create --clone` even runs, is simply absent inside the sandbox. `sbx cp` moves it in and back out, but arrives owned by the host's uid, unwritable by the sandbox's `agent` user — confirmed live, fixed live with `sbx exec -u root <name> chown -R agent:agent <path>`.

**Scope decision:** implemented as a reusable backend capability (`runner.Launch.Clone` / `internal/backends/sbx`), wired into `cmd/run-agent` (`--clone` flag) and `cmd/eval-roles` (`--clone` flag, threaded through `runRoleAgent`/`evaluateCase`) — enough to re-run Task 16's live golden-case checkpoint, which is what this investigation was actually blocking. **Not wired into `internal/pipeline/pipeline.go`** (the tracker-driven production conveyor): that path uses the bare-repo-plus-worktree model directly, which `--clone` rejects as-is, and would need a new `workspace.CloneSource` step (fresh plain clone of the task branch off the bare repo before creating the sandbox) plus routing the post-run fetch into the real worktree (`ws.Dir`) rather than the disposable clone. Only half of that is already generic: `fetchBranch`'s `--ff-only` merge already targets an arbitrary `FetchInto` path, not necessarily the clone source itself (proven in `internal/backends/sbx/clone_test.go`'s `TestFetchBranchFastForwardsFetchInto`) — but `cloneSyncIn`/`cloneSyncOut`'s `Dirs` sync (`.agent`, `.comet/current-change.json`, `.comet/runtime`) is hardcoded to `l.Workspaces[0].Path`, not `FetchInto` (documented honestly on `CloneSync.FetchInto` in `internal/runner/launch.go` after the second review round below — today's one caller, `cmd/run-agent`, keeps them equal on purpose). A pipeline wiring would need that extended too, not just the fetch. Touching the real task-execution pipeline in the same pass as landing an unexercised new backend mode was judged higher-risk than the eval-only path warrants, and out of scope for what Task 16 actually needs. Left as explicit future work, not silently dropped.

**Files:**
- Edit: `internal/runner/launch.go` — new `CloneSync` struct (`FetchInto`, `Branch`, `Dirs`) and `Launch.Clone *CloneSync` field. `nil` means the existing bind-mount behavior; `local` backend never looks at it.
- New: `internal/backends/sbx/clone.go` — `cloneSyncIn`/`cloneSyncOut` (sync `.agent`/`.comet` via `sbx cp` + `chown` before/after the run, tolerating a missing `.comet/` in the sandbox as legitimate rather than an error) and `fetchBranch` (`git remote get-url sandbox-<name>` read from the primary path's own config, `git fetch` that URL, `git merge --ff-only` into `Clone.FetchInto`).
- Edit: `internal/backends/sbx/sbx.go` — `createArgs` inserts `--clone` ahead of the positional agent/paths when `l.Clone != nil`; `Run` calls `cloneSyncIn` right after the sandbox is created (before `--workdir` is ever touched) and `cloneSyncOut` right after the agent process exits, on every return path (timeout, agent non-zero exit, unexpected exec error, clean success) and strictly before the pre-existing `defer remove(name)` — losing that ordering would mean `sbx rm` discards the agent's commits, exactly the warning `sbx rm` itself prints for a `--clone` sandbox.
- Edit: `internal/runagent/runagent.go` — `Options.Clone *runner.CloneSync`, copied onto the built `Launch` in `Prepare`.
- Edit: `cmd/run-agent/main.go` — new `--clone` flag; when set, requires `workdir` to have a real branch (`headBranch`, already existed) and builds `Clone: &runner.CloneSync{FetchInto: workdir, Branch: branch, Dirs: []string{runner.Dir, ".comet"}}` — `FetchInto` is the same `workdir` path deliberately: a manual run has no separate worktree, and it's safe because `--clone` only reads the primary path at sandbox-creation time, never writes back to it directly. `printDryRun` now shows the resolved `Clone` (or its absence) alongside the existing Workspaces listing.
- Edit: `cmd/eval-roles/{main,run,invoke}.go` — new `--clone` flag on `eval-roles` itself, threaded through `evaluateCase`/`runRoleAgent` to the same `run-agent --clone` flag. No fixture changes needed: `materializeFixture` already produces a plain, non-bare, non-worktree repo — exactly the shape `--clone` requires, with no extra preparation step.
- New: `internal/backends/sbx/clone_test.go` — `cloneSyncIn`/`cloneSyncOut` against a fake `step` (existing test-substitution pattern in this package) covering: only-existing dirs get copied and chowned, nothing-to-sync is a true no-op (no `sbx` call at all), a real `sbx cp` failure propagates, a `"not found in container"` failure for one dir doesn't fail the whole sync. `fetchBranch` against real local git repos (mirroring `internal/workspace`'s existing test style) covering a clean fast-forward and an explicit failure on divergence. `createArgs` extended for the `--clone` flag's presence/placement.
- Edit: `cmd/run-agent/main_test.go` — `TestDryRunCloneSetsCloneSync` (dry-run shows the resolved branch/FetchInto/dirs), `TestCloneWithoutBranchFails` (detached HEAD is an infra error, exit 2, not a silent no-op).
- Edit: `cmd/eval-roles/{invoke,run}_test.go` — existing call sites updated for the new `clone bool` parameter (all `false`; the fake-agent harness doesn't exercise real sbx).

**Verification, before writing any code:** every claim above about `--clone`'s behavior — the worktree/bare-repo rejection, the primary-path git-repo-ness check, the mixed-path mount behavior, the `ext4` filesystem, the `sandbox-<name>` remote and its git-daemon URL, the untracked-content gap, the `sbx cp` ownership issue and its `chown` fix, the `"not found in container"` cp error text — was confirmed with real `sbx create`/`sbx exec`/`sbx cp`/`git` commands against disposable local fixtures, at zero agent cost, cleaned up (`sbx rm --force`, `sbx ls` confirmed empty) after each probe. Only after that did implementation start.

**Not done:** `internal/pipeline/pipeline.go` wiring (see "Scope decision" above) — a follow-up task, not started. Task 16 Steps 3–4 (re-run the live golden-case checkpoint with `--clone`) — next.

Full repo `go build/vet/test ./...` clean. Independent review (`pr-review-toolkit:code-reviewer`) dispatched against the diff before committing.

**Review found 2 Critical and 6 Important gaps, all fixed:**

- **Critical — `cloneSyncOut` copied the whole `.comet` dir back to the host *before* `git merge --ff-only`.** Since `roles/analyst/role.md` commits `.comet/config.yaml`, this planted a working-tree copy of a tracked file ahead of the merge that was supposed to bring that same commit in — reproduced locally by the reviewer with git's own refusal (`"your local changes... would be overwritten by merge"`). That means the very first real use case — analyst creating a new Comet Native change — would have failed on `fetchBranch` every time, with the sandbox removed and the agent's commits gone except as unreachable objects. Fixed two ways, deliberately redundant: `cloneSyncOut` now calls `fetchBranch` *first*, dir sync second; and `Dirs` (set in `cmd/run-agent/main.go`) narrowed from `[".agent", ".comet"]` to `[".agent", ".comet/current-change.json", ".comet/runtime"]` — exactly the two untracked paths `runner.ExcludeCometRuntime` names, not the tracked `config.yaml` alongside them.
- **Critical — git's own per-worktree exclude rules never reach the sandbox.** `runner.ExcludeAgentDir`/`ExcludeCometRuntime` write to `.git/info/exclude` on the host, which `git clone` never copies (by design, same as hooks) — confirmed live that a fresh `--clone` sandbox's `.git/info/exclude` is the untouched git default. Inside the sandbox, `.agent`/`.comet/runtime` would show up as ordinary untracked dirt instead of excluded, contradicting `roles/analyst/role.md`'s explicit promise ("the runner keeps the rest out of git") and creating a real risk that `git add -A` — which role.md only discourages in prose, not technically — sweeps the whole exchange directory into the task branch. Fixed with a new `syncExcludeFile` in `internal/backends/sbx/clone.go`, copying the host's `.git/info/exclude` over the sandbox's default one, called from `cloneSyncIn` before the directory sync. Verified live before coding: `sbx cp` lands a single file into an existing directory correctly (overwrite, not nesting), and the copied file's host ownership doesn't matter — git only reads it, and `sbx cp` leaves it world-readable.
- **Important — every `sbx cp`/`git fetch`/`git merge` call in the new code ran with no timeout**, against the file's own convention (`create`/`policy`/`rm` in `sbx.go` all have one). A hang would block the runner forever; worse, on the post-run path, an already-cancelled caller `ctx` (the run's own timeout) could kill the one chance to recover the agent's commits before `defer remove(name)` discards them. Fixed: `cloneSyncTimeout = 5 * time.Minute`; `cloneSyncIn` wraps the caller's `ctx`, `cloneSyncOut` wraps `context.Background()` instead — same reasoning `remove()` already uses for the identical problem.
- **Important — the decision logic inside `Run()` (which of timeout/exit-code/clone-sync-failure wins) had zero test coverage**, only the leaf functions did. Extracted into a pure `cloneOutcome(log io.Writer, name string, timedOut bool, timeout time.Duration, runErr, cloneErr error) (int, error)` in `clone.go`; `Run()` now just calls it. New table-driven `TestCloneOutcome` (8 cases) exercises it with real `*exec.ExitError` values (via `exec.Command("sh", "-c", "exit N").Run()`, not fabricated structs). The call *order* inside `Run()` itself (`cloneSyncIn` before the agent process, `cloneSyncOut` after and strictly before `defer remove`, `presentOnEntry` threading through) remains untested by a unit test — closing that fully would mean injecting the agent-process launch itself, judged disproportionate for what a real live run (next, this same task) exercises end-to-end anyway. Flagged to the reviewer explicitly as an accepted trade-off, not a silently dropped gap.
- **Important — self-contradictory comments.** `cmd/run-agent/main.go` said `--clone` mounts the workdir "read-only"; `internal/backends/sbx/sbx.go` said the same path "must be read/write" (`--clone` itself enforces this, measured live). One was simply wrong, and it was the load-bearing one — it was the whole justification for why writing back into that path post-run is safe. Fixed: `main.go`'s comment now correctly describes cloning (the agent works on the sandbox's own disk; the host path is untouched until sync-back), not a read-only bind-mount.
- **Important — `CloneSync.FetchInto`'s doc comment promised generality the code can't deliver.** `Dirs` sync targets `Workspaces[0].Path`, not `FetchInto` — today's only caller keeps them equal on purpose, but the comment implied a general contract that would silently misroute `.agent/result.json` if anyone ever separated them. Fixed: comment in `internal/runner/launch.go` now says so plainly. Also added, per the review's note that `internal/pipeline/agent.go` leaving `Clone` unset looked like a dropped thread rather than a decision: an explicit comment there pointing at this task. Separately (an answered question, not a finding): added a check in `fetchBranch` that `FetchInto` is actually on `c.Branch` before merging — `--ff-only` alone only guarantees fast-forward, not that it's fast-forwarding the *branch you meant*.
- **Important — `--clone` on the `local` backend was silently ignored**, against this same codebase's own established convention (`NetworkNotice`/`NetworkAudit`, with a test asserting exactly "silent non-application reads as applied"). Fixed: new `runagent.CloneNotice(backend string, clone bool) string`, same shape as `NetworkNotice`, called from `cmd/run-agent/main.go` alongside the existing two.
- **Important — `TestDryRunCloneSetsCloneSync` was partly vacuous.** `.agent` as a bare substring appears in `--dry-run` output even without `--clone` (7 times, from the role's own system prompt) — that half of the assertion would have passed regardless of whether `--clone` did anything. Fixed: asserts the one fully-rendered line (`"каталоги:   .agent, .comet/current-change.json, .comet/runtime"`) instead of loose substrings.

Two self-run mutation tests after the fix round: reverted the `cloneSyncOut` ordering back to dirs-then-fetch — `TestCloneSyncOutStopsAtDirsWhenFetchFails` caught it immediately (cp calls recorded despite the merge having failed first). Defeated the `presentOnEntry` not-found asymmetry check — `TestCloneSyncOutFailsWhenExpectedDirMissingOnExit` caught it immediately. Both reverted, full suite re-confirmed green.

**A scoped re-review of just this fix round confirmed every round-1 fix holds and found 2 more Critical-grade gaps in the fix itself, both fixed:**

- **Critical — `run.log` lives inside `.agent/` (`runner.Dir/runner.FileLog`), which `Dirs` round-trips wholesale.** A stale `run.log` left in a reused workdir gets copied *into* the sandbox by `cloneSyncIn` before `os.Create(logPath)` ever truncates the host copy, then `cloneSyncOut`'s directory-level `sbx cp` copies `.agent` back out and — per `sbx cp`'s own documented "already exists as a directory → place inside it" semantics — overwrites the just-correctly-written current run's log with that stale one. Everything downstream that reads `logPath` after `Run()` returns (`usageOf`, `limitOf`, `endingOf`, `terminationOf`, `runner.Archive` in `runagent.Execute`) would silently judge cost, turn count, and termination classification from a *previous* run's log — exactly the class of bug `internal/runner/input.go` already fixed once for `result.json` (`TestPrepareInputRemovesResultOfPreviousRun`), just not extended to the log file. Harmless under plain bind-mount (`os.Create` truncates the one real file directly, no separate copy to go stale) — only `--clone`'s round-trip resurrects it. Fixed the same way as the existing `result.json` case: `PrepareInput` now also removes `<workdir>/.agent/run.log` before every run, so `cloneSyncIn` never has a stale one to carry in. New `TestPrepareInputRemovesLogOfPreviousRun`; self-run mutation (removed the fix) confirmed the test catches it.
- **Critical (accepted with mitigation, not a bug in this diff) — only *committed* work survives `--clone`.** `role.md` (`roles/implementer/role.md`, "Если прошлый прогон был усечён") is written for a world where a truncated run's uncommitted edits are still there on the next run — true under bind-mount, false under `--clone`: `cloneSyncOut` only recovers git commits (`fetchBranch`) plus the explicitly named `Dirs`, and `defer remove(name)` discards everything else in the sandbox's own disk unrecoverably. A related sharp edge: `git merge --ff-only` reports success (`Already up to date`) whether the agent made zero commits or genuinely lost work, so nothing distinguishes "nothing to do" from "lost it" by exit code alone. Judged proportionate to *mitigate*, not fully solve, in this pass: full recovery of arbitrary uncommitted state is a materially bigger feature (syncing the whole working tree, not just two known directories), and the actual scenario it protects — a truncated run resuming in the *same* reused workdir — isn't reachable through either of this pass's two wired callers (`eval-roles` materializes a fresh fixture per case and discards it; a human's manual `run-agent --clone` against a real, reused worktree is the one case where it could matter, and that path isn't wired into any automated resume flow either). It becomes load-bearing only if/when `internal/pipeline/pipeline.go` is wired up (already deferred, Task 21 "Not done"). Fixed with a loud-not-silent mitigation: new `warnUncommitted` in `internal/backends/sbx/clone.go`, called from `cloneSyncOut` right after a successful `fetchBranch` — runs `git status --porcelain | grep -q .` inside the sandbox and, if it finds anything, writes an explicit warning line into the run's own log (not a returned error — reviewer role legitimately never commits at all, so failing the run on any uncommitted content would be a worse false-positive rate than the silence it replaces). New `TestCloneSyncOutWarnsAboutUncommittedWork` (both directions: warns / stays silent).

Also from this round, all fixed: `cloneSyncIn`/`cloneSyncOut` now `mkdir -p` the destination parent (both directions) before each `cp` — `Dirs` narrowing in the first round (`.comet/current-change.json`, `.comet/runtime` instead of the whole `.comet/`) meant the destination directory might not exist yet on either end, which `sbx cp` documents clearly only for the all-committed-directory case, not a single untracked file with a missing parent; the timeout-wrapping comment and a `wantTimeout`/`errors.Is(err, runner.ErrRunTimeout)` case were restored to `cloneOutcome`/`TestCloneOutcome` (the *code* was already correct after the round-1 refactor, but nothing tested that specific, load-bearing property — `internal/runagent/runagent.go`'s `terminationOf` keys off exactly this `errors.Is`); `CloneNotice` got its own two tests mirroring `NetworkNotice`'s (`TestCloneNoticeWarnsOnLocalBackend`, `TestCloneNoticeSilentWhereNothingIsLost`); `cloneSyncTimeout`'s comment corrected (one shared budget per sync pass, not a per-step limit); a brief note added to `excludeFile` explaining why `.git/info/exclude` can be used directly rather than resolved via `git rev-parse --git-common-dir` (`--clone`'s primary path structurally can never be a worktree — `sbx create --clone` itself already refuses one, measured live in this same task); the plan's own "Scope decision" paragraph above corrected where it overstated how much of a future pipeline wiring is "already generic" (only `fetchBranch`'s target is arbitrary — `Dirs` sync is hardcoded to `Workspaces[0].Path`, as `CloneSync.FetchInto`'s comment now says plainly).

Two more self-run mutation tests: removed the new `run.log` deletion — `TestPrepareInputRemovesLogOfPreviousRun` caught it. Swapped `runner.RunTimeout(...)` for a plain `fmt.Errorf` in `cloneOutcome`'s timeout branch — the strengthened `TestCloneOutcome` caught it. Both reverted, full suite re-confirmed green.

A third review round was judged disproportionate to dispatch given the diminishing-returns pattern (round 2 found real but progressively smaller issues) and given the next step — an actual live run — exercises exactly the scenario Critical #1 was about far more thoroughly than another read-through could.

- [x] **Verify:** `go build/vet/test ./...` clean; two independent review rounds against the diff (2 Critical + 6 Important in round 1, 2 more Critical-grade in round 2), all fixed as documented above. Four self-run mutation tests across both rounds confirm the safety-critical fixes are real, not cosmetic.
- [x] **Commit**

Committed as `0b49599`.

---

## Task 16 resumption: live golden-case checkpoint with `--clone`

`analyst/capability-basic-plan` and `reviewer/capability-spot-defect` — the two cases that had reliably hit `root-move.lock` conflicts on the bind-mount — both passed on the first live `--clone` run, no retries needed. `implementer/capability-reads-brief-and-spec` failed, but not on anything `--clone`-related.

**Diagnosed live, no agent cost (`sbx exec` probes only):** `evals/implementer/capability-reads-brief-and-spec/fixture/.comet/runtime/native/changes/eval-brief/state.json` — pre-seeded on purpose (per its own `diff_scope` comment, "so a fresh materialized checkout doesn't silently no-op on the first Builder handoff call") — carries a `workspace.projectRoot`/`worktreeRoot` frozen at fixture-authoring time. `materializeFixture` copies it verbatim into a fresh `os.MkdirTemp` directory every run, so every single run starts with a path mismatch against Comet Native's own recorded workspace. Fixed generally, not just for this one fixture: new `rewriteLocalExecutionPaths` in `cmd/eval-roles/fixture.go`, called from `materializeFixture` right after copying the fixture tree (before the seed commit) — globs every `.comet/runtime/native/changes/*/state.json` a fixture brought with it and rewrites `projectRoot`/`worktreeRoot` to the real materialized path. `evals/reviewer/capability-spot-defect` pre-seeds the same way and has the identical latent bug; it just never surfaced because its own check (`grep -qE '\bA1\b' .agent/result.json`) never reads the durable YAML the mismatch affects. New `TestMaterializeFixtureRewritesLocalExecutionProjectRoot` and `TestMaterializeFixtureToleratesNoLocalExecutionState`; self-run mutation (dropped the call) confirmed the first test catches it.

That fix alone did not make the case pass. Chased further, live, no agent cost: even with the path corrected, `docs/comet/changes/eval-brief/comet-state.yaml` — the file this case's `fixture_tests` check actually grepped for `phase: verify` — updates on disk *non-deterministically* after a real Builder handoff in `0.4.0-beta.20`. Five isolated probes (same payload, same `--clone`, same corrected path) split roughly evenly between the durable YAML updating immediately and not updating at all; a retried identical handoff on a stuck one **failed outright** ("Native candidate can only be submitted from active Build") — the local-execution cache had already advanced past Build while the durable YAML hadn't, a self-contradictory state neither call nor `doctor` resolved. `comet native status`/`doctor` themselves report the *advanced* phase from their own merged view even when the raw YAML file lags behind. Reads like the same class of internal coordinator/lock race already documented for `root-move.lock`, just on a different write path inside Comet Native itself — not a bug in `--clone`, the fixture, or this project's own code.

User's call on how to handle this, given upfront that the golden-case suite itself was authored speculatively and is due its own pass later, and that what actually matters right now is confirming the role mechanics work: don't chase Comet's internal timing further, make the one check that was actually blocking honest about what it can reliably promise today. Fixed: `evals/implementer/capability-reads-brief-and-spec/expect.yaml`'s `fixture_tests` now checks that `basedOnStateVersion` in the local-execution cache moved off its seeded value (`2`) instead of grepping the flaky durable YAML — the one signal that advanced consistently in every probe, and it still catches the original failure mode the check was written for (a silent no-op handoff that never touches local state at all).

Live-reran all five Comet-Native-dependent golden cases with `--clone` after both fixes:

- [x] `analyst/capability-basic-plan` — PASS
- [x] `reviewer/capability-spot-defect` — PASS
- [x] `implementer/capability-reads-brief-and-spec` — PASS (after both fixes above)
- [x] `reviewer/capability-clean-verify` — PASS
- [x] `analyst/capability-resume-no-reinvoke` — PASS

Full suite (`eval-roles --clone`, all 9 cases including the escalation-* and capability-basic-bugfix cases that don't touch Comet Native at all) run live as a background check to close out Task 16 fully:

```
PASS  analyst/capability-basic-plan
PASS  analyst/capability-resume-no-reinvoke
PASS  analyst/escalation-ambiguous-decision
PASS  implementer/capability-basic-bugfix
PASS  implementer/capability-reads-brief-and-spec
PASS  implementer/escalation-ambiguous-task
PASS  reviewer/capability-clean-verify
PASS  reviewer/capability-spot-defect
PASS  reviewer/escalation-ambiguous-task

9 cases: 9 passed, 0 failed, 0 errored
```

All sandboxes cleaned up after every run (`sbx ls` empty throughout). Task 16's live golden-case checkpoint is closed: all three roles, both Comet-Native-dependent and plain paths, pass under `--clone`.

---

## Task 22 (planned, not started): wire `--clone` into `internal/pipeline/pipeline.go`

Deferred at Task 21 ("Scope decision", "Not done") and reconfirmed explicitly when discussing next steps: `internal/pipeline/agent.go`'s `SandboxAgent.Run` does not set `runagent.Options.Clone`, because the production conveyor hands the agent a bare-repo-plus-worktree pair (`req.Mounts = ws.Mounts()`), and `--clone`'s primary path rejects both a bare repo and a worktree outright (live-verified, Task 21). Until this is wired, every task the real `Office` conveyor runs through `sbx` still hits Comet Native's `root-move.lock` instability on the virtiofs bind-mount — the exact failure `--clone` was built to avoid — for `analyst`/`implementer`/`reviewer` runs going through the tracker-driven pipeline rather than manual `run-agent --clone`.

Shape sketched at Task 21, not built: a new `workspace.CloneSource` step that makes a fresh plain clone of the task branch off the bare repo before `sbx create --clone`, then routes the post-run fetch into the real worktree (`ws.Dir`), not the disposable clone. Only `fetchBranch`'s target is already generic enough for this (`FetchInto` is an arbitrary path, proven by `TestFetchBranchFastForwardsFetchInto`) — `cloneSyncIn`/`cloneSyncOut`'s `Dirs` sync is hardcoded to `Workspaces[0].Path`, so that half needs extending too.

Not started. Decided (2026-08-31) to validate role mechanics first via manual `run-agent --clone` runs against a real (non-eval, non-fixture) task before spending on this wiring — see below.

## Manual real-task check: `analyst → implementer → reviewer` via `run-agent --clone`, no `pipeline.go`

Purpose: golden cases are synthetic fixtures the user described as authored speculatively ("наобум"); this is the first live run of a genuine, freshly-written task through the full three-role chain, driven by hand (one `run-agent --clone` call per role, no tracker, no `Office`) precisely because `pipeline.go` doesn't support `--clone` yet (Task 22 above). This exercises the same `runagent.Execute` engine `pipeline.go` calls, and the same Comet Native Shape → Build → Verify handoffs the golden cases probe individually — just chained on one real task instead of three isolated fixtures, and without any of `pipeline.go`'s own claim/lease/push/graph bookkeeping.

**Task:** a genuinely fresh, non-fixture task — `stats.Median(nums []float64) (float64, error)` against a throwaway plain repo (`scratchpad/manual-client`), invented for this check specifically because it has real decisions (mutate-or-copy, odd/even branch, empty-slice error) rather than a grep-matchable golden-case shape. Chained by hand: `run-agent --role analyst --clone --task-key demo-1` → `--role implementer --clone --task-key demo-1` → `--role reviewer --clone --task-key demo-1 --base master`, reusing `.agent/task.md` across roles (no `--task` after the first call).

**Result: all three roles behaved correctly and did real, verifiable work.** analyst ran the actual Comet Native Shape protocol (`comet native new`, wrote a real `brief.md`/`spec.md`, self-confirmed Shape), implementer wrote a correct non-mutating implementation with real tests and a real Builder handoff, reviewer read the diff, ran `go vet`/`go test` itself, drove `dispatch-verifier` → `final-result` (24/24 acceptance) → `accept-result`. $1.07 total, 55 turns across three runs.

**Found live, by reading the full `run.log` transcripts (`~/.office/runs/<run_id>/run.log`, not the per-run `.agent/run.log` that each next role's `PrepareInput` deletes) — a real data-loss bug in `--clone`, not a golden-case artifact:**

`docs/comet/changes/demo-1/comet-state.yaml` is a git-tracked file, but every `comet native next` call mutates it on disk as a side effect *without committing it* — `roles/analyst/role.md` is the only role text that says to commit it (because it's created fresh at Shape time); `roles/implementer/role.md`'s Builder-handoff instructions never mention re-committing it afterward, because that role's text assumes a bind-mount worktree that survives physically regardless of commit status. Under `--clone`, only git-committed content survives a sandbox's teardown. Confirmed mechanically, not inferred: `warnUncommitted` (Task 21, round 2) fired and logged its warning line on *both* the implementer's and the reviewer's runs. implementer advanced `comet-state.yaml` to `phase: verify` via its handoff but never re-committed it — lost when its sandbox was torn down. reviewer's `--clone` sandbox therefore started from analyst's committed `phase: build, state_version: 2` again, found `next_action: submit-builder-candidate` unexpectedly, reasoned live in its own transcript that "the previous agent didn't send a builder-handoff," and **improvised** — composed and submitted a builder-handoff itself, based on its own reading and its own test run, then drove verify through to `phase: archive, state_version: 6` — which *also* never got committed (this was before Task 23 below; reviewer had no commit rights at the time), so it was lost too. On the host, `comet-state.yaml` is still stuck at `phase: build, state_version: 2` today, even though the code is implemented, tested, and was live-verified against all 24 acceptance criteria in the same session.

This likely also explains part of the "Comet Native's own internal timing is non-deterministic" finding from the Task 16 resumption above: at least in this run, the durable-YAML staleness has a fully mechanical, reproducible cause (`--clone`'s committed-only boundary meeting a role.md written for a persistent worktree), not only an internal race in Comet Native's own coordinator.

**Not fixed yet, left as explicit follow-up** (separate from Task 22 and Task 23): sync `docs/comet/changes/*/comet-state.yaml` back to the host unconditionally after a `--clone` run — the same treatment `.comet/runtime` already gets — likely via a glob in `internal/backends/sbx/clone.go` (matching `cmd/eval-roles/fixture.go`'s existing `rewriteLocalExecutionPaths` glob pattern), since the change name isn't known statically the way `Dirs`'s current fixed entries are.

- [x] **Commit** (`cmd/eval-roles/fixture.go`, `cmd/eval-roles/fixture_test.go`, `evals/implementer/capability-reads-brief-and-spec/expect.yaml`)

---

## Task 23: give `reviewer` real write and commit rights — "review" now includes point-fixing, not just reporting

Raised by the user directly, reasoning from their own `/pr-converge` skill in another project (clens): a reviewer that only ever reports and returns work doesn't match what Comet Native's own Verify protocol actually needs (`final-result`/`accept-result` are themselves state mutations), and — longer-term, explicitly deferred out of this task — a reviewer that drives multiple review cycles including external CI before a human merges. Scoped down for *this* task, by the user's own instruction, to exactly the capability change, with the multi-cycle/GitHub-Actions behavior left for a future task: "я бы не хотел сейчас заниматься GitHub Actions и подробной реализацией ревьювера... важно, чтобы наша роль в будущем имела технические возможности это сделать."

**Key finding that shaped the design, checked before proposing anything:** `internal/pipeline/archive.go`'s `archiveIfReady` already solves the "Comet's protocol needs writes/commits beyond code" half of the user's reasoning — deliberately, as harness code (not the reviewer agent, not any agent at all), running on the runner's host directly against the reused worktree, committing under its own identity (`comet-archive@office.local`) with a live-bug-tested postcondition check (`archiveSucceeded`) rather than trusting `comet native archive`'s exit code. Archiving was never going to become reviewer's job; this task doesn't touch it.

**Design, approved by the user before implementation (bounded path, `superpowers:brainstorming`):**

- `roles/reviewer/role.yaml`: removed the role-specific `deny` (`Bash(git *add*)`, `Bash(git *commit*)`, `Bash(git *restore*)`) — the one real technical enforcement of "reviewer can't write" (tools.allow was never technically enforced for Bash, per the 2026-08-27 finding both role.yaml's own comment and `TestReviewerRoleCannotWrite`'s doc comment cited). Added `Edit`/`Write` to `tools.allow`, matching analyst/implementer, so reviewer uses the normal tools instead of `Bash` heredoc tricks for real edits. `defaults.tools.deny` in `projects.yaml` (push/branch/checkout/rebase/reset/etc.) still applies unchanged — reviewer gains exactly what implementer/analyst already have, nothing more.
- `roles/reviewer/role.md`: new "Точечный фикс вместо возврата" section — a small, self-contained, non-design-altering fix (typos, lint, a locally-contained bug) is fixed and committed by reviewer itself, one fix per commit, same discipline as implementer; anything requiring a judgment call that isn't reviewer's to make is still returned to `implementer`/`analyst` as before, unchanged. Tied explicitly into Comet Native's own state machine rather than fighting it: `comet-hook-router.mjs` only permits `Write`/`Edit` in `phase: build`, so with an active Comet Native change, reviewer must first re-enter `build` — via `fail` on the affected acceptance criterion in `final-result`, or `--revise-implementation` at the `accept-result` step for something not tied to any single criterion — fix and commit there, then resume the verify loop (`dispatch-verifier` → `final-result`, now `passed`) rather than editing mid-`verify` where the hook would reject it outright. Without an active Comet Native change (legacy path), the hook doesn't apply — fix and commit directly.
- `internal/runner/role_test.go`: deleted `TestReviewerRoleCannotWrite` — it technically asserted exactly the invariant this task reverses. reviewer's write boundary is now prose-only, the same way analyst's and implementer's already are (role.yaml's own comment already said tools.allow was never a technical boundary for either of them).
- Documentation accuracy pass: `README.md` (two places — the stage-3 narrative line and the roles-summary bullet), `CLAUDE.md`'s repo-status paragraph, and `docs/contracts/agent-io.md` (a stale parenthetical example, "reviewer их не правит вовсе," used to illustrate a still-true general point about the result-file write grant — reworded to drop the now-false example without losing the point) all previously stated reviewer never writes code; corrected.
- **Deliberately not touched, by explicit user scope:** `internal/pipeline/pipeline.go`, `internal/pipeline/archive.go`, `workflow.yaml`, GitHub Actions / external CI, multi-cycle review, auto-merge (human still merges — confirmed explicitly with the user; `docs/DESIGN.md` §2.8's "человек — единственная точка" principle stands unchanged).
- **Known follow-up, flagged to the user before implementing, not fixed here:** `evals/reviewer/{capability-spot-defect,capability-clean-verify,escalation-ambiguous-task}/expect.yaml` all assert `diff_scope: allow: []` (reviewer changes nothing) — `capability-spot-defect` in particular could now legitimately fail if its injected defect qualifies as a point-fix under the new role.md criteria, since a compliant reviewer might now fix-and-commit instead of only reporting. Not run or adjusted as part of this task.

Verification: `go build ./... && go vet ./... && go test ./...` clean across every package. Dry-run of `run-agent --role reviewer --backend sbx --dry-run` confirmed the rendered `--tools` includes `Edit,Write`, `permissions.allow` grants them broadly, and no `deny` block remains for reviewer — matching implementer/analyst's shape exactly.

- [x] **Commit** — `3b20bec`.

---

## Task 24: `--clone` sweeps up whatever the agent left uncommitted, instead of only warning

Prompted directly by the second live `stats.Median` re-run (task-key `demo-2`, `analyst → implementer → reviewer` again, same repo/task file, freshly re-run specifically to check whether Task 23's reviewer write-rights change fixed the `comet-state.yaml` loss found in Task 22 setup): it did not. `git status -uall` and `git log` on the host confirmed `implementer`'s advance to `phase: verify` was lost again exactly as before (its own `role.md` was untouched, and it still never re-commits `comet-state.yaml` after its Builder handoff), and `reviewer` — now technically able to `git commit` — never used that ability for this file either: read through the full `run.log` transcript, zero `git add`/`git commit` calls anywhere in its 23 turns, `warnUncommitted`'s log line fired again. Giving a role write rights does nothing if nothing in its `role.md` tells it to use them for this.

User's own diagnosis, correcting the direction: the asymmetry ("analyst commits `comet-state.yaml`, implementer/reviewer don't") isn't really a bug in those two roles — `internal/pipeline/archive.go`'s `archiveIfReady` already sweeps up whatever's left dirty in `docs/comet` as its own final commit, under its own identity, in the real (non-`--clone`) pipeline, so intermediate roles never needing to commit this file themselves was fine by design. The user proposed the fix belongs at the `--clone` layer instead: commit *everything* left in the sandbox's working tree at teardown, regardless of who touched it, under a separate identity so it never gets confused with a role's own deliberate commits.

**Implementation** (`internal/backends/sbx/clone.go`, `clone_test.go`): replaced `warnUncommitted` (logged a warning, discarded the data) with `commitLeftovers`, called from `cloneSyncOut` *before* `fetchBranch` (the sweep commit must exist in the sandbox's git history before the fetch pulls from it, or it's discarded with everything else on teardown) — `git add -A` plus a commit under a new `clone-sweep`/`clone-sweep@office.local` identity.

**Two independent review rounds, 4 Critical + 5 Important total, all fixed and each independently confirmed by a targeted self-run mutation (reverting just that one fix and watching the specific new test catch it) using *real* `git`/`sh`, not only call-count assertions against the existing fake `step`:**

Round 1 (3 Critical, 2 Important):
- **Critical — a failed sweep aborted `fetchBranch` entirely**, so a broken safety-net commit (e.g. a stray `.git/index.lock` from a role's own `git commit` interrupted mid-write by a timeout) could lose the agent's real, deliberate, already-made commits when the sandbox was torn down right after — the opposite of `--clone`'s entire purpose. Fixed: `fetchBranch` now runs unconditionally; the sweep's error is accumulated and returned only after it succeeds.
- **Critical — `primary` was string-concatenated into a `sh -c "..."` script** instead of passed as a positional argument; a path containing a space silently broke the command in a way indistinguishable from "tree is clean," discarding real uncommitted work with no log line at all. Fixed: path and pathspec exclusions now travel as trailing positional args (`sh -c script sh "$1" ...`), referenced via `$1`/`"$@"`, never concatenated.
- **Critical — `git add -A` during an unresolved merge/rebase/cherry-pick would stage conflict-marker content** and let `git commit` create a bogus "successful" merge commit, corrupting the task branch silently once `fetchBranch` pulled it to the host. Fixed: new `mergeInProgress` guard skips the sweep entirely (logs why) when a merge is in progress.
- Important — no `--no-verify`: a client repo's own git hooks (installed inside the sandbox by e.g. `npm install`) could fail the sweep commit on unrelated lint noise. Fixed.
- Important — identity was set via `-c user.name=...`, which loses to ambient `GIT_AUTHOR_NAME`/`GIT_AUTHOR_EMAIL` env vars by git's own precedence rules. Fixed with literal env-var assignments on the commit invocation instead.
- Important — the "exchange directory never gets swept in" guarantee relied entirely on a separate, best-effort `.git/info/exclude` copy (`syncExcludeFile`) that silently no-ops if its source is missing. Fixed: explicit `git add -A -- . :!<dir>` pathspec exclusions built from the same `dirs` (`l.Clone.Dirs`) the caller already threads through, independent of whether the exclude-file copy worked.
- Important — no test proved the sweep commit reached `FetchInto` end-to-end through a real `fetchBranch`. Fixed with new `execStep`/`realSandboxStep` test helpers that shell out for real instead of faking `step`.

Round 2, scoped to just the round-1 fix (1 Critical, 2 Important — reviewer reproduced all three live against real `git`, not just read the diff):
- **Critical — `mergeInProgress`'s marker-file check (`MERGE_HEAD` etc.) misses a real class of conflict.** A conflicting `git stash pop` leaves the exact same unresolved index entries as a merge conflict but writes none of the standard marker files — reviewer reproduced it live and got a `clone-sweep` commit with `<<<<<<<` markers in the file. Fixed: replaced the five `test -e/-d` marker checks with `git ls-files --unmerged | grep -q .`, which looks at the index itself rather than which command put it in that state — strictly broader, and simpler.
- Important — **`git status --porcelain` (dirty check) and `git add -A -- . :!dir` (the actual add) look at different sets.** If everything dirty gets filtered out by the pathspec exclusion, or doesn't stage at all (a dirty submodule pointer), `add` succeeds having staged nothing, and `git commit` exits 1 ("nothing to commit") — which `commitLeftovers` was turning into a hard error, and `cloneOutcome` into a reported infrastructure failure for a run that did nothing wrong. Reviewer reproduced both the submodule case and the "everything filtered by dirs" case (the exact scenario the round-1 `:!dir` fix was for). Fixed: split `add`/`commit` into separate exec calls with a `git diff --cached --quiet` check between them — nothing staged after add is treated as legitimately nothing to do, not a failure.
- Important — the round-1 doc comment claiming a "live scenario" where ambient `GIT_AUTHOR_NAME` would silently override `-c user.name=` was checked against `sbx.go` and found factually wrong for *this* exec call specifically (`identityVars`/`--env` are only threaded onto the agent's own launch via `execArgs`, never onto `commitLeftovers`'s separate `sbx exec` calls) — the env-var choice itself is still the right defensive one, just not a fix for a bug that exists today. Comment corrected to say so plainly; added `TestCommitLeftoversIdentityWinsOverAmbientEnv`, which actually injects `GIT_AUTHOR_NAME=agent-implementer` into the exec environment and confirms the sweep commit still attributes to `clone-sweep` — the property the original comment claimed but no test verified.

A third round was not dispatched: both rounds' findings are now independently confirmed by mutation tests run against real `git`/`sh` execution (not just the pre-existing fake-`step` call-count assertions), which is a stronger signal than a third read-through would add on its own, and round 2's findings — while one was genuinely Critical — were each narrower in scope than round 1's.

**Deliberately not changed:** `roles/*/role.md` (no role is told to commit `comet-state.yaml` itself — the fix lives entirely at the `--clone` sync layer, independent of role prompts, matching the user's own framing: this should work "independent of who did it inside the sandbox"). `internal/pipeline/pipeline.go`/`archive.go` (still not wired for `--clone` — Task 22, still open).

Verification: `go build ./... && go vet ./... && go test ./...` clean across every package after every round. Full test count in `internal/backends/sbx`: 27 tests touch this code path directly, including 7 that run real `git`/`sh` (no fake `step`) covering the space-in-path regression, the pathspec-exclusion-without-`.git/info/exclude` regression, the merge-conflict-via-marker-file regression, the merge-conflict-via-stash-pop regression, the everything-filtered-out-is-not-an-error regression, the identity-wins-over-ambient-env regression, and the full sweep-commit-reaches-`FetchInto`-through-`fetchBranch` path.

- [ ] **Commit**
