package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/workspace"
)

// cometExecutable — CLI Comet Native в PATH раннера. Архивирование — код
// раннера (см. openPR в prpass.go), а не роли: оно бежит на машине/хосте, где
// крутится office, а не в песочнице агента — отдельное требование к
// хозяйству раннера, ещё не решённое (docs/notes/sbx.md, tasks.md 1.1).
//
// var, а не const: тесты подменяют его на заведомо отсутствующее имя, чтобы
// проверить, что недостающая зависимость не блокирует PR-проход, не трогая
// PATH целиком — Ensure/Push этой же функции нужен настоящий git.
var cometExecutable = "comet"

// archiveReadyStage — значение data.loop.stage изменения Comet Native, при
// котором reviewer уже передал прошедший final-result и Archive можно
// запускать (design doc, roles/reviewer, «Outcome»: "Native's own state
// moves to archive-ready" — та фраза называет именно стадию цикла, что
// подтверждено живым прогоном `comet native status --json` при ревью Задачи
// 5), а не data.phase, у которой значения — только shape|build|verify|archive.
const archiveReadyStage = "archive-ready"

// cometStatus — часть вывода `comet native status <name> --json`, нужная
// раннеру: обёрнута в конверт {command, exitCode, data: {...}} (реальный
// формат CLI, не то, что предполагал design doc до живой проверки при
// ревью Задачи 5) — раннеру нужны только data.phase и data.loop.stage.
type cometStatus struct {
	Data struct {
		Phase string `json:"phase"`
		Loop  struct {
			Stage string `json:"stage"`
		} `json:"loop"`
	} `json:"data"`
}

// archiveIfReady запускает детерминированный шаг Archive Comet Native —
// прежде чем открыть pull request, а не после того, как человек его сольёт:
// иначе раннеру пришлось бы пушить нерецензированный коммит прямо в ветку по
// умолчанию, а сливает её только человек (DESIGN.md §2.8). До открытия PR
// архивный коммит — обычный коммит в той же ветке, которую и так предстоит
// слить.
//
// Работает не в bare-клоне, которым до сих пор обходился openPR: comet читает
// и пишет собственное состояние фазы рабочего дерева, которого у bare-клона
// нет. archiveIfReady поэтому берёт ту же рабочую папку, что и роли (Ensure
// переиспользует уже существующую, если она жива) и публикует результат тем
// же Push, каким роли публикуют свою работу — тем же путём, каким
// internal/pipeline/pipeline.go's work() уже действует после каждого прогона.
//
// ok=false просит openPR подождать следующего прохода: рабочая папка занята
// прямо сейчас (роль работает в ней), и лезть под чужой замок нельзя. Любая
// другая беда — comet не найден на этой машине, статус не читается, сама
// команда упала, рабочая папка не подготовлена — не блокирует pull request:
// это внешняя зависимость, чей бутстрап на хосте раннера этим изменением не
// решён (tasks.md 1.1), и оставлять задачи без pull request до её появления
// нельзя.
func (o *Office) archiveIfReady(task tracker.Task, project tracker.Project) (ok bool, err error) {
	ws, err := o.Workspaces.Ensure(task.Ref(), project)
	if errors.Is(err, workspace.ErrWorktreeBusy) {
		return false, nil
	}
	if err != nil {
		o.logf("%s: рабочая папка для архивирования не подготовлена: %v", task.Key, err)
		return true, nil
	}
	defer o.unlock(task.Key, ws)

	name := runner.CometChangeName(task.Key)
	status, err := cometNativeStatus(ws.Dir, name)
	if err != nil {
		o.logf("%s: comet native status не прочитан, архивирование пропущено: %v", task.Key, err)
		return true, nil
	}
	if status.Data.Loop.Stage != archiveReadyStage {
		return true, nil // Verify ещё не отдал прошедший final-result
	}

	if _, err := runComet(ws.Dir, "native", "archive", name, "--confirmed"); err != nil {
		o.logf("%s: comet native archive не выполнен: %v", task.Key, err)
		return true, nil
	}

	// Под isolation: current (единственный режим, которым пользуются эти
	// роли) `comet native archive --confirmed` делает голый fs.rename
	// каталога изменения — без git add/commit (git-плюмбинг там есть только
	// для isolation: branch/worktree, проверено по исходнику CLI при ревью
	// Задачи 5). Без коммита Workspaces.Push публиковать нечего, и весь
	// замысел "архивный коммит едет в PR" молча не имеет эффекта.
	if err := commitArchiveRename(ws.Dir, name); err != nil {
		o.logf("%s: коммит архивирования не создан: %v", task.Key, err)
		return true, nil
	}

	switch pushed, err := o.Workspaces.Push(ws); {
	case err != nil:
		o.logf("%s: коммит архивирования не опубликован: %v", task.Key, err)
	case pushed:
		o.logf("%s: изменение %s заархивировано", task.Key, name)
	default:
		o.logf("%s: архивирование %s не создало новых коммитов", task.Key, name)
	}
	return true, nil
}

