package sbx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

// recordedStep — подделка step: запоминает вызовы и отвечает по очереди
// заготовленными ошибками (nil, если их меньше, чем вызовов).
type recordedStep struct {
	calls [][]string
	errs  []error
}

func (r *recordedStep) run(_ context.Context, args ...string) error {
	i := len(r.calls)
	r.calls = append(r.calls, args)
	if i < len(r.errs) {
		return r.errs[i]
	}
	return nil
}

// primaryWithExclude заводит каталог с .git/info/exclude (как оставляет
// runner.PrepareInput на настоящей рабочей папке) и, опционально, дописанные
// каталоги обмена — так cloneSyncIn находит на входе именно то, что нашла бы
// на настоящем прогоне.
func primaryWithExclude(t *testing.T, dirs ...string) string {
	t.Helper()
	primary := t.TempDir()
	if err := os.MkdirAll(filepath.Join(primary, ".git/info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, excludeFile), []byte("/.agent\n/.comet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(primary, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return primary
}

func TestCloneSyncInSyncsExcludeFileDirsAndChowns(t *testing.T) {
	primary := primaryWithExclude(t, ".agent")
	agentDir := filepath.Join(primary, ".agent")
	// .comet намеренно не заводим: задача без активного изменения Comet Native
	// его не имеет вовсе, и это законный случай, а не пропуск.

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{Dirs: []string{".agent", ".comet"}},
	}
	rec := &recordedStep{}

	present, err := cloneSyncIn(context.Background(), "office-test", l, rec.run)
	if err != nil {
		t.Fatalf("cloneSyncIn: %v", err)
	}
	if !slices.Equal(present, []string{".agent"}) {
		t.Errorf("present = %q, ожидалось [.agent]", present)
	}

	if len(rec.calls) != 4 {
		t.Fatalf("ожидалось 4 вызова (exclude + mkdir + cp + chown), получено %d: %q", len(rec.calls), rec.calls)
	}
	excludeCall := rec.calls[0]
	wantExclude := []string{"cp", filepath.Join(primary, excludeFile), "office-test:" + filepath.Join(primary, ".git/info") + "/"}
	if !slices.Equal(excludeCall, wantExclude) {
		t.Errorf("перенос exclude\nполучено:  %q\nожидалось: %q", excludeCall, wantExclude)
	}
	mkdir := rec.calls[1]
	wantMkdir := []string{"exec", "office-test", "mkdir", "-p", primary}
	if !slices.Equal(mkdir, wantMkdir) {
		t.Errorf("mkdir -p\nполучено:  %q\nожидалось: %q", mkdir, wantMkdir)
	}
	cp := rec.calls[2]
	wantCP := []string{"cp", agentDir, "office-test:" + primary + "/"}
	if !slices.Equal(cp, wantCP) {
		t.Errorf("cp\nполучено:  %q\nожидалось: %q", cp, wantCP)
	}
	chown := rec.calls[3]
	if chown[0] != "exec" || chown[1] != "-u" || chown[2] != "root" {
		t.Fatalf("chown не запущен от root: %q", chown)
	}
	if !slices.Contains(chown, agentDir) {
		t.Errorf("chown не назвал %s: %q", agentDir, chown)
	}
	if slices.ContainsFunc(chown, func(s string) bool { return strings.Contains(s, ".comet") }) {
		t.Errorf("chown зацепил несуществующий .comet: %q", chown)
	}
}

func TestCloneSyncInNoopWhenNothingToSync(t *testing.T) {
	primary := t.TempDir() // ни .git/info/exclude, ни .agent, ни .comet не заведены

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{Dirs: []string{".agent", ".comet"}},
	}
	rec := &recordedStep{}

	present, err := cloneSyncIn(context.Background(), "office-test", l, rec.run)
	if err != nil {
		t.Fatalf("cloneSyncIn: %v", err)
	}
	if len(present) != 0 {
		t.Errorf("present = %q, ожидалось пусто", present)
	}
	if len(rec.calls) != 0 {
		t.Errorf("sbx позван, хотя заносить было нечего: %q", rec.calls)
	}
}

func TestCloneSyncInPropagatesCPFailure(t *testing.T) {
	primary := primaryWithExclude(t, ".agent")

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{Dirs: []string{".agent"}},
	}
	// Первый вызов — перенос exclude, он должен пройти; второй — mkdir -p,
	// тоже должен пройти; беда — на самом cp каталога.
	rec := &recordedStep{errs: []error{nil, nil, errors.New("sbx cp: connection refused")}}

	if _, err := cloneSyncIn(context.Background(), "office-test", l, rec.run); err == nil {
		t.Fatal("неудача sbx cp потеряна")
	}
	// chown не должен звучать следом за упавшим cp: занести было нечего.
	if len(rec.calls) != 3 {
		t.Errorf("после упавшего cp прогремел ещё вызов: %q", rec.calls)
	}
}

