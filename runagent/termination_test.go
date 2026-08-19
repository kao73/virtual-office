package runagent

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/runner"
)

// workdir — рабочая папка прогона в том виде, в каком её готовит раннер:
// git-репозиторий с коммитом, спрятанным от git каталогом обмена и снимком
// состояния на старте.
func workdir(t *testing.T) Options {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("-c", "user.email=t@example.test", "-c", "user.name=test", "commit", "-q", "--allow-empty", "-m", "начало")

	if err := runner.ExcludeAgentDir(dir); err != nil {
		t.Fatalf("каталог обмена не спрятан: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, runner.Dir), 0o755); err != nil {
		t.Fatalf("каталог обмена не создан: %v", err)
	}
	status, err := runner.WorktreeStatus(dir)
	if err != nil {
		t.Fatalf("состояние не снято: %v", err)
	}
	if err := os.WriteFile(runner.BaseStatusPath(dir), status, 0o644); err != nil {
		t.Fatalf("снимок не записан: %v", err)
	}

	head, err := runner.HeadCommit(dir)
	if err != nil {
		t.Fatalf("HEAD не прочитан: %v", err)
	}
	return Options{Workdir: dir, Passport: runner.Run{BaseCommit: head}}
}

// Результат есть — прогон состоялся, чем бы ни кончился лог. Работа сделана
// и опубликована, и спорить с ней из-за строчки в логе раннер не станет.
func TestTerminationCompletedWhenResultExists(t *testing.T) {
	broken := runner.Ending{Reason: runner.EndError, Provider: "api_error", Detail: "API Error"}

	term := terminationOf(Options{}, true, broken, 40, errors.New("что-то пошло не так"))

	if term.Kind != runner.TerminationCompleted {
		t.Errorf("вид %q, ожидался %q: результат есть", term.Kind, runner.TerminationCompleted)
	}
	if term.Idle() {
		t.Error("прогон с результатом сочтён пустым")
	}
}

// Предел шагов — усечение: агент работал и не успел отчитаться. Живой образец:
// 8dfc3e3e, 51 шаг при max_turns 50.
func TestTerminationTruncatedByMaxTurns(t *testing.T) {
	ending := runner.Ending{Reason: runner.EndMaxTurns, Provider: "max_turns"}

	term := terminationOf(Options{}, false, ending, 51, nil)

	if term.Kind != runner.TerminationTruncated {
		t.Errorf("вид %q, ожидался %q", term.Kind, runner.TerminationTruncated)
	}
	if !term.Idle() {
		t.Error("усечение не признано прогоном без результата: попытка потратится зря")
	}
	if !strings.Contains(term.Detail, "51") {
		t.Errorf("в объяснении нет числа шагов: %q", term.Detail)
	}
}

// Таймаут бэкенда — то же усечение, и узнаётся он **типом ошибки**. По коду
// возврата это невозможно: -1 бэкенды отдают ещё из трёх мест, где прогона
// не было вовсе.
func TestTerminationTruncatedByTimeout(t *testing.T) {
	// Лог при этом молчит: прогон убит до итогового события.
	term := terminationOf(Options{}, false, runner.Ending{}, 0, runner.RunTimeout(30*time.Minute, ""))

	if term.Kind != runner.TerminationTruncated {
		t.Errorf("вид %q, ожидался %q", term.Kind, runner.TerminationTruncated)
	}
}

// Прочие беды запуска усечением не считаются: песочница не поднялась, лог
// не создан, агент не стартовал — прогона не было, и продолжать в той же папке
// нечего.
func TestTerminationDoesNotMistakeOtherFailuresForTimeout(t *testing.T) {
	opts := workdir(t)

	term := terminationOf(opts, false, runner.Ending{}, 0, errors.New("песочница office-1 не создана"))

	if term.Kind == runner.TerminationTruncated {
		t.Error("беда запуска принята за таймаут: прогон продолжат с несуществующего места")
	}
	if term.Kind != runner.TerminationNotStarted {
		t.Errorf("вид %q, ожидался %q", term.Kind, runner.TerminationNotStarted)
	}
}

// Обрыв связи на втором шаге, ни строки в папке — «не начинал». Попытка
// не тратится. Живой образец: f4705aba, $0.05.
func TestTerminationNotStartedWhenNoTrace(t *testing.T) {
	opts := workdir(t)
	ending := runner.Ending{
		Reason:   runner.EndError,
		Provider: "api_error",
		Detail:   "API Error: Unable to connect to API (ECONNRESET)",
	}

	term := terminationOf(opts, false, ending, 2, nil)

	if term.Kind != runner.TerminationNotStarted {
		t.Errorf("вид %q, ожидался %q", term.Kind, runner.TerminationNotStarted)
	}
	if term.Detail != ending.Detail {
		t.Errorf("объяснение %q, ожидались слова агента", term.Detail)
	}
}

// Тот же обрыв, но на двадцать пятом шаге и без результата — «не справился»:
// агент поработал и сломался. Деньги потрачены, начинать всё равно с нуля,
// попытка тратится.
//
// Событие в логе у обоих обрывов одно и то же. Разводит их только след работы
// и наличие результата, и в этом весь смысл правила «проверять причину,
// а не следствие».
func TestTerminationErroredWhenTraceExists(t *testing.T) {
	opts := workdir(t)
	ending := runner.Ending{
		Reason:   runner.EndError,
		Provider: "api_error",
		Detail:   "API Error: Unable to connect to API (ECONNRESET)",
	}

	term := terminationOf(opts, false, ending, 25, nil)

	if term.Kind != runner.TerminationErrored {
		t.Errorf("вид %q, ожидался %q", term.Kind, runner.TerminationErrored)
	}
	if term.Idle() {
		t.Error("сломавшийся на ходу прогон сочтён пустым: попытка не потратится")
	}
}

// Судить след не вышло — рабочая папка сломана. Считаем «не справился»:
// это сегодняшнее поведение (всякий прогон без результата тратит попытку),
// а не тратить её на то, о чём мы ничего не знаем, значило бы молча ослабить
// предел попыток.
func TestTerminationErroredWhenTraceUnreadable(t *testing.T) {
	opts := Options{Workdir: filepath.Join(t.TempDir(), "нет-такой-папки"), Passport: runner.Run{BaseCommit: "deadbeef"}}

	term := terminationOf(opts, false, runner.Ending{Reason: runner.EndError}, 1, nil)

	if term.Kind != runner.TerminationErrored {
		t.Errorf("вид %q, ожидался %q", term.Kind, runner.TerminationErrored)
	}
	if !strings.Contains(term.Detail, "след") {
		t.Errorf("объяснение не говорит, что след не разобран: %q", term.Detail)
	}
}

// Незнакомое слово поставщика раннер не проглатывает: человек обязан увидеть,
// чего разбор не понял.
func TestTerminationTellsAboutUnknownReason(t *testing.T) {
	opts := workdir(t)
	ending := runner.Ending{Reason: runner.EndUnknown, Provider: "quota_exhausted"}

	term := terminationOf(opts, false, ending, 1, nil)

	if !strings.Contains(term.Detail, "quota_exhausted") {
		t.Errorf("незнакомое слово поставщика потеряно: %q", term.Detail)
	}
}

// Живой узор, обратный обрыву: CLI считает прогон дошедшим до конца
// (terminal_reason: completed), а результата нет.
//
// Это не выдуманный случай: в архиве таких прогонов три — прогоны ревьюера,
// которому забыли дать право на файл результата (docs/notes/smoke.md).
// Агент отрабатывал больше порога шагов, выдавал разбор и отчитаться не мог.
// Перечень с числами — в docs/notes/claude-cli.md.
//
// Вид у них errored, и это по делу: работа шла, денег стоила, а результата нет —
// начинать всё равно с нуля. Событие здесь врёт в сторону, обратную обрыву
// связи: там оно кричало о беде при успехе, тут молчит об успехе при беде.
// Оба вранья ловит одно правило — судить по следу, а не по событию.
func TestTerminationErroredWhenRunEndedSilently(t *testing.T) {
	opts := workdir(t)
	ending := runner.Ending{Reason: runner.EndCompleted, Provider: "completed"}

	term := terminationOf(opts, false, ending, 6, nil)

	if term.Kind != runner.TerminationErrored {
		t.Errorf("вид %q, ожидался %q: лог сказал «дошёл до конца», а результата нет",
			term.Kind, runner.TerminationErrored)
	}
	if term.Idle() {
		t.Error("прогон без результата после шести шагов сочтён пустым: попытка не потратится")
	}
}
