package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
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

// archiveReadyPhase — значение фазы Comet Native изменения, при котором
// reviewer уже передал прошедший final-result и Archive можно запускать:
// "Native's own state moves to archive-ready, which the runner's PR pass
// reads before opening the PR" (design doc, roles/reviewer, «Outcome»).
//
// Это единственное место, где design doc называет значение фазы дословно, а
// не только структуру; подтверждается первым же живым прогоном этого шага
// (план, задача «Full regression + live golden-case run») — расхождение
// правится одной строкой здесь.
const archiveReadyPhase = "archive-ready"

// cometStatus — часть вывода `comet native status <name> --json`, нужная
// раннеру. Остальные поля (loop, blockers, …) читает сама роль внутри своей
// сессии; раннеру среди них важна только фаза.
type cometStatus struct {
	Phase string `json:"phase"`
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
// команда упала — не блокирует pull request: это внешняя зависимость, чей
// бутстрап на хосте раннера этим изменением не решён (tasks.md 1.1), и
// оставлять задачи без pull request до её появления нельзя.
func (o *Office) archiveIfReady(task tracker.Task, project tracker.Project) (ok bool, err error) {
	ws, err := o.Workspaces.Ensure(task.Ref(), project)
	if errors.Is(err, workspace.ErrWorktreeBusy) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer o.unlock(task.Key, ws)

	name := runner.CometChangeName(task.Key)
	status, err := cometNativeStatus(ws.Dir, name)
	if err != nil {
		o.logf("%s: comet native status не прочитан, архивирование пропущено: %v", task.Key, err)
		return true, nil
	}
	if status.Phase != archiveReadyPhase {
		return true, nil // Verify ещё не отдал прошедший final-result
	}

	if _, err := runComet(ws.Dir, "native", "archive", name, "--confirmed", "--finish", "keep"); err != nil {
		o.logf("%s: comet native archive не выполнен: %v", task.Key, err)
		return true, nil
	}
	if _, err := o.Workspaces.Push(ws); err != nil {
		o.logf("%s: коммит архивирования не опубликован: %v", task.Key, err)
	} else {
		o.logf("%s: изменение %s заархивировано", task.Key, name)
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
