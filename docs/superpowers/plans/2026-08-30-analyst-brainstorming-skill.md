---
change: analyst-brainstorming-skill
design-doc: docs/superpowers/specs/2026-08-30-analyst-brainstorming-skill-design.md
base-ref: 88cba0291c3b05799ef5f8f2463889bf51892a5f
---

# Analyst Brainstorming Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `analyst` real, vendored Superpowers `brainstorming` and `writing-plans` skills, with an explicit `role.md` dispatcher governing when to invoke them, how to skip redundant re-invocation on resume, and how to end cleanly at `writing-plans`'s Execution Handoff step — and cover the new behavior with two new eval golden cases plus an update to the one existing case whose expectations change.

**Architecture:** Two full, unmodified Superpowers v6.3.0 skill directories are copied verbatim into `skills/brainstorming/` and `skills/writing-plans/` (each with a plain `SOURCE.md` pin, no drift-check tooling). `roles/analyst/role.yaml` gains `skills: [brainstorming, writing-plans]`; `roles/analyst/role.md`'s old fixed-file `## Выход` section is replaced by a six-rule dispatcher (`## Диспетчер скиллов`), and its `## Как коммитить` section is reworded to stop assuming a single fixed `docs/changes/<KEY>/` directory. No Go code changes anywhere — the `skills:` mounting mechanism (`role.yaml` → `internal/runner.Role.SkillDirs()` → `internal/adapters/claude.buildPlugin` → a `office-role-analyst` Claude Code plugin) already exists and is simply exercised by this change for the first time. Three `evals/analyst/` golden cases close the loop: the existing `capability-basic-plan` case gets a new `expect.yaml` matching its new, forced-architectural output location, and two new cases (`escalation-ambiguous-decision`, `capability-resume-no-reinvoke`) cover the genuinely-ambiguous-task and correct-resume paths respectively.

**Tech Stack:** Markdown-based Claude Code skills (Superpowers v6.3.0, vendored verbatim, no edits); YAML (`role.yaml`, `expect.yaml`); Go 1.26.6 only incidentally, to run the pre-existing `cmd/eval-roles`/`cmd/run-agent` binaries via `./bin/eval-roles` — no `.go` source anywhere in this repo is touched by this plan; `git`, `jq`, `sh` for fixture authoring and the `fixture_tests` check commands (both already used by this convention, see `evals/analyst/capability-basic-plan`).

**Spec:** `docs/superpowers/specs/2026-08-30-analyst-brainstorming-skill-design.md` (deep technical design — authoritative for exact `role.md` wording, `expect.yaml` contents, and the Decisions/Risks this plan implements). Also `docs/openspec/changes/analyst-brainstorming-skill/{proposal.md,design.md,tasks.md}` for the Open/Design-phase why/what and the 4-group/13-item task breakdown this plan maps to. Executors should read the deep design doc in full before starting; this plan does not restate its rationale, only its execution.

## Global Constraints

- **Language of this plan:** English, per the request that produced it.
- **No Go code changes.** This entire change is `role.yaml`/`role.md` text, vendored skill directories, and `evals/` fixtures — nothing under `internal/`, `cmd/`, or any other `.go` file is modified (design doc "Architecture": "no new Go code, no new runner/adapter mechanism").
- **Vendor pinned copies, not a live reference.** `skills/brainstorming/` and `skills/writing-plans/` are full, byte-identical copies of Superpowers v6.3.0, commit `b36e0829c6d0140e93cfef2ca599b1b07d4a7797` (`github.com/obra/superpowers`). No drift-check script, no machine-checked source manifest — a plain `SOURCE.md` per skill is the only record (design doc Decisions, "Vendor pinned copies, not a live reference" — explicit owner decision against building a check now).
- **Dispatcher always invokes `brainstorming`, unconditionally, at the start of every new task**, and forces the **architectural** classification regardless of what the skill's own heuristic would pick. This is stated in `role.md` as a deliberate, temporary office rule, not a rediscovery of the skill's own logic (design doc Decisions, "Dispatcher always invokes brainstorming").
- **Both skills run to their real, unmodified conclusion.** Their own file locations (`docs/superpowers/specs/`, `docs/superpowers/plans/`) and `writing-plans`'s own plan-document format (including its "REQUIRED SUB-SKILL" header) are never reimplemented or edited by `role.md` prose (design doc Decisions, "Both skills run to their real conclusion").
- **`result.json`'s `artifacts` field is the only link between a freely-placed file and its task.** `role.md` makes this explicit rather than relying on emergent behavior (design doc Decisions, "Output location follows the skills' own conventions").
- **Execution Handoff is neutralized by `role.md` instruction only — never by editing a vendored `SKILL.md` or the generated plan file.** `analyst` never answers the "which execution approach?" question and never invokes `subagent-driven-development` or `executing-plans` (design doc Decisions, "writing-plans's Execution Handoff is neutralized by instruction").
- **Skill invocation syntax:** `Skill: office-role-<role-name>:<skill-name>` — the plugin manifest name is `office-role-<role.Name>` (`internal/adapters/claude/adapter.go`, `buildPlugin`), confirmed working in production by `docs/notes/stage-1-retro.md`'s `office-role-codeworder:codeword` precedent. For `analyst`, this is `office-role-analyst:brainstorming`.
- **Explicitly out of scope, not touched by any task below:** `implementer`'s role/tools, `reviewer`'s role, `internal/runner/input.go`'s context composition (including the "План: `<path>`" line), any other Superpowers skill, subagent dispatch for any role, and `projects.yaml`'s hardcoded `docs/changes` convention (proposal.md Impact). Do not add work in these areas even if a gap seems easy to close in passing.
- **`./bin/eval-roles` is manual-only and costs real money/subscription per run** — no CI or git hook ever invokes it (README.md, "Golden-кейсы ролей (eval-roles)"). Task 7 below runs real, paid role invocations.

