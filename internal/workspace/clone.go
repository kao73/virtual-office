package workspace

import (
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

// cloneSweepName/cloneSweepEmail — личность коммита, которым commitUncommitted
// сохраняет незакоммиченный остаток dir перед клонированием — тем же приёмом,
// что cloneSweepName/cloneSweepEmail в internal/backends/sbx/clone.go (своя
// пара констант, не общая: та же логика, что у archiveCommitName/
// archiveCommitEmail в internal/pipeline/archive.go — отдельная личность на
// отдельную сеть безопасности, а не одна на всех). Не решение роли, а сеть
// безопасности обвязки — в git blame это обязано быть видно как таковое.
const (
	cloneSweepName  = "clone-sweep"
	cloneSweepEmail = "clone-sweep@office.local"
)

// CloneSource заводит одноразовый, обычный (не bare, не worktree) git-клон
// ветки branch рабочей папки dir в новый временный каталог. dir может быть
// как обычным репозиторием, так и worktree'ем — git разрешает оба как
// источник клона через .git-файл/-каталог одинаково; branch — уже выкаченная
// в dir ветка задачи (то же значение, что несёт ws.Branch/req.Branch дальше
// по конвейеру).
//
// `sbx create --clone` сам отказывает и на bare-репозитории, и на worktree
// как на своём первичном пути (см. internal/backends/sbx/clone.go, doc-
// комментарий excludeFile) — клон, сделанный здесь, всегда обычный
// репозиторий со своим .git-каталогом и годится туда без исключений.
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
	// Абсолютный путь: fetchDefaultRemote ниже зовёт git с -C tmp, и dir как
	// её позиционный аргумент разрешался бы уже относительно tmp, а не
	// текущего каталога процесса — измерено вживую в разработке этой правки.
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", nil, fmt.Errorf("путь %s не разрешён: %w", dir, err)
	}

	// Прежде чем клонировать: рабочая папка задачи переиспользуется между
	// прогонами (Manager.Ensure, «переиспользование — не оптимизация, а
	// требование»), и хотя под --clone агент больше не пишет в dir напрямую
	// (он работает в одноразовом клоне ниже), незакоммиченная правка там
	// всё равно может остаться — например, при переключении бэкенда между
	// прогонами одной задачи (internal/runner/trace.go, hasNewDirt: «рабочая
	// папка переиспользуется, и чужая незакоммиченная правка лежит в ней ещё
	// до первого шага роли»). `git clone` переносит только закоммиченное:
	// без явного сохранения такая правка молча осталась бы на хосте, и
	// клон-источник её не увидел бы вовсе.
	//
	// Коммит на хосте, а не оверлей патчем поверх уже сделанного клона:
	// первая версия этой правки накладывала патч прямо на клон, оставляя dir
	// грязным, — и следующий за прогоном `fetchBranch`'s `git merge
	// --ff-only` в dir отказывал на «local changes would be overwritten by
	// merge», унося в снесённую песочницу уже настоящую, состоявшуюся работу
	// агента (независимое ревью, воспроизведено вживую: та самая потеря
	// работы, которую весь этот путь обязан предотвращать, только с другой
	// стороны). Коммит здесь оставляет dir чистым до клонирования — clone
	// видит уже закоммиченное, а fetchBranch потом просто перематывает
	// дальше тот же, теперь чистый, dir.
	if err := commitUncommitted(ctx, absDir); err != nil {
		return "", nil, fmt.Errorf("незакоммиченная работа в %s не сохранена перед клонированием: %w", absDir, err)
	}

	tmp, err := os.MkdirTemp("", clonePrefix)
	if err != nil {
		return "", nil, fmt.Errorf("временный каталог клона-источника не заведён: %w", err)
	}
	cleanup := func() error { return os.RemoveAll(tmp) }

	cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", "--branch", branch, absDir, tmp)
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = cleanup()
		return "", nil, fmt.Errorf("клон-источник (%s, ветка %s) не заведён: %w\n%s", absDir, branch, err, out)
	}

	if err := fetchDefaultRemote(ctx, absDir, tmp, branch); err != nil {
		_ = cleanup()
		return "", nil, err
	}

	return tmp, cleanup, nil
}

