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
- **The `sbx`/runner-host bootstrap of the `comet` npm CLI is a separate, only-partially-solved prerequisite.** Task 1 investigates and documents it early but does not block any later task's file-level work — every later task that needs a working `comet` CLI (vendoring, eval fixture authoring, the final live run) says so explicitly and can be executed once a developer has `comet` installed locally, independent of whether the *production sandbox* bootstrap is solved.
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

- [ ] **Step 1: Replace `role.yaml`**

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

- [ ] **Step 2: Replace `### Если есть план` with the Comet-aware, conditional section**

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

- [ ] **Step 3: Update the truncated-run section's `tasks.md` reference**

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

- [ ] **Step 4: Verify the role still loads**

```bash
go test ./internal/runner/... -run TestShippedRolesAreValid -v
```

- [ ] **Step 5: Commit**

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

- [ ] **Step 1: Replace `role.yaml`**

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

- [ ] **Step 2: Replace `## Работа`'s step 1 with the conditional Comet dispatch**

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

- [ ] **Step 3: Update `## Выход`'s success case**

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

- [ ] **Step 4: Verify the role still loads and the write-scope test still passes**

```bash
go test ./internal/runner/... -run TestShippedRolesAreValid -v
go test ./internal/runner/... -run TestReviewerRoleCannotWrite -v
```

Expected: both pass unchanged — `tools.allow`/`tools.deny` are untouched by this task, only `skills:` and `hooks:` changed.

- [ ] **Step 5: Commit**

```bash
git add roles/reviewer/role.yaml roles/reviewer/role.md
git commit -m "feat(reviewer): dispatch Comet Native Verify instead of free-form diff review"
```

---

## Task 12 (tasks.md 6.1): `docs/contracts/agent-io.md`

**Files:**
- Modify: `docs/contracts/agent-io.md`

**Interfaces:** None — prose-only contract documentation update, no code depends on this file's content (it is a source for humans and for the adapter's own embedded `ResultSpec`, which this task does not touch — only the "Каталог изменения" section's prose changes).

- [ ] **Step 1: Replace the outdated closing paragraph of "### Каталог изменения"**

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

- [ ] **Step 2: Clarify the "План:" line's scope**

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

- [ ] **Step 3: Commit**

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

---

## Task 13 (tasks.md 7.1, analyst): `evals/analyst/`

**Files:**
- Modify: `evals/analyst/capability-basic-plan/expect.yaml`
- Modify: `evals/analyst/escalation-ambiguous-decision/expect.yaml`
- Modify: `evals/analyst/capability-resume-no-reinvoke/fixture/` (full rewrite via the shared recipe), `evals/analyst/capability-resume-no-reinvoke/expect.yaml`

**Interfaces:** None new — these are `cmd/eval-roles` golden-case fixtures, consumed only by a live, paid `./bin/eval-roles` invocation (Task 16), never by any Go test in this repo's own suite.

- [ ] **Step 1: Update `capability-basic-plan/expect.yaml`**

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

- [ ] **Step 2: Update `escalation-ambiguous-decision/expect.yaml`**

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

- [ ] **Step 3: Rewrite `capability-resume-no-reinvoke`'s fixture using the shared recipe**

`task.md` stays as-is ("Add an exported function `Whisper`..."). The fixture must now contain a real Comet Native change already past Shape confirmation, so analyst's "resume, no re-invoke" rule (role.md step 1) has something real to recognize. Using the shared recipe above, with:

- Base fixture files: the existing `fixture/go.mod` and `fixture/greet/{greet.go,greet_test.go}` (unchanged — copy them into `$scratch` before running `comet native new`).
- `<name>`: `EVAL-RESUME` (an eval fixture has no real tracker key; this matches the existing manual/no-key convention elsewhere in this repo — `_manual`-style naming — while still being a valid `safeKey` output).
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
- After `comet native next EVAL-RESUME --confirmed`, confirm via `comet native status EVAL-RESUME --json` that phase is `build` (not `shape`) — this is the state analyst's role.md step 1 checks for ("уже есть и стоит в фазе `build`... — Shape по этой задаче уже пройден").

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

- [ ] **Step 4: Sanity-check the fixtures without spending money**

```bash
find evals/analyst/capability-resume-no-reinvoke/fixture -type f | sort
git -C evals/analyst/capability-resume-no-reinvoke/fixture status 2>&1 | head -1
```