// cometNativeStatus разбирает `comet native status <name> --json`.
func cometNativeStatus(dir, name string) (cometStatus, error) {
	out, err := runComet(dir, "native", "status", name, "--json")
	if err != nil {
		return cometStatus{}, err
	}
	var s cometStatus
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		return cometStatus{}, fmt.Errorf("вывод comet native status не разобран: %w", err)
	}
	return s, nil
}

// runComet выполняет comet в рабочей папке задачи и возвращает stdout.
func runComet(dir string, args ...string) (string, error) {
	cmd := exec.Command(cometExecutable, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("comet %s: %w\n%s", strings.Join(args, " "), err, exitErr.Stderr)
		}
		return "", fmt.Errorf("comet %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// archiveCommitName/archiveCommitEmail — личность коммита, которым
// archiveIfReady сам фиксирует переименование, оставленное
// `comet native archive` незакоммиченным (см. комментарий выше). Этот
// коммит не принадлежит ни одной роли: internal/workspace.gitEnv нарочно не
// задаёт личность, потому что «коммиты — работа агента, а не его обвязки»
// — но здесь обратный случай, обвязка коммитит сама, и личность нужна своя,
// того же домена office.local, что и у остальных системных участников
// (ср. GIT_AUTHOR_NAME/EMAIL в cmd/eval-roles/fixture.go).
const (
	archiveCommitName  = "comet-archive"
	archiveCommitEmail = "comet-archive@office.local"
)

// commitArchiveRename коммитит то, что `comet native archive --confirmed`
// оставил в рабочей папке при isolation: current голым fs.rename. Рабочая
// папка чистая — коммитить нечего, и это не ошибка: например, переименование
// могло не оставить изменений (изменение уже было закоммичено раньше).
func commitArchiveRename(dir, name string) error {
	dirty, err := gitDirty(dir)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	if err := gitArchive(dir, "add", "-A"); err != nil {
		return err
	}
	return gitArchive(dir, "commit", "-m", "chore: archive Comet Native change "+name)
}

// gitDirty — есть ли в рабочей папке незакоммиченные изменения.
func gitDirty(dir string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, fmt.Errorf("git status: %w\n%s", err, exitErr.Stderr)
		}
		return false, fmt.Errorf("git status: %w", err)
	}
	return len(out) > 0, nil
}

// gitArchive выполняет git-команду коммита архивирования от лица раннера, а
// не роли — тем же паттерном, что и runComet (cmd.Dir вместо "-C", stderr
// в ошибку при неудаче), но с собственной личностью коммита через окружение.
func gitArchive(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+archiveCommitName, "GIT_AUTHOR_EMAIL="+archiveCommitEmail,
		"GIT_COMMITTER_NAME="+archiveCommitName, "GIT_COMMITTER_EMAIL="+archiveCommitEmail)
	if _, err := cmd.Output(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, exitErr.Stderr)
		}
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}