// Каталог обмена не единственное, что нужно песочнице: без правил
// git-исключения агент внутри увидел бы .agent/.comet как обычную грязь
// рабочего дерева, а не как исключённое — role.md обещает роли обратное.
func TestSyncExcludeFileCopiesHostRules(t *testing.T) {
	primary := primaryWithExclude(t)
	rec := &recordedStep{}

	if err := syncExcludeFile(context.Background(), "office-test", primary, rec.run); err != nil {
		t.Fatalf("syncExcludeFile: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("ожидался 1 вызов, получено %d: %q", len(rec.calls), rec.calls)
	}
	want := []string{"cp", filepath.Join(primary, excludeFile), "office-test:" + filepath.Join(primary, ".git/info") + "/"}
	if !slices.Equal(rec.calls[0], want) {
		t.Errorf("получено:  %q\nожидалось: %q", rec.calls[0], want)
	}
}

func TestSyncExcludeFileNoopWithoutSource(t *testing.T) {
	primary := t.TempDir() // .git/info/exclude не заведён
	rec := &recordedStep{}

	if err := syncExcludeFile(context.Background(), "office-test", primary, rec.run); err != nil {
		t.Fatalf("syncExcludeFile: %v", err)
	}
	if len(rec.calls) != 0 {
		t.Errorf("sbx позван без источника: %q", rec.calls)
	}
}

// Часть l.Clone.Dirs в действительности лежит и в git (roles/analyst/role.md
// велит коммитить .comet/config.yaml) — выгрузи её на хост раньше слияния,
// и следующий git merge --ff-only откажет: "your local changes... would be
// overwritten". cloneSyncOut обязана слить ветку раньше, чем класть каталоги
// поверх рабочего дерева — этот тест проверяет именно порядок, а не только
// то, что оба шага в принципе случаются.
func TestCloneSyncOutStopsAtDirsWhenFetchFails(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)
	// fetchInto уводим на другую ветку — fetchBranch откажет ещё до слияния.
	runGit(t, fetchInto, "checkout", "-q", "-b", "другая-ветка")

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1", Dirs: []string{".agent"}},
	}
	rec := &recordedStep{}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, io.Discard); err == nil {
		t.Fatal("несовпадение веток не замечено — слияние не должно было даже начаться")
	}
	if len(rec.calls) != 0 {
		t.Errorf("cp каталогов позван, хотя слияние ветки провалилось раньше: %q", rec.calls)
	}
}

// "не нашлось в песочнице" терпимо только для каталога, которого не было
// там и на входе (роль его не завела). .agent заносит cloneSyncIn сама —
// его отсутствие на выходе не законный случай, а повод сообщить о беде.
func TestCloneSyncOutTreatsMissingContainerPathAsNotFatalOnlyWhenAbsentOnEntry(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1", Dirs: []string{".agent", ".comet"}},
	}
	// Первый вызов после успешного слияния — warnUncommitted (git status
	// внутри песочницы); дальше — .agent синхронизировался штатно (значит,
	// presentOnEntry его называет), .comet в песочнице не заведён — sbx cp
	// отвечает так, как отвечает вживую (см. notFoundInContainer в clone.go).
	rec := &recordedStep{errs: []error{errors.New("grep: нет совпадений"), nil, errors.New(`ERROR: path ".../.comet" not found in container`)}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, []string{".agent"}, io.Discard); err != nil {
		t.Fatalf("отсутствие .comet в песочнице (не занесённого на входе) не должно проваливать выгрузку: %v", err)
	}
	if len(rec.calls) != 3 {
		t.Fatalf("ожидалось 3 вызова (warnUncommitted + 2 cp), получено %d: %q", len(rec.calls), rec.calls)
	}
}

func TestCloneSyncOutFailsWhenExpectedDirMissingOnExit(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1", Dirs: []string{".agent"}},
	}
	// .agent был занесён на входе (presentOnEntry его называет), но на
	// выходе почему-то пропал — это уже не «роль его не завела», а беда.
	// Первый вызов — warnUncommitted, дальше — сам упавший cp.
	rec := &recordedStep{errs: []error{errors.New("grep: нет совпадений"), errors.New(`ERROR: path ".../.agent" not found in container`)}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, []string{".agent"}, io.Discard); err == nil {
		t.Fatal("пропажа каталога, который сама же занесла cloneSyncIn, прошла молча")
	}
}

