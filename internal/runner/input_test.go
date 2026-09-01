package runner

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// gitRepo — рабочая папка агента: обычный git-репозиторий с одним коммитом.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	git(t, dir, "-c", "user.email=t@example.test", "-c", "user.name=test", "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func fixtureRole(t *testing.T) Role {
	t.Helper()
	role, err := LoadRole(fixtureOffice(t, fixtureRoleYAML), "tester")
	if err != nil {
		t.Fatalf("роль не загружена: %v", err)
	}
	return role
}

func fixturePassport() Run {
	return Run{
		RunID:     "550e8400-e29b-41d4-a716-446655440000",
		Role:      "tester",
		ConfigSHA: "5bc6a3b0000000000000000000000000000000ab",
		StartedAt: time.Date(2026, 8, 16, 16, 40, 0, 0, time.UTC),
	}
}

// Снимок статуса — точка отсчёта ограждений: рабочая папка переиспользуется,
// и чужая незакоммиченная правка лежит в ней ещё до первого шага роли. Судится
// дельта, поэтому снимок обязан быть, даже когда он пуст.
func TestPrepareInputSnapshotsBaseStatus(t *testing.T) {
	workdir := gitRepo(t)
	if err := os.WriteFile(filepath.Join(workdir, "чужое.txt"), []byte("грязь прошлого прогона\n"), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}
	role, passport := fixtureRole(t), fixturePassport()

	if err := PrepareInput(workdir, role, passport, Input{Task: "Сделай хорошо.\n"}); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}

	snapshot := read(t, workdir, FileBaseStatus)
	if !strings.Contains(snapshot, "чужое.txt") {
		t.Errorf("в снимке нет доставшейся грязи:\n%s", snapshot)
	}
	// Каталог обмена исключается из git раньше снимка: иначе ограждение
	// сравнивало бы дельту с собственным конвертом.
	if strings.Contains(snapshot, Dir) {
		t.Errorf("каталог обмена попал в снимок:\n%s", snapshot)
	}
}

// Точку отсчёта наблюдает раннер, а не агент. В репозитории без коммитов её
// не существует — это не ошибка, а «сравнивать не с чем».
func TestHeadCommit(t *testing.T) {
	repo := gitRepo(t)
	head, err := HeadCommit(repo)
	if err != nil {
		t.Fatalf("HEAD не прочитан: %v", err)
	}
	if want := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD")); head != want {
		t.Errorf("HEAD %q, ожидался %q", head, want)
	}

	empty := t.TempDir()
	git(t, empty, "init", "-q")
	switch head, err := HeadCommit(empty); {
	case err != nil:
		t.Errorf("репозиторий без коммитов сочтён поломкой: %v", err)
	case head != "":
		t.Errorf("в репозитории без коммитов найден HEAD %q", head)
	}
}

func TestPrepareInputWritesExchange(t *testing.T) {
	workdir := gitRepo(t)
	role, passport := fixtureRole(t), fixturePassport()

	if err := PrepareInput(workdir, role, passport, Input{Task: "Сделай хорошо.\n"}); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}

	task := read(t, workdir, FileTask)
	if task != "Сделай хорошо.\n" {
		t.Errorf("постановка задачи искажена: %q", task)
	}

	context := read(t, workdir, FileContext)
	for _, want := range []string{role.Name, passport.RunID, role.ResultFile, "Bash(git *)"} {
		if !strings.Contains(context, want) {
			t.Errorf("в контексте нет %q:\n%s", want, context)
		}
	}

	var got Run
	if err := json.Unmarshal([]byte(read(t, workdir, FileRun)), &got); err != nil {
		t.Fatalf("паспорт запуска не разобран: %v", err)
	}
	if got.RunID != passport.RunID || got.Role != passport.Role || got.ConfigSHA != passport.ConfigSHA {
		t.Errorf("паспорт запуска искажён: %+v", got)
	}
	if !got.StartedAt.Equal(passport.StartedAt) {
		t.Errorf("время старта искажено: %v", got.StartedAt)
	}

	// Каталог обмена не должен попадать в поле зрения git.
	if status := git(t, workdir, "status", "--porcelain"); status != "" {
		t.Errorf("git видит конверт обмена:\n%s", status)
	}
}

