package workspace

import (
	"context"
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

	clone, cleanup, err := CloneSource(context.Background(), dir, "agent/OFF-1")
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

	clone, cleanup, err := CloneSource(context.Background(), dir, "agent/OFF-1")
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

	path, cleanup, err := CloneSource(context.Background(), dir, "нет-такой-ветки")
	if err == nil {
		t.Fatal("клонирование несуществующей ветки прошло без ошибки")
	}
	if path != "" || cleanup != nil {
		t.Errorf("на ошибке ожидались нулевые path/cleanup, получено path=%q cleanup=not-nil", path)
	}
}

// Minor-находка независимого ревью: предыдущий тест проверял только форму
// возврата (path/cleanup нулевые), а не то, что временный каталог
// os.MkdirTemp реально убран с диска, а не просто стал недостижим для
// вызывающего.
func TestCloneSourceRemovesTempDirOnMissingBranch(t *testing.T) {
	_, dir := worktreeFixture(t, "agent/OFF-1")

	before, err := filepath.Glob(filepath.Join(os.TempDir(), clonePrefix))
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := CloneSource(context.Background(), dir, "нет-такой-ветки"); err == nil {
		t.Fatal("клонирование несуществующей ветки прошло без ошибки")
	}

	after, err := filepath.Glob(filepath.Join(os.TempDir(), clonePrefix))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) > len(before) {
		t.Errorf("временный каталог клона-источника не убран на ошибке: было %v, стало %v", before, after)
	}
}

// managerRepoStyleWorktree заводит worktree над bare-репозиторием, устроенным
// ровно так, как заводит его Manager.repo (init --bare + remote add + fetch,
// а не clone --bare/push) — в отличие от worktreeFixture выше, где push в
// bare оставляет там refs/heads/master. У Manager.repo ветка по умолчанию
// живёт только в refs/remotes/origin/*, никогда в refs/heads/* — эта разница
// в форме bare-репозитория ровно то, что решает исход TestCloneSourceIncludesDefaultBranchRef
// (независимое ревью: существующие фикстуры её не воспроизводили).
func managerRepoStyleWorktree(t *testing.T, branch string) (dir string) {
	t.Helper()
	root := t.TempDir()

	upstream := filepath.Join(root, "upstream")
	if out, err := exec.Command("git", "init", "-q", "-b", "master", upstream).CombinedOutput(); err != nil {
		t.Fatalf("upstream не создан: %v\n%s", err, out)
	}
	gitT(t, upstream, "commit", "-q", "--allow-empty", "-m", "начало")

	bare := filepath.Join(root, "task.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "master", bare).CombinedOutput(); err != nil {
		t.Fatalf("bare не создан: %v\n%s", err, out)
	}
	gitT(t, bare, "remote", "add", "origin", upstream)
	gitT(t, bare, "fetch", "-q", "--prune", "origin")

	dir = filepath.Join(root, "worktree")
	gitT(t, bare, "worktree", "add", "-q", "-b", branch, dir)
	gitT(t, dir, "commit", "-q", "--allow-empty", "-m", "работа задачи")
	return dir
}

// Blocker-находка независимого ревью, воспроизведена вживую до правки:
// обычный `git clone --branch` переносит только refs/heads/* источника
// (сюда — как refs/remotes/origin/<branch>), а не собственные refs/remotes/*
// источника. В задачном bare-репозитории (Manager.repo) ветка по умолчанию
// живёт только как refs/remotes/origin/<default> — без явного переноса
// клон-источник не может разрешить origin/master вовсе, а именно на ней
// стоят reviewer (`git diff origin/<...>...HEAD`) и implementer
// (`git merge origin/<...>`).
func TestCloneSourceIncludesDefaultBranchRef(t *testing.T) {
	dir := managerRepoStyleWorktree(t, "agent/OFF-1")

	clone, cleanup, err := CloneSource(context.Background(), dir, "agent/OFF-1")
	if err != nil {
		t.Fatalf("CloneSource: %v", err)
	}
	defer cleanup()

	if _, err := exec.Command("git", "-C", clone, "rev-parse", "--verify", "origin/master").Output(); err != nil {
		t.Errorf("origin/master не разрешён в клоне-источнике: %v", err)
	}
}