func TestCloneSyncOutPropagatesRealCPFailure(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1", Dirs: []string{".agent"}},
	}
	// Первый вызов — warnUncommitted, дальше — сам упавший cp.
	rec := &recordedStep{errs: []error{errors.New("grep: нет совпадений"), errors.New("sbx cp: connection refused")}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, io.Discard); err == nil {
		t.Fatal("настоящая неудача sbx cp растворилась в допущении «каталога не было»")
	}
}

// warnUncommitted не роняет прогон и не подменяет cloneErr — только
// дописывает наблюдение в log, когда grep внутри песочницы находит
// незакоммиченные правки (код 0), и молчит, когда не находит (код 1,
// step оборачивает в ошибку).
func TestCloneSyncOutWarnsAboutUncommittedWork(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)
	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1"},
	}

	t.Run("незакоммиченное найдено", func(t *testing.T) {
		rec := &recordedStep{} // grep находит совпадение → step отвечает nil
		var log bytes.Buffer
		if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, &log); err != nil {
			t.Fatalf("cloneSyncOut: %v", err)
		}
		if log.Len() == 0 {
			t.Error("незакоммиченная работа осталась незамеченной в логе")
		}
	})

	t.Run("дерево чистое", func(t *testing.T) {
		rec := &recordedStep{errs: []error{errors.New("grep: нет совпадений")}}
		var log bytes.Buffer
		if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, &log); err != nil {
			t.Fatalf("cloneSyncOut: %v", err)
		}
		if log.Len() != 0 {
			t.Errorf("предупреждение прозвучало на чистом дереве: %q", log.String())
		}
	})
}

func TestFetchBranchFastForwardsFetchInto(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)

	// агент внутри песочницы коммитит поверх того, что уже было в клоне —
	// в тесте это симулирует source: он и есть daemon-конец ремоута.
	source := runGitOutput(t, primary, "remote", "get-url", "sandbox-"+name)
	commit(t, source, "коммит агента внутри песочницы")

	c := &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1"}
	if err := fetchBranch(context.Background(), name, primary, c); err != nil {
		t.Fatalf("ветка не подтянута: %v", err)
	}

	got := runGitOutput(t, fetchInto, "rev-parse", "HEAD")
	want := runGitOutput(t, source, "rev-parse", "HEAD")
	if got != want {
		t.Errorf("fetchInto не на HEAD источника: %s != %s", got, want)
	}
}

// Раннер даёт на рабочую папку задачи ровно один прогон за раз: не-перемотка
// означает, что её тем временем двигал кто-то ещё, и слияние обязано
// отказать явно, а не затереть чужую работу.
func TestFetchBranchFailsOnNonFastForward(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)

	source := runGitOutput(t, primary, "remote", "get-url", "sandbox-"+name)
	commit(t, source, "коммит агента внутри песочницы")

	// fetchInto тем временем разошёлся: свой независимый коммит поверх той же базы.
	runGit(t, fetchInto, "commit", "--allow-empty", "-q", "-m", "параллельная правка")

	c := &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1"}
	if err := fetchBranch(context.Background(), name, primary, c); err == nil {
		t.Fatal("расхождение веток слито молча — рискует затереть чужую работу")
	}
}

// merge --ff-only сам по себе перематывает то, что сейчас выкачено в
// FetchInto, а не обязательно c.Branch — без явной проверки чужая ветка
// была бы передвинута молча.
func TestFetchBranchFailsWhenFetchIntoOnWrongBranch(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)
	runGit(t, fetchInto, "checkout", "-q", "-b", "не-та-ветка")

	c := &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1"}
	if err := fetchBranch(context.Background(), name, primary, c); err == nil {
		t.Fatal("подтяжка в неожиданную ветку прошла молча")
	}
}

func TestCreateArgsWithClone(t *testing.T) {
	l := fixtureLaunch()
	l.Clone = &runner.CloneSync{FetchInto: "/tmp/worktree", Branch: "task-1"}

	got := createArgs("office-550e8400", l)
	if !slices.Contains(got, "--clone") {
		t.Errorf("--clone не выставлен при l.Clone != nil: %q", got)
	}
	// --clone обязан стоять раньше позиционного агента и путей — иначе рискует
	// быть прочитан как их часть.
	agentIdx := slices.Index(got, Agent)
	cloneIdx := slices.Index(got, "--clone")
	if agentIdx < 0 || cloneIdx < 0 || cloneIdx > agentIdx {
		t.Errorf("--clone не перед позиционными аргументами: %q", got)
	}
}

func TestCreateArgsWithoutCloneOmitsFlag(t *testing.T) {
	got := createArgs("office-550e8400", fixtureLaunch())
	if slices.Contains(got, "--clone") {
		t.Errorf("--clone выставлен без l.Clone: %q", got)
	}
}

