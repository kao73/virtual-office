package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// worktreeFixture заводит bare-репозиторий и настоящий git worktree на ветке
// branch — тем же приёмом, что setup() в workspace_test.go, но без Manager:
// CloneSource работает над уже готовой рабочей папкой, а не заводит её сама.
func worktreeFixture(t *testing.T, branch string) (bare, dir string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "origin.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "master", bare).CombinedOutput(); err != nil {
		t.Fatalf("bare-репозиторий не создан: %v\n%s", err, out)
	}

	seed := filepath.Join(root, "seed")
	if out, err := exec.Command("git", "clone", "-q", bare, seed).CombinedOutput(); err != nil {
		t.Fatalf("посевной клон не создан: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("# проект\n"), 0o644); err != nil {
		t.Fatalf("README не записан: %v", err)
	}
	gitT(t, seed, "add", "-A")
	gitT(t, seed, "commit", "-q", "-m", "начало")
	gitT(t, seed, "push", "-q", "origin", "master")

	dir = filepath.Join(root, "worktree")
	gitT(t, seed, "worktree", "add", "-q", "-b", branch, dir)
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "работа задачи")
	return bare, dir
}

func TestCloneSourceClonesCurrentBranchTip(t *testing.T) {
	_, dir := worktreeFixture(t, "agent/OFF-1")
	want := gitT(t, dir, "rev-parse", "HEAD")

	clone, cleanup, err := CloneSource(dir, "agent/OFF-1")
	if err != nil {
		t.Fatalf("CloneSource: %v", err)
	}
	defer cleanup()

	if got := gitT(t, clone, "rev-parse", "--abbrev-ref", "HEAD"); got != "agent/OFF-1" {
		t.Errorf("клон стоит на ветке %q, ожидалось agent/OFF-1", got)
	}
	if got := gitT(t, clone, "rev-parse", "HEAD"); got != want {
		t.Errorf("клон стоит на %s, ожидалось %s (тот же коммит, что и в worktree)", got, want)
	}
	// Обычный репозиторий, не worktree: у клона свой собственный .git-каталог,
	// а не файл-ссылка, как у worktree — --clone (sbx) отказывает на worktree,
	// и клон-источник обязан не быть им.
	info, err := os.Stat(filepath.Join(clone, ".git"))
	if err != nil {
		t.Fatalf(".git клона не найден: %v", err)
	}
	if !info.IsDir() {
		t.Error("клон оказался worktree'ем (.git — файл, не каталог), а обязан быть обычным репозиторием")
	}
}

func TestCloneSourceCleanupRemovesTempDir(t *testing.T) {
	_, dir := worktreeFixture(t, "agent/OFF-1")

	clone, cleanup, err := CloneSource(dir, "agent/OFF-1")
	if err != nil {
		t.Fatalf("CloneSource: %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Errorf("временный каталог клона %s не убран", clone)
	}
}

// Отсутствие ветки не должно оставлять на диске частичный или пустой клон:
// git clone --branch на несуществующей ветке отказывает сам, и CloneSource
// обязана донести эту ошибку, а не тихо вернуть путь до пустоты.
func TestCloneSourcePropagatesErrorOnMissingBranch(t *testing.T) {
	_, dir := worktreeFixture(t, "agent/OFF-1")

	path, cleanup, err := CloneSource(dir, "нет-такой-ветки")
	if err == nil {
		t.Fatal("клонирование несуществующей ветки прошло без ошибки")
	}
	if path != "" || cleanup != nil {
		t.Errorf("на ошибке ожидались нулевые path/cleanup, получено path=%q cleanup=not-nil", path)
	}
}