func TestPrepareInputRequiresTask(t *testing.T) {
	err := PrepareInput(gitRepo(t), fixtureRole(t), fixturePassport(), Input{})
	if err == nil {
		t.Fatal("постановки задачи нет, но вход подготовлен")
	}
	if !strings.Contains(err.Error(), "постановки задачи нет") {
		t.Errorf("ошибка не объясняет причину: %v", err)
	}
}

func TestPrepareInputKeepsTaskAlreadyInPlace(t *testing.T) {
	workdir := gitRepo(t)
	agentDir := filepath.Join(workdir, Dir)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("каталог обмена не создан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, FileTask), []byte("уже лежит\n"), 0o644); err != nil {
		t.Fatalf("постановка не записана: %v", err)
	}

	if err := PrepareInput(workdir, fixtureRole(t), fixturePassport(), Input{}); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}
	if task := read(t, workdir, FileTask); task != "уже лежит\n" {
		t.Errorf("лежавшая постановка перезаписана: %q", task)
	}
}

func TestStateFileGoesIntoContext(t *testing.T) {
	workdir := gitRepo(t)
	if err := os.WriteFile(filepath.Join(workdir, StateFile), []byte("Дошёл до третьего шага.\n"), 0o644); err != nil {
		t.Fatalf("состояние не записано: %v", err)
	}
	if err := PrepareInput(workdir, fixtureRole(t), fixturePassport(), Input{Task: "Задача\n"}); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}
	if context := read(t, workdir, FileContext); !strings.Contains(context, "Дошёл до третьего шага") {
		t.Errorf("состояние задачи не попало в контекст:\n%s", context)
	}
}

