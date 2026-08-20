package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/tracker"
)

// commit кладёт в рабочую папку файл и коммитит его.
func commit(t *testing.T, dir, name, body, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}
	gitT(t, dir, "add", name)
	gitT(t, dir, "commit", "-q", "-m", message)
}

// pushDefault двигает ветку по умолчанию origin вперёд, пока задача ждёт
// слияния.
func pushDefault(t *testing.T, project tracker.Project, name, body string) {
	t.Helper()
	work := t.TempDir()
	gitT(t, work, "init", "-q", "-b", "master")
	gitT(t, work, "remote", "add", "origin", project.RepoURL)
	gitT(t, work, "fetch", "-q", "origin", "master")
	gitT(t, work, "checkout", "-q", "-B", "master", "origin/master")
	commit(t, work, name, body, "правка в ветке по умолчанию")
	gitT(t, work, "push", "-q", "origin", "master")
}

// Ветка, которая сливается чисто: коммиты есть, конфликта нет.
func TestMergeCheckCleanBranch(t *testing.T) {
	m, project, task := setup(t)
	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	commit(t, ws.Dir, "новое.txt", "работа\n", "работа автора")
	if _, err := m.Push(ws); err != nil {
		t.Fatalf("ветка не опубликована: %v", err)
	}

	merge, err := m.MergeCheck(ws.Repo, ws.Branch, project.DefaultBranch)
	if err != nil {
		t.Fatalf("слияние не проверено: %v", err)
	}
	if merge.Empty() || merge.Conflict {
		t.Errorf("чистая ветка признана несливаемой: %+v", merge)
	}
}

// Ветка без собственных коммитов — не конфликт, а «сливать нечего»: открывать
// pull request не из чего, и разговор об этом другой.
func TestMergeCheckEmptyBranch(t *testing.T) {
	m, project, task := setup(t)
	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}

	// Ветки в origin нет вовсе: агент ничего не закоммитил.
	merge, err := m.MergeCheck(ws.Repo, ws.Branch, project.DefaultBranch)
	if err != nil {
		t.Fatalf("слияние не проверено: %v", err)
	}
	if !merge.Empty() || merge.Conflict {
		t.Errorf("пустая ветка описана не так: %+v", merge)
	}
}

// Конфликт считается локально: ни сети, ни forge для этого не нужно.
func TestMergeCheckFindsConflict(t *testing.T) {
	m, project, task := setup(t)
	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	commit(t, ws.Dir, "спорный.txt", "версия ветки\n", "работа автора")
	if _, err := m.Push(ws); err != nil {
		t.Fatalf("ветка не опубликована: %v", err)
	}

	pushDefault(t, project, "спорный.txt", "версия ветки по умолчанию\n")
	// Клон освежается тем же путём, каким это делает системный проход.
	if _, err := m.Repo(task.Project, project); err != nil {
		t.Fatalf("клон не освежён: %v", err)
	}

	merge, err := m.MergeCheck(ws.Repo, ws.Branch, project.DefaultBranch)
	if err != nil {
		t.Fatalf("слияние не проверено: %v", err)
	}
	if !merge.Conflict {
		t.Errorf("конфликт не найден: %+v", merge)
	}
}

// Клон нужен системному проходу без рабочей папки: он читает из него файлы
// изменения и считает слияемость, а папки у задачи может не быть.
func TestRepoWorksWithoutWorktree(t *testing.T) {
	m, project, task := setup(t)

	repo, err := m.Repo(task.Project, project)
	if err != nil {
		t.Fatalf("клон не заведён: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "HEAD")); err != nil {
		t.Fatalf("клона нет: %v", err)
	}
	// git перечисляет и сам bare-клон, поэтому рабочих папок ноль означает
	// ровно одну строку `worktree ` в выводе.
	if got := strings.Count(gitT(t, repo, "worktree", "list", "--porcelain"), "worktree "); got != 1 {
		t.Errorf("клон завёл рабочих папок: %d", got-1)
	}
}

// Файл изменения читается из клона, а не из рабочей папки: тела pull request
// собирают после того, как папку могли убрать.
func TestShowReadsFromClone(t *testing.T) {
	m, project, task := setup(t)
	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	commit(t, ws.Dir, "постановка.md", "Цель: считать среднее.\n", "план")
	if _, err := m.Push(ws); err != nil {
		t.Fatalf("ветка не опубликована: %v", err)
	}

	body, found, err := m.Show(ws.Repo, ws.Branch, "постановка.md")
	if err != nil || !found {
		t.Fatalf("файл не прочитан: found=%v err=%v", found, err)
	}
	if !strings.Contains(body, "считать среднее") {
		t.Errorf("прочитано не то: %q", body)
	}

	// Задача, пришедшая мимо аналитика, каталога изменения не имеет — и это
	// не ошибка обвязки.
	if _, found, err := m.Show(ws.Repo, ws.Branch, "docs/changes/OFF-1/brief.md"); err != nil || found {
		t.Errorf("несуществующий файл: found=%v err=%v", found, err)
	}
}

// Уборка берёт замок уже существующей папки, не создавая её: в папке,
// где работает агент, ей делать нечего.
func TestTryLockRespectsRunningAgent(t *testing.T) {
	m, project, task := setup(t)
	ws, err := m.Ensure(task, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}

	if _, err := m.TryLock(ws); !errors.Is(err, ErrWorktreeBusy) {
		t.Fatalf("замок занятой папки взят: %v", err)
	}
	if err := ws.Unlock(); err != nil {
		t.Fatalf("замок не снят: %v", err)
	}
	locked, err := m.TryLock(ws)
	if err != nil {
		t.Fatalf("замок свободной папки не взят: %v", err)
	}
	if err := locked.Unlock(); err != nil {
		t.Errorf("замок не снят: %v", err)
	}
}