## Notes on task ordering and resolved ambiguities

This plan implements every item in `docs/openspec/changes/analyst-brainstorming-skill/tasks.md` (4 groups: 1.1–1.3, 2.1–2.6, 3.1–3.3, 4.1–4.2), plus one item the design doc requires that `tasks.md`'s checklist does not itemize. Five things are resolved here, grounded in the actual source read during planning, rather than left for whoever executes this plan to guess:

1. **`tasks.md` 1.3 (the `SOURCE.md` per skill) is split across Tasks 1 and 2, not done as a third, trailing task.** Each `SOURCE.md` is only meaningful once its own skill directory exists next to it, and a reviewer should be able to review "brainstorming vendored, pinned, and documented" as one complete, self-contained commit — not a commit that vendors two directories and a separate commit that documents both.
2. **Task 4 below (updating `evals/analyst/capability-basic-plan/expect.yaml`) has no `tasks.md` item number.** It comes from the design doc's own "Testing Strategy" section, not from `tasks.md`'s checklist. It is required, not optional: once Task 3 lands, `analyst`'s output for that case's task ("add `Shout`, write the plan") moves from `docs/changes/_manual/` to `docs/superpowers/{specs,plans}/`, and the *current*, unmodified `capability-basic-plan/expect.yaml` (`diff_scope.allow: ["docs/changes/_manual/**"]`) would then fail on every run — not because the role regressed, but because the check is checking the old location. Named explicitly here so it is not silently dropped.
3. **Eval case naming.** The design doc uses `<case>-ambiguous-decision` / `<case>-resume-no-reinvoke` as placeholders. This plan names them `escalation-ambiguous-decision` and `capability-resume-no-reinvoke`, matching the two-prefix convention every existing case in `evals/` already uses without exception (`capability-*` demonstrates a capability ending in `done`; `escalation-*` proves a case that must end in `needs_human`) — confirmed by reading `evals/analyst/`, `evals/implementer/`, and `evals/reviewer/` in full during planning.
4. **How "a human answer already recorded in the task's simulated conversation history" is represented in Task 6's fixture.** The design doc's own "Boundary Conditions Deliberately Left Open" section says the resume-detection mechanism is "prose, not a deterministic algorithm" and that the first spike validated it "by exploring the repo, not by a formal protocol" — not via any special conversation-history channel. This is corroborated by reading `cmd/eval-roles/invoke.go`: `runRoleAgent` calls `run-agent --role --workdir --task --eval` only — there is no `--context` flag, so `internal/runner/input.go`'s `composeContext` (which is where ticket-conversation text would normally be woven into `.agent/context.md`, via `Input.Context`) never receives any conversation text through this harness; that path only exists in the real production pipeline, never in `eval-roles`. `composeContext` does read a `STATE.md` left at the workdir root by a prior run of the *same* role, but nothing in `role.md` (old or new) tells `analyst` to write or read one, so inventing that channel for this one fixture would test a mechanism the role was never told to use. Task 6's fixture therefore represents "the human already answered" as **content of the pre-committed spec itself** — a resolved Decision section stating what was asked and what the human answered — which is exactly what `analyst` would actually find by reading the repo, matching the spike's own validated mechanism.
5. **`role.md`'s artifacts rule (dispatcher rule 5) needs one clarifying clause beyond the design doc's literal wording, or Task 6's own case cannot pass.** The design doc's rule 5 says list "every file actually created or modified during the run." Task 6 is a *correct* resume: `diff_scope.allow: []` means a passing run changes nothing at all, so read literally, rule 5 would produce an empty `artifacts` list on the one run where the case's own `fixture_tests` check requires `artifacts` to be non-empty. Task 3 below writes rule 5 to also cover this case explicitly: on a resume where nothing needs to change, the pre-existing spec/plan are still the task's artifacts and still belong in the list, because the field exists to point at the task's output, not to log this run's diff. This is not a new decision — it is required for the design doc's own two decisions (rule 5, and the Task 6 check it explicitly says rule 5's mechanism should satisfy) to be mutually consistent.

