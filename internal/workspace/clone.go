package workspace

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// clonePrefix — префикс временных каталогов одноразового клона-источника,
// который заводит CloneSource для internal/pipeline/agent.go (SandboxAgent.Run,
// бэкенд sbx). Простой os.MkdirTemp вне хозяйства Manager: клон одноразовый,
// целиком свой одному прогону и не нуждается в общей уборке репозиториев/
// worktree'ев, которой занимается остальной этот пакет.
const clonePrefix = "pipeline-clone-*"

// CloneSource заводит одноразовый, обычный (не bare, не worktree) git-клон
// ветки branch рабочей папки dir в новый временный каталог, перенося в него
// и текущее незакоммиченное состояние dir. dir может быть как обычным
// репозиторием, так и worktree'ем — git разрешает оба как источник клона
// через .git-файл/-каталог одинаково; branch — уже выкаченная в dir ветка
// задачи (то же значение, что несёт ws.Branch/req.Branch дальше по конвейеру).
//
// `sbx create --clone` сам отказывает и на bare-репозитории, и на worktree
// как на своём первичном пути (см. internal/backends/sbx/clone.go, doc-
// комментарий excludeFile) — клон, сделанный здесь, всегда обычный
// репозиторий со своим .git-каталогом и годится туда без исключений.
//
// Перенос незакоммиченного нужен потому, что рабочая папка задачи
// переиспользуется между прогонами (Manager.Ensure, «переиспользование —
// не оптимизация, а требование: после reap ... задача возвращается к той же
// незаконченной работе»), и на входе в ней рутинно уже лежит чужая
// незакоммиченная правка (internal/runner/trace.go, hasNewDirt: «рабочая
// папка переиспользуется, и чужая незакоммиченная правка лежит в ней ещё
// до первого шага роли»). Обычный `git clone` переносит только
// закоммиченное — без явного переноса эта правка молча остаётся на хосте,
// и агент внутри песочницы никогда её не увидит (независимое ревью).
//
// Возвращает путь к клону и функцию уборки, которую вызывающий обязан звать
// при любом исходе (успех, ошибка агента, таймаут) — тем же приёмом «cleanup
// всегда», каким cloneSyncOut убирает саму песочницу (internal/backends/sbx/
// sbx.go, defer remove(name)).
//
// Ошибка означает, что клон не состоялся: временный каталог в этом случае
// уже убран этой же функцией, и вызывающему чистить нечего — path и cleanup
// оба нулевые, звать cleanup(nil) было бы паникой.
func CloneSource(ctx context.Context, dir, branch string) (string, func() error, error) {
	tmp, err := os.MkdirTemp("", clonePrefix)
	if err != nil {
		return "", nil, fmt.Errorf("временный каталог клона-источника не заведён: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(tmp) }

	// Абсолютный путь: fetchDefaultRemote ниже зовёт git с -C tmp, и dir как
	// её позиционный аргумент разрешался бы уже относительно tmp, а не
	// текущего каталога процесса — измерено вживую в разработке этой правки.
	absDir, err := filepath.Abs(dir)
	if err != nil {
		_ = cleanup()
		return "", nil, fmt.Errorf("путь %s не разрешён: %w", dir, err)
	}

	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--branch", branch, absDir, tmp)
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = cleanup()
		return "", nil, fmt.Errorf("клон-источник (%s, ветка %s) не заведён: %w\n%s", absDir, branch, err, out)
	}

	if err := fetchDefaultRemote(ctx, absDir, tmp); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	if err := overlayUncommitted(ctx, absDir, tmp); err != nil {
		_ = cleanup()
		return "", nil, err
	}

	return tmp, cleanup, nil
}

// fetchDefaultRemote переносит в клон-источник tmp собственные
// refs/remotes/origin/* источника dir — то, что обычный `git clone`
// не переносит вовсе (он превращает refs/heads/* источника в
// refs/remotes/origin/* клона, а не копирует уже готовые refs/remotes/*
// источника). Ветка проекта по умолчанию в задачном bare-репозитории
// (Manager.repo) заведена именно так, через `init --bare` + `remote add` +
// `fetch`, и живёт только в refs/remotes/origin/*, никогда в refs/heads/* —
// без этого переноса клон-источник не может разрешить даже
// `origin/<ветка по умолчанию>` вовсе, а именно на ней стоит
// `git diff origin/<...>...HEAD` роли reviewer и `git merge origin/<...>`
// роли implementer (независимое ревью, воспроизведено вживую).
func fetchDefaultRemote(ctx context.Context, dir, tmp string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", tmp, "fetch", "--quiet", dir,
		"+refs/remotes/origin/*:refs/remotes/origin/*")
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ветки origin/* не перенесены в клон-источник: %w\n%s", err, out)
	}
	return nil
}

// overlayUncommitted переносит в клон-источник tmp незакоммиченное
// состояние dir: правку отслеживаемых файлов (staged и unstaged разом,
// через патч `git diff HEAD`) и неотслеживаемые, но не игнорируемые файлы
// (через `git ls-files --others --exclude-standard» — то же определение
// «грязи», каким уже пользуется internal/runner.WorktreeStatus/hasNewDirt
// с флагом -uall). Файлы, исключённые правилами git (.agent, .comet/runtime —
// ExcludeAgentDir/ExcludeCometRuntime), в это множество не попадают и здесь
// не переносятся: их отдельно и по-другому переносит cloneSyncIn
// (internal/backends/sbx/clone.go) уже внутри песочницы.
//
// Обе git-команды — только на чтение dir: CloneSource не вправе трогать
// индекс переиспользуемой рабочей папки, которую в это же время может
// готовить или обходить другой процесс.
func overlayUncommitted(ctx context.Context, dir, tmp string) error {
	diff, err := exec.CommandContext(ctx, "git", "-C", dir, "diff", "--binary", "HEAD").Output()
	if err != nil {
		return fmt.Errorf("незакоммиченная правка %s не прочитана: %w", dir, err)
	}
	if len(diff) > 0 {
		apply := exec.CommandContext(ctx, "git", "apply")
		apply.Dir = tmp
		apply.Env = gitEnv()
		apply.Stdin = bytes.NewReader(diff)
		if out, err := apply.CombinedOutput(); err != nil {
			return fmt.Errorf("незакоммиченная правка не перенесена в клон-источник: %w\n%s", err, out)
		}
	}

	untracked, err := untrackedFiles(ctx, dir)
	if err != nil {
		return err
	}
	for _, rel := range untracked {
		if err := copyUntracked(filepath.Join(dir, rel), filepath.Join(tmp, rel)); err != nil {
			return fmt.Errorf("незакоммиченный файл %s не перенесён в клон-источник: %w", rel, err)
		}
	}
	return nil
}

// untrackedFiles — незакоммиченные, но не игнорируемые файлы dir.
// core.quotepath=false — по той же причине, что и у WorktreeStatus
// (internal/runner/input.go): без него git отдаёт кириллицу восьмеричными
// escape-последовательностями, и путь перестаёт совпадать с настоящим.
func untrackedFiles(ctx context.Context, dir string) ([]string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir,
		"-c", "core.quotepath=false", "ls-files", "--others", "--exclude-standard").Output()
	if err != nil {
		return nil, fmt.Errorf("незакоммиченные файлы %s не перечислены: %w", dir, err)
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// copyUntracked копирует один неотслеживаемый файл src (внутри dir) на то же
// относительное место dst внутри клона-источника, сохраняя права доступа.
func copyUntracked(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}
