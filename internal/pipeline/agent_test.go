package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runagent"
	"github.com/kao73/virtual-office/internal/runner"
)

// agentWorktreeFixture — bare-репозиторий и настоящий git worktree на ветке
// branch, тем же приёмом, что internal/workspace/clone_test.go: cloneOptionsFor
// зовёт workspace.CloneSource на уже готовой рабочей папке, а не заводит её сама.
func agentWorktreeFixture(t *testing.T, branch string) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "master", bare).CombinedOutput(); err != nil {
		t.Fatalf("bare-репозиторий не создан: %v\n%s", err, out)
	}
	seed := filepath.Join(root, "seed")
	if out, err := exec.Command("git", "clone", "-q", bare, seed).CombinedOutput(); err != nil {
		t.Fatalf("посевной клон не создан: %v\n%s", err, out)
	}
	env := append(os.Environ(), "GIT_AUTHOR_NAME=тест", "GIT_AUTHOR_EMAIL=test@office.local",
		"GIT_COMMITTER_NAME=тест", "GIT_COMMITTER_EMAIL=test@office.local")
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# проект\n"), 0o644); err != nil {
		t.Fatalf("README не записан: %v", err)
	}
	run(seed, "add", "-A")
	run(seed, "commit", "-q", "-m", "начало")
	run(seed, "push", "-q", "origin", "master")

	dir := filepath.Join(root, "worktree")
	run(seed, "worktree", "add", "-q", "-b", branch, dir)
	run(dir, "commit", "-q", "--allow-empty", "-m", "работа задачи")
	return dir
}

func TestCloneOptionsForLocalKeepsRealWorkdirAndNoClone(t *testing.T) {
	req := Request{Workdir: "/куда-угодно", Branch: "agent/OFF-1"}

	workdir, clone, cleanup, err := cloneOptionsFor(runagent.BackendLocal, req)
	if err != nil {
		t.Fatalf("cloneOptionsFor: %v", err)
	}
	if workdir != req.Workdir {
		t.Errorf("workdir = %q, ожидался req.Workdir %q — local обязан остаться на настоящей рабочей папке", workdir, req.Workdir)
	}
	if clone != nil {
		t.Errorf("Clone = %+v, ожидался nil на local", clone)
	}
	if cleanup == nil {
		t.Fatal("cleanup не должен быть nil даже на local — вызывающий зовёт его безусловно")
	}
	if err := cleanup(); err != nil {
		t.Errorf("cleanup на local не должна ничего чистить и не должна падать: %v", err)
	}
}

func TestCloneOptionsForSbxBuildsDisposableCloneSource(t *testing.T) {
	dir := agentWorktreeFixture(t, "agent/OFF-1")
	req := Request{Workdir: dir, Branch: "agent/OFF-1"}

	workdir, clone, cleanup, err := cloneOptionsFor(runagent.DefaultBackend, req)
	if err != nil {
		t.Fatalf("cloneOptionsFor: %v", err)
	}
	defer cleanup()

	if workdir == dir {
		t.Error("workdir не сменился на одноразовый клон-источник")
	}
	if _, err := os.Stat(workdir); err != nil {
		t.Errorf("клон-источник не найден: %v", err)
	}
	if clone == nil {
		t.Fatal("Clone не выставлен для бэкенда sbx")
	}
	if clone.FetchInto != dir {
		t.Errorf("FetchInto = %q, ожидалась настоящая рабочая папка %q", clone.FetchInto, dir)
	}
	if clone.Branch != "agent/OFF-1" {
		t.Errorf("Branch = %q, ожидалось agent/OFF-1", clone.Branch)
	}
	if len(clone.Dirs) != 2 || clone.Dirs[0] != runner.Dir || clone.Dirs[1] != ".comet/runtime" {
		t.Errorf("Dirs = %q, ожидалось [%s .comet/runtime]", clone.Dirs, runner.Dir)
	}

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("клон-источник %s не убран", workdir)
	}
}

func TestCloneOptionsForSbxPropagatesCloneSourceFailure(t *testing.T) {
	dir := agentWorktreeFixture(t, "agent/OFF-1")
	req := Request{Workdir: dir, Branch: "нет-такой-ветки"}

	if _, _, _, err := cloneOptionsFor(runagent.DefaultBackend, req); err == nil {
		t.Fatal("неудача клона-источника (несуществующая ветка) прошла без ошибки")
	}
}