All shell commands below were checked against the actual repository during planning (file layout, `role.go` validation rules, `cmd/eval-roles` dispatch code, `README.md`'s documented `eval-roles`/`fixture_tests` conventions) but nothing was written to the repo — `git status` is clean; this plan performs no implementation.

---

## Task 1 (tasks.md 1.1, 1.3): Vendor Superpowers `brainstorming` v6.3.0

**Files:**
- Create: `skills/brainstorming/` (full copy of Superpowers v6.3.0's `skills/brainstorming/` subtree — `SKILL.md`, `scripts/`, `spec-document-reviewer-prompt.md`, `visual-companion.md`)
- Create: `skills/brainstorming/SOURCE.md`
- Delete: `skills/.gitkeep` (placeholder for the previously-empty `skills/` directory; redundant once any real content lands under `skills/`)

**Interfaces:**
- Produces: `skills/brainstorming/` on disk, at the path `internal/runner.Role.SkillDirs()` will resolve for `role.Skills == ["brainstorming", ...]` (`filepath.Join(configRoot, "skills", "brainstorming")`) — consumed by Task 3, which lists `brainstorming` in `roles/analyst/role.yaml`'s `skills:`. Nothing in this task references `role.yaml` yet, so this task does not change `analyst`'s behavior on its own.

- [x] **Step 1: Fetch the pinned source**

Primary path (reproducible from the pinned commit):

```bash
rm -rf /tmp/superpowers-vendor
git clone https://github.com/obra/superpowers.git /tmp/superpowers-vendor
git -C /tmp/superpowers-vendor checkout b36e0829c6d0140e93cfef2ca599b1b07d4a7797
```

Fallback, only if GitHub is unreachable from this machine: the operator's local Claude Code plugin cache already holds an installed copy of Superpowers tagged `6.3.0` (confirmed identical version, same `SKILL.md` content, during planning for this change) at `~/.claude/plugins/cache/claude-plugins-official/superpowers/6.3.0/`. If using the fallback, skip straight to Step 2 using that path instead of `/tmp/superpowers-vendor`, and skip the `diff -r` sub-step of Step 3 (there is nothing independent to diff against).

- [x] **Step 2: Copy the skill subtree into this repo**

```bash
mkdir -p skills/brainstorming
cp -R /tmp/superpowers-vendor/skills/brainstorming/. skills/brainstorming/
```

Only the `skills/brainstorming/` subtree is copied — not the top-level Superpowers repo/plugin files (`LICENSE`, `package.json`, `.claude-plugin/`, etc.). Those are the plugin wrapper around all fourteen Superpowers skills; `analyst` only mounts this one skill's own directory.

- [x] **Step 3: Verify the copy is complete and unmodified**

```bash
test -f skills/brainstorming/SKILL.md
test -f skills/brainstorming/scripts/server.cjs
diff -r /tmp/superpowers-vendor/skills/brainstorming skills/brainstorming
```

Expected: both `test` commands exit 0 (files present); `diff -r` prints nothing (byte-identical copy — this is the "vendor pinned copies, not a live reference" decision made verifiable).

- [x] **Step 4: Write the source record**

Create `skills/brainstorming/SOURCE.md`:

```markdown
# Source

- Repository: https://github.com/obra/superpowers
- Tag: v6.3.0
- Commit: b36e0829c6d0140e93cfef2ca599b1b07d4a7797
```

- [x] **Step 5: Remove the now-redundant placeholder**

```bash
git rm skills/.gitkeep
```

- [x] **Step 6: Clean up the temp clone (primary path only)**

```bash
rm -rf /tmp/superpowers-vendor
```

- [x] **Step 7: Stage and commit**

```bash
git add skills/brainstorming skills/.gitkeep
git status --short
```

Expected: `skills/.gitkeep` shows as deleted (`D`), every file under `skills/brainstorming/` shows as a new file (`A`), nothing else changed.

```bash
git commit -m "$(cat <<'EOF'
feat(analyst-brainstorming-skill): vendor Superpowers brainstorming v6.3.0

Full, unmodified copy of github.com/obra/superpowers @ b36e0829c6d0140e93cfef2ca599b1b07d4a7797,
skills/brainstorming/ subtree only. Not yet mounted by any role.
EOF
)"
```

---

## Task 2 (tasks.md 1.2, 1.3): Vendor Superpowers `writing-plans` v6.3.0

**Files:**
- Create: `skills/writing-plans/` (full copy of Superpowers v6.3.0's `skills/writing-plans/` subtree — `SKILL.md`, `plan-document-reviewer-prompt.md`)
- Create: `skills/writing-plans/SOURCE.md`

**Interfaces:**
- Produces: `skills/writing-plans/` on disk, at the path `SkillDirs()` will resolve for `role.Skills` containing `"writing-plans"` — consumed by Task 3.
- Consumes: nothing from Task 1 (the two skills vendor independently; only Task 3 needs both to exist).

- [x] **Step 1: Fetch the pinned source**

If the primary-path clone from Task 1 was already cleaned up, re-clone (or reuse it if you kept it around — the commit is the same):

```bash
rm -rf /tmp/superpowers-vendor
git clone https://github.com/obra/superpowers.git /tmp/superpowers-vendor
git -C /tmp/superpowers-vendor checkout b36e0829c6d0140e93cfef2ca599b1b07d4a7797
```

Fallback (no network): `~/.claude/plugins/cache/claude-plugins-official/superpowers/6.3.0/`, same as Task 1.

- [x] **Step 2: Copy the skill subtree into this repo**

```bash
mkdir -p skills/writing-plans
cp -R /tmp/superpowers-vendor/skills/writing-plans/. skills/writing-plans/
```

- [x] **Step 3: Verify the copy is complete and unmodified**

```bash
test -f skills/writing-plans/SKILL.md
test -f skills/writing-plans/plan-document-reviewer-prompt.md
diff -r /tmp/superpowers-vendor/skills/writing-plans skills/writing-plans
```

Expected: both `test` commands exit 0; `diff -r` prints nothing.

- [x] **Step 4: Write the source record**

Create `skills/writing-plans/SOURCE.md`:

```markdown
# Source

- Repository: https://github.com/obra/superpowers
- Tag: v6.3.0
- Commit: b36e0829c6d0140e93cfef2ca599b1b07d4a7797
```

- [x] **Step 5: Clean up the temp clone (primary path only)**

```bash
rm -rf /tmp/superpowers-vendor
```

- [x] **Step 6: Stage and commit**

```bash
git add skills/writing-plans
git status --short
```

Expected: every file under `skills/writing-plans/` shows as new (`A`), nothing else changed.

```bash
git commit -m "$(cat <<'EOF'
feat(analyst-brainstorming-skill): vendor Superpowers writing-plans v6.3.0

Full, unmodified copy of github.com/obra/superpowers @ b36e0829c6d0140e93cfef2ca599b1b07d4a7797,
skills/writing-plans/ subtree only. Not yet mounted by any role.
EOF
)"
```

---

## Task 3 (tasks.md 2.1–2.6): Wire the skill dispatcher into `analyst`

**Files:**
- Modify: `roles/analyst/role.yaml:9` (the `skills:` line)
- Modify: `roles/analyst/role.md:17–50` (replace the `## Выход` section with a new `## Диспетчер скиллов` section)
- Modify: `roles/analyst/role.md:52–71` (reword the `## Как коммитить` body; heading unchanged)

**Interfaces:**
- Consumes: `skills/brainstorming/` and `skills/writing-plans/` must already exist on disk (Tasks 1–2) — `internal/runner.Role.validate()` (`internal/runner/role.go`) fails `LoadRole` with "скилл не найден" for any name in `role.Skills` whose directory doesn't exist, so this task cannot land before Tasks 1–2.
- Produces: `analyst`'s system prompt (assembled by `Role.SystemPrompt()` from `role.md` + `_base/base.md`) now instructs invoking `Skill: office-role-analyst:brainstorming` at the start of every task; `analyst`'s Claude Code plugin (built by `internal/adapters/claude.buildPlugin`) now contains both skills, mounted read-only, discoverable via the `Skill` tool.

- [x] **Step 1: Change `role.yaml`'s skill list**

In `roles/analyst/role.yaml`, change line 9 from:

```yaml
skills: []
```

to:

```yaml
skills: [brainstorming, writing-plans]
```

Nothing else in `role.yaml` changes — `tools.allow` already has `Edit`/`Write` unrestricted (from the separate `remove-role-guards` change), and the `Skill` tool is added automatically by the adapter whenever `role.Skills` is non-empty (`internal/adapters/claude/adapter.go:143–150`), not by anything in `role.yaml` itself.

- [x] **Step 2: Replace `role.md`'s `## Выход` section**

In `roles/analyst/role.md`, delete the entire block from the `## Выход` heading (line 17) through the end of the size-threshold paragraph (line 50) — i.e. everything between `## Вход`'s last line and `## Как коммитить`'s heading — and replace it with:

```markdown
## Диспетчер скиллов

Роли подключены два скилла — `brainstorming` и `writing-plans`. Это настоящие,
невендорские копии Superpowers: их текст не переписан под офис, и заканчиваются
они своим естественным путём, а не тем, что здесь написано. Ниже — как их
вызывать и когда останавливаться; порядок правил важен, следуй ему сверху вниз.

1. **Вызов.** В начале новой задачи, раньше всего остального, вызови
   `Skill: office-role-analyst:brainstorming`. Не жди, что скилл подключится
   сам по своему `description` — на практике это ненадёжно (проверено
   в `experiment/skill-question-spike`).
2. **Принудительная классификация.** Когда `brainstorming` попросит или сам
   определит классификацию (spike/bounded/architectural), считай задачу
   **architectural** независимо от того, что подсказала бы его собственная
   эвристика. Скажи прямо, что это осознанное, временное правило офиса — не
   пересмотр логики скилла и не повод сомневаться в ней от задачи к задаче.
   Так план каждый раз оказывается настоящим, закоммиченным файлом: более
   лёгкие пути скилла (`bounded`, `spike`) не оставляют файла вовсе, а офис
   пока не умеет читать такой исход.
3. **Пропуск повтора.** Перед вызовом скилла проверь, нет ли уже сделанной
   работы по этой задаче — закоммиченного спека/плана (там, куда его положили
   `brainstorming`/`writing-plans`), который уже учитывает любой полученный
   ответ человека (виден в истории переписки по задаче, включая твои
   собственные прошлые записи `artifacts`). Если есть — не вызывай скилл
   заново: убедись, что существующая работа всё ещё отвечает задаче, и сразу
   переходи к исходу.
4. **Куда пишется результат.** Пусть `brainstorming` и `writing-plans`
   сохраняют спек и план там, куда указывает их собственная логика (их путь
   по умолчанию). Не перенаправляй, не копируй и не переименовывай их
   результат в `docs/changes/<KEY>/` или любое другое место по своему выбору.
5. **Артефакты.** Перед завершением перечисли в результате (`result.json`,
   поле `artifacts`) файлы, которые составляют результат по этой задаче, — где
   бы они ни лежали. Обычно это то, что ты создал или изменил за этот прогон;
   а на чистом пропуске повтора (пункт 3 выше), где менять нечего, — те же
   самые уже существующие спек и план, даже если в этом прогоне ты их не
   трогал. Это не необязательная деталь: это единственная связь между
   свободно размещённым файлом и задачей для того, кто потом читает отчёт.
6. **Execution Handoff.** Когда `writing-plans` дойдёт до своего шага
   «Execution Handoff» (выбор между `subagent-driven-development` и
   `executing-plans`), не отвечай на этот вопрос и не вызывай ни один из
   названных скиллов. В этом офисе план всегда выполняет `implementer`, один
   пункт — один коммит; ответ на вопрос от этого не меняется, и тратить круг
   `needs_human` на предрешённый вопрос незачем. Работа роли заканчивается,
   как только план сохранён; сам файл плана — включая шапку
   «REQUIRED SUB-SKILL» — коммитится ровно таким, каким его написал
   `writing-plans`, без правок.
```

- [x] **Step 3: Reword `role.md`'s `## Как коммитить` body**

Immediately after Step 2's replacement, the `## Как коммитить` heading and its body follow. Replace the body (everything from the paragraph after the heading through the third bullet, i.e. the old lines 54–71) with:

```markdown
Писать тебе положено **только то, что ты реально создал или изменил в этом
прогоне** — остальное в репозитории ты читаешь, а не правишь. Технической
проверки этому нет: инструмент правки у тебя не ограничен, и держит границу
только эта инструкция.

Закоммить сделанное в текущую ветку последним действием:

    git add <файлы, которые ты создал или изменил>
    git commit -m "..."

Три правила, которые держишь сам, а не ограждение:

- **коммить то, что сделал, а не всё подряд.** `git add -A` сметёт в коммит
  окружение и кэши, оставшиеся от запущенных тобой тестов, — это не твоя
  работа, в каком бы каталоге она ни лежала;
- **исход `done` без закоммиченной работы не годится.** «Нет плана» теперь
  значит «ничего из того, что произвели `brainstorming`/`writing-plans`, не
  закоммичено» — не обязательно пустой `docs/changes/<KEY>/`. Незакоммиченный
  файл для следующей роли не существует: она видит ветку, а не твою папку;
- **свою ошибку откатывай `git revert`**, а незакоммиченное — `git restore`.
  Ветку раннер публикует при любом исходе, так что лишний коммит уже уехал,
  и «просто не коммитить дальше» его не отменит.
```

The `## Как коммитить` heading itself is unchanged. `## Вход`, `## Исходы`, and `## Когда задачу вернул разработчик` are untouched by this task — none of them reference the old fixed-file contract.

- [x] **Step 4: Structural sanity check**

```bash
grep -c '^## ' roles/analyst/role.md
grep -q '^## Диспетчер скиллов$' roles/analyst/role.md && echo "dispatcher heading: ok"
grep -q '^## Как коммитить$' roles/analyst/role.md && echo "commit heading: ok"
grep -q 'office-role-analyst:brainstorming' roles/analyst/role.md && echo "invocation line: ok"
grep -q 'Execution Handoff' roles/analyst/role.md && echo "handoff rule: ok"
grep -qv 'Три файла в каталоге изменения' roles/analyst/role.md && echo "old fixed-file text removed: ok"
```

Expected: five headings total (`## Вход`, `## Диспетчер скиллов`, `## Как коммитить`, `## Исходы`, `## Когда задачу вернул разработчик`), all four `echo` lines print.

- [x] **Step 5: Integration check — role loads and both skills mount (no LLM call, no cost)**

```bash
mkdir -p /tmp/analyst-dry-run && cd /tmp/analyst-dry-run
git init -q
git commit -q --allow-empty -m "seed"
echo "sanity check task" > task.md
cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office
./bin/run-agent --role analyst --workdir /tmp/analyst-dry-run --task /tmp/analyst-dry-run/task.md --dry-run
```

Expected: exits 0; the `== скиллы ==` section of the output lists both `brainstorming` and `writing-plans` (not "роль не подключает скиллов"); no error about `LoadRole`, `SkillDirs`, or `buildPlugin`. Clean up afterward:

```bash
rm -rf /tmp/analyst-dry-run
```

- [x] **Step 6: Stage and commit**

```bash
git add roles/analyst/role.yaml roles/analyst/role.md
git diff --stat --cached
```

Expected: exactly these two files listed.

```bash
git commit -m "$(cat <<'EOF'
feat(analyst-brainstorming-skill): wire skill dispatcher into analyst role

roles/analyst/role.yaml mounts brainstorming and writing-plans. role.md's
fixed-file "## Выход" section is replaced by an explicit six-rule dispatcher
("## Диспетчер скиллов"): always invoke brainstorming first, force
architectural classification, skip re-invocation on resume, let both skills
save output at their own default paths, always report artifacts, and never
answer writing-plans's Execution Handoff question. "## Как коммитить" is
generalized from "the change directory" to "what was actually created".
EOF
)"
```

---

## Task 4 (design.md "Testing Strategy" — not itemized in tasks.md): Update `capability-basic-plan` for the forced-architectural path

**Files:**
- Modify: `evals/analyst/capability-basic-plan/expect.yaml`

**Interfaces:**
- Consumes: Task 3's dispatcher (this case now exercises the forced-architectural path; its `task.md` and `fixture/` are untouched — the *task* asked ("add `Shout`, write the plan") stays a plain, unambiguous plan-writing request, only the resulting *check* changes to match where the new dispatcher puts the output).

- [x] **Step 1: Read the current case to confirm nothing else needs to change**

```bash
cat evals/analyst/capability-basic-plan/task.md
ls evals/analyst/capability-basic-plan/fixture/
```

Expected: `task.md` still reads "Add a new exported function `Shout`..." and `fixture/` still has `go.mod`, `greet/greet.go`, `greet/greet_test.go` — nothing here needs a content change, only `expect.yaml`.

- [x] **Step 2: Replace `expect.yaml`**

Replace the full contents of `evals/analyst/capability-basic-plan/expect.yaml` with:

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
      find docs/superpowers/specs -name '*.md' | grep -q . &&
      find docs/superpowers/plans -name '*.md' | grep -q .
```

This keeps the case's original purpose (a plain, unambiguous plan-writing task ending in `done`) as a canary that the new, forced-architectural path still produces real, findable output — the check no longer asserts a specific file name or heading structure (that's now `brainstorming`/`writing-plans`'s own concern, not this office's), only that *some* spec and *some* plan landed under the two new default directories.

- [x] **Step 3: Stage and commit**

```bash
git add evals/analyst/capability-basic-plan/expect.yaml
git diff --cached
```

Expected: a clean diff replacing the old `diff_scope.allow`/`fixture_tests` block with the new one shown above.

```bash
git commit -m "$(cat <<'EOF'
test(analyst-brainstorming-skill): update capability-basic-plan for forced-architectural path

Under the new dispatcher every task, including this case's plain "add Shout"
request, goes through brainstorming's architectural path and lands under
docs/superpowers/{specs,plans}/ instead of docs/changes/_manual/. The check
now asserts a spec and a plan exist somewhere under those two directories,
not a specific file name or section structure — that shape now belongs to
the vendored skills, not this office.
EOF
)"
```

Do not run this case for real yet — Task 7 runs the full paid sweep once all fixtures are in place.

---

## Task 5 (tasks.md 3.1): Add the `escalation-ambiguous-decision` golden case

**Files:**
- Create: `evals/analyst/escalation-ambiguous-decision/task.md`
- Create: `evals/analyst/escalation-ambiguous-decision/fixture/go.mod`
- Create: `evals/analyst/escalation-ambiguous-decision/fixture/README.md`
- Create: `evals/analyst/escalation-ambiguous-decision/expect.yaml`

**Interfaces:**
- Consumes: same shape as the pre-existing `evals/analyst/escalation-ambiguous-task` (task.md and fixture/ content reused, per design.md's explicit "reused/adapted rather than invented from scratch" instruction).
- Produces: nothing consumed elsewhere in this plan — this is a leaf golden case.

- [ ] **Step 1: Create the case directory and its task/fixture, reusing the existing ambiguous-task shape**

```bash
mkdir -p evals/analyst/escalation-ambiguous-decision/fixture
```

`evals/analyst/escalation-ambiguous-decision/task.md`:

```
Add payment support to this project.
```

`evals/analyst/escalation-ambiguous-decision/fixture/go.mod`:

```
module fixture

go 1.22
```

`evals/analyst/escalation-ambiguous-decision/fixture/README.md`:

```markdown
# fixture

A tiny placeholder module. Nothing here resolves what payment provider,
currency, or flow the task below refers to.
```

This is the same task and fixture as `evals/analyst/escalation-ambiguous-task` — deliberately. That case tests `analyst`'s own baseline judgment (it predates this change and never invokes any skill). This new case tests the *same* genuine, repository-unresolvable ambiguity routed through the new forced-`brainstorming` dispatcher, to confirm the skill path still ends in the same correct escalation rather than fabricating a provider choice.

- [ ] **Step 2: Write `expect.yaml`**

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

`diff_scope.allow` is broadened from the existing `escalation-ambiguous-task`'s empty list: `brainstorming`'s own checklist asks clarifying questions (step 3) *before* writing a design doc (step 6), so it may reach the payment-provider ambiguity before writing anything — or it may commit a partial spec first. Either is a legitimate pass; `diffScopeChecker` accepts an empty diff against any `allow` list, so this broadened list does not require a file to exist, it only permits one if `brainstorming` got that far.

- [ ] **Step 3: Prove the check can actually fail (no paid run required)**

```bash
go build -o /tmp/fakeagent ./cmd/eval-roles/testdata/fakeagent
EVAL_ROLES_RUN_AGENT_BIN=/tmp/fakeagent \
FAKE_AGENT_RESULT='{"outcome":"done","summary":"picked Stripe and shipped it","next_owner":"implementer"}' \
./bin/eval-roles --role analyst --case escalation-ambiguous-decision
```

Expected: the case reports `failed` — the `outcome` check catches the fake agent silently guessing a payment provider instead of escalating. This proves the check is a real gate, not a decorative one, before spending money on a real run in Task 7.

```bash
rm -f /tmp/fakeagent
```

- [ ] **Step 4: Stage and commit**

```bash
git add evals/analyst/escalation-ambiguous-decision
git status --short
```

Expected: four new files listed, all under `evals/analyst/escalation-ambiguous-decision/`.

```bash
git commit -m "$(cat <<'EOF'
test(analyst-brainstorming-skill): add escalation-ambiguous-decision golden case

Same genuine, repository-unresolvable ambiguity as escalation-ambiguous-task
("Add payment support to this project"), routed through the new forced-
brainstorming dispatcher instead of analyst's own baseline judgment. Expects
needs_human with non-empty questions; diff_scope is broadened to tolerate a
partial spec brainstorming may commit en route to the question.
EOF
)"
```

Do not run this case for real yet — Task 7 runs the full paid sweep once all fixtures are in place.

---

## Task 6 (tasks.md 3.2, 3.3): Add the `capability-resume-no-reinvoke` golden case

**Files:**
- Create: `evals/analyst/capability-resume-no-reinvoke/task.md`
- Create: `evals/analyst/capability-resume-no-reinvoke/fixture/go.mod`
- Create: `evals/analyst/capability-resume-no-reinvoke/fixture/greet/greet.go`
- Create: `evals/analyst/capability-resume-no-reinvoke/fixture/greet/greet_test.go`
- Create: `evals/analyst/capability-resume-no-reinvoke/fixture/docs/superpowers/specs/2026-08-29-greet-whisper-design.md`
- Create: `evals/analyst/capability-resume-no-reinvoke/fixture/docs/superpowers/plans/2026-08-29-greet-whisper.md`
- Create: `evals/analyst/capability-resume-no-reinvoke/expect.yaml`

**Interfaces:**
- Consumes: nothing from other tasks — this case's fixture is self-contained (see Note 4 in "Notes on task ordering and resolved ambiguities" above for why it carries the "human already answered" fact as spec content, not as `context.md`/`STATE.md`).

- [ ] **Step 1: Create the case directory**

```bash
mkdir -p evals/analyst/capability-resume-no-reinvoke/fixture/greet
mkdir -p evals/analyst/capability-resume-no-reinvoke/fixture/docs/superpowers/specs
mkdir -p evals/analyst/capability-resume-no-reinvoke/fixture/docs/superpowers/plans
```

- [ ] **Step 2: Write the task and the plain Go fixture**

`evals/analyst/capability-resume-no-reinvoke/task.md`:

```
Add an exported function `Whisper` to the `greet` package (`greet/greet.go`). It should
build on the existing `Greet` function but signal a hushed tone in its output — exact
formatting is a design decision, not fixed here.

Write the implementation plan for a developer to pick up: what to change, and a test
that pins the new behavior down.
```

`evals/analyst/capability-resume-no-reinvoke/fixture/go.mod`:

```
module fixture

go 1.22
```

`evals/analyst/capability-resume-no-reinvoke/fixture/greet/greet.go`:

```go
package greet

import "fmt"

// Greet returns a friendly greeting for name.
func Greet(name string) string {
	return fmt.Sprintf("Hello, %s!", name)
}
```

`evals/analyst/capability-resume-no-reinvoke/fixture/greet/greet_test.go`:

```go
package greet

import "testing"

func TestGreet(t *testing.T) {
	if got := Greet("Ada"); got != "Hello, Ada!" {
		t.Errorf("Greet(Ada) = %q, want %q", got, "Hello, Ada!")
	}
}
```

- [ ] **Step 3: Write the pre-committed spec, recording the resolved decision**

`evals/analyst/capability-resume-no-reinvoke/fixture/docs/superpowers/specs/2026-08-29-greet-whisper-design.md`:

```markdown
# greet-whisper — Design

## Context

`greet.Greet` returns a friendly, exclamation-marked greeting. The task asks for a
second exported function, `Whisper`, that signals a hushed tone. The package has no
existing convention for tone variants, so the exact shape of "hushed" is a real design
choice, not something the repo already answers.

## Approaches considered

1. **Lowercase + ellipsis.** `Whisper("Ada")` → `"...hello, ada..."`. Cheap, obviously
   "quiet" visually, no new dependency.
2. **Parenthetical stage direction.** `Whisper("Ada")` → `"(hello, ada)"`. Reads as a
   stage direction rather than a hushed voice; rejected — doesn't actually look quiet.
3. **Reduced punctuation, no case change.** `Whisper("Ada")` → `"hello, ada"` (no `!`,
   no case change). Too close to `Greet` with the `!` stripped; doesn't read as a
   distinct tone on its own.

## Decision

**Q1 (asked as `needs_human`, yes/no): "Approach 1 — lowercase, wrapped in `...` —
proceed?"** Human answered **yes** on 2026-08-29 (see this task's tracker history).
Approach 1 is adopted: `Whisper` lowercases the name, formats it through the same
greeting shape as `Greet`, and wraps the result in `...`.

`Whisper("Ada")` → `"...hello, ada..."`.

## What we touch

- `greet/greet.go`: add `func Whisper(name string) string`.
- `greet/greet_test.go`: add a test pinning `Whisper("Ada") == "...hello, ada..."`.

## Risks

None beyond normal test coverage — this is a pure, dependency-free string function.

## How this is verified

`go test ./...` in the fixture module; the new test in `greet/greet_test.go` is the
acceptance check for this spec.
```

- [ ] **Step 4: Write the pre-committed plan, matching `writing-plans`'s real, unedited output format**

`evals/analyst/capability-resume-no-reinvoke/fixture/docs/superpowers/plans/2026-08-29-greet-whisper.md`:

```markdown
# Greet Whisper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `Whisper`, a hushed-tone counterpart to `Greet`, to the `greet` package.

**Architecture:** One pure function, `Whisper(name string) string`, implemented in
`greet/greet.go` alongside `Greet`, reusing `Greet`'s own formatting and wrapping the
result in `...` after lowercasing.

**Tech Stack:** Go (module `fixture`, go 1.22), standard library only (`fmt`, `strings`).

**Spec:** `docs/superpowers/specs/2026-08-29-greet-whisper-design.md`

## Global Constraints

- Output format is fixed by the spec's Decision section: `Whisper("Ada")` ==
  `"...hello, ada..."` — lowercase, wrapped in `...`, reusing `Greet`'s own shape.

---

## Task 1: Add `Whisper`

**Files:**
- Modify: `greet/greet.go`
- Test: `greet/greet_test.go`

**Interfaces:**
- Produces: `func Whisper(name string) string` — the package's only other exported
  function besides `Greet`.

- [ ] **Step 1: Write the failing test**

```go
func TestWhisper(t *testing.T) {
	if got := Whisper("Ada"); got != "...hello, ada..." {
		t.Errorf("Whisper(Ada) = %q, want %q", got, "...hello, ada...")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./greet/... -run TestWhisper -v`
Expected: FAIL — `Whisper` undefined.

- [ ] **Step 3: Implement `Whisper`**

```go
// Whisper returns a hushed-tone greeting for name: Greet's own shape, lowercased
// and wrapped in "...".
func Whisper(name string) string {
	return "..." + strings.ToLower(Greet(name)) + "..."
}
```

Add `"strings"` to the existing `import` block in `greet/greet.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./greet/... -run TestWhisper -v`
Expected: PASS

- [ ] **Step 5: Run the full package suite**

Run: `go test ./...`
Expected: PASS — `TestGreet` and `TestWhisper` both green.

- [ ] **Step 6: Commit**

```bash
git add greet/greet.go greet/greet_test.go
git commit -m "feat(greet): add Whisper, a hushed-tone greeting"
```

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-08-29-greet-whisper.md`. Two
execution options:

1. **Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review
   between tasks, fast iteration
2. **Inline Execution** - Execute tasks in this session using executing-plans, batch
   execution with checkpoints

Which approach?
```

This plan document is written exactly as `writing-plans` would leave it, unedited — including the "REQUIRED SUB-SKILL" header and the unanswered "Execution Handoff" question at the end (dispatcher rule 6: `analyst` never strips or answers this).

- [ ] **Step 5: Write `expect.yaml`**

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

`diff_scope.allow: []` is deliberate: a correct resume makes no further edits at all — the pre-seeded spec and plan already exist and already match the human's answer, so a passing run produces an empty diff. The `fixture_tests` command is the concrete, harness-runnable form of `tasks.md` item 3.3 — confirm `result.json`'s `artifacts` is non-empty and every listed path actually exists on disk inside the fixture.

- [ ] **Step 6: Prove the check can actually fail (no paid run required)**

Two separate fake-agent runs, each proving a different failure mode this case's checks exist to catch:

```bash
go build -o /tmp/fakeagent ./cmd/eval-roles/testdata/fakeagent
```

a) Wrong outcome (re-investigating instead of resuming):

