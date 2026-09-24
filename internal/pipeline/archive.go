package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/workspace"
)

// CometExecutable — CLI Comet Native в PATH раннера. Архивирование — код
// раннера (см. openPR в prpass.go), а не роли: оно бежит на машине/хосте, где
// крутится office, а не в песочнице агента — отдельное требование к
// хозяйству раннера, ещё не решённое (docs/notes/sbx.md, tasks.md 1.1).
// Экспортирован: cmd/runner/doctor.go проверяет присутствие того же самого
// имени на PATH, тем же приёмом, что и claude.Executable/sbx.Executable —
// имя не должно разойтись в двух местах. const, как и они — читающему извне
// пакету незачем иметь возможность его подменить.
const CometExecutable = "comet"

// cometExecutable — то же самое имя, но var: archive_test.go подменяет его
// на заведомо отсутствующее, чтобы проверить, что недостающая зависимость
// не блокирует PR-проход, не трогая настоящий PATH целиком — Ensure/Push
// этой же функции нужен настоящий git. runComet зовёт эту переменную, не
// константу выше.
var cometExecutable = CometExecutable

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

	// Независимое ревью (раунд 2): archiveIfReady никогда не проходит через
	// PrepareInput — она не прогон роли, а детерминированный шаг раннера
	// (см. doc-комментарий выше), и берёт рабочую папку напрямую через
	// Ensure/hold. ClearStaleCometLocks в PrepareInput её поэтому не
	// защищает вовсе — а мёртвый лок здесь оставляет не только усечённый
	// прогон роли, а ЛЮБОЙ: песочница сносится при teardown независимо от
	// исхода, claim с её pid+hostname остаётся в этой рабочей папке на
	// хосте, и archiveIfReady, дойдя досюда следующим проходом, натыкается
	// на то же exit 73 — «comet native status не прочитан, архивирование
	// пропущено» — уже навсегда: Archive — последний шаг, PrepareInput для
	// этой задачи больше не случится. Тот же барьер worktree (hold() выше),
	// то же обоснование, что и в doc-комментарии ClearStaleCometLocks.
	if err := runner.ClearStaleCometLocks(ws.Dir); err != nil {
		o.logf("%s: устаревшие блокировки Comet Native не убраны: %v", task.Key, err)
		return true, nil
	}

	// .comet/current-change.json называет изменение точно, если аналитик его
	// уже завёл — тем же способом, что и composeContext в internal/runner/
	// input.go, и по той же причине: угаданное по task-key имя может
	// разойтись с тем, что аналитик реально выбрал (живой случай: задача
	// demo-3, изменение stats-median). Угадывание остаётся резервом только
	// для рабочей папки, где current-change.json ещё не появился.
	name := runner.CurrentChangeName(ws.Dir)
	if name == "" {
		name = runner.CometChangeName(task.Key)
	}
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

	// exit 0 самой команды не значит, что архивирование по-настоящему
	// случилось: живой прогон при финальном ревью нашёл случай, когда
	// `comet native archive --confirmed` в рабочей папке без
	// .comet/runtime/**-состояния печатает "Rebuilt local execution from the
	// portable boundary..." и завершается exit 0, но откатывает
	// comet-state.yaml изменения обратно на прежнюю фазу и вовсе не
	// переименовывает каталог изменения. Без этой проверки код ниже
	// закоммитил и запушил бы этот откат как будто это успешное
	// архивирование.
	if ok, reason := archiveSucceeded(ws.Dir, name); !ok {
		o.logf("%s: comet archive сообщил об успехе (exit 0), но %s — архивирование пропущено, коммит и push отменены", task.Key, reason)
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

// cometArchiveDestGlob — маска пути, куда `comet native archive --confirmed`
// переименовывает каталог изменения при настоящем успехе:
// docs/comet/archive/<дата>-<name> (дата — YYYY-MM-DD; формат подтверждён
// живым прогоном при финальном ревью — `comet native archive eval-brief
// --confirmed" напечатал "...docs/comet/archive/2026-08-31-eval-brief").
// Дату заранее не предсказать — её на момент вызова решает сам CLI, отсюда
// маска, а не точный путь.
//
// Маска — по цифрам даты ([0-9]{4}-[0-9]{2}-[0-9]{2}-<name>), а не "*-"+name:
// независимое ревью (раунд 3) нашло, что общий "*" совпал бы и с чужим
// архивом в том же общем каталоге docs/comet/archive/ (где копятся
// изменения всех задач репозитория), чьё имя случайно оканчивается тем же
// хвостом — тот же класс коллизии, что и HasSuffix в prpass.go, только
// на уровне filepath.Glob вместо strings.HasSuffix.
func cometArchiveDestGlob(name string) string {
	return filepath.Join(cometArchiveScope, "archive", "[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]-"+name)
}

// archiveSucceeded проверяет постусловие успешного архивирования — не
// доверяя одному exit 0 команды `comet native archive --confirmed`. Живой
// прогон при финальном ревью нашёл случай, когда эта команда в рабочей
// папке без .comet/runtime/**-состояния печатает сообщение о восстановлении
// локального исполнения и завершается exit 0, но на самом деле откатывает
// comet-state.yaml изменения обратно на прежнюю фазу и вовсе не
// переименовывает каталог изменения — настоящего архивирования не было,
// хотя exit-код утверждает обратное.
//
// Проверяются оба сигнала: под fs.rename (единственный способ, которым
// `comet native archive` действует при isolation: current — см. комментарий
// у cometArchiveScope ниже) они при настоящем успехе логически
// эквивалентны, переименование атомарно, — но по отдельности каждый уже
// ловит воспроизведённый живьём сбой: исходный каталог изменения остаётся
// на месте, а каталог назначения не появляется вовсе.
func archiveSucceeded(dir, name string) (ok bool, reason string) {
	oldDir := filepath.Join(runner.CometChangesDir, name)
	if info, err := os.Stat(filepath.Join(dir, oldDir)); err == nil && info.IsDir() {
		return false, fmt.Sprintf("каталог изменения %s всё ещё существует — переименования не было", oldDir)
	}
	destGlob := cometArchiveDestGlob(name)
	matches, err := filepath.Glob(filepath.Join(dir, destGlob))
	if err != nil {
		return false, fmt.Sprintf("маска каталога архива %s не разобрана: %v", destGlob, err)
	}
	if len(matches) == 0 {
		return false, fmt.Sprintf("каталог архива по маске %s не найден", destGlob)
	}
	return true, ""
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

// cometArchiveScope — единственный путь, который `comet native archive`
// трогает под isolation: current — переименование каталога изменения
// (docs/comet/archive/<date>-<name>) плюс синхронизация delta-spec
// (docs/comet/specs/**), проверено живым прогоном при ревью Задачи 5 —
// тот же корень, что и runner.CometChangesDir, но без "changes". Рабочая
// папка общая с ролью (analyst/implementer/reviewer), которая могла оставить
// в ней своё незакоммиченное — commitArchiveRename не должен ни видеть, ни
// трогать ничего вне этого пути.
var cometArchiveScope = filepath.Dir(runner.CometChangesDir)

// commitArchiveRename коммитит то, что `comet native archive --confirmed`
// оставил в рабочей папке при isolation: current голым fs.rename. Рабочая
// папка чистая в пределах cometArchiveScope — коммитить нечего, и это не
// ошибка: например, переименование могло не оставить изменений (изменение
// уже было закоммичено раньше). Незакоммиченное вне cometArchiveScope —
// не наше дело: это либо чужая рабочая копия роли, либо её легитимный
// незакоммиченный остаток (см. sweepWorktrees в prpass.go), и он не должен
// ни блокировать, ни провоцировать архивный коммит.
func commitArchiveRename(dir, name string) error {
	dirty, err := gitDirty(dir, cometArchiveScope)
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	if err := gitArchive(dir, "add", "-A", "--", cometArchiveScope); err != nil {
		return err
	}
	return gitArchive(dir, "commit", "-m", "chore: archive Comet Native change "+name, "--", cometArchiveScope)
}

// gitDirty — есть ли в рабочей папке незакоммиченные изменения в пределах
// scope. Пейсспек обязателен: без него команда видит всю рабочую папку,
// включая незакоммиченный остаток роли за пределами scope, который не
// должен ни считаться "есть что архивировать", ни попадать под подозрение.
func gitDirty(dir, scope string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain", "--", scope)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
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
// в ошибку при неудаче), но с собственной личностью коммита через окружение
// и с GIT_TERMINAL_PROMPT=0 — той же защитной конвенцией, что и
// internal/workspace.gitEnv для остальных git-вызовов раннера (сетевых
// операций здесь нет, но конвенция общая для всех git-вызовов раннера).
func gitArchive(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
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
