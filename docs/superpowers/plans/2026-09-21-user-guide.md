---
change: user-guide
design-doc: docs/superpowers/specs/2026-09-21-user-guide-design.md
base-ref: 47bbd733baf53f8eeca54c46418a748675fc3171
---

# user-guide Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the scheduler samples inside the payload so `runner init` lays them out, stop them pinning `--role implementer`, and re-sort the user-facing documentation into Diátaxis quadrants for a reader who installed from a release and has no clone.

**Architecture:** Three unit files move from `bootstrap/` into `office/scheduler/` and ride the existing `//go:embed` payload; `cmd/runner/init.go` grows a second placement loop over one extracted `place()` helper so both kinds of sample share one never-overwrite semantics. The documentation is redistributed, not rewritten from scratch: `docs/ONBOARDING.md` shrinks to a spine of links and its 635 lines disperse into two new `docs/guide/` how-tos, two new `docs/reference/` documents, and `bootstrap/jira/README.md`. A new shell harness, `scripts/doc-recipe-test.sh`, walks the documented recipe end to end with a fake `claude` on `PATH`, so the recipe is executable rather than merely proofread.

**Tech Stack:** Go 1.26 (`embed`, `io/fs`, standard `testing`), POSIX sh for the harness, Markdown for the documentation, git for the moves.

**Spec:** `docs/superpowers/specs/2026-09-21-user-guide-design.md` (how), `docs/openspec/changes/user-guide/specs/office-install/spec.md` (what must be true), `docs/openspec/changes/user-guide/proposal.md` (why), `docs/openspec/changes/user-guide/design.md` (framework decisions D1–D7).

## Global Constraints

Every task's requirements implicitly include this section.

- **Documentation language is Russian.** Identifiers, terms and config keys are English (`CLAUDE.md`, «Конвенции»). This plan is in English; every word this plan tells you to write into a `.md`, `.yaml`, `.plist`, `.service`, `.timer` or Go comment is Russian, and every quoted Russian string in this plan is to be copied verbatim.
- **Commit messages follow the repository's existing convention:** English subject in Conventional-Commit form (`feat(runner): …`, `docs(guide): …`), English body, and the trailer `Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>`.
- **`master` is protected. Nothing is pushed to it.** All work stays on the current branch `comet/user-guide`. Do not `git push` at all unless the task says so; no task in this plan says so.
- **Commits are split by kind.** Code and sample moves go in their own commits; documentation goes in separate commits. Never mix a Go change and a Markdown change in one commit. Tasks 1–2 are code commits, Tasks 3–14 and 16 are documentation commits, Task 15 is a script (code) commit.
- **Every file move uses `git mv`,** never `cp` + `rm`, so history follows the file.
- **Base ref:** `47bbd733baf53f8eeca54c46418a748675fc3171`.
- **After any Go change, the triad must be clean:** `gofmt -l .` prints nothing, `go build ./...` succeeds, `go test ./...` passes.
- **Comet phase is Build** (`docs/openspec/changes/user-guide/.comet.yaml`, `phase: build`), so ordinary implementation writes are allowed. Do not edit `.comet.yaml`, `tasks.md`, `proposal.md`, `design.md` or the delta spec — they are the change's own artifacts and belong to other phases.
- **Scope is exactly the 31 items of `docs/openspec/changes/user-guide/tasks.md`.** Tick each `- [ ]` in that file as its work lands (that file *is* writable — it is the change's task ledger — but nothing else under `docs/openspec/changes/user-guide/`).

---

### Task 1: The scheduler samples move into the payload and stop pinning a role

Covers tasks.md 1.1, 1.2, 1.3, 1.7.

**Files:**
- Move: `bootstrap/local.office.runner.plist` → `office/scheduler/local.office.runner.plist`
- Move: `bootstrap/office-runner.service` → `office/scheduler/office-runner.service`
- Move: `bootstrap/office-runner.timer` → `office/scheduler/office-runner.timer`
- Modify: `office/payload.go:19-20` (the `//go:embed` directives)
- Test: `office/payload_test.go:15` (`payloadDirs`), `office/payload_test.go:59-64` (`want` in `TestPayloadNamesTheEssentials`), plus one new test

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: the payload paths `scheduler/local.office.runner.plist`, `scheduler/office-runner.service`, `scheduler/office-runner.timer`, readable through `payload.Payload` (`github.com/kao73/virtual-office/office`, `var Payload embed.FS`). Task 2 reads exactly those three paths.

- [x] **Step 1: Move the three files with `git mv`**

```bash
mkdir -p office/scheduler
git mv bootstrap/local.office.runner.plist office/scheduler/local.office.runner.plist
git mv bootstrap/office-runner.service     office/scheduler/office-runner.service
git mv bootstrap/office-runner.timer       office/scheduler/office-runner.timer
git status --short
```

Expected: three `R` (rename) entries and nothing else. `bootstrap/` now holds only `README.md` and `jira/`.

- [x] **Step 2: Run the payload tests and watch the mirror test fail**

Run: `go test ./office/ -run TestPayloadMirrorsOfficeDirectory -v`

Expected: FAIL, three errors of the form

```
office/scheduler/local.office.runner.plist лежит в office/, но не едет в поставке
```

That test (`office/payload_test.go:111`) walks `office/` on disk and demands every file be in `Payload`. It is the existing guard that a new top-level entry needs its own embed directive.

- [x] **Step 3: Add the embed directive**

In `office/payload.go`, change line 19 from

```go
//go:embed all:roles all:skills all:hooks all:sbx-kits
```

to

```go
//go:embed all:roles all:skills all:hooks all:sbx-kits all:scheduler
```

`all:` rather than plain `scheduler`: the directory has no `_`- or `.`-prefixed entry today, but the comment right above the directive warns that embed silently skips such paths without it, and a sample that silently stops shipping is the exact failure this change exists to remove.

- [x] **Step 4: Run the mirror test again**

Run: `go test ./office/ -run TestPayloadMirrorsOfficeDirectory -v`

Expected: PASS.

- [x] **Step 5: Teach `payload_test.go` about the new directory**

`office/payload_test.go:15` currently reads

```go
	payloadDirs  = []string{"roles", "skills", "hooks", "sbx-kits"}
```

Change it to

```go
	payloadDirs  = []string{"roles", "skills", "hooks", "sbx-kits", "scheduler"}
```

and in `TestPayloadNamesTheEssentials` (`office/payload_test.go:59-64`) extend `want` so the three unit files are named individually — the disk walk above would pass silently if a file vanished from disk altogether:

```go
	want := []string{
		"roles/_base/base.yaml", "roles/_base/base.md",
		"hooks/require-result.sh", "hooks/debug-env.sh",
		"skills/comet/scripts/comet-hook-router.mjs",
		"sbx-kits/bake-comet-template.sh", "sbx-kits/comet-cli/spec.yaml",
		"scheduler/local.office.runner.plist",
		"scheduler/office-runner.service",
		"scheduler/office-runner.timer",
	}
```

- [x] **Step 6: Write the failing test that no shipped unit pins a role**

Append to `office/payload_test.go`:

```go
// Ни один образец задания не сужает раннер до одной роли. Образец с --role
// называет одну роль из трёх, и две оставшиеся не запускаются никогда:
// конвейер не падает, а встаёт, и сказать об этом некому. Читается поставка,
// а не диск: копия в ${OFFICE_HOME}/scheduler/ побайтно равна поставке
// (cmd/runner/init_test.go), а в клоне поставка равна каталогу office/
// (TestPayloadMirrorsOfficeDirectory выше) — значит один источник накрывает
// обоих читателей из сценария спецификации.
func TestSchedulerSamplesDoNotPinRole(t *testing.T) {
	units := []string{
		"scheduler/local.office.runner.plist",
		"scheduler/office-runner.service",
		"scheduler/office-runner.timer",
	}
	for _, name := range units {
		raw, err := fs.ReadFile(Payload, name)
		if err != nil {
			t.Fatalf("%s не найден в поставке: %v", name, err)
		}
		if bytes.Contains(raw, []byte("--role")) {
			t.Errorf("%s передаёт раннеру --role: две роли из трёх не запустятся", name)
		}
	}
}
```

`bytes` and `io/fs` are already imported by this file (`office/payload_test.go:4,5`); add nothing.

- [x] **Step 7: Run it and watch it fail**

Run: `go test ./office/ -run TestSchedulerSamplesDoNotPinRole -v`

Expected: FAIL with two errors —

```
scheduler/local.office.runner.plist передаёт раннеру --role: две роли из трёх не запустятся
scheduler/office-runner.service передаёт раннеру --role: две роли из трёх не запустятся
```

(the `.timer` has no invocation at all and must already pass).

- [x] **Step 8: Strip `--role implementer` from the launchd sample**

In `office/scheduler/local.office.runner.plist`, the `ProgramArguments` array currently reads

```xml
  <key>ProgramArguments</key>
  <array>
    <string>/Users/ВЛАДЕЛЕЦ/.office/bin/runner</string>
    <string>loop</string>
    <string>--role</string>
    <string>implementer</string>
    <string>--every</string>
    <string>2m</string>
  </array>
```

Replace it with

```xml
  <key>ProgramArguments</key>
  <array>
    <string>/Users/ВЛАДЕЛЕЦ/.office/bin/runner</string>
    <string>loop</string>
    <string>--every</string>
    <string>2m</string>
  </array>
```

- [x] **Step 9: Strip `--role implementer` from the systemd sample**

In `office/scheduler/office-runner.service`, line 15 currently reads

```
ExecStart=%h/.office/bin/runner tick --role implementer
```

Replace it with

```
ExecStart=%h/.office/bin/runner tick
```

Nothing else in the two units changes: the systemd side stays `Type=oneshot` under a timer, the launchd side stays a `loop`, and the reasoning for that asymmetry stays in `bootstrap/README.md` where it already is.

- [x] **Step 10: Fix the one stale cross-reference inside the unit**

`office/scheduler/office-runner.service` lines 16-17 say

```
# Остановка сигналом: идущий прогон агента прерывается, задачу вернёт reap
# (bootstrap/README.md). Дожидаться конца прогона раннер пока не умеет.
```

The reference is still correct — `bootstrap/README.md` keeps the explanation — so leave those two lines exactly as they are. This step exists so you do not "helpfully" retarget the comment at `office/scheduler/`: the explanation does not move, only the files do.

- [x] **Step 11: Run the whole payload package**

Run: `go test ./office/ -v`

Expected: PASS, including `TestSchedulerSamplesDoNotPinRole`, `TestPayloadNamesTheEssentials`, `TestPayloadCarriesEveryFileOnDisk`, `TestPayloadMirrorsOfficeDirectory`, `TestExecutablePayloadFilesStartWithShebang`.

- [x] **Step 12: Run the triad**

Run:

```bash
gofmt -l .
go build ./...
go test ./...
```

Expected: `gofmt -l .` prints nothing; build and tests pass.

- [x] **Step 13: Commit**

```bash
git add office/payload.go office/payload_test.go office/scheduler
git commit -m "$(cat <<'MSG'
feat(office): ship the scheduler samples inside the payload

The three unit files move from bootstrap/ into office/scheduler/ and gain
their own embed directive: a new top-level entry needs one, files inside an
already embedded directory do not. One copy of each unit, and it is the one
the binary carries, so a reader with a clone and a reader who installed from
a release are answered from the same file.

Both invocations lose --role implementer. A sample that names one of three
roles starves the other two, and the pipeline does not fail — it stalls with
nothing saying why. A test over the payload pins that no shipped unit carries
the flag, so the defect cannot come back silently.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 2: `runner init` lays out `${OFFICE_HOME}/scheduler/`

Covers tasks.md 1.4, 1.5, 1.6, 1.8, 1.10 (Go half).

**Files:**
- Modify: `cmd/runner/init.go:17-89`
- Modify: `cmd/runner/main.go:19-35` (the `usage` const)
- Test: `cmd/runner/init_test.go:18-100`

**Interfaces:**
- Consumes: from Task 1, the payload paths `scheduler/local.office.runner.plist`, `scheduler/office-runner.service`, `scheduler/office-runner.timer`.
- Produces: `${OFFICE_HOME}/scheduler/` containing byte-identical copies of the three units; the package-level identifiers `schedulerDir string`, `schedulerSamples []string` and `place(out io.Writer, payloadPath, dst string) error` in `package main` of `cmd/runner`. Task 15's harness asserts the five files exist after `runner init`.

- [x] **Step 1: Widen the "exactly two samples" assertion in the fresh-home test**

`cmd/runner/init_test.go:31-40` currently reads

```go
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	want := []string{tracker.ProjectsLocalExampleFile, jira.ExampleFile}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("в хозяйстве %v, ожидались ровно %v", names, want)
	}
```

**Widen it, do not delete it.** It is the only check that init writes nothing extra, and the delta spec's first scenario still says "contains exactly". Replace those ten lines with:

```go
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	// Образцы конфигурации — файлы, задания планировщика — каталог: «ровно»
	// осталось «ровно», просто список вырос на scheduler/. Убрать это
	// утверждение значило бы остаться без единственной проверки, что init
	// не кладёт в хозяйство ничего сверх обещанного.
	wantFiles := []string{tracker.ProjectsLocalExampleFile, jira.ExampleFile}
	wantTop := append(append([]string{}, wantFiles...), schedulerDir)
	slices.Sort(wantTop)
	if !slices.Equal(names, wantTop) {
		t.Errorf("в хозяйстве %v, ожидались ровно %v", names, wantTop)
	}