// Important-находка независимого ревью: рабочая папка задачи переиспользуется
// между прогонами (Manager.Ensure, «переиспользование — не оптимизация,
// а требование»), и на входе в ней рутинно уже лежит чужая незакоммиченная
// правка (internal/runner/trace.go, hasNewDirt). Обычный `git clone`
// переносит только закоммиченное — без явного переноса эта правка молча
// осталась бы на хосте, и агент внутри песочницы никогда её не увидел бы.
func TestCloneSourceOverlaysUncommittedTrackedChange(t *testing.T) {
	_, dir := worktreeFixture(t, "agent/OFF-1")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# незакоммиченная правка\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	clone, cleanup, err := CloneSource(context.Background(), dir, "agent/OFF-1")
	if err != nil {
		t.Fatalf("CloneSource: %v", err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(clone, "README.md"))
	if err != nil {
		t.Fatalf("README.md не найден в клоне: %v", err)
	}
	if string(got) != "# незакоммиченная правка\n" {
		t.Errorf("README.md = %q, ожидалась незакоммиченная правка", got)
	}
	// Источник не тронут: CloneSource не имеет права мутировать индекс
	// переиспользуемой рабочей папки.
	if status := gitT(t, dir, "status", "--porcelain"); status == "" {
		t.Error("источник перестал быть грязным после CloneSource — индекс/дерево тронуты")
	}
}

// То же самое, но для файла, которого нет в git вовсе (не только
// незакоммиченного, а никогда не заведённого `git add`): `git diff HEAD`
// такой файл не покажет, и перенос ему нужен отдельным путём.
func TestCloneSourceOverlaysUntrackedFile(t *testing.T) {
	_, dir := worktreeFixture(t, "agent/OFF-1")
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("совсем новый файл\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	clone, cleanup, err := CloneSource(context.Background(), dir, "agent/OFF-1")
	if err != nil {
		t.Fatalf("CloneSource: %v", err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(clone, "new.txt"))
	if err != nil {
		t.Fatalf("new.txt не найден в клоне: %v", err)
	}
	if string(got) != "совсем новый файл\n" {
		t.Errorf("new.txt = %q, ожидалось «совсем новый файл»", got)
	}
}

// Файлы, исключённые git-правилами (.agent, .comet/runtime — та же
// договорённость, что ExcludeAgentDir/ExcludeCometRuntime), переносит
// отдельно и по-другому cloneSyncIn уже внутри песочницы — сюда попадать
// не должны, иначе они уедут не в тот момент и не тем путём.
func TestCloneSourceSkipsExcludedUntracked(t *testing.T) {
	_, dir := worktreeFixture(t, "agent/OFF-1")
	// dir — worktree: .git там файл-ссылка, а не каталог, и info/exclude
	// лежит в общем git-каталоге (та же оговорка, что у resolveExcludeFile
	// в internal/backends/sbx/clone.go).
	common := gitT(t, dir, "rev-parse", "--git-common-dir")
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	if err := os.MkdirAll(filepath.Join(common, "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(common, "info", "exclude"), []byte("/.agent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agent", "result.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	clone, cleanup, err := CloneSource(context.Background(), dir, "agent/OFF-1")
	if err != nil {
		t.Fatalf("CloneSource: %v", err)
	}
	defer cleanup()

	if _, err := os.Stat(filepath.Join(clone, ".agent")); !os.IsNotExist(err) {
		t.Error(".agent перенесён в клон-источник — его обязана переносить только cloneSyncIn внутри песочницы")
	}
}

// Ревью на C5: отменённый контекст обязан остановить CloneSource немедленно,
// а не провалиться на реальный таймаут где-то в другом месте — тем же
// приёмом, что и TestResolveExcludeFileRespectsContext в internal/backends/sbx.
func TestCloneSourceRespectsContext(t *testing.T) {
	_, dir := worktreeFixture(t, "agent/OFF-1")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := CloneSource(ctx, dir, "agent/OFF-1"); err == nil {
		t.Fatal("отменённый контекст не остановил git clone — CloneSource не читает ctx")
	}
}