// Приоритет между таймаутом, кодом выхода агента и неудачей синхронизации
// --clone — самое уязвимое место всей обвязки (дороже всего молча потерять
// коммиты агента), и cloneOutcome проверяется отдельно от настоящего sbx.
func TestCloneOutcome(t *testing.T) {
	exitErr := func(code int) error {
		cmd := exec.Command("sh", "-c", "exit "+strconv.Itoa(code))
		return cmd.Run() // возвращает настоящий *exec.ExitError
	}

	cases := []struct {
		name           string
		timedOut       bool
		runErr         error
		cloneErr       error
		wantCode       int
		wantErr        bool
		wantTimeout    bool // err оборачивает runner.ErrRunTimeout — на нём стоит terminationOf в runagent.go
		wantErrLogged  bool // cloneErr дописан в лог, а не подменил возвращаемую ошибку
		wantErrReplace bool // cloneErr подменил собой возвращаемую ошибку/код
	}{
		{name: "успех без --clone", wantCode: 0, wantErr: false},
		{name: "успех, но синхронизация не удалась", cloneErr: errors.New("сеть"), wantCode: -1, wantErr: true, wantErrReplace: true},
		{name: "таймаут без беды синхронизации", timedOut: true, wantCode: -1, wantErr: true, wantTimeout: true},
		{name: "таймаут и беда синхронизации — таймаут остаётся причиной", timedOut: true, cloneErr: errors.New("сеть"), wantCode: -1, wantErr: true, wantTimeout: true, wantErrLogged: true},
		{name: "агент вышел с кодом 1, синхронизация в порядке", runErr: exitErr(1), wantCode: 1, wantErr: false},
		{name: "агент вышел с кодом 1, синхронизация не удалась", runErr: exitErr(1), cloneErr: errors.New("сеть"), wantCode: -1, wantErr: true, wantErrReplace: true},
		{name: "exec вовсе не запустился", runErr: errors.New("permission denied"), wantCode: -1, wantErr: true},
		{name: "exec не запустился и синхронизация тоже — первопричина остаётся", runErr: errors.New("permission denied"), cloneErr: errors.New("сеть"), wantCode: -1, wantErr: true, wantErrLogged: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var log bytes.Buffer
			code, err := cloneOutcome(&log, "office-test", tc.timedOut, 0, tc.runErr, tc.cloneErr)

			if code != tc.wantCode {
				t.Errorf("код = %d, ожидался %d", code, tc.wantCode)
			}
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if tc.wantTimeout && !errors.Is(err, runner.ErrRunTimeout) {
				t.Errorf("err не оборачивает runner.ErrRunTimeout (terminationOf это не заметит): %v", err)
			}
			if tc.wantErrReplace && tc.cloneErr != nil && err != nil && !strings.Contains(err.Error(), tc.cloneErr.Error()) {
				t.Errorf("cloneErr не подменил возвращаемую ошибку: %v", err)
			}
			if tc.wantErrLogged {
				if !strings.Contains(log.String(), tc.cloneErr.Error()) {
					t.Errorf("cloneErr не дописан в лог: %q", log.String())
				}
				if err != nil && strings.Contains(err.Error(), tc.cloneErr.Error()) {
					t.Errorf("cloneErr подменил собой первопричину вместо того, чтобы просто дописаться в лог: %v", err)
				}
			}
		})
	}
}

// setupCloneFixture заводит source (как будто внутриконтейнерный клон),
// primary (одноразовый клон-источник на хосте с ремоутом sandbox-<name>,
// который sbx create --clone сам бы завёл на git-daemon песочницы) и
// fetchInto (рабочая папка задачи) — все на ветке task-1 и в одном состоянии.
// fetchBranch до URL ремоута не привередлив: git одинаково фетчит что
// git-daemon, что локальный путь, и для теста второе проще и не требует sbx.
func setupCloneFixture(t *testing.T) (primary, fetchInto, sandboxName string) {
	t.Helper()

	source := initRepo(t)
	commit(t, source, "seed")
	runGit(t, source, "checkout", "-q", "-b", "task-1")
	commit(t, source, "первый коммит задачи")

	primary = cloneRepo(t, source, "task-1")
	name := "office-test"
	runGit(t, primary, "remote", "add", "sandbox-"+name, source)

	fetchInto = cloneRepo(t, source, "task-1")
	return primary, fetchInto, name
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "master", ".")
	return dir
}

func cloneRepo(t *testing.T, source, branch string) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", "--branch", branch, source, dir).CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
	return dir
}

func commit(t *testing.T, dir, message string) {
	t.Helper()
	runGit(t, dir, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", message)
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", full, err, out)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", full, err, out)
	}
	return strings.TrimSpace(string(out))
}
