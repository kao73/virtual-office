package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	wantHead := gitT(t, dir, "rev-parse", "origin/master")

	clone, cleanup, err := CloneSource(context.Background(), dir, "agent/OFF-1")
	if err != nil {
		t.Fatalf("CloneSource: %v", err)
	}
	defer cleanup()

	// В самом клоне-источнике это локальная ветка master, не origin/master —
	// именно локальной веткой она обязана быть здесь, чтобы пережить ещё
	// один clone внутрь песочницы (см. TestCloneSourceDefaultBranchSurvivesInnerClone
	// и doc-комментарий fetchDefaultRemote).
	if got, err := exec.Command("git", "-C", clone, "rev-parse", "--verify", "master").Output(); err != nil {
		t.Errorf("master не разрешена в клоне-источнике: %v", err)
	} else if got := strings.TrimSpace(string(got)); got != wantHead {
		t.Errorf("master = %s, ожидалось %s (тот же коммит, что origin/master в источнике)", got, wantHead)
	}
	if got := gitT(t, clone, "rev-parse", "--abbrev-ref", "HEAD"); got != "agent/OFF-1" {
		t.Errorf("клон стоит на ветке %q, ожидалась agent/OFF-1 — заведённая master-ветка не должна перебивать checkout", got)
	}
}

// Blocker-находка независимого ревью (round 1), воспроизведена вживую до
// правки: fetchDefaultRemote заводила в клоне-источнике refs/remotes/
// origin/* — но `sbx create --clone` сам заводит внутри контейнера ещё один
// обычный `git clone` этого клона-источника, а обычный clone по той же
// причине не переносит чужие refs/remotes/* дальше. Ветка чинилась не на том
// слое: origin/master пережил бы клон-источник, но не пережил бы следующий
// клон внутрь песочницы. Локальная ветка (refs/heads/<default>) — то, что
// именно такой второй clone сам превращает в свой origin/<default>; здесь
// это проверяется третьим, симулирующим сам clone-источник шагом.
func TestCloneSourceDefaultBranchSurvivesInnerClone(t *testing.T) {
	dir := managerRepoStyleWorktree(t, "agent/OFF-1")

	clone, cleanup, err := CloneSource(context.Background(), dir, "agent/OFF-1")
	if err != nil {
		t.Fatalf("CloneSource: %v", err)
	}
	defer cleanup()

	inner := filepath.Join(t.TempDir(), "inner")
	if out, err := exec.Command("git", "clone", "-q", clone, inner).CombinedOutput(); err != nil {
		t.Fatalf("симулированный внутренний клон (sbx create --clone) не заведён: %v\n%s", err, out)
	}
	if _, err := exec.Command("git", "-C", inner, "rev-parse", "--verify", "origin/master").Output(); err != nil {
		t.Errorf("origin/master не пережил внутренний клон (симулирует sbx create --clone): %v", err)
	}
	if got := gitT(t, inner, "rev-parse", "--abbrev-ref", "HEAD"); got != "agent/OFF-1" {
		t.Errorf("внутренний клон стоит на ветке %q, ожидалась agent/OFF-1 — заведённая master-ветка не должна была перебить checkout", got)
	}
}

// Blocker-находка независимого ревью (round 1), воспроизведена вживую до
// правки: первая версия этой правки накладывала незакоммиченную работу
// патчем прямо на клон, оставляя dir грязным, — и следующий за прогоном
// fetchBranch's `git merge --ff-only` в dir отказывал на «local changes
// would be overwritten by merge», унося уже настоящую работу агента
// в снесённую песочницу. CloneSource обязана оставить dir чистым.
func TestCloneSourceCommitsUncommittedTrackedChange(t *testing.T) {
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
	// dir обязан стать чистым — иначе следующий fetchBranch's merge --ff-only
	// откажет на «local changes would be overwritten» (независимое ревью).
	if status := gitT(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("источник остался грязным после CloneSource: %q — следующий merge --ff-only откажет", status)
	}
	if author := gitT(t, dir, "log", "-1", "--format=%an"); author != cloneSweepName {
		t.Errorf("коммит-подчистка приписана не той личности: %q, ожидалось %q", author, cloneSweepName)
	}
}

// То же самое, но для файла, которого нет в git вовсе (не только
// незакоммиченного, а никогда не заведённого `git add`).
func TestCloneSourceCommitsUntrackedFile(t *testing.T) {
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
	if status := gitT(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("источник остался грязным после CloneSource: %q", status)
	}
}

// Незавершённое слияние в dir — commitUncommitted не имеет права коммитить
// поверх него (зафиксировала бы конфликтные маркеры как разрешённые) и молча
// пропускает сохранение, тем же рассуждением, что и commitLeftovers внутри
// песочницы. CloneSource при этом всё равно успевает: `git clone --branch`
// клонирует последний настоящий коммит той же ветки (сама попытка слияния
// коммита не создала), не трогая рабочее дерево base вовсе.
func TestCloneSourceLeavesRealMergeConflictAlone(t *testing.T) {
	base := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "-b", "master", base).CombinedOutput(); err != nil {
		t.Fatalf("репозиторий не создан: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(base, "f.txt"), []byte("база\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, base, "add", "f.txt")
	gitT(t, base, "commit", "-q", "-m", "seed")
	gitT(t, base, "checkout", "-q", "-b", "a")
	if err := os.WriteFile(filepath.Join(base, "f.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, base, "add", "f.txt")
	gitT(t, base, "commit", "-q", "-m", "a-версия")

	gitT(t, base, "checkout", "-q", "master")
	gitT(t, base, "checkout", "-q", "-b", "agent/OFF-1")
	if err := os.WriteFile(filepath.Join(base, "f.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitT(t, base, "add", "f.txt")
	gitT(t, base, "commit", "-q", "-m", "b-версия")

	mergeCmd := exec.Command("git", "-C", base, "merge", "a")
	_ = mergeCmd.Run() // ожидаемо ненулевой код — конфликт

	if _, err := os.Stat(filepath.Join(base, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("подготовка теста: конфликт слияния не начался: %v", err)
	}

	clone, cleanup, err := CloneSource(context.Background(), base, "agent/OFF-1")
	if err != nil {
		t.Fatalf("CloneSource: %v", err)
	}
	defer cleanup()

	if _, err := os.Stat(filepath.Join(base, ".git", "MERGE_HEAD")); err != nil {
		t.Error("MERGE_HEAD пропал — CloneSource тронула незавершённое слияние")
	}
	if status := gitT(t, base, "status", "--porcelain"); !strings.Contains(status, "UU") {
		t.Errorf("конфликт в base разрешён/потерян сам собой: %q", status)
	}
	if got := gitT(t, clone, "log", "-1", "--format=%s"); got != "b-версия" {
		t.Errorf("клон стоит не на последнем настоящем коммите: %q, ожидалось «b-версия»", got)
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
