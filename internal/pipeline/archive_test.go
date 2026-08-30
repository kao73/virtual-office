package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

// fakeComet подкладывает на PATH подложный `comet`: на "native status ... --json"
// печатает конверт {command,exitCode,data:{phase,loop:{stage}}} — тот же
// формат, что и настоящий CLI (обнаружено живым запуском при ревью Задачи 5),
// а не голый {"phase":...}, который эмулировался до фикса. На "native archive
// ..." создаёт archivedMarker (подтверждает, что archive был вызван) и,
// отдельно, оставляет рабочую папку грязной внутри cometArchiveScope — как
// настоящий `comet native archive --confirmed` при isolation: current,
// который делает голый fs.rename каталога изменения внутри docs/comet, а не
// git-коммит и не запись где-то ещё в рабочей папке (проверено живым
// прогоном при ревью Задачи 5; scope с тех пор сузился с "вся рабочая папка"
// до docs/comet при повторном ревью). Он предваряет системный PATH, а не
// заменяет его: git, которым archiveIfReady пользуется через Ensure/Push и
// через свой собственный коммит переименования, обязан остаться доступным.
func fakeComet(t *testing.T, stage string) (archivedMarker string) {
	t.Helper()
	binDir := t.TempDir()
	archivedMarker = filepath.Join(t.TempDir(), "archived")

	script := "#!/bin/sh\n" +
		"case \"$2\" in\n" +
		"  status) echo \"{\\\"command\\\":\\\"status\\\",\\\"exitCode\\\":0,\\\"data\\\":{\\\"phase\\\":\\\"archive\\\",\\\"loop\\\":{\\\"stage\\\":\\\"" + stage + "\\\"}}}\" ;;\n" +
		"  archive) : > \"" + archivedMarker + "\"; mkdir -p " + cometArchiveScope + "; date > " + cometArchiveScope + "/.archived-rename ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(filepath.Join(binDir, "comet"), []byte(script), 0o755); err != nil {
		t.Fatalf("подложный comet не записан: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return archivedMarker
}

func TestArchiveIfReadyRunsArchiveWhenStageMatches(t *testing.T) {
	o := newOffice(t)
	marker := fakeComet(t, archiveReadyStage)
	task := o.approved(t, "OFF-1")

	ok, err := o.archiveIfReady(task, o.Projects["OFF"])
	if err != nil || !ok {
		t.Fatalf("archiveIfReady = %v, %v", ok, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("comet native archive не был вызван: %v", err)
	}
}

// Пин на реальный гейт: подложный comet действительно вернул именно эту
// стадию (не что-то ещё — comet не найден, JSON не разобран и т. п. тоже
// оставили бы marker отсутствующим), и эта стадия не archiveReadyStage.
// Иначе "маркер отсутствует" не доказывал бы, что сработала именно проверка
// стадии.
func TestArchiveIfReadySkipsWhenStageNotReady(t *testing.T) {
	o := newOffice(t)
	const stage = "verify"
	if stage == archiveReadyStage {
		t.Fatalf("тестовая стадия %q совпадает с archiveReadyStage — тест ничего не проверяет", stage)
	}
	marker := fakeComet(t, stage)
	task := o.approved(t, "OFF-1")
	project := o.Projects["OFF"]

	ws, err := o.Workspaces.Ensure(task.Ref(), project)
	if err != nil {
		t.Fatalf("рабочая папка не готова для проверки: %v", err)
	}
	status, err := cometNativeStatus(ws.Dir, runner.CometChangeName(task.Key))
	if err := ws.Unlock(); err != nil {
		t.Fatalf("рабочая папка не отпущена: %v", err)
	}
	if err != nil {
		t.Fatalf("comet native status не прочитан: %v", err)
	}
	if status.Data.Loop.Stage != stage {
		t.Fatalf("подложный comet вернул stage=%q, тест собран неверно", status.Data.Loop.Stage)
	}

	ok, err := o.archiveIfReady(task, project)
	if err != nil || !ok {
		t.Fatalf("archiveIfReady = %v, %v", ok, err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("comet native archive вызван, хотя стадия не archive-ready")
	}
}

// Отсутствие comet на этой машине — известный, ещё не решённый пробел
// (tasks.md 1.1), а не повод никогда не открывать pull request.
func TestArchiveIfReadyIsNonBlockingWithoutComet(t *testing.T) {
	o := newOffice(t)
	old := cometExecutable
	cometExecutable = "comet-not-installed-in-tests"
	t.Cleanup(func() { cometExecutable = old })
	task := o.approved(t, "OFF-1")

	ok, err := o.archiveIfReady(task, o.Projects["OFF"])
	if err != nil || !ok {
		t.Fatalf("отсутствие comet не должно блокировать PR-проход: ok=%v, err=%v", ok, err)
	}
}

// Рабочая папка занята — единственный случай, где archiveIfReady просит
// openPR подождать следующего прохода, а не открывать PR без архивирования.
func TestArchiveIfReadyDefersWhenWorktreeBusy(t *testing.T) {
	o := newOffice(t)
	task := o.approved(t, "OFF-1")
	project := o.Projects["OFF"]

	ws, err := o.Workspaces.Ensure(task.Ref(), project)
	if err != nil {
		t.Fatalf("рабочая папка не занята для теста: %v", err)
	}
	defer ws.Unlock()

	ok, err := o.archiveIfReady(task, project)
	if err != nil {
		t.Fatalf("archiveIfReady вернул ошибку вместо мягкого отказа: %v", err)
	}
	if ok {
		t.Error("archiveIfReady должен был отступить: рабочая папка занята")
	}
}

// Под isolation: current `comet native archive --confirmed` делает голый
// fs.rename каталога изменения, а не git-коммит (проверено живым прогоном
// при ревью Задачи 5) — без коммита Workspaces.Push публиковать нечего.
// fakeComet's archive case имитирует rename, оставляя рабочую папку грязной;
// этот тест проверяет, что archiveIfReady сам фиксирует изменение коммитом
// прежде, чем звать Push.
func TestArchiveIfReadyCommitsTheRename(t *testing.T) {
	o := newOffice(t)
	fakeComet(t, archiveReadyStage)
	task := o.approved(t, "OFF-1")
	project := o.Projects["OFF"]

	ok, err := o.archiveIfReady(task, project)
	if err != nil || !ok {
		t.Fatalf("archiveIfReady = %v, %v", ok, err)
	}

	ws, err := o.Workspaces.Ensure(task.Ref(), project)
	if err != nil {
		t.Fatalf("рабочая папка не открыта для проверки: %v", err)
	}
	defer ws.Unlock()

	out, err := exec.Command("git", "-C", ws.Dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("рабочая папка осталась грязной после архивирования: %q", out)
	}

	subject, err := exec.Command("git", "-C", ws.Dir, "log", "-1", "--format=%s").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	wantSubject := "chore: archive Comet Native change " + runner.CometChangeName(task.Key)
	if got := strings.TrimSpace(string(subject)); got != wantSubject {
		t.Errorf("тема коммита = %q, ожидалось %q", got, wantSubject)
	}
}

// Рабочая папка — общая с ролью (analyst/implementer/reviewer), и её
// незакоммиченный остаток вне docs/comet защищён так же, как в
// sweepWorktrees (prpass.go): это чужая, легитимная незакоммиченная работа,
// а не мусор. commitArchiveRename обязан коммитить только docs/comet
// (cometArchiveScope) — этот тест кладёт в рабочую папку незакоммиченный
// файл вне scope и проверяет, что после архивирования он остаётся
// нетронутым (не подобран в архивный коммит "git add -A" без пейсспека), а
// переименование внутри docs/comet при этом всё равно коммитится.
func TestArchiveIfReadyCommitsOnlyCometScope(t *testing.T) {
	o := newOffice(t)
	fakeComet(t, archiveReadyStage)
	task := o.approved(t, "OFF-1")
	project := o.Projects["OFF"]

	ws, err := o.Workspaces.Ensure(task.Ref(), project)
	if err != nil {
		t.Fatalf("рабочая папка не готова для подготовки теста: %v", err)
	}
	const leftover = "role-leftover.txt"
	if err := os.WriteFile(filepath.Join(ws.Dir, leftover), []byte("незакоммиченная работа роли\n"), 0o644); err != nil {
		t.Fatalf("незакоммиченный остаток роли не создан: %v", err)
	}
	if err := ws.Unlock(); err != nil {
		t.Fatalf("рабочая папка не отпущена после подготовки: %v", err)
	}

	ok, err := o.archiveIfReady(task, project)
	if err != nil || !ok {
		t.Fatalf("archiveIfReady = %v, %v", ok, err)
	}

	ws, err = o.Workspaces.Ensure(task.Ref(), project)
	if err != nil {
		t.Fatalf("рабочая папка не открыта для проверки: %v", err)
	}
	defer ws.Unlock()

	out, err := exec.Command("git", "-C", ws.Dir, "status", "--porcelain").Output()
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	status := strings.TrimSpace(string(out))
	wantStatus := "?? " + leftover
	if status != wantStatus {
		t.Errorf("git status после архивирования = %q, ожидалось только %q — незакоммиченный остаток роли задет архивным коммитом", status, wantStatus)
	}
	if _, err := os.Stat(filepath.Join(ws.Dir, leftover)); err != nil {
		t.Errorf("незакоммиченный остаток роли пропал из рабочей папки: %v", err)
	}

	subject, err := exec.Command("git", "-C", ws.Dir, "log", "-1", "--format=%s").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	wantSubject := "chore: archive Comet Native change " + runner.CometChangeName(task.Key)
	if got := strings.TrimSpace(string(subject)); got != wantSubject {
		t.Errorf("тема коммита = %q, ожидалось %q — переименование внутри docs/comet должно закоммититься несмотря на остаток вне scope", got, wantSubject)
	}

	files, err := exec.Command("git", "-C", ws.Dir, "show", "--name-only", "--format=", "HEAD").Output()
	if err != nil {
		t.Fatalf("git show: %v", err)
	}
	if strings.Contains(string(files), leftover) {
		t.Errorf("архивный коммит захватил незакоммиченный остаток роли вне docs/comet: %q", string(files))
	}
}