// commitUncommitted сохраняет отслеживаемую и неотслеживаемую-но-не-
// игнорируемую незакоммиченную правку dir отдельным коммитом от служебной
// личности cloneSweepName/cloneSweepEmail — тем же приёмом, что уже
// применяют commitLeftovers (internal/backends/sbx/clone.go, внутри
// песочницы после прогона) и EnsureCometHookAllowPaths
// (internal/runner/input.go, на хосте до прогона): сеть безопасности
// обвязки, а не решение роли, и в git blame это обязано быть видно.
//
// Незавершённое слияние (или rebase/cherry-pick/revert — те же неразрешённые
// записи индекса без MERGE_HEAD конкретно, но со своим файлом-маркером) не
// трогаем вовсе — тем же рассуждением, что и у commitLeftovers: коммит
// поверх зафиксировал бы конфликтные маркеры как разрешённые, а порча ветки
// задачи хуже, чем не спасти незакоммиченное в этом одном случае.
//
// Коммит-подчистка, сделанная здесь, но так и не подтянутая обратно (sbx
// create упал, контекст отменён до конца прогона), остаётся в ветке задачи
// навсегда и уедет в origin следующим успешным push — осознанная цена:
// откатывать её было бы отдельной, куда более рискованной операцией
// (независимое ревью, round 2).
func commitUncommitted(ctx context.Context, dir string) error {
	for _, marker := range []string{"MERGE_HEAD", "REBASE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD"} {
		if _, err := os.Stat(filepath.Join(dir, ".git", marker)); err == nil {
			return nil // незавершённая git-операция — не трогаем
		}
	}

	unmergedCmd := exec.CommandContext(ctx, "git", "-C", dir, "ls-files", "--unmerged")
	unmergedCmd.Env = gitEnv()
	unmerged, err := unmergedCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("незавершённое слияние в %s не проверено: %w\n%s", dir, err, unmerged)
	}
	if len(unmerged) > 0 {
		return nil
	}

	statusCmd := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain")
	statusCmd.Env = gitEnv()
	status, err := statusCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("состояние %s не проверено: %w\n%s", dir, err, status)
	}
	if len(status) == 0 {
		return nil // дерево чистое — нечего сохранять
	}

	addCmd := exec.CommandContext(ctx, "git", "-C", dir, "add", "-A", "--", ".")
	addCmd.Env = gitEnv()
	if out, err := addCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("незакоммиченная работа в %s не занесена в индекс: %w\n%s", dir, err, out)
	}

	// Между add и commit — отдельная проверка: `status --porcelain` непуст
	// не значит, что `add -A` что-то реально занесло (грязный сабмодуль —
	// живой пример, `add` его не подхватывает вовсе) — тот же прецедент,
	// что и у commitLeftovers/stagedCheckScript (internal/backends/sbx/
	// clone.go). Без неё `git commit` ответил бы «nothing to commit»,
	// и CloneSource провалила бы прогон там, где спасать было нечего.
	if err := exec.CommandContext(ctx, "git", "-C", dir, "diff", "--cached", "--quiet").Run(); err == nil {
		return nil // после add ничего не застейджено — нечего коммитить, не беда
	}

	commitCmd := exec.CommandContext(ctx, "git", "-C", dir, "commit", "-q", "--no-verify",
		"-m", "chore: preserve worktree changes before --clone")
	commitCmd.Env = append(gitEnv(),
		"GIT_AUTHOR_NAME="+cloneSweepName, "GIT_AUTHOR_EMAIL="+cloneSweepEmail,
		"GIT_COMMITTER_NAME="+cloneSweepName, "GIT_COMMITTER_EMAIL="+cloneSweepEmail)
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("незакоммиченная работа в %s не сохранена: %w\n%s", dir, err, out)
	}
	return nil
}

// fetchDefaultRemote заводит в клоне-источнике tmp локальные ветки
// (refs/heads/<имя>), зеркальные собственным refs/remotes/origin/<имя>
// источника dir, кроме символической origin/HEAD. Ветка проекта по
// умолчанию в задачном bare-репозитории (Manager.repo) заведена через
// `init --bare` + `remote add` + `fetch» и живёт только в
// refs/remotes/origin/<default>, никогда в refs/heads/<default> — обычный
// `git clone --branch» этого не видит вовсе (он переносит только
// refs/heads/* источника), и без переноса ни `git diff origin/...» роли
// reviewer, ни `git merge origin/...» роли implementer не находят опору.
//
// Именно локальными ветками, не refs/remotes/origin/* самого tmp:
// `sbx create --clone tmp` сам заводит внутри контейнера ещё один обычный
// `git clone» этого tmp — а обычный clone по той же причине переносит
// только refs/heads/* источника, не его refs/remotes/*. Заведи здесь
// refs/remotes/origin/* у tmp — она пережила бы этот шаг, но не пережила
// бы следующий, тот самый (независимое ревью, round 1: фикс чинил не тот
// слой). Локальная ветка, наоборот, — это ровно то, что контейнерный
// git clone сам превращает в свой origin/<имя>, и происходит она
// добавочно: агентская агент/OFF-N остаётся текущей веткой без изменений,
// у new-branch'ей просто нет собственного checkout.
//
// branch — ветка задачи, уже выложенная в tmp как refs/heads/<branch>
// (git clone --branch перед этим вызовом): исключается из переноса. Не
// исключи её, и на любом прогоне после первого — как только ветка задачи
// сама попадёт в refs/remotes/origin/* источника (pipeline.go пушит её
// после каждого прогона, Manager.Ensure фетчит origin при каждом заходе) —
// git fetch целиком откажет: «refusing to fetch into branch ... checked out
// at ...» (git не позволяет обновить fetch'ем ветку, выкаченную в целевом
// репозитории прямо сейчас) — и вместе с ней теряется и origin/<default>,
// ради которой этот перенос вообще существует (независимое ревью, round 2,
// воспроизведено вживую).
//
// Проверено вживую (тремя уровнями клонов: bare → tmp → симулированная
// песочница) в процессе разработки этой правки.
func fetchDefaultRemote(ctx context.Context, dir, tmp, branch string) error {
	listCmd := exec.CommandContext(ctx, "git", "-C", dir,
		"for-each-ref", "--format=%(refname)", "refs/remotes/origin")
	listCmd.Env = gitEnv()
	out, err := listCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ветки origin/* источника %s не перечислены: %w\n%s", dir, err, out)
	}

	var refspecs []string
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		name := strings.TrimPrefix(line, "refs/remotes/origin/")
		if name == "" || name == "HEAD" || name == branch {
			continue // символическая origin/HEAD, либо уже выложенная ветка задачи
		}
		refspecs = append(refspecs, fmt.Sprintf("+refs/remotes/origin/%s:refs/heads/%s", name, name))
	}
	if len(refspecs) == 0 {
		return nil
	}

	args := append([]string{"-C", tmp, "fetch", "--quiet", dir}, refspecs...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ветки origin/* не перенесены в клон-источник: %w\n%s", err, out)
	}
	return nil
}
