package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/runner"
	"github.com/kao73/virtual-office/tracker"
)

// git выполняет команду в каталоге и возвращает вывод, падая с ним же.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=тест", "GIT_AUTHOR_EMAIL=test@office.local",
		"GIT_COMMITTER_NAME=тест", "GIT_COMMITTER_EMAIL=test@office.local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// origin — «удалённый» репозиторий проекта-клиента с одним коммитом в master.
func origin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	seed := filepath.Join(root, "seed")

	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "master", bare).CombinedOutput(); err != nil {
		t.Fatalf("origin не создан: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "init", "-q", "-b", "master", seed).CombinedOutput(); err != nil {
		t.Fatalf("посевной репозиторий не создан: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# проект\n"), 0o644); err != nil {
		t.Fatalf("README не записан: %v", err)
	}
	gitT(t, seed, "add", "-A")
	gitT(t, seed, "commit", "-q", "-m", "начало")
	gitT(t, seed, "remote", "add", "origin", bare)
	gitT(t, seed, "push", "-q", "origin", "master")
	return bare
}

// setup — менеджер, конфигурация проекта и ссылка на задачу.
func setup(t *testing.T) (*Manager, tracker.Project, tracker.TaskRef) {
	t.Helper()
	m := New(t.TempDir())
	project := tracker.Project{
		RepoURL:       origin(t),
		DefaultBranch: "master",
		BranchPrefix:  "agent/",
	}
	return m, project, tracker.TaskRef{Key: "OFF-1", Project: "OFF"}
}

func TestEnsureCreatesWorktreeFromDefaultBranch(t *testing.T) {
	m, project, task := setup(t)

	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}

	if ws.Branch != "agent/OFF-1" {
		t.Errorf("ветка %q, ожидалась agent/OFF-1", ws.Branch)
	}
	if _, err := os.Stat(filepath.Join(ws.Dir, "README.md")); err != nil {
		t.Errorf("содержимое origin/master не приехало: %v", err)
	}
	if got := gitT(t, ws.Dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "agent/OFF-1" {
		t.Errorf("worktree стоит на %q", got)
	}

	// Bare-клон живёт в хозяйстве раннера, а не рядом с задачей.
	if _, err := os.Stat(filepath.Join(ws.Repo, "HEAD")); err != nil {
		t.Errorf("bare-клон не найден: %v", err)
	}
}

// Пути обязаны быть каноническими. git записывает в файл .git разрешённый путь
// к каталогу репозитория, и если раннер отдаст песочнице путь через симлинк
// (/tmp против /private/tmp на macOS), внутри git скажет «not a git repository».
// Проверено вживую, см. docs/notes/sbx.md.
func TestEnsureReturnsCanonicalPaths(t *testing.T) {
	m, project, task := setup(t)

	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}

	for _, path := range []string{ws.Dir, ws.Repo} {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatalf("путь %s не разрешён: %v", path, err)
		}
		if resolved != path {
			t.Errorf("путь %s не канонический, разрешается в %s", path, resolved)
		}
	}
}

// Внутри песочницы worktree — это файл .git со ссылкой на bare-репозиторий,
// и без него git не заводится вовсе: проверено — «not a git repository».
// Поэтому в песочницу едут оба каталога, и bare — на запись: туда пишутся объекты.
func TestMountsCarryBothWorktreeAndRepo(t *testing.T) {
	m, project, task := setup(t)

	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}

	mounts := ws.Mounts()
	if len(mounts) != 2 {
		t.Fatalf("рабочих пространств %d, ожидалось 2: %+v", len(mounts), mounts)
	}
	var repo runner.Workspace
	for _, mount := range mounts {
		if mount.Path == ws.Repo {
			repo = mount
		}
	}
	if repo.Path == "" {
		t.Error("bare-репозиторий не отдан песочнице: git внутри не заведётся")
	}
	if repo.ReadOnly {
		t.Error("bare-репозиторий отдан только на чтение: коммитить будет некуда")
	}
}

// После reap или needs_human задача возвращается к тому же worktree: там лежит
// незаконченная работа, и терять её нельзя.
func TestEnsureReusesWorktreeWithItsWork(t *testing.T) {
	m, project, task := setup(t)

	first, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	if err := os.WriteFile(filepath.Join(first.Dir, "работа.txt"), []byte("наполовину\n"), 0o644); err != nil {
		t.Fatalf("работа не записана: %v", err)
	}
	gitT(t, first.Dir, "add", "-A")
	gitT(t, first.Dir, "commit", "-q", "-m", "половина работы")

	second, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не переиспользована: %v", err)
	}
	if second.Dir != first.Dir {
		t.Errorf("вторая папка %s, ожидалась та же %s", second.Dir, first.Dir)
	}
	if _, err := os.Stat(filepath.Join(second.Dir, "работа.txt")); err != nil {
		t.Errorf("работа предыдущего прогона пропала: %v", err)
	}
	if got := gitT(t, second.Dir, "log", "--oneline", "-1"); !strings.Contains(got, "половина работы") {
		t.Errorf("история потеряна: %s", got)
	}
}