func TestExcludeAgentDirIsIdempotent(t *testing.T) {
	workdir := gitRepo(t)

	for i := range 3 {
		if err := ExcludeAgentDir(workdir); err != nil {
			t.Fatalf("исключение не записано на попытке %d: %v", i+1, err)
		}
	}

	raw, err := os.ReadFile(filepath.Join(workdir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("файл исключений не прочитан: %v", err)
	}
	exclude := string(raw)
	if got := strings.Count(exclude, Dir+"/"); got != 1 {
		t.Errorf("правило записано %d раз, ожидался один:\n%s", got, exclude)
	}
	if !strings.Contains(exclude, excludeComment) {
		t.Error("правило не помечено происхождением: чужой файл, надо объяснить, откуда строка")
	}
}

// Решение варианта C держится на том, что worktree читает info/exclude
// основного репозитория. Если это перестанет быть правдой, конверт обмена
// начнёт попадать в коммиты клиента — тест обязан поймать это первым.
func TestExcludeAgentDirWorksInWorktree(t *testing.T) {
	main := gitRepo(t)
	worktree := filepath.Join(t.TempDir(), "wt")
	git(t, main, "worktree", "add", "-q", worktree, "-b", "feature")

	if err := os.MkdirAll(filepath.Join(worktree, Dir), 0o755); err != nil {
		t.Fatalf("каталог обмена не создан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, Dir, FileTask), []byte("з\n"), 0o644); err != nil {
		t.Fatalf("постановка не записана: %v", err)
	}

	if status := git(t, worktree, "status", "--porcelain"); !strings.Contains(status, Dir) {
		t.Fatalf("подготовка теста неверна: git и так не видит конверт:\n%s", status)
	}
	if err := ExcludeAgentDir(worktree); err != nil {
		t.Fatalf("исключение не записано: %v", err)
	}
	if status := git(t, worktree, "status", "--porcelain"); status != "" {
		t.Errorf("git в worktree всё ещё видит конверт обмена:\n%s", status)
	}

	// Правило обязано лечь в общий каталог, а не в каталог worktree.
	if _, err := os.Stat(filepath.Join(main, ".git", "info", "exclude")); err != nil {
		t.Errorf("правило не попало в общий каталог git: %v", err)
	}
}

// `comet native new` не пишет собственного .gitignore, а .comet/runtime/** —
// кэш локального исполнения этой конкретной песочницы (абсолютные хостовые
// пути внутри state.json, живой прогон это подтвердил): без исключения
// worktree навсегда остаётся "грязным" (`?? .comet/runtime/`), и
// sweepWorktrees считает его небезопасным для удаления. .comet/config.yaml
// и .comet/current-change.json — наоборот, обычные трекируемые пути: без
// них `comet native status` не восстановится в другой рабочей папке,
// а следующая роль не найдёт каталог изменения через context.md — исключать
// их нельзя.
func TestExcludeCometRuntimeExcludesOnlyRuntimeCache(t *testing.T) {
	workdir := gitRepo(t)

	cometDir := filepath.Join(workdir, ".comet")
	if err := os.MkdirAll(filepath.Join(cometDir, "runtime", "native", "changes", "demo"), 0o755); err != nil {
		t.Fatalf("каталог .comet/runtime не создан: %v", err)
	}
	write := func(rel, content string) {
		path := filepath.Join(cometDir, rel)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("%s не записан: %v", rel, err)
		}
	}
	write("config.yaml", "schema: comet.project.v1\n")
	write("current-change.json", `{"change":"demo"}`+"\n")
	write(filepath.Join("runtime", "native", "changes", "demo", "state.json"), "{}\n")

	// -uall: иначе git сворачивает смешанный (частично исключённый) неотслеживаемый
	// каталог в одну строку `?? .comet/`, и по ней не сказать, что внутри уже
	// исключено, а что нет — та же причина, что объясняется у WorktreeStatus.
	if status := git(t, workdir, "status", "--porcelain", "-uall"); !strings.Contains(status, ".comet") {
		t.Fatalf("подготовка теста неверна: git и так не видит .comet/:\n%s", status)
	}

	if err := ExcludeCometRuntime(workdir); err != nil {
		t.Fatalf("исключение не записано: %v", err)
	}

	status := git(t, workdir, "status", "--porcelain", "-uall")
	if strings.Contains(status, "runtime") {
		t.Errorf("runtime/ всё ещё виден git:\n%s", status)
	}
	if !strings.Contains(status, "config.yaml") {
		t.Errorf(".comet/config.yaml исчез из git, а обязан остаться обычным трекируемым путём:\n%s", status)
	}
	if !strings.Contains(status, "current-change.json") {
		t.Errorf("current-change.json исчез из git, а обязан остаться обычным трекируемым путём:\n%s", status)
	}
}

// Регрессия на живой прогон (demo-3, второй заход): рабочая папка, уже
// тронутая версией ExcludeCometRuntime до этой правки, несёт в info/exclude
// устаревшее правило на current-change.json — appendExcludeRules только
// дописывает, само оно не удаляет ничего. git reset --hard такую папку не
// чистит: info/exclude — метаданные git, не часть дерева. Без миграции
// analyst, честно следуя новой инструкции role.md, получал бы ровно ту же
// ошибку git add, что и cloneSweep до фикса addScript: «paths are ignored
// by one of your .gitignore files».
func TestExcludeCometRuntimeMigratesAwayStaleCurrentChangeRule(t *testing.T) {
	workdir := gitRepo(t)

	// Правило на current-change.json, как его писала прежняя версия
	// ExcludeCometRuntime — то же appendExcludeRules, чтобы не разойтись
	// форматом строки с настоящим кодом миграции.
	if err := appendExcludeRules(workdir, cometExcludeComment, []string{
		".comet/current-change.json",
		".comet/runtime/",
	}); err != nil {
		t.Fatalf("подготовка теста: устаревшее правило не записано: %v", err)
	}

	cometDir := filepath.Join(workdir, ".comet")
	if err := os.MkdirAll(cometDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cometDir, "current-change.json"), []byte(`{"change":"demo"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if status := git(t, workdir, "status", "--porcelain", "-uall"); status != "" {
		t.Fatalf("подготовка теста неверна: current-change.json уже виден git до миграции:\n%s", status)
	}

	if err := ExcludeCometRuntime(workdir); err != nil {
		t.Fatalf("исключение не записано: %v", err)
	}

	status := git(t, workdir, "status", "--porcelain", "-uall")
	if !strings.Contains(status, "current-change.json") {
		t.Errorf("устаревшее правило не убрано — current-change.json всё ещё невидим git:\n%s", status)
	}

	// Живой сценарий: тот же git add, которым роль коммитит файл — обязан
	// пройти без «paths are ignored», а не только «файл виден git status».
	git(t, workdir, "add", ".comet/current-change.json")

	// Повторный вызов идемпотентен: правило уже убрано, второй раз чистить
	// нечего, и runtime/ остаётся исключённым как обычно.
	if err := ExcludeCometRuntime(workdir); err != nil {
		t.Fatalf("повторное исключение не записано: %v", err)
	}
	if status := git(t, workdir, "status", "--porcelain", "-uall"); strings.Contains(status, "runtime") {
		t.Errorf("runtime/ перестал быть исключён после повторного вызова:\n%s", status)
	}
}

// Независимое ревью: продовый конвейер (internal/pipeline/agent.go) идёт
// бинд-маунтом с переиспользуемым worktree (workspace.Manager.Ensure), а
// не эфемерной песочницей --clone — усечённый по таймауту прогон оставляет
// lock/.coordinator с pid+hostname текущего процесса, координатор Comet
// Native их живость не проверяет, и следующий прогон той же задачи
// блокируется навсегда (exit 73, тот же отказ, что и в EXP-2). Остальное
// .comet/runtime (реальное состояние исполнения — changes/, transactions/)
// обязано пережить чистку нетронутым.
func TestClearStaleCometLocksRemovesLocksKeepsExecutionState(t *testing.T) {
	workdir := gitRepo(t)
	cometDir := filepath.Join(workdir, ".comet", "runtime", "native")

	locksDir := filepath.Join(cometDir, "locks")
	if err := os.MkdirAll(filepath.Join(locksDir, ".coordinator"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locksDir, "root-move.lock"), []byte("pid: 16810\nhostname: office-e677f34a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locksDir, ".coordinator", "abc.claim"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	statePath := filepath.Join(cometDir, "changes", "demo", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"phase":"build"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ClearStaleCometLocks(workdir); err != nil {
		t.Fatalf("ClearStaleCometLocks: %v", err)
	}

	if _, err := os.Stat(locksDir); !os.IsNotExist(err) {
		t.Errorf("locks/ не убран: %v", err)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Errorf("реальное состояние исполнения задето чисткой: %v", err)
	}
}

func TestClearStaleCometLocksNoopWithoutCometRuntime(t *testing.T) {
	workdir := gitRepo(t)

	if err := ClearStaleCometLocks(workdir); err != nil {
		t.Fatalf("ClearStaleCometLocks: %v", err)
	}
}

// Без активного изменения Comet Native (.comet/config.yaml ещё нет) —
// нечего дописывать, тот же принцип лучших усилий, что и у ExcludeCometRuntime.
func TestEnsureCometHookAllowPathsNoopWithoutConfig(t *testing.T) {
	workdir := gitRepo(t)

	if err := EnsureCometHookAllowPaths(workdir); err != nil {
		t.Fatalf("EnsureCometHookAllowPaths: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, ".comet", "config.yaml")); !os.IsNotExist(err) {
		t.Errorf(".comet/config.yaml заведён из ничего: %v", err)
	}
}

// Независимое ревью: этот блок был описан только в roles/analyst/role.md —
// implementer и reviewer о нём не знали, и для reviewer отсутствие блока
// молча ломало Verify (запись result.json откатывала изменение в build).
// EnsureCometHookAllowPaths гарантирует блок для любой роли, не полагаясь
// на то, что его допишет только аналитик.
func TestEnsureCometHookAllowPathsAppendsMissingBlock(t *testing.T) {
	workdir := gitRepo(t)
	cometDir := filepath.Join(workdir, ".comet")
	if err := os.MkdirAll(cometDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(cometDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("schema: comet.project.v1\nlanguage: en\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureCometHookAllowPaths(workdir); err != nil {
		t.Fatalf("EnsureCometHookAllowPaths: %v", err)
	}

	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(got)
	if !strings.Contains(content, "schema: comet.project.v1") || !strings.Contains(content, "language: en") {
		t.Errorf("существующие ключи задеты:\n%s", content)
	}
	if !strings.Contains(content, "hook:") || !strings.Contains(content, "allow_paths:") ||
		!strings.Contains(content, ".agent") || !strings.Contains(content, "STATE.md") {
		t.Errorf("блок hook.allow_paths не дописан:\n%s", content)
	}
}

// Независимое ревью: .comet/config.yaml коммитить умеет только analyst
// (roles/analyst/role.md, «Как коммитить») — если блок допишется на
// прогоне implementer'а или reviewer'а, никто из них его не закоммитит,
// и рабочая папка останется "грязной" навсегда (sweepWorktrees её не
// уберёт). EnsureCometHookAllowPaths обязана закоммитить свою правку сама.
func TestEnsureCometHookAllowPathsCommitsItsOwnEdit(t *testing.T) {
	workdir := gitRepo(t)
	cometDir := filepath.Join(workdir, ".comet")
	if err := os.MkdirAll(cometDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(cometDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("schema: comet.project.v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, workdir, "add", ".")
	git(t, workdir, "-c", "user.email=t@example.test", "-c", "user.name=test",
		"commit", "-q", "-m", "заводит изменение")

	if err := EnsureCometHookAllowPaths(workdir); err != nil {
		t.Fatalf("EnsureCometHookAllowPaths: %v", err)
	}

	if status := git(t, workdir, "status", "--porcelain"); status != "" {
		t.Errorf("правка hook.allow_paths осталась незакоммиченной:\n%s", status)
	}
	subject := strings.TrimSpace(git(t, workdir, "log", "-1", "--format=%s"))
	if subject != "chore: add hook.allow_paths to .comet/config.yaml" {
		t.Errorf("тема коммита = %q", subject)
	}
	authorName := strings.TrimSpace(git(t, workdir, "log", "-1", "--format=%an"))
	if authorName != hookConfigCommitName {
		t.Errorf("автор коммита = %q, ожидалось %q — не должно выглядеть работой роли", authorName, hookConfigCommitName)
	}
}

// Ничего не дописывалось (блок hook: уже был) — коммита тоже быть не
// должно: EnsureCometHookAllowPaths не создаёт пустых системных коммитов.
func TestEnsureCometHookAllowPathsSkipsCommitWhenNoop(t *testing.T) {
	workdir := gitRepo(t)
	cometDir := filepath.Join(workdir, ".comet")
	if err := os.MkdirAll(cometDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(cometDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("hook:\n  allow_paths:\n    - .agent\n    - STATE.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, workdir, "add", ".")
	git(t, workdir, "-c", "user.email=t@example.test", "-c", "user.name=test",
		"commit", "-q", "-m", "заводит изменение с уже полным конфигом")

	before := git(t, workdir, "log", "-1", "--format=%H")

	if err := EnsureCometHookAllowPaths(workdir); err != nil {
		t.Fatalf("EnsureCometHookAllowPaths: %v", err)
	}

	after := git(t, workdir, "log", "-1", "--format=%H")
	if before != after {
		t.Error("создан лишний коммит там, где дописывать было нечего")
	}
}

// Повторный вызов не дублирует блок и не трогает существующий (в т.ч. если
// он расширен вручную сверх .agent/STATE.md).
func TestEnsureCometHookAllowPathsIdempotent(t *testing.T) {
	workdir := gitRepo(t)
	cometDir := filepath.Join(workdir, ".comet")
	if err := os.MkdirAll(cometDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(cometDir, "config.yaml")
	original := "schema: comet.project.v1\nhook:\n  allow_paths:\n    - .agent\n    - STATE.md\n    - notes.md\n"
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := EnsureCometHookAllowPaths(workdir); err != nil {
		t.Fatalf("EnsureCometHookAllowPaths: %v", err)
	}

	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("существующий блок hook: изменён\nбыло:  %q\nстало: %q", original, string(got))
	}
}

// Независимое ревью: cometHookKeyPattern раньше требовал, чтобы после
// "hook:" до конца строки не было ничего, кроме пробелов — "hook: {}" или
// "hook:  # комментарий" не узнавались бы вовсе, и блок дописался бы
// второй раз, дав дублирующийся верхнеуровневый ключ hook: в YAML.
func TestEnsureCometHookAllowPathsRecognizesInlineHookKey(t *testing.T) {
	cases := []string{
		"schema: comet.project.v1\nhook: {}\n",
		"schema: comet.project.v1\nhook:  # заполним позже\n",
	}
	for _, original := range cases {
		workdir := gitRepo(t)
		cometDir := filepath.Join(workdir, ".comet")
		if err := os.MkdirAll(cometDir, 0o755); err != nil {
			t.Fatal(err)
		}
		configPath := filepath.Join(cometDir, "config.yaml")
		if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
			t.Fatal(err)
		}

		if err := EnsureCometHookAllowPaths(workdir); err != nil {
			t.Fatalf("EnsureCometHookAllowPaths: %v", err)
		}

		got, err := os.ReadFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(got), "hook:"); n != 1 {
			t.Errorf("hook: с инлайн-содержимым не узнан — верхнеуровневый ключ встречается %d раз(а), ожидался 1:\n%s", n, got)
		}
	}
}

// Ветки агент сам узнать не может: в рабочей папке видно только HEAD, а от чего
// он отведён — уже нет. Без базы `git diff` показывает не то, и reviewer'у неоткуда
// взять свою работу.
func TestContextNamesBranches(t *testing.T) {
	workdir := gitRepo(t)
	in := Input{Task: "Задача\n", Branch: "agent/OFF-1", BaseBranch: "origin/master"}

	if err := PrepareInput(workdir, fixtureRole(t), fixturePassport(), in); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}

	context := read(t, workdir, FileContext)
	for _, want := range []string{"agent/OFF-1", "origin/master"} {
		if !strings.Contains(context, want) {
			t.Errorf("в контексте нет %q:\n%s", want, context)
		}
	}
}

// Ручной запуск ветками не распоряжается: там нет ни проекта, ни рабочей папки
// задачи. Строки, которую нечем заполнить, в контексте быть не должно.
func TestContextOmitsBranchesWhenUnknown(t *testing.T) {
	workdir := gitRepo(t)
	if err := PrepareInput(workdir, fixtureRole(t), fixturePassport(), Input{Task: "Задача\n"}); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}

	if context := read(t, workdir, FileContext); strings.Contains(context, "етка") {
		t.Errorf("контекст говорит о ветках, которых не знает:\n%s", context)
	}
}

// read возвращает содержимое файла из каталога обмена внутри workdir.
func read(t *testing.T, workdir, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(workdir, Dir, name))
	if err != nil {
		t.Fatalf("%s не прочитан: %v", name, err)
	}
	return string(raw)
}

// Раннер этапа 2 приносит агенту переписку тикета, номер попытки и ключ задачи —
// всё, чего он не может узнать сам, сидя в рабочей папке.
func TestContextCarriesRunnerSections(t *testing.T) {
	workdir := gitRepo(t)
	if err := os.WriteFile(filepath.Join(workdir, StateFile), []byte("Разбираю модуль оплаты.\n"), 0o644); err != nil {
		t.Fatalf("черновик не записан: %v", err)
	}

	passport := fixturePassport()
	passport.TaskKey = "OFF-1"
	extra := "## Переписка\n\nЧеловек: берём Stripe.\n"

	if err := PrepareInput(workdir, fixtureRole(t), passport, Input{Task: "Задача\n", Context: extra}); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}

	context := read(t, workdir, FileContext)
	for _, want := range []string{"OFF-1", "берём Stripe", "Разбираю модуль оплаты"} {
		if !strings.Contains(context, want) {
			t.Errorf("в контексте нет %q:\n%s", want, context)
		}
	}

	var got Run
	if err := json.Unmarshal([]byte(read(t, workdir, FileRun)), &got); err != nil {
		t.Fatalf("паспорт запуска не разобран: %v", err)
	}
	if got.TaskKey != "OFF-1" {
		t.Errorf("ключ задачи не попал в паспорт: %+v", got)
	}
}

// Путь каталога изменения агент не выведет сам: он собирается из ключа задачи,
// а на первом прогоне каталог ещё пуст и от прочих не отличается. План
// называется отдельной строкой и только когда он в git: незакоммиченный файл
// для следующей роли не существует, и обещать его нельзя.
func TestContextNamesChangeDirAndPlan(t *testing.T) {
	workdir, role := gitRepo(t), fixtureRole(t)
	passport := fixturePassport()
	passport.TaskKey = "OFF-1"

	prepare := func() string {
		t.Helper()
		if err := PrepareInput(workdir, role, passport, Input{Task: "Задача\n"}); err != nil {
			t.Fatalf("вход не подготовлен: %v", err)
		}
		return read(t, workdir, FileContext)
	}

	if context := prepare(); strings.Contains(context, "Каталог изменения") {
		t.Errorf("обещан каталог, которого нет:\n%s", context)
	}

	dir := filepath.Join(workdir, ChangeDirRel(passport.TaskKey))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("каталог изменения не создан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileTasks), []byte("- [ ] план\n"), 0o644); err != nil {
		t.Fatalf("план не записан: %v", err)
	}
	context := prepare()
	if !strings.Contains(context, "Каталог изменения: docs/changes/OFF-1") {
		t.Errorf("в контексте нет каталога изменения:\n%s", context)
	}
	if strings.Contains(context, "План:") {
		t.Errorf("обещан незакоммиченный план:\n%s", context)
	}

	git(t, workdir, "add", filepath.Join(ChangeDirRel(passport.TaskKey), FileTasks))
	git(t, workdir, "-c", "user.email=t@example.test", "-c", "user.name=test", "commit", "-q", "-m", "план")

	if context := prepare(); !strings.Contains(context, "План: docs/changes/OFF-1/tasks.md") {
		t.Errorf("в контексте нет закоммиченного плана:\n%s", context)
	}
}

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

// Регрессия на живой прогон (demo-3): аналитик завёл изменение "stats-median"
// при task-key "demo-3" — угаданный по ключу каталог docs/comet/changes/demo-3
// не существовал, и следующая роль осталась без единой строки "Каталог
// изменения", хотя изменение было и стояло в фазе build.
// .comet/current-change.json называет реально выбранное имя точно и обязан
// победить угадывание по task-key, когда они расходятся.
func TestContextPrefersCurrentChangeFileOverTaskKeyGuess(t *testing.T) {
	workdir, role := gitRepo(t), fixtureRole(t)
	passport := fixturePassport()
	passport.TaskKey = "demo-3"

	selected := filepath.Join(workdir, CometChangeDirRel("stats-median"))
	if err := os.MkdirAll(selected, 0o755); err != nil {
		t.Fatalf("каталог реально выбранного изменения не создан: %v", err)
	}
	cometDir := filepath.Join(workdir, ".comet")
	if err := os.MkdirAll(cometDir, 0o755); err != nil {
		t.Fatalf(".comet не создан: %v", err)
	}
	selection := `{"schema":"comet.selection.v2","workflow":"native","change":"stats-median","branch":null}` + "\n"
	if err := os.WriteFile(filepath.Join(cometDir, "current-change.json"), []byte(selection), 0o644); err != nil {
		t.Fatalf("current-change.json не записан: %v", err)
	}

	if err := PrepareInput(workdir, role, passport, Input{Task: "Задача\n"}); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}
	context := read(t, workdir, FileContext)

	if !strings.Contains(context, "Каталог изменения: "+CometChangeDirRel("stats-median")) {
		t.Errorf("current-change.json не победил угадывание по task-key:\n%s", context)
	}
	if strings.Contains(context, "Каталог изменения: "+CometChangeDirRel(passport.TaskKey)) {
		t.Errorf("угаданный по task-key (несуществующий) каталог не должен появиться в контексте:\n%s", context)
	}
}

// Рабочая папка переиспользуется, а `result.json` в ней остаётся от прошлого
// прогона. Прогон, не успевший написать свой — упёршийся в предел шагов или
// убитый таймаутом, — прочитал бы чужой и выдал бы чужие слова за собственный
// отчёт. Поймано нагрузочным прогоном этапа 4, стоило ложного «done» в тикете.
func TestPrepareInputRemovesResultOfPreviousRun(t *testing.T) {
	workdir, role := gitRepo(t), fixtureRole(t)
	path := filepath.Join(workdir, role.ResultFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("каталог обмена не создан: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"outcome":"done","summary":"чужой отчёт","next_owner":"none"}`), 0o644); err != nil {
		t.Fatalf("результат прошлого прогона не записан: %v", err)
	}

	if err := PrepareInput(workdir, role, fixturePassport(), Input{Task: "Задача\n"}); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("результат прошлого прогона пережил подготовку входа: %v", err)
	}
}

// run.log лежит внутри Dir и в бэкенде sbx --clone едет туда-обратно
// с остальным каталогом обмена (internal/backends/sbx/clone.go): чужой,
// оставшийся от прошлого прогона лог, занесённый в песочницу раньше, чем
// os.Create(logPath) его обрежет, приезжает назад поверх свежего и подменяет
// собой то, что runagent.Execute потом читает для расхода и классификации
// окончания прогона. Найдено ревью Task 21.
func TestPrepareInputRemovesLogOfPreviousRun(t *testing.T) {
	workdir, role := gitRepo(t), fixtureRole(t)
	path := filepath.Join(workdir, Dir, FileLog)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("каталог обмена не создан: %v", err)
	}
	if err := os.WriteFile(path, []byte("чужой лог прошлого прогона\n"), 0o644); err != nil {
		t.Fatalf("лог прошлого прогона не записан: %v", err)
	}

	if err := PrepareInput(workdir, role, fixturePassport(), Input{Task: "Задача\n"}); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}

	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("лог прошлого прогона пережил подготовку входа: %v", err)
	}
}