```

Then, at `cmd/runner/init_test.go:41`, the byte-comparison loop `for _, name := range want {` must iterate the files only — `scheduler` is a directory and `os.ReadFile` would fail on it. Change its header to

```go
	for _, name := range wantFiles {
```

leaving its body untouched.

- [x] **Step 2: Widen the "создан" count in the same test**

`cmd/runner/init_test.go:54-57` currently reads

```go
	printed := out.String()
	if strings.Count(printed, "создан") != 2 {
		t.Errorf("ожидались два «создан»:\n%s", printed)
	}
```

Replace with

```go
	printed := out.String()
	if got := strings.Count(printed, "создан"); got != 5 {
		t.Errorf("«создан» в выводе %d, ожидалось пять (два образца и три задания):\n%s", got, printed)
	}
```

- [x] **Step 3: Add the scheduler assertions to the fresh-home test**

Immediately after the widened `wantFiles` byte-comparison loop (i.e. before `printed := out.String()`), insert:

```go
	// scheduler/ — ровно три задания, побайтно из поставки. Все три на любой
	// платформе: машина, на которой юнит правят, не всегда та, на которой он
	// работает, и выбирать по GOOS значило бы вешать теги сборки на данные.
	schedEntries, err := os.ReadDir(filepath.Join(home, schedulerDir))
	if err != nil {
		t.Fatalf("%s не прочитан: %v", schedulerDir, err)
	}
	var schedNames []string
	for _, e := range schedEntries {
		schedNames = append(schedNames, e.Name())
	}
	slices.Sort(schedNames)
	wantSched := append([]string{}, schedulerSamples...)
	slices.Sort(wantSched)
	if !slices.Equal(schedNames, wantSched) {
		t.Errorf("в %s %v, ожидались ровно %v", schedulerDir, schedNames, wantSched)
	}
	for _, name := range wantSched {
		got, err := os.ReadFile(filepath.Join(home, schedulerDir, name))
		if err != nil {
			t.Fatalf("%s не прочитан: %v", name, err)
		}
		exp, err := fs.ReadFile(payload.Payload, schedulerDir+"/"+name)
		if err != nil {
			t.Fatalf("%s не найден в поставке: %v", name, err)
		}
		if !bytes.Equal(got, exp) {
			t.Errorf("%s отличается от образца в поставке", name)
		}
	}
```

- [x] **Step 4: Add the "an edited unit survives a second init" test**

Append to `cmd/runner/init_test.go` (this is the delta spec's third scenario):

```go
// Правленое задание переживает второй init: в нём уже стоят настоящая учётка
// и путь, и переписать его значило бы снести настройку машины одной командой.
func TestInitLeavesEditedSchedulerSampleAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv(runner.HomeEnv, home)

	var first bytes.Buffer
	if err := initCommand(nil, &first); err != nil {
		t.Fatalf("первый init отказал: %v", err)
	}

	edited := filepath.Join(home, schedulerDir, "office-runner.service")
	body := "ExecStart=/home/owner/.office/bin/runner tick\n"
	if err := os.WriteFile(edited, []byte(body), 0o644); err != nil {
		t.Fatalf("%s не записан: %v", edited, err)
	}

	var second bytes.Buffer
	if err := initCommand(nil, &second); err != nil { // nil error — код возврата 0
		t.Fatalf("второй init отказал: %v", err)
	}
	got, err := os.ReadFile(edited)
	if err != nil {
		t.Fatalf("%s не прочитан: %v", edited, err)
	}
	if string(got) != body {
		t.Errorf("правленое задание изменено: было %q, стало %q", body, got)
	}
	printed := second.String()
	if !strings.Contains(printed, "оставлен") || !strings.Contains(printed, edited) {
		t.Errorf("вывод не сообщает об оставленном задании:\n%s", printed)
	}
	if strings.Contains(printed, "создан") {
		t.Errorf("второй init что-то создал:\n%s", printed)
	}
}
```

- [x] **Step 5: Run the tests and see them fail**

Run: `go test ./cmd/runner/ -run 'TestInit' -v`

Expected: FAIL — `schedulerDir` and `schedulerSamples` are undefined, so the package does not compile:

```
./init_test.go:NN:NN: undefined: schedulerDir
./init_test.go:NN:NN: undefined: schedulerSamples
```

This is the failing state the design asks for. Do not touch `init.go` before you have seen it.

- [x] **Step 6: Extract the `place` helper in `cmd/runner/init.go`**

Insert after `writeAndClose` (i.e. after `cmd/runner/init.go:34`):

```go
// schedulerDir — подкаталог заданий планировщика. Имя одно и то же в поставке
// и в хозяйстве: искать образец там же, где он лежит в клоне, проще, чем
// помнить два имени.
const schedulerDir = "scheduler"

// schedulerSamples — задания планировщика из поставки. Отдельным списком,
// а не в samples: рабочего имени у них нет — их не копируют, а ставят
// в launchd или systemd, и правят в них учётку и путь ${OFFICE_HOME},
// а не четыре значения конфигурации. Кладутся все три на любой платформе:
// машина, на которой юнит правят, не всегда та, на которой он работает.
var schedulerSamples = []string{
	"local.office.runner.plist",
	"office-runner.service",
	"office-runner.timer",
}

// place кладёт один файл поставки в хозяйство, не трогая уже лежащий там.
// Одна функция на оба вида образцов нарочно: разойдись у них семантика
// перезаписи, узнали бы мы об этом чужим правленым юнитом.
func place(out io.Writer, payloadPath, dst string) error {
	raw, err := fs.ReadFile(payload.Payload, payloadPath)
	if err != nil {
		return fmt.Errorf("образец %s не найден в поставке: %w", payloadPath, err)
	}
	// O_EXCL, а не «проверить и записать»: существующий образец не
	// трогается ни при какой гонке, а недописанный (кончилось место)
	// не остаётся лежать под видом оставленного — он убирается.
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	switch {
	case errors.Is(err, fs.ErrExist):
		fmt.Fprintf(out, "оставлен %s\n", dst)
		return nil
	case err != nil:
		return fmt.Errorf("образец не записан: %w", err)
	}
	if err := writeAndClose(f, raw); err != nil {
		os.Remove(dst) // недописанный образец не должен сойти за оставленный
		return fmt.Errorf("образец %s не записан: %w", dst, err)
	}
	fmt.Fprintf(out, "создан   %s\n", dst)
	return nil
}
```

- [x] **Step 7: Rewrite the placement loops in `initCommand`**

`cmd/runner/init.go:61-83` currently holds the inlined loop. Replace everything from `for _, s := range samples {` through its closing `}` (line 83) with:

```go
	for _, s := range samples {
		if err := place(out, s.example, filepath.Join(home, s.example)); err != nil {
			return err
		}
	}

	schedHome := filepath.Join(home, schedulerDir)
	if err := os.MkdirAll(schedHome, 0o755); err != nil {
		return fmt.Errorf("каталог заданий планировщика не создан: %w", err)
	}
	// Права явно — по той же причине, что у самого хозяйства выше: под строгим
	// umask каталог вышел бы 0700, и в него не вошёл бы ни чужой uid песочницы,
	// ни второй пользователь машины. Не избыточно: MkdirAll отдаёт права umask'у.
	if err := os.Chmod(schedHome, 0o755); err != nil {
		return fmt.Errorf("права каталога заданий планировщика не выставлены: %w", err)
	}
	for _, name := range schedulerSamples {
		// Путь внутри поставки — всегда через косую черту: так устроен embed.FS.
		if err := place(out, schedulerDir+"/"+name, filepath.Join(schedHome, name)); err != nil {
			return err
		}
	}
```

- [x] **Step 8: Add the second closing stanza**

`cmd/runner/init.go:84-88` currently ends the function with

```go
	fmt.Fprintln(out, "дальше: скопируйте каждый образец под рабочее имя и поправьте значения:")
	for _, s := range samples {
		fmt.Fprintf(out, "  cp %s %s   # %s\n", filepath.Join(home, s.example), filepath.Join(home, s.working), s.edit)
	}
	return nil
```

Replace with

```go
	fmt.Fprintln(out, "дальше: скопируйте каждый образец под рабочее имя и поправьте значения:")
	for _, s := range samples {
		fmt.Fprintf(out, "  cp %s %s   # %s\n", filepath.Join(home, s.example), filepath.Join(home, s.working), s.edit)
	}
	// Вторая подсказка отдельной: задания не копируют под рабочее имя — их
	// ставят в планировщик, и правят в них учётку и путь, а не значения
	// конфигурации. Слить обе в одну фразу значило бы получить фразу, неверную
	// для обеих.
	fmt.Fprintf(out, "задания планировщика — в %s: поставьте нужное в launchd или systemd,\n", schedHome)
	fmt.Fprintln(out, "поправив в нём учётку и путь ${OFFICE_HOME}:")
	fmt.Fprintf(out, "  %s   # macOS, launchd\n", filepath.Join(schedHome, "local.office.runner.plist"))
	fmt.Fprintf(out, "  %s\n", filepath.Join(schedHome, "office-runner.service"))
	fmt.Fprintf(out, "  %s   # Linux, systemd: пара service + timer\n", filepath.Join(schedHome, "office-runner.timer"))
	return nil
```

- [x] **Step 9: Update the doc comment on `initCommand`**

`cmd/runner/init.go:36-39` says "каталог `${OFFICE_HOME}` и два образца". Change that first paragraph to

```go
// initCommand заводит хозяйство раннера: каталог ${OFFICE_HOME}, два образца
// конфигурации рядом с местом, где будут лежать рабочие файлы, и подкаталог
// scheduler/ с тремя заданиями планировщика. Всё — из поставки в бинарнике
// в любом режиме, в том числе из клона: init не разрешает офис и ничего
// не распаковывает.
```

Leave the second paragraph (lines 41-44, about working files never being touched) exactly as it is.

- [x] **Step 10: Run the tests and see them pass**

Run: `go test ./cmd/runner/ -run 'TestInit' -v`

Expected: PASS for `TestInitLaysOutFreshHome`, `TestInitLeavesConfiguredHomeAlone`, `TestInitLeavesEditedSchedulerSampleAlone`.

- [x] **Step 11: Fix the usage text in `cmd/runner/main.go`**

Two lines of the `usage` const are now wrong. Line 22 currently reads

```
  runner loop [--every 2m]      то же по расписанию, пока не остановят
```

Replace with

```
  runner loop [--every 2m] [--role R]   то же по расписанию, пока не остановят
```

Both `tick` and `loop` accept `--role` (`cmd/runner/office.go:286` defines it on `loop`), and only `tick` said so. Line 28 currently reads

```
  runner init                   завести ${OFFICE_HOME} и положить образцы projects.local.example.yaml и tracker.example.yaml
```

Replace with

```
  runner init                   завести ${OFFICE_HOME}: образцы конфигурации и задания планировщика в scheduler/
```

- [x] **Step 12: Run the triad**

Run:

```bash
gofmt -l .
go build ./...
go test ./...
```

Expected: `gofmt -l .` prints nothing; build and tests pass.

- [x] **Step 13: Eyeball the real output once**

Run:

```bash
OFFICE_HOME="$(mktemp -d)/office" go run ./cmd/runner init
```

Expected: five `создан` lines (two in the home root, three under `scheduler/`), then the `cp …` stanza for the two configuration samples, then the scheduler stanza. Confirm the paths printed are absolute and point inside the temporary home.

- [x] **Step 14: Commit**

```bash
git add cmd/runner/init.go cmd/runner/init_test.go cmd/runner/main.go
git commit -m "$(cat <<'MSG'
feat(runner): init lays out ${OFFICE_HOME}/scheduler/

The two placement loops share one extracted place() helper, so the O_EXCL
open, the "оставлен"/"создан" reporting and the remove-on-partial-write
cleanup cannot drift between configuration samples and unit files. The new
directory gets an explicit Chmod(0o755) beside MkdirAll: under a strict umask
it would otherwise come out 0700, the same bug the home itself already guards
against.

The fresh-home assertion that the home contains exactly two samples widens
rather than disappears — it is the only check that init writes nothing extra.
A new scenario pins that an edited unit survives a second init.

usage: loop now says it takes --role, which it always did, and init no longer
claims it places only the two configuration samples.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 3: `bootstrap/README.md` keeps the explanation and loses the files

Covers tasks.md 1.9, 1.10 (documentation half).

**Files:**
- Modify: `bootstrap/README.md` (52 lines, whole file reviewed; lines 3-8, 12-18, 24-29, 38-48 rewritten)

**Interfaces:**
- Consumes: from Task 1, the clone path `office/scheduler/`; from Task 2, the installed path `${OFFICE_HOME}/scheduler/`.
- Produces: the canonical explanation of what launchd and systemd each need, cited by `docs/guide/operations.md` (Task 8) and `docs/ONBOARDING.md` (Task 11).

- [ ] **Step 1: Rewrite the opening paragraph**

`bootstrap/README.md:1-10` currently reads

```markdown
# Обвязка машины

Здесь то, что стоит вокруг офиса на конкретной машине и в его поставку не входит:
задания планировщика (ниже), локальная JIRA в контейнере (`jira/`) и образ
песочницы `sbx` с запечённым `comet`/`openspec` (`office/sbx-kits/`). Раннер про них не
знает: с планировщиком он говорит через сигналы, с трекером — через адрес
из `${OFFICE_HOME}/tracker.yaml`, а от песочницы просто ожидает, что нужный
образ уже испечён.

Порядок, в котором это заводят на новой машине, — `docs/ONBOARDING.md`.
```

Replace with

```markdown
# Обвязка машины

Здесь объяснение того, что стоит вокруг офиса на конкретной машине: запуск
раннера по расписанию (ниже), локальная JIRA в контейнере (`jira/`) и образ
песочницы `sbx` с запечённым `comet`/`openspec` (`office/sbx-kits/`). Раннер про них не
знает: с планировщиком он говорит через сигналы, с трекером — через адрес
из `${OFFICE_HOME}/tracker.yaml`, а от песочницы просто ожидает, что нужный
образ уже испечён.

**Самих заданий планировщика здесь больше нет.** Они едут в поставке: в клоне
это `office/scheduler/`, а на машине без клона их кладёт `runner init`
в `${OFFICE_HOME}/scheduler/`. Копия одна, и она в поставке, — два образца
одного юнита были бы двумя ответами на один вопрос.

Порядок, в котором это заводят на новой машине, — `docs/ONBOARDING.md`.
```

- [ ] **Step 2: Rewrite the launchd subsection**

`bootstrap/README.md:24-29` currently reads

```markdown
### macOS, launchd

`~/Library/LaunchAgents/local.office.runner.plist` — правь пути и загружай:

    launchctl load ~/Library/LaunchAgents/local.office.runner.plist
    launchctl unload ~/Library/LaunchAgents/local.office.runner.plist
```

Replace with

```markdown
### macOS, launchd

Образец — `local.office.runner.plist`: в клоне `office/scheduler/`, на машине
`${OFFICE_HOME}/scheduler/`. Правится в нём учётка в путях (`ВЛАДЕЛЕЦ`) и сам
путь `${OFFICE_HOME}`, если он не дефолтный; после этого копируется в
`~/Library/LaunchAgents/` и загружается:

    cp "${OFFICE_HOME:-$HOME/.office}/scheduler/local.office.runner.plist" ~/Library/LaunchAgents/
    launchctl load ~/Library/LaunchAgents/local.office.runner.plist
    launchctl unload ~/Library/LaunchAgents/local.office.runner.plist
```

Leave the paragraph about `SIGTERM` and `reap` (lines 31-36) unchanged.

- [ ] **Step 3: Rewrite the systemd subsection**

`bootstrap/README.md:38-48` currently reads

```markdown
### Linux, systemd

Пара `office-runner.service` + `office-runner.timer`. Раннер работает
разовым запуском (`Type=oneshot`), а расписание держит таймер:

    systemctl --user enable --now office-runner.timer
    systemctl --user list-timers office-runner.timer

Разовый запуск вместо `loop` выбран намеренно: планировщик уже умеет расписание,
и дублировать его циклом внутри процесса незачем. `loop` пригодится там, где
планировщика нет вовсе.
```

Replace with

```markdown
### Linux, systemd

Пара `office-runner.service` + `office-runner.timer` — оттуда же,
`office/scheduler/` в клоне и `${OFFICE_HOME}/scheduler/` на машине. Пути в них
написаны через `%h`, так что править надо только `${OFFICE_HOME}`, если он
не дефолтный. Раннер работает разовым запуском (`Type=oneshot`), а расписание
держит таймер:

    mkdir -p ~/.config/systemd/user
    cp "${OFFICE_HOME:-$HOME/.office}/scheduler/office-runner.service" ~/.config/systemd/user/
    cp "${OFFICE_HOME:-$HOME/.office}/scheduler/office-runner.timer"   ~/.config/systemd/user/
    systemctl --user daemon-reload
    systemctl --user enable --now office-runner.timer
    systemctl --user list-timers office-runner.timer

Разовый запуск вместо `loop` выбран намеренно: планировщик уже умеет расписание,
и дублировать его циклом внутри процесса незачем. `loop` пригодится там, где
планировщика нет вовсе.

Ни один образец не сужает раннер до одной роли. `--role` у `tick` и `loop` есть,
но образец с ним назвал бы одну роль из трёх, а две оставшиеся не запускались бы
никогда — и конвейер не упал бы, а встал, ничего об этом не сказав.
```

- [ ] **Step 4: Check the file for surviving claims that the units live here**

Run: `grep -n 'bootstrap/' bootstrap/README.md`

Expected: no hit that names a `.plist`, `.service` or `.timer` under `bootstrap/`. If any remains, fix it. Then run `grep -rn 'bootstrap/local.office.runner.plist\|bootstrap/office-runner' -- . ':!docs/comet' ':!docs/superpowers/plans'` from the repo root: the only remaining hits should be in `docs/guide/operations.md` (fixed in Task 8) and `docs/openspec/changes/user-guide/` (the change's own artifacts, left alone).

- [ ] **Step 5: Commit**

```bash
git add bootstrap/README.md
git commit -m "$(cat <<'MSG'
docs(bootstrap): point at office/scheduler/ for the unit files

bootstrap/ keeps what it is good at — what launchd and systemd each need, and
why the systemd side is a oneshot under a timer — and stops claiming the
samples live in it. A reader with a clone reads them in office/scheduler/; a
reader without one gets them from runner init.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 4: `docs/reference/configuration.md`

Covers tasks.md 2.1, 2.2.

**Files:**
- Create: `docs/reference/configuration.md`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: the destination for `docs/ONBOARDING.md` Б5 (Task 11 links here instead of repeating it) and one row of `docs/README.md` (Task 12).

**Source material, verbatim map.** Everything in this document comes from one of these; nothing is invented:

| Section to write | Source |
|---|---|
| Границы: репозиторий — фреймворк, `${OFFICE_HOME}` — инстанс | `internal/tracker/config.go:20-24` (the comment above the file constants) and `docs/ONBOARDING.md:401-404` |
| Три слоя правил и что кого перекрывает | `internal/tracker/config.go:667-672` (`LoadProjects` doc comment) and `:747-752` (the `runner.Union` calls) |
| `defaults` и что в нём разрешено | `internal/tracker/config.go:637-654` (`reservedRulesKey`, `defaultsKeys`, `checkKeys`) |
| Какой файл читается откуда | `internal/runner/office.go:104-140` (`ResolveOffice`, four branches) and `docs/guide/operations.md:173-192` (the `${OFFICE_HOME}` tree) |
| Переменные окружения | the grep in Step 2 below |

- [ ] **Step 1: Create the directory and the skeleton**

Create `docs/reference/configuration.md` with exactly these headings, in this order:

```markdown
# Конфигурация: что где лежит и что что перекрывает

## Две половины: репозиторий и `${OFFICE_HOME}`

## Три слоя правил

## Какой файл читается откуда

## Переменные окружения

## Ключи: где искать
```

Write the body under each heading from the source map above. Hard rules for the content:

- **This document owns structure, not keys.** Per-key documentation stays in `office/tracker.example.yaml` and `office/projects.local.example.yaml`, which ride inside the binary and cannot drift from the release. The last section links them as the authority and does not restate a single key name beyond the four the projects sample asks you to edit.
- The three layers, in precedence order, lowest first: `office/roles/_base/base.yaml` (базовый слой, кладёт в роль `runner.LoadRole`) → `defaults` в `${OFFICE_HOME}/projects.local.yaml` (машинный слой) → сама запись проекта там же (проектный слой). State plainly that `network` and `tools` **объединяются, а не перекрываются**: `runner.Union` даёт объединение списков, и убрать домен или запрет нижнего слоя верхним нельзя — `internal/tracker/config.go:747-752`. Назвать можно, убрать — нет.
- `defaults` accepts only `network` and `tools` (`defaultsKeys`, `internal/tracker/config.go:642`); anything else is a refusal naming the project, the key and the file.
- The four office-resolution branches, first match wins: `OFFICE_CONFIG_ROOT` → клон и его commit; версия из ldflags → релиз; `vcs.revision` из build info → сборка из клона без обёртки; иначе отказ.

- [ ] **Step 2: Establish the environment-variable list against the code, not from memory**

Run:

```bash
grep -rn 'OFFICE_[A-Z_]*' --include='*.go' . | grep -v '_test.go'
grep -rn 'OFFICE_[A-Z_]*' install.sh scripts/*.sh
grep -n 'user_env\|secret_env' office/tracker.example.yaml
```

The answer these produce, and which the document must match exactly:

| Переменная | Кто читает | Что двигает |
|---|---|---|
| `OFFICE_HOME` | `internal/runner/archive.go:11` (`HomeEnv`), и через него всё хозяйство | где хозяйство раннера; умолчание `~/.office` |
| `OFFICE_CONFIG_ROOT` | `internal/runner/office.go:19` (`ConfigRootEnv`) | брать офис из клона, а не из поставки; выставляют обёртки `bin/*` |
| `OFFICE_INVOCATION_DIR` | `cmd/run-agent/main.go:296` | каталог, из которого позвали обёртку: относительные пути в аргументах не едут после перехода в корень |
| `OFFICE_INSTALL_FROM` | `install.sh:19` | ставить из каталога артефактов, без сети |
| `OFFICE_VERSION` | `install.sh:37` | какой тег ставить; несовместим с `OFFICE_INSTALL_FROM` (`install.sh:54`) |
| `OFFICE_BACKEND` | `scripts/smoke.sh:35` | бэкенд для smoke-прогонов |
| `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN` | `internal/adapters/claude/adapter.go:73` | кред агента; заданы оба — берётся первый |
| `GITHUB_TOKEN` | пуш ветки и PR-проход | право писать в репозиторий проекта |
| имена из `accounts.*.user_env` / `secret_env` | `${OFFICE_HOME}/tracker.yaml` | учётки JIRA: в файле только имена переменных, значения в окружении |

**Two corrections the proposal's wording needs, and which the document must state honestly:**
- `OFFICE_BACKEND` is **not** read by the runner. It is read only by `scripts/smoke.sh:35`. Write it as such: «читает только `scripts/smoke.sh`; сам раннер бэкенд берёт из флага `--backend`». Do not imply the runner honours it.
- `OFFICE_VERSION` and `OFFICE_INVOCATION_DIR` are real and were not in the proposal's list. Include them; a reference that omits a variable the reader will meet is the same failure as one that invents a variable.

- [ ] **Step 3: Verify every layer claim against the loader**

Run: `sed -n '630,760p' internal/tracker/config.go`

Read it and confirm, line by line, each of these sentences before it goes into the document:
1. There is exactly one projects file, and it is machine-side (`LoadProjects` takes one path).
2. `defaults` is deleted from the project map before the loop, so it is never a project.
3. `Network` and `Tools.Allow`/`Tools.Deny` are `runner.Union(defaults…, local…)`.
4. `branch_prefix` defaults to `agent/` inside the loader, not at use (`DefaultBranchPrefix`, `:43`).
5. `worktree_root` must be absolute.
6. `auto_merge.enabled` without `forge` is a refusal.

Fix the document text for anything that does not match. A reference that is wrong is worse than absent.

- [ ] **Step 4: Write the closing "where the keys live" section**

```markdown
## Ключи: где искать

Каждый ключ описан там, где он едет, — в самих образцах, и они ездят внутри
бинарника, так что разойтись с релизом не могут:

- проекты — `office/projects.local.example.yaml`, он же
  `${OFFICE_HOME}/projects.local.example.yaml` после `runner init`;
- подключение к JIRA — `office/tracker.example.yaml`, он же
  `${OFFICE_HOME}/tracker.example.yaml`;
- бюджеты — `office/budgets.yaml` и необязательная накладка
  `${OFFICE_HOME}/budgets.yaml`, разобранные в `docs/notes/budgets.md`;
- граф статусов — `office/workflow.yaml`; его читает раннер, агент его не видит.

Здесь их нет намеренно: два описания одного ключа расходятся при первой правке,
и расходится всегда то, которое не едет вместе с кодом.
```

- [ ] **Step 5: Commit**

```bash
git add docs/reference/configuration.md
git commit -m "$(cat <<'MSG'
docs(reference): configuration — layers, precedence, environment

What the two shipped examples cannot show from inside themselves: the three
rule layers and that they union rather than override, the four office
resolution branches, and every environment variable with the file that reads
it. Per-key documentation stays in the examples, which ride inside the binary
and cannot drift from the release.

OFFICE_BACKEND is documented as what it is — read by scripts/smoke.sh, not by
the runner — rather than as the proposal's shorthand suggested.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 5: `docs/reference/jira-requirements.md`

Covers tasks.md 2.3, 2.4.

**Files:**
- Create: `docs/reference/jira-requirements.md`
- Modify: `docs/notes/jira-setup.md` — **content unchanged**; only confirm it stays and is linked from the new document (tasks.md 2.4)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: the destination for `docs/ONBOARDING.md` Б1 (instance requirements half), Б2 (non-polygon half) and Б3, and for the «Что остаётся руками» table; one row of `docs/README.md` (Task 12).

**Audience.** Someone else's JIRA administrator, who will not run a stranger's script with admin rights. Every requirement is stated as a requirement with a way to check it. `scripts/jira-setup.sh` is named as *one* way to satisfy them, not *the* way.

**Source material, verbatim map:**

| Section to write | Source (file, lines) |
|---|---|
| Версия и API | `docs/ONBOARDING.md:165` («Годится любая Jira Server 8.13 с REST v2 и basic-авторизацией») |
| Проект и шаблон | `docs/ONBOARDING.md:207-232` (the Kanban-template requirement and *why the order is mandatory*) |
| Статусы, которых требует граф | `office/workflow.yaml:23` (`statuses`) and `office/tracker.example.yaml:66-74` (`status_map`) |
| Переходы | `docs/notes/jira-setup.md:130-141` (шаги под шесть статусов и двадцать два перехода) and `:157-166` («Проверить, что вышло») |
| Четыре поля аренды и их типы | `docs/notes/jira-setup.md:292-309` (the table `office_owner` / `office_run_id` / `office_lease_until` / `office_attempts`) |
| Метка ожидания человека | `docs/notes/jira-setup.md:307-309` and `office/tracker.example.yaml:84-86` |
| Тип связи | `office/tracker.example.yaml:93-96` (`depends_on_link: Depends`) |
| Учётки и минимум две | `docs/notes/jira-setup.md:212-232`, `docs/ONBOARDING.md:294-334` |
| `/myself` и канонический регистр имени | `docs/notes/jira-setup.md:248-254`, `docs/ONBOARDING.md:441-445` |
| Права учётки роли | `docs/notes/jira-setup.md:222-226` (`jira-software-users`, Developers) |
| Право пуша у токена forge | `docs/ONBOARDING.md:385-400` |
| Доски и «ВНЕ КОЛОНОК» | `docs/ONBOARDING.md:243-247` and the symptom at `:614-618`; explanation in `docs/notes/jira-setup.md:44-94` |
| Что нельзя сделать скриптом | `docs/ONBOARDING.md:587-600` (the four-row table), **minus** the «Память виртуалки Docker» row, which is polygon-only and already lives in `bootstrap/jira/README.md:63-89` |

- [ ] **Step 1: Create the file with this skeleton**

```markdown
# Что офис требует от вашей JIRA

## Инстанс

## Проект

## Статусы

## Переходы

## Четыре поля аренды

## Метка «ждёт человека»

## Тип связи «зависит от»

## Учётки

## Право пуша у токена forge

## Доски

## Что нельзя сделать скриптом

## Один способ всё это получить
```

- [ ] **Step 2: Write each requirement as «требование — как проверить»**

Every section is two things and nothing else: the requirement, and a command or observation that answers yes/no. Use the checks already proven on the polygon rather than inventing new ones:

- **Инстанс.** Jira Server 8.13 с REST v2 и basic-авторизацией. Проверка: `curl -fsS <base_url>/rest/api/2/serverInfo` отдаёт JSON с версией.
- **Проект.** Kanban-шаблон, доска приезжает вместе с ним. Порядок обязателен: проект заводится **до** раскладки полей, иначе поля встанут мимо его экранов и первый же захват ответит «Field cannot be set. It is not on the appropriate screen» (`docs/ONBOARDING.md:209-215`).
- **Статусы.** Восемь: `Backlog`, `Analysis`, `Ready`, `In Progress`, `Review`, `Approved`, `Done`, `Blocked`. Имена на инстансе могут быть другими — соответствие задаёт `status_map` в `${OFFICE_HOME}/tracker.yaml`, и все восемь там названы явно, включая совпадающие.
- **Переходы.** Двадцать два, ровно те, которыми ходит раннер по `office/workflow.yaml`: захват, исходы трёх ролей, PR-проход, reap, ответ человека, остановка по бюджету и человеческий триаж `Backlog → Analysis`, `Backlog → Ready`. Проверка: у задачи в `Ready` в списке переходов есть `In Progress`, у задачи в `Review` — `Approved`:

```sh
curl -su "$JIRA_USER:$JIRA_PASSWORD" '<base_url>/rest/api/2/issue/<KEY>/transitions' |
  python3 -c "import json,sys; print([t['to']['name'] for t in json.load(sys.stdin)['transitions']])"
```

- **Четыре поля аренды.** Transcribe the table from `docs/notes/jira-setup.md:294-300` unchanged: `office_owner` (текст), `office_run_id` (текст), `office_lease_until` (дата и время), `office_attempts` (число). Add the warning that identifiers differ per instance and that on a Software project they are shifted, because `customfield_10000` is taken by `Rank`. Add the one that costs people a day: `runner ls` не подставляет поля аренды в запрос, а JIRA молча игнорирует неизвестный `customfield_*`, — перепутанный номер всплывает только на `runner tick` отказом «Field 'cf[NNNNN]' does not exist» (`docs/ONBOARDING.md:456-465`).
- **Метка.** Атрибут «ждёт человека» — label, экранов не требует; имя задаётся `human_flag_label` в `${OFFICE_HOME}/tracker.yaml`.
- **Тип связи.** Тип «зависит от» под именем `Depends`; переименование означает правку и скрипта, и `depends_on_link`.
- **Учётки.** Минимум две: офисная и человеческая, потому что ответом человека считается комментарий **не от учётки офиса**. Третья, на роль, — опция. Учётке нужны права писать поля аренды и делать переходы (группа `jira-software-users`, входящая в Developers схемы прав по умолчанию). Проверка — комментарий от этой учётки возвращает `201`. Имя пишется ровно так, как его канонизирует JIRA: вход она принимает и в другом регистре, а подписывает канонично, и раннер сверяет имя с `/myself` при сборке и отказывается работать при расхождении.
- **Право пуша.** Transcribe the check from `docs/ONBOARDING.md:385-400`, including the two traps: `404` вместо `403` у токена без охвата, и то, что право открывать pull request этой проверкой не видно.
- **Доски.** Статус, не попавший ни в одну колонку, исчезает с доски вместе с задачами — офис при этом работает, а владельцу кажется, что нет. Проверка: открыть доску и увидеть задачу.
- **Что нельзя сделать скриптом.** The three-row table (экран перехода `Office: Claim`, галочка «Allow all statuses to transition to this one», поле на карточке и быстрый фильтр по аренде), each with the reason from `docs/ONBOARDING.md:589-600`, and a pointer to `docs/notes/jira-setup.md`, разделы «Workflow для нескольких раннеров» и «Кто работает над задачей, если рабочего статуса нет».

- [ ] **Step 3: Write the closing section that names the script as one way**

```markdown
## Один способ всё это получить

Всё перечисленное выше умеют завести три скрипта из этого репозитория —
`scripts/jira-setup.sh` (статусы, четыре поля, тип связи, экраны, учётки),
`scripts/jira-workflow.sh` (шаги и переходы) и `scripts/jira-boards.sh`
(раскладка досок). Они запускаются в этом порядке и после того, как проект
заведён; повторный запуск безопасен. Это один способ, а не единственный:
требования выше сформулированы так, чтобы их можно было выполнить руками,
и администратор, который не станет запускать чужой скрипт с правами админа,
ничего этим не теряет — кроме времени.

Почему настройка устроена именно так, какие грабли нашлись на полигоне и почему
workflow приходится делать скриптом, хотя REST его не знает, — `docs/notes/jira-setup.md`.
Сам полигон в контейнере — `bootstrap/jira/README.md`.
```

- [ ] **Step 4: Confirm `docs/notes/jira-setup.md` is untouched**

Run: `git status --short docs/notes/jira-setup.md`

Expected: no output. The note keeps its job — the explanation of why the setup is shaped that way — and the new document links to it, per tasks.md 2.4. Do not delete or trim it.

- [ ] **Step 5: Commit**

```bash
git add docs/reference/jira-requirements.md
git commit -m "$(cat <<'MSG'
docs(reference): what the office needs from your JIRA

Written for someone else's administrator: each requirement with a way to check
it, and scripts/jira-setup.sh named as one way to satisfy them rather than the
way. An admin who will not run a stranger's script with admin rights now has
somewhere to turn.

Extracted from docs/notes/jira-setup.md and ONBOARDING Б2–Б3; the note stays
where it is, as the explanation of why the setup is shaped that way.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 6: The polygon half leaves `docs/ONBOARDING.md` for `bootstrap/jira/README.md`

Covers tasks.md 3.1, 3.2.

**Files:**
- Modify: `bootstrap/jira/README.md` (lines 10 and 69 replaced; the container material folded into existing sections)
- Modify: `docs/ONBOARDING.md` — remove only the container material of Б1–Б2; the rest of ONBOARDING is Task 11's job

**Interfaces:**
- Consumes: from Task 5, `docs/reference/jira-requirements.md` as the destination for the non-polygon half of the same sections.
- Produces: `bootstrap/jira/README.md` as the single account of the container.

- [ ] **Step 1: Read the destination before moving anything**

Run: `cat bootstrap/jira/README.md`

Note its existing sections: «Сначала SDK» (13), «Поднять» (32), «Второй инстанс рядом» (48), «Три вещи, о которых лучше знать заранее» (63), «Почему так, а не иначе» (90). The three warnings in ONBOARDING Б1 (licence lives three days, ~6 GiB of VM memory, death by OOM looks like a live container) are **already** in «Три вещи, о которых лучше знать заранее». Fold, do not append: a second account of the same thing is exactly what this task exists to prevent.

- [ ] **Step 2: Move the material that is genuinely only in ONBOARDING**

From `docs/ONBOARDING.md:167-206` (Б1), the only things `bootstrap/jira/README.md` does not already say are:
1. the SDK check as a checkbox — `ls bootstrap/jira/sdk/atlassian-plugin-sdk-8.2.10` shows `bin`, `repository` and `apache-maven-3.9.5`;
2. the measured first-run time (887 seconds) and that `docker compose -p office-jira ps` holds `starting` while the healthcheck waits out twenty minutes;
3. the `-p office-jira` explanation — the compose file stays in `bootstrap/jira/` while you have returned to the office root, and the project name is set inside the file so it is found from anywhere;
4. the `serverInfo` check returning version `8.13.19`.

Add (1) as a verification line at the end of «Сначала SDK», and (2)–(4) into «Поднять». Keep them as single sentences; do not restate the three warnings.

From `docs/ONBOARDING.md:207-293` (Б2), move to `bootstrap/jira/README.md` only what is polygon-specific: that the local instance's admin credentials are `admin:admin` and that `curl -su admin:admin` is written that way **because** it is the local polygon's account, printed in this very README — «с настоящим паролем так не делайте» (`docs/ONBOARDING.md:263-266`). Everything else in Б2 — the Kanban template, the mandatory order, the three scripts, the trial task, the `--workflow` flag for a second project — is instance setup, and it went to `docs/reference/jira-requirements.md` in Task 5. Delete it from ONBOARDING rather than copying it a third time.

- [ ] **Step 3: Replace the two pointers back at ONBOARDING**

`bootstrap/jira/README.md:10` currently reads

```markdown
Порядок настройки — `docs/ONBOARDING.md`, дорожка «новый проект». Здесь только
сам контейнер.
```

Replace with

```markdown
Здесь сам контейнер. Что офис требует от любого инстанса JIRA, полигонного или
чужого, — `docs/reference/jira-requirements.md`; порядок, в котором это
проходят, — `docs/ONBOARDING.md`.
```

`bootstrap/jira/README.md:69` currently reads

```markdown
и настройка заново, то есть все скрипты из `ONBOARDING.md` ещё раз. Стоит это
```

Replace with

```markdown
и настройка заново, то есть все скрипты из `docs/reference/jira-requirements.md`
ещё раз. Стоит это
```

- [ ] **Step 4: Cut the moved material out of `docs/ONBOARDING.md`**

Delete `docs/ONBOARDING.md` lines 163-293 (Б1, Б2 and «Пробная задача»). Do not renumber or restructure the rest yet — Task 11 rewrites the whole document into a spine, and doing it twice wastes a review.

- [ ] **Step 5: Check nothing now claims ONBOARDING owns the container**

Run: `grep -n 'ONBOARDING' bootstrap/jira/README.md`

Expected: exactly one hit, the line from Step 3.

- [ ] **Step 6: Commit**

```bash
git add bootstrap/jira/README.md docs/ONBOARDING.md
git commit -m "$(cat <<'MSG'
docs(bootstrap): the container's account lives in one place

bootstrap/jira/README.md already documented the container; the polygon half of
ONBOARDING Б1–Б2 folds into its existing sections rather than appending a
second account of the same thing. Its two pointers back at ONBOARDING now name
the documents that actually own that material.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 7: `docs/guide/quickstart.md` becomes a tutorial and nothing else

Covers tasks.md 4.1.

**Files:**
- Modify: `docs/guide/quickstart.md` (213 lines)
- Modify: `docs/guide/roles-and-flow.md` (receives what leaves the tutorial)

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: the tutorial that `README.md` (Task 13) and `docs/README.md` (Task 12) point at as the single "first hour" path.

**The rule being applied.** A tutorial is one path that works. No alternatives, no "you could also", no explanation of why. Where the explanation is worth keeping, it moves to `docs/guide/roles-and-flow.md` (explanation) or `docs/reference/configuration.md` (reference, Task 4). Where a link suffices, drop the prose and link.

- [ ] **Step 1: Delete the alternative paths**

Remove these passages from `docs/guide/quickstart.md`:
- lines 41-43 («Всё ниже написано командой `runner …`… см. [«Путь из клона»]») — the pointer to the alternative;
- lines 195-213, the whole section «## Путь из клона». It is a contributor's concern and `docs/guide/development.md` already owns it (`## Где что лежит`, and the wrapper explanation). Replace both with nothing: `development.md` is linked from `docs/README.md` and from README.
- lines 117-120 («Роль можно вызвать и по одной… Задача, заведённая сразу в `Ready`…») — a second path through the same tutorial.

- [ ] **Step 2: Move the explanatory passages to `docs/guide/roles-and-flow.md`**

Three passages in the tutorial explain rather than instruct. Move them, verbatim where they still read correctly, into `docs/guide/roles-and-flow.md`:

| Passage | Lines | Destination heading in `roles-and-flow.md` |
|---|---|---|
| «Каждый `tick` делает всё за один вызов: …» | 98-104 | new subsection `### Что делает один тик` under `## Путь задачи` |
| «Раннер разберёт ответ по меткам… Прозу тоже никто не теряет» plus the comment-author paragraph | 152-168 | new subsection `### Как офис отличает голос человека` under `## Возвраты и круги` |
| «Отличие одно: настроенный инстанс. Какой трекер у проекта, сказано в его записи…» | 172-173 | one sentence into `## Статус — не колонка`, where the tracker/graph distinction already lives |

In the tutorial, replace each with the single instruction that remains plus a link — e.g. after the four `runner tick` calls, one line: «Что именно делает один тик — [«Роли и путь задачи»](roles-and-flow.md#что-делает-один-тик).»

- [ ] **Step 3: Replace the install block's explanation with the release path and a link**

`docs/guide/quickstart.md:21-35` explains the payload, the update semantics and the missing-tag situation. Keep only what the reader must do; move the rest behind links:

```markdown
`install.sh` кладёт `runner` и `run-agent` в `${OFFICE_HOME:-~/.office}/bin`,
проверив контрольную сумму архива, и говорит, лежит ли этот каталог в `PATH`.
Обновление — та же команда ещё раз и перезапуск цикла.

Пока в репозитории нет ни одного тега релиза, качать с `releases/latest` нечего —
ставится локальный снапшот: `sh scripts/release-snapshot.sh`, затем
`OFFICE_INSTALL_FROM=dist sh install.sh`.

Что где оказывается после установки и что чем перекрывается —
[«Конфигурация»](../reference/configuration.md).
```

- [ ] **Step 4: Point the JIRA section at the new reference instead of ONBOARDING**

`docs/guide/quickstart.md:181-193` currently sends the reader to `docs/ONBOARDING.md` and `docs/notes/jira-setup.md`. Replace the two pointers with:

```markdown
Что инстанс обязан уметь и чем это проверить — [«Что офис требует от вашей
JIRA»](../reference/jira-requirements.md); в каком порядке это проходят —
[`docs/ONBOARDING.md`](../ONBOARDING.md); почему именно так —
`docs/notes/jira-setup.md`. Сам инстанс, если своего нет, поднимается из
`bootstrap/jira/`.
```

Keep line 6's existing pointer at `docs/ONBOARDING.md` — the tutorial is allowed to say what it is not.

- [ ] **Step 5: Read the result end to end and check the tutorial property**

Run: `cat docs/guide/quickstart.md`

Confirm by reading: exactly one path; no sentence beginning «Можно и…», «Роль можно вызвать и…», «У разработчика… путь другой»; every «почему» either gone or behind a link. The document should be roughly 120-140 lines, down from 213.

- [ ] **Step 6: Commit**

```bash
git add docs/guide/quickstart.md docs/guide/roles-and-flow.md
git commit -m "$(cat <<'MSG'
docs(guide): quickstart is a tutorial and nothing else

One path that works: the alternatives (the clone path, the single-role tick,
the task started straight in Ready) are gone, and the passages that explained
rather than instructed move to roles-and-flow.md, which is the explanation
document. What the reader needs to do stays; what they may want to know is a
link away.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 8: `docs/guide/operations.md` schedules from `${OFFICE_HOME}/scheduler/`

Covers tasks.md 4.2.

**Files:**
- Modify: `docs/guide/operations.md:9-38` (the «По расписанию» section) and `:189-192` (the `runner init` sentence in the `${OFFICE_HOME}` tree)

**Interfaces:**
- Consumes: from Task 2, the layout `${OFFICE_HOME}/scheduler/{local.office.runner.plist,office-runner.service,office-runner.timer}`; from Task 3, `bootstrap/README.md` as the explanation.
- Produces: nothing later tasks depend on beyond the link Task 16 walks.

- [ ] **Step 1: Replace the «По расписанию» opening**

`docs/guide/operations.md:9-20` currently reads

```markdown
## По расписанию

`runner loop` — не демон и не supervisor: он не следит за собой, не
перезапускается и не держит состояния между циклами (`cmd/runner/main.go`).
Поднимать и ронять его должен системный планировщик — примеры для macOS
и Linux лежат в `bootstrap/` вместе с объяснением, что и почему
(`bootstrap/README.md`).

```sh
runner reap                  # вернуть задачи с истёкшей арендой
runner loop --every 2m       # цикл до сигнала, если планировщика нет
```
```

Replace with

```markdown
## По расписанию

`runner loop` — не демон и не supervisor: он не следит за собой, не
перезапускается и не держит состояния между циклами (`cmd/runner/main.go`).
Поднимать и ронять его должен системный планировщик.

```sh
runner reap                  # вернуть задачи с истёкшей арендой
runner loop --every 2m       # цикл до сигнала, если планировщика нет
```
```

- [ ] **Step 2: Replace the launchd/systemd subsection**

`docs/guide/operations.md:22-38` currently reads

```markdown
### launchd (macOS) и systemd (Linux)

`bootstrap/local.office.runner.plist` и пара `bootstrap/office-runner.service` +
`bootstrap/office-runner.timer` — рабочие образцы, оба задают одно и то же:
рабочий каталог, кред агента, каталог хозяйства и лог. launchd останавливает
задание сигналом `SIGTERM`: идущий прогон агента прерывается, задачу вернёт
`reap` (при `loop` он идёт каждым заходом). systemd вместо `loop` использует
разовый запуск (`Type=oneshot`) под таймером — расписание уже умеет
планировщик, дублировать его циклом внутри процесса незачем.

**Меняя образец под свою машину, укажите путь к `${OFFICE_HOME}/bin/runner` —
тот бинарник, что кладёт `install.sh`, — а не к обёртке `./bin/runner` из
клона.** Обёртка при каждом запуске собирает раннер заново (`go build`)
и требует Go и сам клон на диске; заданию планировщика это ни к чему, ему
нужен готовый бинарник. Файлы-образцы в репозитории уже показывают этот путь
плейсхолдером (`~/.office/bin/runner`, `ВЛАДЕЛЕЦ`/`%h` в примерах) — под свою
машину останется поправить учётку и сам путь `OFFICE_HOME`, если он не дефолтный.
```

Replace with

```markdown
### launchd (macOS) и systemd (Linux)

Образцы уже лежат на машине: `runner init` кладёт их в
`${OFFICE_HOME}/scheduler/` — `local.office.runner.plist` для launchd и пару
`office-runner.service` + `office-runner.timer` для systemd. Все три на любой
платформе: машина, на которой юнит правят, не всегда та, на которой он
работает. В клоне те же файлы лежат в `office/scheduler/`.

Правится в образце две вещи: учётка в путях (`ВЛАДЕЛЕЦ` у launchd; у systemd её
подставляет `%h`) и сам путь `${OFFICE_HOME}`, если он не дефолтный. Ни одного
`--role` в образцах нет и быть не должно: он сузил бы раннер до одной роли
из трёх, и две оставшиеся не запускались бы никогда.

```sh
# macOS, launchd
cp "${OFFICE_HOME:-~/.office}/scheduler/local.office.runner.plist" ~/Library/LaunchAgents/
launchctl load ~/Library/LaunchAgents/local.office.runner.plist
```

```sh
# Linux, systemd
mkdir -p ~/.config/systemd/user
cp "${OFFICE_HOME:-~/.office}/scheduler/office-runner.service" ~/.config/systemd/user/
cp "${OFFICE_HOME:-~/.office}/scheduler/office-runner.timer"   ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now office-runner.timer
```

launchd останавливает задание сигналом `SIGTERM`: идущий прогон агента
прерывается, задачу вернёт `reap` (при `loop` он идёт каждым заходом). systemd
вместо `loop` использует разовый запуск (`Type=oneshot`) под таймером —
расписание уже умеет планировщик, дублировать его циклом внутри процесса
незачем. Подробнее, что каждому из двух нужно и почему, — `bootstrap/README.md`.

**Путь в образце ведёт к `${OFFICE_HOME}/bin/runner` — тому бинарнику, что
кладёт `install.sh`, — а не к обёртке `./bin/runner` из клона.** Обёртка при
каждом запуске собирает раннер заново (`go build`) и требует Go и сам клон
на диске; заданию планировщика это ни к чему, ему нужен готовый бинарник.
```

- [ ] **Step 3: Add `scheduler/` to the `${OFFICE_HOME}` tree**

`docs/guide/operations.md:175-187` draws the home. Insert a line after `budgets.yaml`:

```
├── scheduler/                      образцы заданий планировщика: launchd и systemd
```

and replace lines 189-192, which currently read

```markdown
`runner init` создаёт только сам каталог и два образца
(`projects.local.example.yaml`, `tracker.example.yaml`) прямо в его корне;
`office/<версия>/`, `repos/`, `worktrees/`, `runs/` и `ledger.jsonl` появляются
по факту первого настоящего прогона.
```

with

```markdown
`runner init` создаёт сам каталог, два образца конфигурации
(`projects.local.example.yaml`, `tracker.example.yaml`) в его корне и
`scheduler/` с тремя заданиями планировщика; `office/<версия>/`, `repos/`,
`worktrees/`, `runs/` и `ledger.jsonl` появляются по факту первого настоящего
прогона. Уже лежащий файл init не трогает никогда — ни образец, ни правленое
задание.
```

- [ ] **Step 4: Check no claim about `bootstrap/` survives here**

Run: `grep -n 'bootstrap' docs/guide/operations.md`

Expected: exactly one hit, the `bootstrap/README.md` reference added in Step 2.

- [ ] **Step 5: Commit**

```bash
git add docs/guide/operations.md
git commit -m "$(cat <<'MSG'
docs(guide): schedule from ${OFFICE_HOME}/scheduler/

The files are on the reader's machine after runner init; the release archive
never carried bootstrap/, so sending them there was an instruction that could
not be followed. The edit is the account and the path, and the section now
says so with the copy commands for both schedulers.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 9: `docs/guide/machine-setup.md` — preparing a machine, without a clone

Covers tasks.md 4.3.

**Files:**
- Create: `docs/guide/machine-setup.md`
- Modify: `docs/ONBOARDING.md` — delete lines 37-160 (Дорожка А, A1–A5) once transcribed

**Interfaces:**
- Consumes: from Task 2, that `runner init` creates the home; from Task 4, `docs/reference/configuration.md` for the environment variables.
- Produces: the destination that `docs/ONBOARDING.md`'s spine (Task 11) links to for steps 1-4, and one row of `docs/README.md` (Task 12).

**Source material.** `docs/ONBOARDING.md` A1 (39-70), A2 (71-102), A3 (103-122), A4 (123-138), A5 (139-160), transcribed and rewritten for a reader who installed from a release. Everything there is correct except the assumptions it makes about a clone; those are what changes.

- [ ] **Step 1: Create the file with this skeleton**

```markdown
# Подготовка машины

Это делается раз на машину. В конце раннер установлен, хозяйство заведено,
кред агента лежит в окружении, песочница закрыта от сети и отвечает.

Ставится офис из релиза, без клона и без Go — [«Быстрый
старт»](quickstart.md#установка) показывает саму команду. Здесь — всё, что
должно быть на машине вокруг неё.

## Инструменты

## Креды

## Сеть песочниц

## Хозяйство раннера

## Проверка машины
```

- [ ] **Step 2: Transcribe «Инструменты» with the clone assumptions removed**

From A1 (`docs/ONBOARDING.md:39-70`), keep as checkboxes:
- `git` 2.32+ — with the reason unchanged: the bare-clone recipe passes the branch name as protocol-v2 «unborn HEAD», which git learned in 2.32, and on an older one `push origin master` hits a mismatch.
- Docker — under it live both the agent's sandbox and the local JIRA.
- `sbx` (Docker Sandboxes) — the default backend, with the platform requirements verbatim (macOS 26+ only on Apple Silicon, or Linux Ubuntu 24.04+ x86_64 with KVM on bare metal, or Windows 11 x86_64) and the `sbx login` requirement.
- `claude` — needed only by the `local` backend.
- `python3` — needed by the four `scripts/jira-*.sh` and by the checks in `docs/reference/jira-requirements.md`; **not** needed by the runner.

**Drop, because this document is for a reader without a clone:** the Go 1.26+ line (state instead, in one sentence, that Go is needed only to build from source and that `docs/guide/development.md` covers that) and the `uv` line (it is a tool of the training task, not of the office).

Keep the verification paragraph, adjusted: `git --version`, `python3 --version`, `docker info`, and for `sbx` the real check — `sbx policy ls` answers `401 Unauthorized` without a login and prints rules with one, because `--help` works either way.

- [ ] **Step 3: Transcribe «Креды» unchanged**

A2 (`docs/ONBOARDING.md:71-102`) needs no rewriting: both credentials live in the environment and only there. Transcribe it whole — the two agent variables and that `ANTHROPIC_API_KEY` wins when both are set, `GITHUB_TOKEN` and that its scope is checked later against a named repository, the `[ -n "$VAR" ] && echo есть` check, and the warning that a command line is visible in `ps`. Keep the link to `docs/notes/auth.md`.

- [ ] **Step 4: Transcribe «Сеть песочниц» unchanged**

A3 (`docs/ONBOARDING.md:103-122`) is machine setup and is already release-neutral. Transcribe whole, including `sbx policy rm network --id default-allow-all`, the `sbx policy check network example.com --json` verification returning `"allowed": false`, and how to put it back.

- [ ] **Step 5: Rewrite «Хозяйство раннера» around `runner init`**

A4 (`docs/ONBOARDING.md:123-138`) tells the reader to `mkdir -p`. That is now wrong in the sense that matters — `runner init` does it and more:

```markdown
## Хозяйство раннера

```sh
runner init
```

- [ ] Каталог есть, в нём два образца конфигурации и `scheduler/` с тремя
      заданиями планировщика.

Проверка: `ls "${OFFICE_HOME:-$HOME/.office}"` показывает
`projects.local.example.yaml`, `tracker.example.yaml` и `scheduler/`. Команда
идемпотентна: уже лежащий файл она не трогает никогда, ни образец, ни правленое
задание, — и говорит об этом словом «оставлен».

`OFFICE_HOME` стоит задать явно и там же, где кред: иначе она разойдётся между
вашей сессией и планировщиком, и хозяйств станет два. Что именно там лежит и
что чем перекрывается — [«Конфигурация»](../reference/configuration.md).
```

- [ ] **Step 6: Rewrite «Проверка машины» without `go test`**

A5 (`docs/ONBOARDING.md:139-160`) opens with `go test ./...`, which a release reader cannot run. Replace the three checks with:

```markdown
## Проверка машины

- [ ] `runner version` называет личность раннера и каталог его офиса.
- [ ] `runner ledger` отвечает «прогонов нет» и называет путь к реестру: раннер
      видит своё хозяйство.
- [ ] На бэкенде `sbx` (по умолчанию) — `sbx template ls` показывает
      `office-claude-comet:<версия>`. Без него роли Comet Native (`analyst`,
      `implementer`, `reviewer`) откажут на первом же прогоне кодом
      `403 Forbidden: pull failed for image` — на глаз неотличимо от сетевого
      отказа политики, а причина другая: образ с запечённым `comet` не испечён
      на этой машине. Печётся один раз, скриптом из распакованного офиса:
      `"${OFFICE_HOME:-$HOME/.office}"/office/<версия>/sbx-kits/bake-comet-template.sh`.
      Каталог версии появляется после первой команды, которая открывает офис
      целиком, — например `runner ls`. На бэкенде `local` шаг не нужен.

Без файла проектов работают `ledger`, `worktree ls` и `mock ls` — им нужно
только хозяйство. Остальные — `tick`, `loop`, `reap`, `ls` — откажутся и назовут
файл, которого не хватает, и ключи, которых в нём ждут; код возврата такого
отказа `2`, «запускать было нечем». Это правильное поведение, а не поломка.
```

- [ ] **Step 7: Delete Дорожка А from `docs/ONBOARDING.md`**

Delete `docs/ONBOARDING.md` lines 37-160 (the `# Дорожка А. Новая машина` heading through the end of A5). Leave the rest for Task 11.

- [ ] **Step 8: Verify the no-clone property of this document**

Run: `grep -n 'go test\|go build\|go version\|\./bin/\|клон' docs/guide/machine-setup.md`

Expected: the only hits are the one sentence saying Go is needed solely to build from source, pointing at `development.md`. Anything else is a step the target reader cannot perform.

- [ ] **Step 9: Commit**

```bash
git add docs/guide/machine-setup.md docs/ONBOARDING.md
git commit -m "$(cat <<'MSG'
docs(guide): preparing a machine, for a reader without a clone

ONBOARDING A1–A5 transcribed and corrected for someone who installed from a
release: Go and uv leave the tool list, mkdir -p becomes runner init, and the
machine check drops go test for runner version and runner ledger. The sbx kit
is named at its unpacked path rather than at a path inside a clone.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 10: `docs/guide/project-setup.md` — putting the office on a project

Covers tasks.md 4.4.

**Files:**
- Create: `docs/guide/project-setup.md`
- Modify: `docs/ONBOARDING.md` — delete the Б4 and Б7 material once transcribed

**Interfaces:**
- Consumes: from Task 4, `docs/reference/configuration.md`; from Task 5, `docs/reference/jira-requirements.md`; from Task 9, `docs/guide/machine-setup.md` as the prerequisite.
- Produces: the destination that `docs/ONBOARDING.md`'s spine (Task 11) links to for the repository and the four checks, and one row of `docs/README.md` (Task 12).

**Source material.** `docs/ONBOARDING.md` Б4 (335-400, the client repository) and Б7 (482-584, the four checks by rising cost). The ladder of four checks is good and survives intact — the same four, in the same order, for the same reasons. Only the commands change from `./bin/runner` to `runner`.

- [ ] **Step 1: Create the file with this skeleton**

```markdown
# Завести проект

Это делается на каждый проект. Машина к этому моменту подготовлена
([«Подготовка машины»](machine-setup.md)), а трекер отвечает требованиям из
[«Что офис требует от вашей JIRA»](../reference/jira-requirements.md) — или
проект ведётся на файловом трекере `mock` и трекера не нужно вовсе.

## Репозиторий проекта-клиента

## Запись о проекте

## Проверка: четыре по возрастанию цены
```

- [ ] **Step 2: Transcribe «Репозиторий проекта-клиента» from Б4**

`docs/ONBOARDING.md:335-400`, transcribed whole. Three requirements, each with its check:
1. the default branch matches `default_branch`, checked with `git -C … symbolic-ref --short HEAD`;
2. `.gitignore` is **in the repository**, with all three checks and the explanation of why the last one alone is not enough (it reads the working tree and knows nothing about the commit or the push, and the runner clones the repository, not your clone);
3. the forge token's scope over this repository, with both traps — `404` rather than `403` for a token with no scope at all, and the fact that the right to open a pull request is not visible in that answer.

Keep the bare-repo recipe verbatim (lines 344-355) — including the `rm -rf` first line that makes the recipe repeatable — and the paragraph explaining why `git add` is written out rather than `commit -am … --allow-empty`. Keep the closing note that a project without a forge is a legitimate and ordinary first case: the PR pass degenerates and takes the task to `Done` with a `pr-skipped` record.

- [ ] **Step 3: Write «Запись о проекте» as a pointer, not a copy**

The project entry itself is reference material and lives in `docs/reference/configuration.md` (Task 4) and in the shipped example. This section is three sentences and a link:

```markdown
## Запись о проекте

Проекты — свойство инстанса: в репозитории офиса их нет и не будет. Каждый
описывается одной записью в `${OFFICE_HOME}/projects.local.yaml`, которую
делают копией образца:

```sh
cp "${OFFICE_HOME:-$HOME/.office}/projects.local.example.yaml" \
   "${OFFICE_HOME:-$HOME/.office}/projects.local.yaml"
```

Править в копии нужно ровно четыре значения: ключ проекта, `repo_url`,
`default_branch` и `tracker`. Остальные ключи в образце закомментированы, у
каждого своя строка-подсказка. Слои, перекрытия и переменные окружения —
[«Конфигурация»](../reference/configuration.md); подключение к JIRA — там же
и в `${OFFICE_HOME}/tracker.example.yaml`.
```

- [ ] **Step 4: Transcribe the four checks from Б7, intact**

`docs/ONBOARDING.md:482-584`, in the same order and with the same reasoning. Only these substitutions:
- `./bin/runner` → `runner`, `./bin/run-agent` → `run-agent` throughout;
- the reference to «Б5» in the field-identifier trap becomes «[«Что офис требует от вашей JIRA»](../reference/jira-requirements.md), раздел «Четыре поля аренды»»;
- the reference to «Б2» for the trial task becomes «пробная задача из [«Что офис требует от вашей JIRA»](../reference/jira-requirements.md)»;
- the reference to «Б4» for the forge-less project becomes «выше, «Репозиторий проекта-клиента»»;
- the reference to «Б5» for `auto_merge.enabled` becomes «[«Конфигурация»](../reference/configuration.md)».

Keep, word for word, the things that are easy to lose:
- that the first three checks cost nothing and change nothing in the tracker, and that the first and third still *read* it;
- the sample config-layout output block;
- that a truncated layout list shows where the runner stopped, because each line is printed as soon as its path resolves;
- the three `--dry-run` invocations and why `reviewer` needs `--base`;
- the fourth check's fork: with a forge, with `auto_merge.enabled`, and without a forge.

- [ ] **Step 5: Delete Б4 and Б7 from `docs/ONBOARDING.md`**

Delete the `## Б4. Репозиторий проекта-клиента` section and the `## Б7. Проверка: четыре по возрастанию цены` section. Leave Б5, Б6 and the two tail sections for Task 11.

- [ ] **Step 6: Verify the no-clone property**

Run: `grep -n './bin/\|go test\|go build' docs/guide/project-setup.md`

Expected: no hits.

- [ ] **Step 7: Commit**

```bash
git add docs/guide/project-setup.md docs/ONBOARDING.md
git commit -m "$(cat <<'MSG'
docs(guide): putting the office on a project

ONBOARDING Б4 and Б7 transcribed for a release install: the client repository
with its three requirements and their checks, and the ladder of four checks by
rising cost, intact and in the same order. Commands lose the ./bin/ prefix; the
back-references to Б-numbers become links to the documents that now own that
material.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 11: `docs/ONBOARDING.md` becomes a spine

Covers tasks.md 4.5, 4.6.

**Files:**
- Modify: `docs/ONBOARDING.md` (rewritten whole; what remains after Tasks 6, 9 and 10 is Б3's residue, Б5, Б6 and the two tail sections)
- Modify: `docs/guide/operations.md` (receives two symptoms from ONBOARDING's tail)

**Interfaces:**
- Consumes: `docs/guide/machine-setup.md` (Task 9), `docs/guide/project-setup.md` (Task 10), `docs/reference/configuration.md` (Task 4), `docs/reference/jira-requirements.md` (Task 5), `bootstrap/jira/README.md` (Task 6).
- Produces: the ordered checklist that `README.md` (Task 13), `bootstrap/README.md`, `bootstrap/jira/README.md` and `docs/guide/quickstart.md` all keep pointing at. **The path does not change** — it is the most likely externally linked document after README.

- [ ] **Step 1: Disperse what is left**

Three pieces remain in the file and must land somewhere before the rewrite:

| Remaining material | Lines (original numbering) | Destination |
|---|---|---|
| Б5 «Конфигурация: инстанс в `${OFFICE_HOME}`» | 401-465 | already covered by `docs/reference/configuration.md` (Task 4) and `docs/guide/project-setup.md` «Запись о проекте» (Task 10); delete, do not copy |
| Б6 «Бюджеты» | 466-481 | already covered by `docs/notes/budgets.md` («Что это значит для пределов») and `docs/guide/operations.md` («Расход»); delete, do not copy |
| «Что остаётся руками» | 585-600 | the three non-polygon rows went to `docs/reference/jira-requirements.md` (Task 5); the Docker-memory row is already in `bootstrap/jira/README.md`; delete |

And the tail, «Когда что-то пошло не так» (601-635), four symptoms:

| Symptom | Destination |
|---|---|
| «Контейнер `running`, а порт молчит» | already in `bootstrap/jira/README.md`, «Три вещи, о которых лучше знать заранее»; delete |
| «Задача пропала с доски» | `docs/reference/jira-requirements.md`, «Доски» (Task 5 wrote that section; add the symptom sentence there if it is not already covered) |
| «`connection reset by peer` на `localhost:2990`» | already in `bootstrap/jira/README.md`; delete |
| «В метке прогона `config:…-dirty`» and «Раннер отказался стартовать» | **move into `docs/guide/operations.md`**, section «Когда что-то пошло не так», as two new numbered entries after the existing five — that section is the office's troubleshooting index and these two are not JIRA-specific |

- [ ] **Step 2: Add the two symptoms to `docs/guide/operations.md`**

In `docs/guide/operations.md`, section «Когда что-то пошло не так», after the existing numbered list (which ends at item 5, «Тикет»), insert:

```markdown
6. **В метке прогона `config:…-dirty`.** Рабочее дерево конфиг-репозитория
   не чисто: незакоммиченная правка или неотслеживаемый файл. Заводя инстанс,
   править репозиторий не нужно — всё своё лежит в `${OFFICE_HOME}`, — так что
   метка означает именно вашу правку под себя или забытый файл. `git status`
   в корне конфигурации покажет, что именно.
7. **Раннер отказался стартовать.** Это штатный способ рассказать
   о конфигурации: он называет проект, ключ и файл, куда ключ переехал. Читайте
   отказ целиком — там сказано и что не так, и где чинить. Какие отказы бывают
   и почему их пять — `docs/notes/stage-5-config.md`, раздел «Пять отказов
   вместо доверия».
```

- [ ] **Step 3: Rewrite `docs/ONBOARDING.md` whole**

Replace the entire file with this. Each step is one sentence and a link to the document that owns it; nothing here repeats what it links to.

```markdown
# Внедрение: от чистой машины до первой задачи

Этот документ — порядок, а не содержание. Каждый шаг — одна строка и ссылка
на документ, который им владеет; проходят их сверху вниз и отмечают, когда
сработала проверка в том документе, а не когда выполнена команда.

Дорожек две, и они разной частоты: **машина** проходится раз на машину,
**проект** — на каждый проект. Ставите офис впервые — обе подряд; заводите
второй проект на настроенной машине — только вторую.

Хотите сперва посмотреть, как это работает, не заводя ничего, —
[«Быстрый старт»](guide/quickstart.md): одна задача через три роли на файловом
трекере, без JIRA.

## Машина

- [ ] 1. Поставить офис — [«Быстрый старт», «Установка»](guide/quickstart.md#установка).
- [ ] 2. Инструменты, креды и сеть песочниц —
      [«Подготовка машины»](guide/machine-setup.md), разделы «Инструменты»,
      «Креды», «Сеть песочниц».
- [ ] 3. Хозяйство раннера: `runner init` —
      [«Подготовка машины», «Хозяйство раннера»](guide/machine-setup.md#хозяйство-раннера).
- [ ] 4. Проверить машину —
      [«Подготовка машины», «Проверка машины»](guide/machine-setup.md#проверка-машины).

## Проект

- [ ] 5. Инстанс трекера отвечает требованиям офиса —
      [«Что офис требует от вашей JIRA»](reference/jira-requirements.md).
      Своего инстанса нет — поднимается локальный:
      [`bootstrap/jira/README.md`](../bootstrap/jira/README.md). Проект на
      файловом трекере `mock` этого шага не требует вовсе.
- [ ] 6. Учётки: минимум две, офисная и человеческая —
      [«Что офис требует от вашей JIRA», «Учётки»](reference/jira-requirements.md#учётки).
- [ ] 7. Репозиторий проекта-клиента: ветка по умолчанию, `.gitignore`, охват
      токена — [«Завести проект», «Репозиторий
      проекта-клиента»](guide/project-setup.md#репозиторий-проекта-клиента).
- [ ] 8. Запись о проекте и подключение к трекеру —
      [«Завести проект», «Запись о проекте»](guide/project-setup.md#запись-о-проекте);
      слои и переменные — [«Конфигурация»](reference/configuration.md).
- [ ] 9. Бюджеты: на первом заходе `warn`, а лучше вовсе без файла — учёт
      ведётся всегда. Почему так — `docs/notes/budgets.md`, раздел «Что это
      значит для пределов»; где перекрывается —
      [«Эксплуатация», «Расход»](guide/operations.md#расход-runner-ledger-budgetsyaml).
- [ ] 10. Четыре проверки по возрастанию цены — [«Завести проект»,
      «Проверка»](guide/project-setup.md#проверка-четыре-по-возрастанию-цены).
      Последняя из них и есть первая настоящая задача, проведённая офисом.

## Дальше

Запустить по расписанию — [«Эксплуатация»,
«По расписанию»](guide/operations.md#по-расписанию). Что делает каждая роль
и как задача идёт по графу — [«Роли и путь задачи»](guide/roles-and-flow.md).
Что-то пошло не так — [«Эксплуатация», «Когда что-то пошло не
так»](guide/operations.md#когда-что-то-пошло-не-так).
```

- [ ] **Step 4: Check the spine's own claim — no step needs a clone, Go, or reading source**

This is tasks.md 4.6. Walk the chain from README through every document a step links to and confirm each instruction can be carried out with `runner`/`run-agent` from a release install:

```bash
grep -n 'go test\|go build\|go run\|go version\|\./bin/\|git clone.*virtual-office\|cd .*virtual-office' \
  README.md docs/ONBOARDING.md docs/guide/quickstart.md docs/guide/machine-setup.md \
  docs/guide/project-setup.md docs/guide/operations.md docs/reference/*.md
```

Expected hits, and only these:
- `README.md` and `docs/guide/quickstart.md` — the `sh scripts/release-snapshot.sh` snapshot path, which is correct until the first release tag exists;
- `docs/guide/machine-setup.md` — the single sentence saying Go is needed only to build from source;
- `docs/guide/operations.md` — the paragraph explaining why the scheduler must not point at the `./bin/runner` wrapper, and the «Ручной запуск агента» section (which is about `run-agent`, installed by `install.sh`).

Anything else is a step the target reader cannot perform: fix it in place.

- [ ] **Step 5: Commit**

```bash
git add docs/ONBOARDING.md docs/guide/operations.md
git commit -m "$(cat <<'MSG'
docs: ONBOARDING is a spine, not a container

Its job narrows to one: the ordered checklist for putting the office on a real
project, each step a sentence and a link to the document that owns it. 635
lines become ten steps. The path does not change — README, bootstrap/README.md,
bootstrap/jira/README.md and quickstart.md all point here, and it is the most
likely externally linked document after README.

The two office-wide symptoms from its tail (config:…-dirty and the startup
refusal) join the troubleshooting index in operations.md, where the other five
already live.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 12: `docs/README.md` — the index the directory has never had

Covers tasks.md 5.1.

**Files:**
- Create: `docs/README.md`

**Interfaces:**
- Consumes: every document created or moved by Tasks 4, 5, 9, 10, 11, 14.
- Produces: the index `README.md` points at (Task 13).

**Note on ordering.** `docs/notes/stages/` (Task 14) does not exist yet. Write the index now describing the layout as it will be after Task 14, and Task 16's link walk will catch it if Task 14 is skipped. If you are executing tasks strictly in order, run Task 14 before Task 16 regardless.

- [ ] **Step 1: Write the file**

Create `docs/README.md`:

```markdown
# Документация офиса

У каждого документа одна работа. Четыре работы, и они не смешиваются: **учебник**
проводит по одному пути, который работает; **инструкция** решает одну задачу
того, кто уже знает, чего хочет; **справочник** отвечает на вопрос «как оно
устроено на самом деле»; **объяснение** говорит, почему так, а не иначе.

## Учебник

| Документ | О чём |
|---|---|
| [guide/quickstart.md](guide/quickstart.md) | от установки до первой задачи, проведённой тремя ролями на файловом трекере |

## Инструкции

| Документ | О чём |
|---|---|
| [ONBOARDING.md](ONBOARDING.md) | порядок внедрения: десять шагов, каждый — ссылка на документ, который им владеет |
| [guide/machine-setup.md](guide/machine-setup.md) | подготовка машины: инструменты, креды, сеть песочниц, хозяйство раннера |
| [guide/project-setup.md](guide/project-setup.md) | завести проект: репозиторий клиента, запись о проекте, четыре проверки по возрастанию цены |
| [guide/operations.md](guide/operations.md) | эксплуатация: расписание, доска, расход, ручной запуск роли, хозяйство `${OFFICE_HOME}`, разбор бед |
| [guide/development.md](guide/development.md) | для того, кто меняет сам офис: проверки, golden-кейсы, релиз, карта репозитория |
| [../bootstrap/jira/README.md](../bootstrap/jira/README.md) | локальная JIRA в контейнере |
| [../bootstrap/README.md](../bootstrap/README.md) | обвязка машины: что launchd и systemd нужно от раннера |

## Справочники

| Документ | О чём |
|---|---|
| [reference/configuration.md](reference/configuration.md) | две половины конфигурации, три слоя правил и что что перекрывает, переменные окружения |
| [reference/jira-requirements.md](reference/jira-requirements.md) | что офис требует от инстанса JIRA — каждое требование со способом проверить |
| [contracts/tracker-protocol.md](contracts/tracker-protocol.md) | протокол трекера: аренда, комментарии, маркеры событий |
| [contracts/agent-io.md](contracts/agent-io.md) | контракт «раннер ↔ агент»: что раннер кладёт в рабочую папку и чего ждёт обратно |
| [contracts/role-sandbox-permissions.md](contracts/role-sandbox-permissions.md) | слоистые разрешения песочницы: сеть и инструменты роли |

Ключи конфигурации описаны не здесь, а в самих образцах —
`office/projects.local.example.yaml` и `office/tracker.example.yaml`: они едут
внутри бинарника и с релизом разойтись не могут.

## Объяснения

| Документ | О чём |
|---|---|
| [DESIGN.md](DESIGN.md) | архитектурные решения и их причины — источник истины по устройству |
| [guide/roles-and-flow.md](guide/roles-and-flow.md) | что делает каждая роль, как задача идёт по графу, как считаются круги возврата |

## Вне схемы

`notes/` — датированный лабораторный журнал: замеры, разборы прогонов,
ретроспективы этапов и замороженные планы этапов в `notes/stages/`. Это не
документация и под четыре работы не попадает: записи не обновляются задним
числом, и читать их надо как записи, а не как инструкции.

`openspec/`, `superpowers/` и `comet/` — рабочие каталоги процесса изменений:
предложения, спецификации, планы и отчёты о проверке. Тоже вне схемы.
```

- [ ] **Step 2: Check every link in the index resolves**

Run:

```bash
grep -o '](\([^)#]*\)' docs/README.md | sed 's/^](//' | while read -r l; do
  [ -e "docs/$l" ] || echo "BROKEN: $l"
done
```

Expected: no output. (`notes/stages/` is named in prose, not as a link, so Task 14's ordering cannot break this check.)

- [ ] **Step 3: Commit**

```bash
git add docs/README.md
git commit -m "$(cat <<'MSG'
docs: an index for docs/, with the job each document does

The directory has never had one, and the role was played by a section of
README, which works only for a reader who arrives through README. Every
document is listed under the one job it does — tutorial, how-to, reference,
explanation — and notes/ is named as a dated journal that is outside the
scheme rather than left to look like neglected documentation.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 13: `README.md` — platforms in the install section, a named channel for problems

Covers tasks.md 5.2.

**Files:**
- Modify: `README.md:29-44` (Установка), `:99-108` (Требования), `:122-134` (Документация), `:136-140` (Статус и лицензия)

**Interfaces:**
- Consumes: `docs/README.md` from Task 12.
- Produces: nothing later tasks depend on beyond Task 16's link walk.

**Source of the three items.** `docs/notes/readme-conventions.md`, the twenty-two-point checklist measured against twelve comparable projects, items 9, 11 and 14. **That file is not on this branch** — it lives on the unmerged branch `repo-hygiene` (commit `393bb60`). You do not need it: the three items are reproduced here in full, and that is all this task uses.

> **11. Платформы названы там же, где установка.** Нет: список `darwin/arm64`, `linux/amd64`, `linux/arm64` стоит в разделе «Требования», двумя разделами ниже установки; полная матрица — в `docs/notes/install.md`. У k9s, lazygit и glow платформы перечислены внутри раздела установки.
>
> **9. В `docs/` есть индекс.** Нет: `docs/README.md` отсутствует… Роль индекса сейчас играет раздел README, что работает только при заходе через README.
>
> **14. У читателя есть названный канал для сообщения о проблеме.** Нет: `has_issues=false` (Issues выключены коммитом f1b2b48 «drop Issues (unused)»), `has_discussions=false`, чата нет. Единственный названный канал — приватный security-репорт в `SECURITY.md`. У всех двенадцати эталонов есть хотя бы один публичный канал. Это решение владельца, но в публичном README его стоит назвать явно, иначе отсутствие канала читается как недосмотр.

- [ ] **Step 1: Move the platform list into «Установка»**

`README.md:108` currently reads

```markdown
Платформы релиза: `darwin/arm64`, `linux/amd64`, `linux/arm64`.
```

Delete that line from «Требования» and insert it into «Установка», immediately after the code block (i.e. after line 38), as:

```markdown
Платформы релиза: `darwin/arm64`, `linux/amd64`, `linux/arm64`. Полная матрица —
[docs/notes/install.md](docs/notes/install.md).
```

Then shorten the existing paragraph at `README.md:40-44` so it does not repeat the matrix pointer:

```markdown
`install.sh` кладёт `runner` и `run-agent` в `${OFFICE_HOME:-~/.office}/bin`
и говорит, лежит ли этот путь в `PATH`. С первым тегом релиза установка станет
одной командой из релиза (`curl -fsSL .../install.sh | sh`), без клона и без Go.
```

- [ ] **Step 2: Point «Документация» at the index**

`README.md:122-134` currently lists eight bullets. Replace the whole section with

```markdown
## Документация

- [docs/README.md](docs/README.md) — **указатель**: каждый документ и работа,
  которую он делает;
- [docs/guide/quickstart.md](docs/guide/quickstart.md) — от установки до первой
  задачи, проведённой тремя ролями;
- [docs/ONBOARDING.md](docs/ONBOARDING.md) — порядок внедрения на боевой проект,
  десять шагов со ссылками;
- [docs/DESIGN.md](docs/DESIGN.md) — архитектурные решения и их причины.
```

The full list now lives in the index; keeping a second copy here is the duplication this change exists to remove.

- [ ] **Step 3: Name the channel for problem reports**

Replace `README.md:136-140`, currently

```markdown
## Статус и лицензия

Пятый этап сдан и проверен дважды на живом проекте — подробности и то, что
не доделано, — [docs/notes/stage-5-retro.md](docs/notes/stage-5-retro.md).
Лицензия — [MIT](LICENSE). Как сообщить об уязвимости — [SECURITY.md](SECURITY.md).
```

with

```markdown
## Статус, обратная связь и лицензия

Пятый этап сдан и проверен дважды на живом проекте — подробности и то, что
не доделано, — [docs/notes/stage-5-retro.md](docs/notes/stage-5-retro.md).

**Issues выключены намеренно, и это не недосмотр.** Сопровождающий один,
публичной очереди он не потянет, и пустая очередь хуже отсутствующей. Канал
один и он приватный: об уязвимости — по [SECURITY.md](SECURITY.md), там же
сказано про область и про то, что SLA нет. Всё остальное — форк и своя ветка:
репозиторий открыт, лицензия это позволяет.

Лицензия — [MIT](LICENSE).
```

- [ ] **Step 4: Check the «Требования» table still makes sense without the platform line**

Run: `sed -n '95,110p' README.md`

Confirm the table survives and reads correctly, and that `Go 1.26+ | только для сборки из исходников` is still there — it is true and belongs in Требования, not in Установка.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "$(cat <<'MSG'
docs: platforms in the install section, a named channel for problems

Three items from the measured README checklist: the platform list moves from
Требования into Установка where the reader is standing when they need it, the
documentation section leads with the new docs/README.md index instead of
duplicating it, and the absence of Issues is stated as the decision it is.
Silence currently read as an oversight.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 14: Frozen stage plans join their retrospectives

Covers tasks.md 6.1, 6.2.

**Files:**
- Move: `docs/STAGE-1-agent-runtime.md`, `docs/STAGE-2-runner-tracker.md`, `docs/STAGE-3-reviewer-budgets.md`, `docs/STAGE-4-analyst-questions.md`, `docs/STAGE-5-first-live.md` → `docs/notes/stages/` (filenames unchanged)
- Modify: `docs/notes/stage-5-config.md:163`, `docs/notes/analyst-task-splitting.md:602`, `docs/notes/aidlc-workflows-comparison.md:5,103,491-495`

**Interfaces:**
- Consumes: nothing.
- Produces: the `docs/notes/stages/` directory named by `docs/README.md` (Task 12).

- [ ] **Step 1: Move the five files**

```bash
mkdir -p docs/notes/stages
git mv docs/STAGE-1-agent-runtime.md      docs/notes/stages/
git mv docs/STAGE-2-runner-tracker.md     docs/notes/stages/
git mv docs/STAGE-3-reviewer-budgets.md   docs/notes/stages/
git mv docs/STAGE-4-analyst-questions.md  docs/notes/stages/
git mv docs/STAGE-5-first-live.md         docs/notes/stages/
git status --short
```

Expected: five `R` entries. `docs/` now holds `DESIGN.md`, `ONBOARDING.md`, `README.md` and the five subdirectories.

- [ ] **Step 2: Find every reference**

Run:

```bash
grep -rn 'docs/STAGE-' --include='*.md' . | grep -v '^./docs/comet/archive/'
```

Expected hits, all in `docs/notes/`:

```
docs/notes/stage-5-config.md:163
docs/notes/analyst-task-splitting.md:602
docs/notes/aidlc-workflows-comparison.md:5
docs/notes/aidlc-workflows-comparison.md:103
docs/notes/aidlc-workflows-comparison.md:491
docs/notes/aidlc-workflows-comparison.md:492
docs/notes/aidlc-workflows-comparison.md:493
docs/notes/aidlc-workflows-comparison.md:494
docs/notes/aidlc-workflows-comparison.md:495
```

- [ ] **Step 3: Fix them**

Since all three files live in `docs/notes/`, the reference from inside that directory is `stages/STAGE-N-….md`. Apply:

```bash
sed -i '' 's|docs/STAGE-|stages/STAGE-|g' \
  docs/notes/stage-5-config.md \
  docs/notes/analyst-task-splitting.md \
  docs/notes/aidlc-workflows-comparison.md
```

(on Linux, `sed -i` without the empty argument). Then re-run the grep from Step 2: expected no hits outside `docs/comet/archive/`.

- [ ] **Step 4: Leave the Comet archive alone, and record why**

Run: `grep -rn 'docs/STAGE-' docs/comet/archive/`

Expected: four hits, in `2026-08-20-pr-pass/verification.md`, `2026-08-19-run-termination/brief.md`, `2026-08-21-config-per-machine/verification.md`, `2026-08-21-onboarding-doc/verification.md`. **Do not edit them.** Rewriting an archived verification report to keep a link alive falsifies the record of what was verified and when. This is stated in the proposal under «Known breakage, accepted»; tasks.md 6.2 asks for it to be recorded in the change's verification, which is the verify phase's job, not this plan's — leave a note in the commit message so the verifier can find it.

- [ ] **Step 5: Commit**

```bash
git add docs/notes/stages docs/notes/stage-5-config.md docs/notes/analyst-task-splitting.md docs/notes/aidlc-workflows-comparison.md
git commit -m "$(cat <<'MSG'
docs(notes): frozen stage plans join their retrospectives

1521 lines of implementation plans for Claude Code leave the docs/ root for
docs/notes/stages/, beside the stage-N-retro.md that judges each of them. A
frozen plan and its retrospective belong in the same journal, and this avoids a
new top-level docs/archive/ colliding in the reader's head with the archive/*
git tags CLAUDE.md gives a specific meaning.

References from docs/notes/ are fixed. The four references from
docs/comet/archive/ are deliberately not: editing an archived verification
report to keep a link alive falsifies the record of what was verified and when.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 15: `scripts/doc-recipe-test.sh` — the documented recipe becomes executable

Covers tasks.md 7.1, 7.2.

**Files:**
- Create: `scripts/doc-recipe-test.sh` (mode `0755`)
- Modify: `docs/guide/development.md:8-17` (the «Проверки» block)

**Interfaces:**
- Consumes: from Task 2, that `runner init` creates five files and `scheduler/`; from Task 7 and Task 13, the recipe the documentation actually prints.
- Produces: `sh scripts/doc-recipe-test.sh`, exit 0 on success, exit 1 with `FAIL: …` on the first failing step. Task 16 runs it as part of the closing check.

**How the fake agent was determined, and how you can re-verify it.** The harness's fake `claude` writes one file and exits zero. Three facts make that sufficient, each read out of the tree rather than assumed:

1. **Where the file goes.** `internal/runner/agentio.go:20,32` define `Dir = ".agent"` and `FileResult = "result.json"`; `internal/runagent/runagent.go:207` calls `runner.ReadResult(opts.Workdir)`, which is `filepath.Join(workdir, Dir, FileResult)` (`agentio.go:199-201`). The role's own `result_file: .agent/result.json` (`office/roles/analyst/role.yaml`, last line — and the same in `implementer` and `reviewer`) agrees. `internal/backends/local/local.go:36` sets `cmd.Dir = l.Workdir`, so from inside the fake the path is simply `./.agent/result.json`. The existing Go fake does exactly this: `cmd/eval-roles/testdata/fakeagent/main.go:83-91` writes `filepath.Join(*workdir, ".agent", "result.json")`.
2. **What must be in it.** `runner.Result` (`internal/runner/agentio.go:124-135`) and `Result.Validate()` (`:233-…`) require `outcome` ∈ {done, needs_human, blocked, failed, split}, a non-empty `summary`, and a non-empty `next_owner`; `questions` is required for `needs_human`/`split` and *forbidden* otherwise, `blocker` is required for `blocked` and forbidden otherwise, `split` only for `split`. So `{"outcome":"done","summary":…,"next_owner":"implementer"}` is the minimal valid document.
3. **Why `next_owner` must be `implementer` and not `human`.** `office/workflow.yaml:51-54`: analyst's `done` carries a `by_next_owner` map, and `office/workflow.yaml:48-50` states in so many words that a value absent from the map hits the «маршрута нет» rule and goes to the human. `implementer: Ready` is in the map, so the task lands in `Ready`, which is what the harness asserts.
4. **Why an empty run log is not a failure.** `internal/runagent/runagent.go:391-402`, `fromLog`, returns the zero value when the log cannot be read or parsed: usage and ending come back unknown, and `terminationOf` (`:333-336`) short-circuits on `hasResult` before any of that matters.

Two more things the harness must supply, which are easy to miss:
- **A credential must be in the environment**, even though no real agent runs: `internal/adapters/claude/adapter.go:85` calls `credential()` (`:73`), which refuses the launch outright when neither `ANTHROPIC_API_KEY` nor `CLAUDE_CODE_OAUTH_TOKEN` is set. A fake value is fine.
- **`OFFICE_CONFIG_ROOT` must point at the clone.** In payload mode the runner takes the `validate-result` guard from `office/validators/`, which is empty unless the binary was built with `-tags release` (`office/validators/validators_dev.go`, and `EnsureValidator` at `internal/runner/validator.go:55-80`). In clone mode it builds the guard with `go build`. `runner init` is unaffected either way — it reads the embedded payload in every mode, as its own comment states.

- [ ] **Step 1: Read the model before writing**

Run: `cat scripts/install-test.sh`

Match its conventions exactly: `#!/bin/sh` (POSIX sh, not bash), a comment header that says what it proves and how to invoke it, `set -eu`, `root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"`, `work="$(mktemp -d)"` with `trap 'rm -rf "$work"' EXIT`, a one-line `fail()` helper, numbered `# N. …` section comments, `echo "ok: …"` after each passing step, and one summary line at the end.

- [ ] **Step 2: Write `scripts/doc-recipe-test.sh`**

```sh
#!/bin/sh
# Проверка документированного рецепта: пройти путь, который обещают README
# и docs/guide/quickstart.md, в одноразовом ${OFFICE_HOME} — без сети и без
# платного прогона. Ловится то, что уже однажды уехало в релиз: рецепт,
# умирающий на «projects.local.yaml не заведён», потому что `runner init`
# кладёт только образцы.
#
#   sh scripts/doc-recipe-test.sh
#
# Агент подменён: на PATH кладётся поддельный `claude`, который пишет
# .agent/result.json и выходит нулём. Этого довольно — результат раннер читает
# из рабочей папки (internal/runner/agentio.go, ReadResult), а цену и причину
# конца прогона берёт из лога разбором адаптера, и нечитаемый лог означает
# «неизвестно», а не отказ (internal/runagent/runagent.go, fromLog).
set -eu

root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

home="$work/home"
export OFFICE_HOME="$home"
# Офис — из клона: ограждение validate-result в этом режиме собирается
# go build'ом, а сборка без -tags release ограждений не несёт вовсе
# и в режиме поставки отказала бы (internal/runner/validator.go,
# EnsureValidator). На сам `runner init` режим не влияет: образцы он берёт
# из поставки в бинарнике в любом.
export OFFICE_CONFIG_ROOT="$root"
# Кред поддельный: настоящего агента здесь нет, но без переменной адаптер
# отказывается собирать запуск (internal/adapters/claude/adapter.go, credential).
export ANTHROPIC_API_KEY=поддельный-кред
unset CLAUDE_CODE_OAUTH_TOKEN || :

runner="$work/bin/runner"
( cd "$root" && go build -o "$runner" ./cmd/runner ) || fail "раннер не собрался"

# 1. Хозяйство: пять файлов и каталог заданий, код 0.
"$runner" init > "$work/out-init" 2>&1 || fail "init: $(cat "$work/out-init")"
for f in projects.local.example.yaml \
         tracker.example.yaml \
         scheduler/local.office.runner.plist \
         scheduler/office-runner.service \
         scheduler/office-runner.timer; do
  [ -f "$home/$f" ] || fail "init не положил $f"
done
[ -d "$home/scheduler" ] || fail "init не завёл scheduler/"
grep -q 'projects.local.yaml' "$work/out-init" || fail "init не сказал, куда копировать образец проектов"
grep -q 'tracker.yaml' "$work/out-init" || fail "init не сказал, куда копировать образец трекера"
echo "ok: runner init"

# 2. Репозиторий проекта-клиента — тот же рецепт, что в README: пустого мало,
# ветку задачи раннер ответвляет от origin/<default_branch>.
client="$work/client.git"
git init -q --bare -b master "$client"
git clone -q "$client" "$work/client"
git -C "$work/client" -c user.name=you -c user.email=you@local commit -q --allow-empty -m init
git -C "$work/client" push -q origin master
echo "ok: репозиторий клиента"

# 3. Образец под рабочим именем и ровно четыре правки — это и есть проверяемое
# утверждение: образец обещает, что больше править нечего.
example="$home/projects.local.example.yaml"
cfg="$home/projects.local.yaml"
cp "$example" "$cfg"
sed -e 's|^PROJ:|OFF:|' \
    -e "s|^  repo_url: .*|  repo_url: $client|" \
    -e 's|^  default_branch: .*|  default_branch: master|' \
    -e 's|^  tracker: .*|  tracker: mock|' \
    "$cfg" > "$cfg.new"
mv "$cfg.new" "$cfg"
edits="$(diff "$example" "$cfg" | grep -c '^<' || :)"
[ "$edits" = 4 ] || fail "правок в projects.local.yaml $edits, документация обещает четыре"
echo "ok: образец проектов правится четырьмя значениями"

# 4. Задача заведена и видна: раскладка конфигурации называет рабочий файл,
# а не образец, и доска показывает задачу.
"$runner" mock add OFF-1 --status Analysis \
  --summary "Добавить hello.py" \
  --description "Создай hello.py, печатающий приветствие, и тест к нему. Закоммить." \
  > "$work/out-add" 2>&1 || fail "mock add: $(cat "$work/out-add")"
"$runner" ls > "$work/out-ls" 2>&1 || fail "ls: $(cat "$work/out-ls")"
grep -qF "$cfg" "$work/out-ls" || fail "раскладка не назвала $cfg: $(cat "$work/out-ls")"
grep -q 'OFF-1' "$work/out-ls" || fail "доска не показала OFF-1: $(cat "$work/out-ls")"
echo "ok: задача заведена и видна на доске"

# 5. Один тик с поддельным агентом: задача уезжает из Analysis в Ready.
fakebin="$work/fakebin"
mkdir -p "$fakebin"
cat > "$fakebin/claude" <<'FAKE'
#!/bin/sh
# Поддельный агент. Раннер запускает его в рабочей папке задачи
# (internal/backends/local/local.go: cmd.Dir = l.Workdir), поэтому файл
# результата пишется относительно текущего каталога. Форма — runner.Result
# (internal/runner/agentio.go): обязательны outcome, summary и next_owner;
# questions только у needs_human и split, blocker только у blocked.
# next_owner именно implementer: у analyst'а карта by_next_owner названа явно,
# и значение вне карты уехало бы к человеку, а не в Ready (office/workflow.yaml).
cat > /dev/null                     # промпт приходит на stdin, читать его нечем
mkdir -p .agent
cat > .agent/result.json <<'JSON'
{
  "outcome": "done",
  "summary": "поддельный прогон: план записан",
  "next_owner": "implementer"
}
JSON
exit 0
FAKE
chmod 0755 "$fakebin/claude"

PATH="$fakebin:$PATH" "$runner" tick --backend local > "$work/out-tick" 2>&1 \
  || fail "tick: $(cat "$work/out-tick")"
"$runner" mock show OFF-1 > "$work/out-show" 2>&1 || fail "mock show: $(cat "$work/out-show")"
head -1 "$work/out-show" | grep -q 'Ready' \
  || fail "задача не сдвинулась в Ready: $(head -1 "$work/out-show")"
echo "ok: тик провёл задачу из Analysis в Ready"

echo "все пять шагов рецепта прошли"
```

- [ ] **Step 3: Make it executable and run it**

```bash
chmod 0755 scripts/doc-recipe-test.sh
sh scripts/doc-recipe-test.sh
```

Expected output:

```
ok: runner init
ok: репозиторий клиента
ok: образец проектов правится четырьмя значениями
ok: задача заведена и видна на доске
ok: тик провёл задачу из Analysis в Ready
все пять шагов рецепта прошли
```

- [ ] **Step 4: If step 5 does not pass, degrade deliberately — do not weaken the assertion**

The design's Risks section allows exactly one fallback and no other. If the fake `claude` turns out to need more than a result file, and one round of reading the failure in `"$work/out-tick"` does not settle it:

- delete the whole `# 5.` block,
- change the final line to `echo "все четыре шага рецепта прошли"`,
- add this comment where the block was:

```sh
# Шага «один тик» здесь нет намеренно: поддельному агенту оказалось мало файла
# результата (см. docs/superpowers/specs/2026-09-21-user-guide-design.md,
# Risks). Четыре шага выше накрывают тот дефект, что уехал в релиз прошлой
# веткой; настоящий прогон роли проверяет cmd/eval-roles.
```

Record in the commit message *what* the fake turned out to need. Do not loosen the assertion to «tick exited zero» — an assertion that passes when the recipe is broken is worse than a missing one.

- [ ] **Step 5: Break the recipe on purpose and watch the harness fail (tasks.md 7.2)**

A passing run means nothing until you have seen a failing one. Revert exactly one documented edit: in the `sed` of section 3, change

```sh
    -e 's|^  tracker: .*|  tracker: mock|' \
```

to

```sh
    -e 's|^  tracker: .*|  tracker: jira|' \
```

Run: `sh scripts/doc-recipe-test.sh`

Expected: the run stops at section 3 or 4 —

- at section 3 if the edit count no longer matches (`FAIL: правок в projects.local.yaml 3, документация обещает четыре`, because `tracker: jira` is already the sample's value and `diff` sees three changed lines), or
- at section 4 with `FAIL: ls: …`, because a project declaring `tracker: jira` makes the runner open `${OFFICE_HOME}/tracker.yaml`, which the recipe never created.

Either failure is the harness doing its job. **Restore the line to `mock` and re-run to green before continuing.**

- [ ] **Step 6: List the new check in `docs/guide/development.md`**

In the `## Проверки` code block (`docs/guide/development.md:9-17`), add after the `install-test.sh` line:

```sh
sh scripts/doc-recipe-test.sh               # документированный рецепт целиком, с поддельным агентом
```

and after the paragraph about `install-test.sh` (which ends at line 42), add:

```markdown
`doc-recipe-test.sh` проходит рецепт, который печатают README
и `docs/guide/quickstart.md`: одноразовое `${OFFICE_HOME}`, `runner init`, bare
репозиторий клиента, образец проектов, правленый ровно в четырёх обещанных
значениях, `runner mock add`, `runner ls` и один `runner tick --backend local`
с поддельным `claude` на `PATH`. Сети не трогает и подписки не тратит; нужны
только `go` и `git`. Ловит он ровно тот класс беды, что уехал в релиз прошлой
веткой: документированный путь, который обрывается на файле, которого `runner
init` не создаёт.
```

- [ ] **Step 7: Run the full triad plus the new harness**

```bash
gofmt -l .
go build ./...
go test ./...
sh scripts/doc-recipe-test.sh
```

Expected: all clean.

- [ ] **Step 8: Commit the script and the documentation separately**

```bash
git add scripts/doc-recipe-test.sh
git commit -m "$(cat <<'MSG'
test(scripts): the documented recipe becomes executable

Walks the path README and quickstart.md promise, in a scratch ${OFFICE_HOME},
with no network and no paid run: runner init and its five files, the bare
client repository, the projects sample edited in exactly the four documented
values, mock add, ls, and one tick --backend local against a fake claude on
PATH. The fake only writes .agent/result.json and exits zero — the runner reads
the result from the workdir and treats an unreadable run log as "unknown"
rather than as a failure.

A human walking the recipe once catches this once; this catches it every run.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
git add docs/guide/development.md
git commit -m "$(cat <<'MSG'
docs(guide): list doc-recipe-test.sh among the checks

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

---

### Task 16: Closing checks

Covers tasks.md 7.3, 7.4, 7.5.

**Files:**
- Modify: whatever the checks below find broken, in place
- Modify: `docs/openspec/changes/user-guide/tasks.md` (tick the 31 boxes)

**Interfaces:**
- Consumes: every artifact from Tasks 1-15.
- Produces: a tree that passes `gofmt`, `go build`, `go test` and `sh scripts/doc-recipe-test.sh`, with every relative link in the user path resolving.

- [ ] **Step 1: Walk every relative link in the user path (tasks.md 7.3)**

```bash
for f in README.md docs/README.md docs/ONBOARDING.md docs/guide/*.md docs/reference/*.md \
         bootstrap/README.md bootstrap/jira/README.md; do
  d="$(dirname "$f")"
  grep -o '](\([^)#]*\)' "$f" | sed 's/^](//' | while read -r l; do
    case "$l" in
      http*|mailto:*|'') continue ;;
    esac
    [ -e "$d/$l" ] || echo "BROKEN in $f: $l"
  done
done
```

Expected: no output. Fix every hit at its source — a broken link in the document that is supposed to be the reader's entry point is the failure this change exists to remove.

- [ ] **Step 2: Walk the bare-path references too**

Markdown links are not the only way these documents point at files; many say `` `docs/notes/jira-setup.md` `` in backticks. Check those:

```bash
grep -rhno '`[a-zA-Z0-9_./-]*\.\(md\|yaml\|sh\|go\|plist\|service\|timer\)`' \
  README.md docs/README.md docs/ONBOARDING.md docs/guide docs/reference \
  bootstrap/README.md bootstrap/jira/README.md \
  | sed 's/^[0-9]*://; s/`//g' | sort -u | while read -r p; do
    [ -e "$p" ] || echo "MISSING: $p"
  done
```

Expected: the only survivors should be paths that are deliberately relative to something else (e.g. `role.yaml`, `role.md`, `spec.yaml` named without a directory) or files inside `${OFFICE_HOME}` that do not exist in the repository (`projects.local.yaml`, `tracker.yaml`, `budgets.yaml`, `ledger.jsonl`, `runner.log`, `env`). Every other miss is a real break: fix it.

- [ ] **Step 3: Re-read the two references against the code (tasks.md 7.4)**

The harness cannot check prose. Read these with the code open beside them:

```bash
grep -rn 'OFFICE_[A-Z_]*' --include='*.go' . | grep -v '_test.go'
sed -n '630,760p' internal/tracker/config.go
```

Against `docs/reference/configuration.md`: every variable named in the document appears in that grep or in `install.sh`/`scripts/smoke.sh`, with the right reader; every layer claim matches `LoadProjects`; `OFFICE_BACKEND` is still described as a `smoke.sh` variable and not as a runner variable.

Then read `docs/reference/jira-requirements.md` against `docs/notes/jira-setup.md` (sections «Учётки», «Поля аренды», «Workflow»), `office/workflow.yaml:23` and `office/tracker.example.yaml:66-96`: the eight statuses, the four field names and their types, the label, and the link type `Depends` must all match. A reference that is wrong is worse than absent.

- [ ] **Step 4: Confirm nothing still points at the old unit locations**

```bash
grep -rn 'bootstrap/local.office.runner.plist\|bootstrap/office-runner' \
  --include='*.md' --include='*.go' --include='*.sh' --include='*.yaml' . \
  | grep -v '^./docs/comet/' | grep -v '^./docs/superpowers/plans/' \
  | grep -v '^./docs/openspec/changes/user-guide/'
```

Expected: no output.

- [ ] **Step 5: Run the full check set (tasks.md 7.5)**

```bash
gofmt -l .
go build ./...
go test ./...
sh scripts/doc-recipe-test.sh
git status --short
```

Expected: `gofmt -l .` silent; build and tests pass; the harness prints its five `ok:` lines and the summary; `git status --short` shows only the `tasks.md` ticks from the next step (everything else is committed).

- [ ] **Step 6: Tick the 31 boxes in the change's task ledger**

Edit `docs/openspec/changes/user-guide/tasks.md`, changing each `- [ ]` to `- [x]`. All 31 are covered by Tasks 1-15 of this plan; if any is not, stop and say so rather than ticking it.

- [ ] **Step 7: Commit**

```bash
git add docs/openspec/changes/user-guide/tasks.md
git add -A
git commit -m "$(cat <<'MSG'
docs(openspec): user-guide tasks done

Links walked across README, docs/README.md, docs/guide/*, docs/reference/*,
ONBOARDING and both bootstrap READMEs; the two new references re-read against
internal/tracker/config.go, the OFFICE_ grep, office/workflow.yaml and
office/tracker.example.yaml. gofmt, go build, go test and
scripts/doc-recipe-test.sh all clean.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>
MSG
)"
```

- [ ] **Step 8: Report, do not push**

`master` is protected and nothing in this plan pushes. Report the branch state — commit count, the four check results verbatim — and stop.

---

## Self-Review

Run by the plan's author against the design, the delta spec and `tasks.md`.

**1. Spec coverage.**

Delta spec, MODIFIED requirement «`runner init` lays out the office home»:
- creates `${OFFICE_HOME}` and places two samples, each only when absent → unchanged code, pinned by the existing `TestInitLeavesConfiguredHomeAlone` (Task 2).
- creates `${OFFICE_HOME}/scheduler/` and places three samples under the same rule → Task 2, Steps 6-7.
- all three on every platform → Task 2, Step 6 (`schedulerSamples` has no build tag; the comment says why) and the comment in Task 2 Step 3's test.
- never touches `projects.local.yaml`, `tracker.yaml`, `budgets.yaml` → unchanged; `TestInitLeavesConfiguredHomeAlone`.
- prints what it created, what it left alone, and the next step → Task 2, Steps 6 and 8.
- samples copy-ready: projects sample loads after four edits → Task 15, section 3, asserted by diff count; scheduler samples installable after editing account and path → Task 2 Step 8's output stanza, Task 3, Task 8.
- Scenario «fresh machine» → Task 2 Steps 1-3. Scenario «configured home untouched» → existing test, left in place. Scenario «edited scheduler sample survives» → Task 2 Step 4. Scenario «projects sample works with four edits» → Task 15 sections 3-4.

Delta spec, ADDED requirement «one sample per unit, none pins a role»:
- exactly one copy, inside the payload → Task 1 Steps 1-4; Task 3 removes the claim that they live in `bootstrap/`; Task 16 Step 4 greps for survivors.
- reaches both kinds of reader → Task 3 (clone: `office/scheduler/`) and Task 8 (release: `${OFFICE_HOME}/scheduler/`).
- no `--role` → Task 1 Steps 6-9, test `TestSchedulerSamplesDoNotPinRole`.
- Scenario «no shipped sample pins a role» → same test; the plan states why reading the payload covers the `${OFFICE_HOME}` copy too (byte-equality is asserted in `init_test.go`).
- Scenario «one unit, one file» → Task 16 Step 4.
- Scenario «a copied sample moves a task through every role» → **not covered by a test, deliberately.** The design says so in «What stays unverified»: driving a task to `Done` needs a fake satisfying three roles' contracts plus the PR pass, which is `cmd/eval-roles`' job. Task 15's tick covers the first role and pins that the sample's command form (`tick`/`loop` with no `--role`) reaches an agent at all. Flagged in the handover.

Design doc: payload directive (T1), `place()` helper (T2), `MkdirAll`+`Chmod` (T2), second output stanza (T2), units keep their shape (T1 S10), `git mv` (T1, T14), `bootstrap/README.md` rewrite (T3), the document-ownership table (T4, T5, T7, T8, T9, T10, T11, T12, T13, T14), TDD ordering (T2 S5 before S6), the `--role` test (T1), `scripts/doc-recipe-test.sh` with all five steps (T15), what stays unverified (T11 S4, T16 S3).

`tasks.md`: 1.1→T1S1; 1.2→T1S8-9; 1.3→T1S3,S5; 1.4→T2S1-5; 1.5→T2S6-10; 1.6→T2S1-2; 1.7→T1S6-7; 1.8→T2S11; 1.9→T3; 1.10→T1S12-13,T2S12-14,T3S5; 2.1→T4S1,S4; 2.2→T4S2-3; 2.3→T5S1-3; 2.4→T5S4; 3.1→T6S1-2; 3.2→T6S3; 4.1→T7; 4.2→T8; 4.3→T9; 4.4→T10; 4.5→T11S1-3; 4.6→T11S4; 5.1→T12; 5.2→T13; 6.1→T14S1; 6.2→T14S2-4; 7.1→T15S1-3; 7.2→T15S5; 7.3→T16S1-2; 7.4→T16S3; 7.5→T16S5. All 31 present, none added.

**2. Placeholder scan.** No "TBD", no "similar to Task N", no "add appropriate error handling". Every code step carries the code; every test step carries the test; every documentation step names the source file and line range and the destination file. The one conditional branch (Task 15 Step 4) is a fallback the design explicitly authorises, and it spells out exactly what to delete and what comment to leave.

**3. Type consistency.** `schedulerDir` (string const) and `schedulerSamples` (`[]string`) are defined in Task 2 Step 6 and used by name in Task 2 Steps 1, 3, 4 — same spelling throughout. `place(out io.Writer, payloadPath, dst string) error` is defined once and called twice with that signature. The payload paths `scheduler/<name>` are produced in Task 1 and consumed with the same spelling in Task 1 Step 5-6, Task 2 Step 3 and Task 2 Step 7 (`schedulerDir+"/"+name`, forward slash, as `embed.FS` requires). `payload.Payload` is the same identifier used by the existing `init.go` import alias. `wantFiles` / `wantTop` / `wantSched` in the widened test do not collide with the original `want`, which is fully replaced.

---

## Controller rulings

The planner surfaced findings that this plan's text alone does not settle.
Each is ruled on here, with what it costs if the ruling is wrong. These bind
the implementer exactly as the Global Constraints do.

**R1 — `docs/notes/readme-conventions.md` is not on this branch.** Correct: it
lives on the unmerged branch `repo-hygiene` (`393bb60`). *Ruling:* proceed; the
plan quotes checklist items 9, 11 and 14 verbatim into Task 13, so neither the
file nor the branch is needed. Do not cite the file by path in any document
this change writes — it may not exist for the reader. *Cost if wrong:* a
citation nobody can follow, fixed by one line once `repo-hygiene` merges.

**R2 — `OFFICE_BACKEND` is not read by the runner.** Verified: the only reader
is `scripts/smoke.sh:35`, and `docs/guide/development.md:17` uses it for smoke
runs. The proposal listed it among the runner's variables; the proposal is
wrong and the plan is right. *Ruling:* document it as the planner specifies —
a `smoke.sh` variable, with the runner's own backend coming from the
`--backend` flag — and include `OFFICE_VERSION` and `OFFICE_INVOCATION_DIR`,
which are real and which the proposal omitted. Do not "fix" the proposal;
open-phase artifacts belong to a closed phase. *Cost if wrong:* a reference
that teaches a variable the runner ignores — the exact defect this document
exists to prevent.

**R3 — the design doc said "four files" where there are five.** My error.
*Ruling:* corrected in the design doc; the plan's five-file assertion stands.

**R4 — the harness runs in clone mode and so never exercises payload mode.**
Verified: `office/validators/validators_dev.go` carries an empty `embed.FS`
without `-tags release`, and `EnsureValidator` refuses in payload mode, so
`tick` cannot run from a plain `go build`. *Ruling:* accept clone mode; do not
grow the harness into a release-snapshot test. The part of the recipe this
change actually touches — `runner init` — reads the embedded payload in every
mode, as `init.go`'s own comment states, so the mode makes no difference to
what is being verified here. The harness header must say this in so many
words, which the plan's draft already does. *Cost if wrong:* payload-mode
`tick` breaks and this harness stays green — a risk confined to code this
change does not touch, partly covered by `scripts/install-test.sh dist` and
the release gates. Record it as residual risk in the verification report.

**R5 — the unpacked office will hold a third copy of the units** at
`${OFFICE_HOME}/office/<версия>/scheduler/`. *Ruling:* accept, no special case
in the unpack. It is consistent with `roles/`, `skills/` and `hooks/`, which
are unpacked there and are likewise not for editing, and the delta spec's
"One unit, one file" scenario searches the repository, not a user's disk.
Documentation points only at `${OFFICE_HOME}/scheduler/`. *Cost if wrong:* a
reader edits the unpacked copy and their change is lost at the next version
bump — mitigated by never naming that path in the documentation.

**R6 — three destinations the task list did not name.** ONBOARDING's «Что
остаётся руками» table → `docs/reference/jira-requirements.md` minus the
Docker-memory row; the two remaining troubleshooting symptoms → the existing
list in `docs/guide/operations.md`; one line for the new harness →
`docs/guide/development.md`'s checks block, notwithstanding the design table
marking that file unchanged. *Ruling:* all three accepted. The material has to
land somewhere, and each destination is the document that already owns its
subject. *Cost if wrong:* a paragraph in the wrong file, cheap to move.

**R7 — the delta spec scenario "A copied sample moves a task through every
role" has no automated test.** True, and it follows from the owner's own choice
of a harness that stops after the first tick rather than a four-tick
walkthrough. *Ruling:* do not reopen that decision. The scenario is covered in
the verify phase by an independent execution, and the verification report must
name it explicitly as manually verified rather than let it pass unmentioned.
*Cost if wrong:* a shipped sample that runs one role and passes review anyway —
which is why the no-`--role` test in Task 1 exists as the cheap half of the
same guarantee.