```bash
EVAL_ROLES_RUN_AGENT_BIN=/tmp/fakeagent \
FAKE_AGENT_RESULT='{"outcome":"needs_human","summary":"re-checking the approach","next_owner":"human","questions":[{"id":"Q1","text":"still sure?"}]}' \
./bin/eval-roles --role analyst --case capability-resume-no-reinvoke
```

Expected: `failed` — the `outcome` check catches the deviation from `done`/`implementer`.

b) Empty artifacts (correct outcome, but the report doesn't point at the existing spec/plan):

```bash
EVAL_ROLES_RUN_AGENT_BIN=/tmp/fakeagent \
FAKE_AGENT_RESULT='{"outcome":"done","summary":"resumed, nothing to add","next_owner":"implementer","artifacts":[]}' \
./bin/eval-roles --role analyst --case capability-resume-no-reinvoke
```

Expected: `failed` — the `fixture_tests` check's `jq -e '.artifacts | length > 0'` catches the empty list. This is the specific new failure mode this case exists to prevent (design doc: "`artifacts` is now the only link between a freely-placed file and its task").

```bash
rm -f /tmp/fakeagent
```

- [ ] **Step 7: Stage and commit**

```bash
git add evals/analyst/capability-resume-no-reinvoke
git status --short
```

Expected: seven new files listed, all under `evals/analyst/capability-resume-no-reinvoke/`.

```bash
git commit -m "$(cat <<'EOF'
test(analyst-brainstorming-skill): add capability-resume-no-reinvoke golden case

Fixture pre-seeds a fully committed spec and plan (docs/superpowers/{specs,plans}/)
whose spec already records a resolved needs_human decision and its human answer.
Expects analyst to recognize this as already-done work, skip re-invoking
brainstorming/writing-plans, and finish done/next_owner=implementer with a fully
empty diff — the resume-skip dispatcher rule's own success case.
EOF
)"
```

Do not run this case for real yet — Task 7 runs the full paid sweep now that all fixtures are in place.

---

## Task 7 (tasks.md 4.1, 4.2): Verification

**Files:** none (no code changes — this task only runs existing tooling).

- [ ] **Step 1: Confirm the repo builds and the pre-existing Go test suite is unaffected**

```bash
go build ./...
go vet ./...
go test ./...
```

Expected: all three succeed. This plan makes zero `.go` changes, so this is a pure regression check — a failure here means something in the environment is broken, not something this change caused; investigate before proceeding.

- [ ] **Step 2: Run the two new golden cases (tasks.md 4.1)**

```bash
./bin/eval-roles --role analyst --case escalation-ambiguous-decision
./bin/eval-roles --role analyst --case capability-resume-no-reinvoke
```

Expected: both report `passed`. These are real, paid role invocations (per `README.md`, "Golden-кейсы ролей (eval-roles)" — no simulation this time, `EVAL_ROLES_RUN_AGENT_BIN` unset). If either fails, use `--keep-failed` to inspect the preserved fixture directory (path printed to stderr) before re-running:

```bash
./bin/eval-roles --role analyst --case escalation-ambiguous-decision --keep-failed
./bin/eval-roles --role analyst --case capability-resume-no-reinvoke --keep-failed
```

- [ ] **Step 3: Run the full `analyst` suite, including the two cases this plan did not touch**

```bash
./bin/eval-roles --role analyst
```

Expected: all four cases (`capability-basic-plan`, `escalation-ambiguous-task`, `escalation-ambiguous-decision`, `capability-resume-no-reinvoke`) report `passed`.

`capability-basic-plan` passing here confirms Task 4's updated `expect.yaml` actually matches what the new dispatcher produces (not just what the design doc predicted).

`escalation-ambiguous-task` is the one case this plan never modifies. If it now fails where it previously passed, that is a real, unresolved finding, not something to silently patch: it would mean the forced-`brainstorming` dispatcher (Task 3) writes something under `docs/superpowers/**` even on a task this simple and short-circuited, which this case's own `diff_scope.allow: []` does not tolerate. Neither `proposal.md` nor the design doc's Risks/Boundary-Conditions sections anticipate this specific regression, so if it happens: **stop, do not edit `escalation-ambiguous-task/expect.yaml` on your own judgment**, and report it back as an open design question (whether that case's `allow` should also be broadened like `escalation-ambiguous-decision`'s, or whether the dispatcher's forced-architectural rule needs a narrower trigger) rather than resolving it unilaterally — this is exactly the kind of gap the design doc's own "Boundary Conditions Deliberately Left Open" section asks Build to watch for.

- [ ] **Step 4: Record the result**

No file to write — this task's only output is the pass/fail state confirmed in Steps 1–3. If all cases passed, the `analyst-brainstorming-skill` change is functionally complete per this plan; any further action (archiving the OpenSpec change, updating `docs/openspec/changes/analyst-brainstorming-skill/tasks.md`'s checkboxes) belongs to whichever Comet phase or workflow owns that bookkeeping, not to this plan.
