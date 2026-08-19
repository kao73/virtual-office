package runner

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// snapshotNow готовит рабочую папку так же, как это делает раннер перед
// прогоном: прячет каталог обмена от git и кладёт снимок состояния.
//
// Порядок здесь не косметика, а воспроизведение производственного пути: замок
// берётся раньше постановки задачи, и без правила в info/exclude свежая рабочая
// папка выглядела бы грязной (см. workspace.hold).
func snapshotNow(t *testing.T, workdir string) {
	t.Helper()
	if err := ExcludeAgentDir(workdir); err != nil {
		t.Fatalf("каталог обмена не спрятан от git: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(workdir, Dir), 0o755); err != nil {
		t.Fatalf("каталог обмена не создан: %v", err)
	}
	status, err := WorktreeStatus(workdir)
	if err != nil {
		t.Fatalf("состояние не снято: %v", err)
	}
	if err := os.WriteFile(BaseStatusPath(workdir), status, 0o644); err != nil {
		t.Fatalf("снимок не записан: %v", err)
	}
}

func head(t *testing.T, workdir string) string {
	t.Helper()
	commit, err := HeadCommit(workdir)
	if err != nil {
		t.Fatalf("HEAD не прочитан: %v", err)
	}
	return commit
}

// Прогон, оборванный на первом-втором шаге, не оставляет ни коммита,
// ни строки в папке — это и есть «не начинал». Живой образец: f4705aba,
// два шага, $0.05, ECONNRESET.
func TestLeftTraceIsEmptyForUntouchedWorkdir(t *testing.T) {
	workdir := gitRepo(t)
	snapshotNow(t, workdir)

	left, err := LeftTrace(workdir, head(t, workdir), 2)
	if err != nil {
		t.Fatalf("след не посчитан: %v", err)
	}
	if left {
		t.Error("след найден там, где агент не сделал ничего")
	}
}

// Коммит — самый явный след: работа не только сделана, но и сохранена.
func TestLeftTraceSeesCommit(t *testing.T) {
	workdir := gitRepo(t)
	snapshotNow(t, workdir)
	base := head(t, workdir)

	if err := os.WriteFile(filepath.Join(workdir, "main.py"), []byte("print(1)\n"), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}
	git(t, workdir, "add", "main.py")
	git(t, workdir, "-c", "user.email=t@example.test", "-c", "user.name=test", "commit", "-q", "-m", "работа")

	left, err := LeftTrace(workdir, base, 1)
	if err != nil {
		t.Fatalf("след не посчитан: %v", err)
	}
	if !left {
		t.Error("коммит прогона не признан следом работы")
	}
}

// Незакоммиченная правка — тоже след: агент работал, просто не успел сохранить.
func TestLeftTraceSeesNewDirt(t *testing.T) {
	workdir := gitRepo(t)
	snapshotNow(t, workdir)

	if err := os.WriteFile(filepath.Join(workdir, "черновик.md"), []byte("начал\n"), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}

	left, err := LeftTrace(workdir, head(t, workdir), 1)
	if err != nil {
		t.Fatalf("след не посчитан: %v", err)
	}
	if !left {
		t.Error("незакоммиченная работа прогона не признана следом")
	}
}

// Рабочая папка переиспользуется: после reap или ответа человека в ней уже
// лежит чужая незакоммиченная правка. Она следом **этого** прогона не является,
// иначе всякий обрыв в переиспользованной папке считался бы работой.
func TestLeftTraceIgnoresDirtFromPreviousRun(t *testing.T) {
	workdir := gitRepo(t)
	if err := os.WriteFile(filepath.Join(workdir, "прошлый.md"), []byte("чужое\n"), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}
	snapshotNow(t, workdir) // снимок уже с чужой правкой

	left, err := LeftTrace(workdir, head(t, workdir), 1)
	if err != nil {
		t.Fatalf("след не посчитан: %v", err)
	}
	if left {
		t.Error("чужая правка из прошлого прогона зачтена этому")
	}
}

// Ревьюер работает без инструментов записи, аналитик читает репозиторий —
// в папке они не оставляют ничего. Судить их по одной лишь папке значило бы
// объявлять «не начинал» всякий их обрыв, поэтому в счёт идут и шаги.
//
// Живые образцы у этого правила есть, и как раз ревьюерские: три прогона
// разбора отработали больше порога шагов, а в рабочей папке не оставили
// ничего — писать им было нечем. Перечень с числами — в docs/notes/claude-cli.md,
// см. также комментарий к notStartedTurns.
func TestLeftTraceSeesTurnsBeyondThreshold(t *testing.T) {
	workdir := gitRepo(t)
	snapshotNow(t, workdir)
	base := head(t, workdir)

	if left, err := LeftTrace(workdir, base, notStartedTurns); err != nil || left {
		t.Errorf("порог сработал не строго: шагов %d, след %v, ошибка %v", notStartedTurns, left, err)
	}
	left, err := LeftTrace(workdir, base, notStartedTurns+1)
	if err != nil {
		t.Fatalf("след не посчитан: %v", err)
	}
	if !left {
		t.Errorf("%d шагов не признаны следом работы, а порог %d", notStartedTurns+1, notStartedTurns)
	}
}

// Каталог обмена следом работы не является, и это несущее условие всего
// разбора: лог прогона пишется туда **всегда**, в том числе прогоном, который
// не сделал ни шага. Считай раннер его работой — вид not_started не наступил бы
// никогда, и попытка тратилась бы на каждый обрыв связи. От git каталог прячет
// ExcludeAgentDir; здесь проверяется, что этого довольно.
func TestLeftTraceIgnoresExchangeDir(t *testing.T) {
	workdir := gitRepo(t)
	snapshotNow(t, workdir)

	for _, name := range []string{FileLog, "result.json"} {
		if err := os.WriteFile(filepath.Join(workdir, Dir, name), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("%s не записан: %v", name, err)
		}
	}

	left, err := LeftTrace(workdir, head(t, workdir), 1)
	if err != nil {
		t.Fatalf("след не посчитан: %v", err)
	}
	if left {
		t.Error("каталог обмена зачтён следом работы: not_started не наступит никогда")
	}
}

// Прогон мог умереть раньше, чем раннер снял снимок. Отсутствие снимка —
// это ответ «работать не начинали», а не беда разбора: ронять из-за него
// разбор прогона значило бы менять решение на ошибку.
func TestLeftTraceSurvivesMissingSnapshot(t *testing.T) {
	workdir := gitRepo(t)

	left, err := LeftTrace(workdir, head(t, workdir), 1)
	if err != nil {
		t.Fatalf("отсутствие снимка признано бедой: %v", err)
	}
	if left {
		t.Error("след найден там, где прогон не дожил до снимка")
	}
}

// Репозиторий без коммитов — законное состояние: первая задача в пустом
// проекте выглядит именно так, и сравнивать в ней не с чем.
func TestLeftTraceSurvivesRepoWithoutCommits(t *testing.T) {
	workdir := t.TempDir()
	git(t, workdir, "init", "-q")
	snapshotNow(t, workdir)

	if _, err := LeftTrace(workdir, "", 1); err != nil {
		t.Fatalf("репозиторий без коммитов признан бедой: %v", err)
	}
}

// Таймаут обязан узнаваться типом, а не текстом: бэкенды возвращают -1
// из четырёх разных мест, и по коду «работал и не успел» неотличимо
// от «прогона не было».
func TestRunTimeoutIsRecognizedByType(t *testing.T) {
	err := RunTimeout(30*time.Minute, "песочница office-1234")

	if !errors.Is(err, ErrRunTimeout) {
		t.Errorf("errors.Is не узнаёт таймаут: %v", err)
	}
	for _, want := range []string{"30m", "office-1234"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в ошибке нет %q: %v", want, err)
		}
	}
	if errors.Is(errors.New("агент не запущен"), ErrRunTimeout) {
		t.Error("чужая ошибка принята за таймаут")
	}
}