// Каталог worktree могли снести руками, а запись о нём осталась. Раннер обязан
// подняться сам, не потеряв ветку с работой.
func TestEnsureRecreatesLostWorktreeKeepingBranch(t *testing.T) {
	m, project, task := setup(t)

	first, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	gitT(t, first.Dir, "commit", "-q", "--allow-empty", "-m", "работа")
	if err := os.RemoveAll(first.Dir); err != nil {
		t.Fatalf("каталог не удалён: %v", err)
	}

	second, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не восстановлена: %v", err)
	}
	if got := gitT(t, second.Dir, "log", "--oneline", "-1"); !strings.Contains(got, "работа") {
		t.Errorf("ветка пересоздана с нуля, работа потеряна: %s", got)
	}
}

// fetch идёт перед каждой задачей: свежая задача обязана начинаться от свежего
// origin/<default>, а не от того, что успели склонировать однажды.
func TestEnsureFetchesBeforeNewTask(t *testing.T) {
	m, project, task := setup(t)

	if _, err := m.Ensure(task, project); err != nil {
		t.Fatalf("первая рабочая папка не создана: %v", err)
	}

	// origin уехал вперёд.
	seed := t.TempDir()
	gitT(t, seed, "clone", "-q", project.RepoURL, ".")
	if err := os.WriteFile(filepath.Join(seed, "новое.txt"), []byte("свежее\n"), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}
	gitT(t, seed, "add", "-A")
	gitT(t, seed, "commit", "-q", "-m", "новое в origin")
	gitT(t, seed, "push", "-q", "origin", "master")

	next, err := m.Ensure(tracker.TaskRef{Key: "OFF-2", Project: "OFF"}, project)
	if err != nil {
		t.Fatalf("вторая рабочая папка не создана: %v", err)
	}
	if _, err := os.Stat(filepath.Join(next.Dir, "новое.txt")); err != nil {
		t.Errorf("новая задача началась от устаревшего origin: %v", err)
	}
}

func TestPushOnlyWhenThereIsSomethingToPush(t *testing.T) {
	m, project, task := setup(t)

	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}

	// Свежая ветка без своих коммитов: пушить нечего.
	pushed, err := m.Push(ws)
	if err != nil {
		t.Fatalf("пуш не удался: %v", err)
	}
	if pushed {
		t.Error("пуш пустой ветки объявлен состоявшимся")
	}
	if refs := gitT(t, project.RepoURL, "branch", "--list", "agent/OFF-1"); refs != "" {
		t.Errorf("в origin появилась ветка без коммитов: %s", refs)
	}

	// Появилась работа — ветка обязана уехать, каким бы ни был исход прогона.
	gitT(t, ws.Dir, "commit", "-q", "--allow-empty", "-m", "работа агента")
	if pushed, err = m.Push(ws); err != nil {
		t.Fatalf("пуш не удался: %v", err)
	}
	if !pushed {
		t.Fatal("работа не запушена")
	}
	if got := gitT(t, project.RepoURL, "log", "--oneline", "-1", "agent/OFF-1"); !strings.Contains(got, "работа агента") {
		t.Errorf("в origin не та история: %s", got)
	}

	// Повторный пуш без новых коммитов ничего не делает и не врёт, что сделал.
	if pushed, err = m.Push(ws); err != nil {
		t.Fatalf("повторный пуш не удался: %v", err)
	}
	if pushed {
		t.Error("повторный пуш объявлен состоявшимся")
	}
}

// Конверт обмена — хозяйство раннера, и в репозиторий проекта-клиента он попасть
// не должен ни файлом в дереве, ни строкой в .gitignore. В worktree это отдельный
// случай: правило пишется в info/exclude общего каталога git.
func TestAgentDirStaysOutOfHistory(t *testing.T) {
	m, project, task := setup(t)

	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ws.Dir, runner.Dir), 0o755); err != nil {
		t.Fatalf("конверт не создан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws.Dir, runner.Dir, runner.FileResult), []byte("{}"), 0o644); err != nil {
		t.Fatalf("результат не записан: %v", err)
	}
	if err := runner.ExcludeAgentDir(ws.Dir); err != nil {
		t.Fatalf("конверт не спрятан: %v", err)
	}

	if status := gitT(t, ws.Dir, "status", "--porcelain"); status != "" {
		t.Errorf("конверт виден git: %s", status)
	}
	gitT(t, ws.Dir, "add", "-A")
	gitT(t, ws.Dir, "commit", "-q", "--allow-empty", "-m", "работа")
	if files := gitT(t, ws.Dir, "log", "--name-only", "--pretty=format:"); strings.Contains(files, runner.Dir) {
		t.Errorf("конверт попал в историю: %s", files)
	}
	if _, err := os.Stat(filepath.Join(ws.Dir, ".gitignore")); err == nil {
		t.Error("раннер дописал .gitignore проекта-клиента")
	}
}