Expected: the fixture directory tree now contains `docs/comet/changes/EVAL-RESUME/{brief.md,specs/greet/spec.md}` and whatever state file(s) `comet native new`/`next` produced, plus the unchanged `go.mod`/`greet/` files; it is a plain directory (not itself a `.git` repo — the second command should report "not a git repository" or similar, matching every other `evals/*/fixture/` in this repo, since `cmd/eval-roles`'s own `fixture.go` materializes the git repo around it at run time).

- [ ] **Step 5: Commit**

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

- [ ] **Step 1: Author the fixture using the shared recipe**

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

- `<name>`: `EVAL-BRIEF`.
- `brief.md`:
  ```markdown
  # Brief: Double

  Add an exported `Double` function to the `calc` package.

  ## Decisions

  - `Double(x)` returns `x * 2`. No edge cases beyond ordinary `int` overflow
    semantics — not in scope for this change.
  ```
- `specs/calc/spec.md` — one capability spec with one explicit acceptance item, e.g. `AC1: Double(x) returns 2*x, pinned by a test`.
- Confirm via `comet native status EVAL-BRIEF --json` that phase is `build` after `comet native next EVAL-BRIEF --confirmed` — this is the phase implementer's role.md step (Task 10) checks for.

- [ ] **Step 2: Write `task.md`**

```markdown
Continue the work described in this change: the acceptance items live in the
change's own spec, not repeated here.
```

(Deliberately minimal — the point of this case is that implementer gets its real instructions from the Comet Native change via context, not from `task.md`, mirroring how `capability-resume-no-reinvoke` deliberately keeps `task.md` unchanged while the fixture around it carries the real state.)

- [ ] **Step 3: Write `expect.yaml`**

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
      grep -q "builder-handoff\|stage" docs/comet/changes/EVAL-BRIEF/*.yaml 2>/dev/null ||
      find docs/comet/changes/EVAL-BRIEF -iname "comet-state.yaml" | xargs grep -q "build"
```

The `fixture_tests` command's second half is deliberately written to tolerate not knowing the exact `comet-state.yaml` path/shape in advance (per this plan's "Notes on task ordering" item 4/7): it first tries a permissive grep across any `.yaml` file in the change directory, and falls back to explicitly locating a file named `comet-state.yaml` if the first form doesn't match. Tighten this command once Task 13/14's own fixture-authoring step (which runs the real CLI) has shown the real file name and its exact "phase advanced past build" marker — do not leave the permissive form in place if a precise one is easy to write by then.

- [ ] **Step 4: Sanity-check without spending money**

```bash
cd evals/implementer/capability-reads-brief-and-spec/fixture && go build ./... && go vet ./...
```

Expected: the base fixture (without `Double` yet) builds — this only proves the *starting point* compiles, which it must so implementer isn't fighting a broken baseline; `go test ./...` at this point still passes too (only `TestAdd` exists).

- [ ] **Step 5: Commit**

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

- [ ] **Step 1: Augment `capability-spot-defect`'s fixture with a real verify-phase change**

The existing fixture (`calc.go`/`calc_test.go`, a colleague's buggy `Max` implementation) stays exactly as-is as the *code under review*. Using the shared recipe, additionally seed a Comet Native change already in `verify` phase with an acceptance item the buggy `Max` violates:

- `<name>`: `EVAL-SPOT-DEFECT`.
- `brief.md` / `specs/calc/spec.md`: one acceptance item, e.g. `AC1: Max(a, b) returns the larger of a and b for all int inputs, including when a == b`.
- After `comet native next EVAL-SPOT-DEFECT --confirmed` (→ `build`), simulate a Builder handoff to reach `verify`: `comet native next EVAL-SPOT-DEFECT --runner-input <a minimal builder-handoff JSON naming AC1 as addressed>` (the design doc's own `{kind, summary, addressed_acceptance_ids, checks, known_limits}` shape from its `adapter.go`/implementer section — reuse it here verbatim as the JSON body). Confirm via `--json` that phase is now `verify`.

Update `evals/reviewer/capability-spot-defect/task.md` only if its current wording ("A colleague implemented the `Max` function... Review their work") no longer matches once a real acceptance ID exists to reference — if the existing wording still reads naturally, leave it unchanged.

- [ ] **Step 2: Update `capability-spot-defect/expect.yaml`**

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
    # AC1 — реальный критерий приёмки заведённого для этого кейса изменения
    # Comet Native; провалившийся вердикт по нему — точный сигнал, что
    # ревьюер прошёл через dispatch-verifier/final-result, а не свободный
    # разбор угадал слово "Max" в тексте.
    command: "grep -qi 'AC1' .agent/result.json"
```

- [ ] **Step 3: Author `capability-clean-verify`'s fixture using the shared recipe**

- Base fixture: a small, already-correct package (e.g. `calc.Max` implemented correctly this time), plus its test.
- `<name>`: `EVAL-CLEAN-VERIFY`.
- Seed through Shape confirmation, a Builder handoff, and then simulate a *passing* `final-result` for the sole acceptance item (`comet native next EVAL-CLEAN-VERIFY --runner-input <a final-result JSON with acceptance: [{"id": "AC1", "result": "passed", "reason": "..."}], verdict: "pass">`). Confirm via `--json` that phase reaches `archive-ready` — this is the exact state `internal/pipeline/archive.go` (Task 5) looks for.

- [ ] **Step 4: Write `task.md` and `expect.yaml`**

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
      find docs/comet/changes/EVAL-CLEAN-VERIFY -iname "comet-state.yaml" |
      xargs grep -q "archive-ready"
```

(Same tolerant-then-tighten note as Task 14 Step 3 applies to this `fixture_tests` command.)

- [ ] **Step 5: Sanity-check without spending money**

```bash
find evals/reviewer/capability-spot-defect/fixture evals/reviewer/capability-clean-verify/fixture -type f | sort
```

- [ ] **Step 6: Commit**

```bash
git add evals/reviewer
git commit -m "test(evals): dispatch-verifier and archive-ready reviewer golden cases"
```

---

## Task 16: Full regression + live golden-case run

This is the manual, paid checkpoint — mirrors this repository's existing convention (see the `2026-08-30-analyst-brainstorming-skill` plan's own final task) of a real, costed run as the last step, never as part of automated CI.

**Files:** None — verification only.

- [ ] **Step 1: Full non-agent regression**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: everything passes, with no test skipped or newly failing relative to `base-ref`. Pay particular attention to `internal/runner`, `internal/adapters/claude`, and `internal/pipeline` — the three packages every earlier task touched.

- [ ] **Step 2: Confirm `comet` is installed locally (developer machine, not the sandbox)**

```bash
comet --version
```

If missing, install per Task 7 Step 1's `npm install -g @rpamis/comet@0.4.0-beta.18` — this is the local-machine case Task 1 explicitly separates from the still-open sandbox bootstrap gap.

- [ ] **Step 3: Run the new/changed golden cases**

```bash
./bin/eval-roles --role analyst --case capability-basic-plan
./bin/eval-roles --role analyst --case escalation-ambiguous-decision
./bin/eval-roles --role analyst --case capability-resume-no-reinvoke
./bin/eval-roles --role implementer --case capability-reads-brief-and-spec
./bin/eval-roles --role reviewer --case capability-spot-defect
./bin/eval-roles --role reviewer --case capability-clean-verify
```

- [ ] **Step 4: Run the untouched cases to confirm no regression**

```bash
./bin/eval-roles --role implementer --case capability-basic-bugfix
./bin/eval-roles --role implementer --case escalation-ambiguous-task
./bin/eval-roles --role reviewer --case escalation-ambiguous-task
```

Expected: unchanged behavior — each still exercises the "no Comet Native change found" fallback path exactly as it did before this whole change (per "Notes on task ordering" item 6).

- [ ] **Step 5: Record and act on findings**

If any run's outcome, `next_owner`, or `diff_scope` disagrees with its `expect.yaml`, treat it the way `README.md`'s own eval-roles section documents — do not treat a fixture-authoring assumption (state file path/shape, phase name) as sacred over what the real CLI actually did. The specific places this plan already named as "confirm empirically, adjust if wrong" are: `archiveReadyPhase`/`cometStatus.Phase` in `internal/pipeline/archive.go` (Task 5), and the `fixture_tests` commands in Tasks 14/15 that grep for a `comet-state.yaml`-shaped file. Fix forward with a new commit; do not silently weaken an `expect.yaml` assertion to make a case pass without understanding why it initially didn't.

- [ ] **Step 6: Final commit, if Step 5 produced fixes**

```bash
git add -A
git status --short   # confirm the diff is exactly the fix, nothing stray
git commit -m "fix: correct Comet Native assumptions found by the live golden-case run"
```
