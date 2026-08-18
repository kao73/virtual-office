package runner

import (
	"encoding/json"
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
	if err := os.WriteFile(filepath.Join(workdir, PlanFile), []byte("1. Сделать\n2. Проверить\n"), 0o644); err != nil {
		t.Fatalf("план не записан: %v", err)
	}

	passport := fixturePassport()
	passport.TaskKey = "OFF-1"
	extra := "## Переписка\n\nЧеловек: берём Stripe.\n"

	if err := PrepareInput(workdir, fixtureRole(t), passport, Input{Task: "Задача\n", Context: extra}); err != nil {
		t.Fatalf("вход не подготовлен: %v", err)
	}

	context := read(t, workdir, FileContext)
	for _, want := range []string{"OFF-1", "берём Stripe", "2. Проверить"} {
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