// Удаление worktree не должно уносить работу: ветка остаётся в bare-клоне.
func TestRemoveKeepsBranch(t *testing.T) {
	m, project, task := setup(t)

	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	gitT(t, ws.Dir, "commit", "-q", "--allow-empty", "-m", "работа агента")

	if err := m.Remove(ws); err != nil {
		t.Fatalf("worktree не удалён: %v", err)
	}
	if _, err := os.Stat(ws.Dir); !os.IsNotExist(err) {
		t.Errorf("каталог worktree остался: %v", err)
	}
	if got := gitT(t, ws.Repo, "log", "--oneline", "-1", "agent/OFF-1"); !strings.Contains(got, "работа агента") {
		t.Errorf("вместе с worktree потеряна ветка: %s", got)
	}
}

// worktree_root из projects.yaml важнее умолчания: проекты могут жить на разных
// дисках, и раннер обязан слушаться конфигурации.
func TestWorktreeRootFromProject(t *testing.T) {
	m, project, task := setup(t)
	project.WorktreeRoot = filepath.Join(t.TempDir(), "особое-место")

	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	root, err := filepath.EvalSymlinks(project.WorktreeRoot)
	if err != nil {
		t.Fatalf("каталог не создан: %v", err)
	}
	if !strings.HasPrefix(ws.Dir, root) {
		t.Errorf("рабочая папка %s не в заданном корне %s", ws.Dir, root)
	}
}

// Токен подкладывается git'у помощником по учётным данным. Локальному пушу он
// не нужен, но аргументы всё равно уезжают в командную строку — проверяем, что
// они её не ломают. Настоящий пуш по https с токеном проверяется на шаге 5.
func TestPushWorksWithTokenInEnvironment(t *testing.T) {
	t.Setenv(TokenEnv, "тестовый-токен")
	m, project, task := setup(t)

	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	gitT(t, ws.Dir, "commit", "-q", "--allow-empty", "-m", "работа агента")

	pushed, err := m.Push(ws)
	if err != nil {
		t.Fatalf("пуш с токеном в окружении не удался: %v", err)
	}
	if !pushed {
		t.Error("работа не запушена")
	}
}

// Рабочие папки копятся, и первым это замечает не человек, а закончившееся место.
// Список показывает, что лежит, — по всем проектам сразу.
func TestListShowsWorktreesOfProjects(t *testing.T) {
	m, project, task := setup(t)
	if _, err := m.Ensure(task, project); err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	if _, err := m.Ensure(tracker.TaskRef{Key: "OFF-2", Project: "OFF"}, project); err != nil {
		t.Fatalf("вторая рабочая папка не создана: %v", err)
	}

	entries, err := m.List()
	if err != nil {
		t.Fatalf("список не получен: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("в списке %d папок, заведено 2: %+v", len(entries), entries)
	}

	first := entries[0]
	if first.Key != "OFF-1" || first.Project != "OFF" {
		t.Errorf("папка описана как %s/%s, ожидалось OFF/OFF-1", first.Project, first.Key)
	}
	if first.Branch != "agent/OFF-1" {
		t.Errorf("ветка %q, ожидалась agent/OFF-1", first.Branch)
	}
	if first.Size == 0 {
		t.Error("размер нулевой: ради него список и заводится")
	}
	if first.Dirty != 0 {
		t.Errorf("свежая папка сочтена грязной: %d", first.Dirty)
	}
	// Bare-клон сам себе не рабочая папка и в уборку не входит.
	for _, e := range entries {
		if strings.HasSuffix(e.Dir, ".git") {
			t.Errorf("в списке оказался сам клон: %s", e.Dir)
		}
	}
}

// Удаление уносит незакоммиченное — и это единственное, что оно способно унести:
// ветка живёт в клоне. Поэтому список обязан показывать грязь до удаления.
func TestListCountsUncommittedWork(t *testing.T) {
	m, project, task := setup(t)
	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws.Dir, "недоделка.py"), []byte("# ещё не готово\n"), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}

	entries, err := m.List()
	if err != nil {
		t.Fatalf("список не получен: %v", err)
	}
	if len(entries) != 1 || entries[0].Dirty == 0 {
		t.Fatalf("незакоммиченная работа не замечена: %+v", entries)
	}
}

// Запись о worktree переживает свой каталог: его сносят руками, а иногда вместе
// с диском. Список должен сказать об этом, а не упасть на обходе пустоты.
func TestListMarksLostDirectory(t *testing.T) {
	m, project, task := setup(t)
	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	if err := os.RemoveAll(ws.Dir); err != nil {
		t.Fatalf("каталог не снесён: %v", err)
	}

	entries, err := m.List()
	if err != nil {
		t.Fatalf("список не получен: %v", err)
	}
	if len(entries) != 1 || !entries[0].Missing {
		t.Fatalf("потерянный каталог не отмечен: %+v", entries)
	}
}
