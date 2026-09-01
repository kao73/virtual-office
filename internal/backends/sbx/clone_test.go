package sbx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// Независимое ревью: .comet/runtime несёт с собой locks/.coordinator
// Comet Native, а координатор не проверяет живость записанного там pid —
// перенос этого состояния между эфемерными песочницами превращает мёртвую
// блокировку одной снесённой песочницы в постоянную для всех последующих
// прогонов той же рабочей папки (docs/notes/stage-5-live-backlog.md,
// «Находки живого прогона EXP-2»). Свежая песочница обязана начинать
// с чистыми locks — остальной .comet/runtime переносится как есть.
func TestCloneSyncInClearsStaleLocksFromCometRuntime(t *testing.T) {
	primary := primaryWithExclude(t, ".comet/runtime")
	locksDir := filepath.Join(primary, ".comet/runtime/native/locks")
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locksDir, "root-move.lock"), []byte("stale pid"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{Dirs: []string{".comet/runtime"}},
	}
	rec := &recordedStep{}

	if _, err := cloneSyncIn(context.Background(), "office-test", l, rec.run); err != nil {
		t.Fatalf("cloneSyncIn: %v", err)
	}

	last := rec.calls[len(rec.calls)-1]
	want := []string{"exec", "office-test", "rm", "-rf", locksDir}
	if !slices.Equal(last, want) {
		t.Errorf("устаревшие locks не убраны последним вызовом\nполучено:  %q\nожидалось: %q", last, want)
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
// commitLeftovers обязана позваться до fetchBranch (см. её собственный
// doc-comment) — здесь это проверяется тем, что даже при заведомо
// провальном fetchBranch (несовпадение веток) один вызов run() всё равно
// происходит (сам commitLeftovers, на чистом дереве — только проверка,
// без коммита), а вот до каталогов Dirs дело дойти не должно вовсе.
func TestCloneSyncOutStopsAtDirsWhenFetchFails(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)
	// fetchInto уводим на другую ветку — fetchBranch откажет ещё до слияния.
	runGit(t, fetchInto, "checkout", "-q", "-b", "другая-ветка")

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1", Dirs: []string{".agent"}},
	}
	// Не идёт слияние (mergeInProgress), дерево чистое (grep не находит
	// совпадений) — commitLeftovers ограничится двумя проверками и не
	// полезет коммитить.
	rec := &recordedStep{errs: []error{errors.New("не идёт"), errors.New("grep: нет совпадений")}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, io.Discard); err == nil {
		t.Fatal("несовпадение веток не замечено — слияние не должно было даже начаться")
	}
	if len(rec.calls) != 2 {
		t.Errorf("ожидалось 2 вызова (обе проверки commitLeftovers до провалившегося fetchBranch), получено %d: %q",
			len(rec.calls), rec.calls)
	}
	for _, call := range rec.calls {
		if len(call) > 0 && call[0] == "cp" {
			t.Errorf("cp каталогов позван, хотя слияние ветки провалилось раньше: %q", rec.calls)
		}
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
	// Первые два вызова, до fetchBranch, — commitLeftovers (mergeInProgress,
	// потом git status внутри песочницы; дерево чистое, до коммита дело не
	// доходит); дальше — .agent синхронизировался штатно (значит,
	// presentOnEntry его называет), .comet в песочнице не заведён — sbx cp
	// отвечает так, как отвечает вживую (см. notFoundInContainer в clone.go).
	rec := &recordedStep{errs: []error{errors.New("не идёт"), errors.New("grep: нет совпадений"), nil, errors.New(`ERROR: path ".../.comet" not found in container`)}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, []string{".agent"}, io.Discard); err != nil {
		t.Fatalf("отсутствие .comet в песочнице (не занесённого на входе) не должно проваливать выгрузку: %v", err)
	}
	if len(rec.calls) != 4 {
		t.Fatalf("ожидалось 4 вызова (commitLeftovers x2 + 2 cp), получено %d: %q", len(rec.calls), rec.calls)
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
	// Первые два вызова — commitLeftovers (дерево чистое), дальше — сам упавший cp.
	rec := &recordedStep{errs: []error{errors.New("не идёт"), errors.New("grep: нет совпадений"), errors.New(`ERROR: path ".../.agent" not found in container`)}}

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
	// Первые два вызова — commitLeftovers (дерево чистое), дальше — сам упавший cp.
	rec := &recordedStep{errs: []error{errors.New("не идёт"), errors.New("grep: нет совпадений"), errors.New("sbx cp: connection refused")}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, io.Discard); err == nil {
		t.Fatal("настоящая неудача sbx cp растворилась в допущении «каталога не было»")
	}
}

// commitLeftovers не роняет прогон на пустом месте и не лезет коммитить,
// когда слияние не идёт, а grep внутри песочницы ничего не находит (код 1,
// step оборачивает в ошибку) — молчит и возвращает nil без третьего вызова.
func TestCommitLeftoversNoopWhenClean(t *testing.T) {
	rec := &recordedStep{errs: []error{errors.New("не идёт"), errors.New("grep: нет совпадений")}}
	var log bytes.Buffer

	if err := commitLeftovers(context.Background(), &log, "office-x", "/primary", nil, rec.run); err != nil {
		t.Fatalf("commitLeftovers на чистом дереве вернула ошибку: %v", err)
	}
	if len(rec.calls) != 2 {
		t.Errorf("на чистом дереве ожидалось 2 вызова (обе проверки), получено %d: %q", len(rec.calls), rec.calls)
	}
	if log.Len() != 0 {
		t.Errorf("сообщение о сохранении прозвучало на чистом дереве: %q", log.String())
	}
}

// Незавершённое слияние — commitLeftovers обязана отказаться от подчистки
// вовсе, а не смести конфликтные маркеры в коммит: `git add -A` на таком
// дереве разрешил бы пути с «<<<<<<<» прямо в индексе (независимое ревью,
// живой сценарий с усечённым по таймауту разрешением конфликта).
func TestCommitLeftoversSkipsWhenMergeInProgress(t *testing.T) {
	rec := &recordedStep{} // mergeInProgress: err == nil ⇒ маркер найден
	var log bytes.Buffer

	if err := commitLeftovers(context.Background(), &log, "office-x", "/primary", nil, rec.run); err != nil {
		t.Fatalf("commitLeftovers при незавершённом слиянии вернула ошибку: %v", err)
	}
	if len(rec.calls) != 1 {
		t.Errorf("при незавершённом слиянии ожидался ровно один вызов (сама проверка), получено %d: %q",
			len(rec.calls), rec.calls)
	}
	if log.Len() == 0 {
		t.Error("пропуск подчистки из-за незавершённого слияния не отмечен в логе")
	}
}

// Настоящий отказ git внутри mergeCheckScript (не «нет несовпадений» от
// grep, а реальный fatal git ls-files, например побитый индекс или унесённый
// primary) обязан читаться как «слияние идёт» — не тише, чем легитимный
// незавершённый merge: gitCheckFailed отличает его от штатного «код 1» по
// тексту fatal-ошибки в комбинированном выводе, который несёт обёрнутая
// ошибка step.
func TestMergeInProgressFailsClosedOnRealGitFailure(t *testing.T) {
	rec := &recordedStep{errs: []error{errors.New("sbx exec office-x sh -c ...: exit status 1\n" +
		"fatal: cannot change to '/primary': No such file or directory")}}

	if !mergeInProgress(context.Background(), "office-x", "/primary", rec.run) {
		t.Fatal("настоящий отказ git ls-files принят за «слияния нет»")
	}
}

// Тот же настоящий отказ git на dirtyCheckScript обязан стать видимой
// ошибкой commitLeftovers, а не молчаливым «нечего сохранять» — до этой
// правки оба случая (grep не нашёл совпадений и git реально упал) давали
// один и тот же ненулевой код выхода скрипта под sh, и настоящая беда
// терялась без единой строки в логе.
func TestCommitLeftoversSurfacesRealGitFailureOnDirtyCheck(t *testing.T) {
	rec := &recordedStep{errs: []error{
		errors.New("не идёт"), // mergeInProgress: слияния нет
		errors.New("sbx exec office-x sh -c ...: exit status 1\n" +
			"fatal: index file corrupt"), // dirtyCheckScript: настоящий отказ git
	}}
	var log bytes.Buffer

	err := commitLeftovers(context.Background(), &log, "office-x", "/primary", nil, rec.run)
	if err == nil {
		t.Fatal("настоящий отказ git status принят за «дерево чистое»")
	}
	if !strings.Contains(err.Error(), "не проверено") {
		t.Errorf("ошибка не называет причину: %v", err)
	}
}

// Незакоммиченное найдено (mergeInProgress — не идёт, grep — код 0, step
// отвечает nil) — commitLeftovers обязана добавить и закоммитить его
// отдельной, не-агентской личностью (cloneSweepName/cloneSweepEmail, через
// переменные окружения, а не -c — см. doc-комментарий), исключив dirs через
// add+reset (см. doc-комментарий addScript) и передав primary позиционным
// аргументом, а не подставив его в текст скрипта.
func TestCommitLeftoversCommitsWhenDirty(t *testing.T) {
	// call0: не идёт слияние. call1: дирти. call2: add — успех. call3: staged
	// непусто (err != nil ⇒ есть что коммитить). call4: commit — успех (по умолчанию nil).
	rec := &recordedStep{errs: []error{errors.New("не идёт"), nil, nil, errors.New("непусто")}}
	var log bytes.Buffer

	dirs := []string{".agent", ".comet/runtime"}
	if err := commitLeftovers(context.Background(), &log, "office-x", "/primary", dirs, rec.run); err != nil {
		t.Fatalf("commitLeftovers: %v", err)
	}
	if len(rec.calls) != 5 {
		t.Fatalf("ожидалось 5 вызовов (merge + dirty + add + staged + commit), получено %d: %q",
			len(rec.calls), rec.calls)
	}

	addCall := rec.calls[2]
	if !slices.Contains(addCall, "/primary") {
		t.Errorf("primary не передан add-вызову позиционным аргументом: %q", addCall)
	}
	for _, dir := range dirs {
		if !slices.Contains(addCall, dir) {
			t.Errorf("исключение %q не передано add-вызову отдельным аргументом: %q", dir, addCall)
		}
	}

	commitCall := rec.calls[4]
	joined := strings.Join(commitCall, " ")
	for _, want := range []string{
		"commit", "--no-verify",
		`GIT_AUTHOR_NAME="` + cloneSweepName + `"`, `GIT_AUTHOR_EMAIL="` + cloneSweepEmail + `"`,
		`GIT_COMMITTER_NAME="` + cloneSweepName + `"`, `GIT_COMMITTER_EMAIL="` + cloneSweepEmail + `"`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("коммит-вызов не содержит %q: %s", want, joined)
		}
	}
	if !slices.Contains(commitCall, "/primary") {
		t.Errorf("primary не передан commit-вызову позиционным аргументом: %q", commitCall)
	}
	if log.Len() == 0 {
		t.Error("сохранённая незакоммиченная работа не отмечена в логе")
	}
}

// Important-находка независимого ревью: `git status --porcelain` и `git add
// -A -- .` плюс `reset -q -- dirs` смотрят на разные множества — то, что
// было грязным, не обязано остаться застейдженным после исключения dirs
// (или не стейджится add'ом вовсе, как указатель подмодуля). Без явной проверки
// между add и commit это превращало совершенно здоровый прогон в ложный
// провал синхронизации.
func TestCommitLeftoversNoopWhenNothingStagedAfterFiltering(t *testing.T) {
	// call0: не идёт слияние. call1: дирти. call2: add — успех.
	// call3: staged пусто (err == nil ⇒ commitLeftovers должна остановиться).
	rec := &recordedStep{errs: []error{errors.New("не идёт"), nil, nil, nil}}
	var log bytes.Buffer

	if err := commitLeftovers(context.Background(), &log, "office-x", "/primary", []string{".agent"}, rec.run); err != nil {
		t.Fatalf("нечего коммитить после фильтрации не должно быть ошибкой: %v", err)
	}
	if len(rec.calls) != 4 {
		t.Fatalf("ожидалось 4 вызова (merge + dirty + add + staged), без коммита, получено %d: %q",
			len(rec.calls), rec.calls)
	}
	if log.Len() != 0 {
		t.Errorf("сообщение о сохранении прозвучало, хотя коммита не было: %q", log.String())
	}
}

// Неудача самого git add — настоящая ошибка: не добраться даже до вопроса,
// есть ли что коммитить.
func TestCommitLeftoversPropagatesAddFailure(t *testing.T) {
	rec := &recordedStep{errs: []error{errors.New("не идёт"), nil, errors.New("git: index corrupt")}}
	var log bytes.Buffer

	err := commitLeftovers(context.Background(), &log, "office-x", "/primary", nil, rec.run)
	if err == nil {
		t.Fatal("неудача git add прошла молча")
	}
	if log.Len() != 0 {
		t.Errorf("лог не должен утверждать успех при провалившемся add: %q", log.String())
	}
}

// Неудача самого commit (после того как есть что коммитить) — настоящая
// ошибка, которую cloneSyncOut обязан пробросить дальше, а не проглотить
// молча: цена та же, что у потери коммитов агента.
func TestCommitLeftoversPropagatesCommitFailure(t *testing.T) {
	rec := &recordedStep{errs: []error{errors.New("не идёт"), nil, nil, errors.New("непусто"), errors.New("git: identity unknown")}}
	var log bytes.Buffer

	err := commitLeftovers(context.Background(), &log, "office-x", "/primary", nil, rec.run)
	if err == nil {
		t.Fatal("неудача коммита-подчистки прошла молча")
	}
	if log.Len() != 0 {
		t.Errorf("лог не должен утверждать успех при провалившемся коммите: %q", log.String())
	}
}

// cloneSyncOut обязана позвать commitLeftovers и пробросить её ошибку дальше
// как cloneErr, а не проигнорировать: warnUncommitted раньше только
// предупреждала и терялась в этом же месте была бы точно та потеря, которую
// commitLeftovers существует, чтобы закрыть.
func TestCloneSyncOutPropagatesCommitLeftoversFailure(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)
	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1"},
	}
	// Не идёт слияние, дирти найдено, add и staged в порядке, но сам коммит проваливается.
	rec := &recordedStep{errs: []error{errors.New("не идёт"), nil, nil, errors.New("непусто"), errors.New("git: identity unknown")}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, io.Discard); err == nil {
		t.Fatal("неудача commitLeftovers не остановила cloneSyncOut")
	}
}

// Critical-находка независимого ревью: раньше commitLeftovers стояла перед
// fetchBranch и, провалившись (например, .git/index.lock от прерванного
// git commit роли), отменяла его совсем — настоящие коммиты агента
// терялись вместе с песочницей ради того, чтобы не потерять объедки,
// которых, возможно, и не было. fetchBranch обязан выполниться и подтянуть
// реальный коммит независимо от исхода commitLeftovers — ошибка копится
// и возвращается только после него.
func TestCloneSyncOutFetchesRealCommitsEvenWhenCommitLeftoversFails(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)
	source := runGitOutput(t, primary, "remote", "get-url", "sandbox-"+name)
	commit(t, source, "настоящий коммит агента")

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1"},
	}
	rec := &recordedStep{errs: []error{errors.New("не идёт"), nil, errors.New("git: identity unknown")}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, io.Discard); err == nil {
		t.Fatal("неудача commitLeftovers не вернула ошибку")
	}

	got := runGitOutput(t, fetchInto, "rev-parse", "HEAD")
	want := runGitOutput(t, source, "rev-parse", "HEAD")
	if got != want {
		t.Errorf("настоящий коммит агента не подтянут при провалившейся подчистке: fetchInto=%s source=%s", got, want)
	}
}

// Важная находка независимого ревью: до этой правки sweepErr возвращался
// сразу после fetchBranch и до цикла по l.Clone.Dirs, так что провалившаяся
// подчистка отменяла и подтяжку .agent/result.json уже состоявшегося
// прогона — здоровый прогон читался бы дальше по цепочке (runagent.
// ReadResult) как синтетический `failed`. С Dirs: nil (как в
// TestCloneSyncOutFetchesRealCommitsEvenWhenCommitLeftoversFails выше) эта
// потеря не видна — цикл пуст в обоих случаях.
func TestCloneSyncOutRetrievesDirsEvenWhenCommitLeftoversFails(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)
	source := runGitOutput(t, primary, "remote", "get-url", "sandbox-"+name)
	commit(t, source, "настоящий коммит агента")

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1", Dirs: []string{".agent"}},
	}
	rec := &recordedStep{errs: []error{errors.New("не идёт"), nil, errors.New("git: identity unknown")}}

	if err := cloneSyncOut(context.Background(), name, l, rec.run, nil, io.Discard); err == nil {
		t.Fatal("неудача commitLeftovers не вернула ошибку")
	}

	var sawCopy bool
	for _, call := range rec.calls {
		if len(call) > 0 && call[0] == "cp" {
			sawCopy = true
		}
	}
	if !sawCopy {
		t.Error(".agent не подтянут после провалившейся подчистки — sweepErr отменил retrieval")
	}
}

// execStep исполняет вызовы вида exec <name> sh -c <скрипт> sh <args...>
// по-настоящему, через sh — именно так зовут commitLeftovers и
// mergeInProgress. Используется там, где тест должен проверить настоящий
// эффект скрипта (кавычки, pathspec), а не только форму вызова.
func execStep(t *testing.T) step {
	t.Helper()
	return func(ctx context.Context, args ...string) error {
		if len(args) < 3 || args[0] != "exec" {
			t.Fatalf("execStep умеет только exec <name> <argv...>: %q", args)
		}
		cmd := exec.CommandContext(ctx, args[2], args[3:]...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w\n%s", args, err, out)
		}
		return nil
	}
}

// realSandboxStep — execStep, но с primary, подменённым на source: внутри
// настоящей песочницы commitLeftovers видит primary как путь к собственному
// клону песочницы (здесь — source), а не к одноразовому клону-источнику на
// хосте, которым в этом файле является primary.
func realSandboxStep(t *testing.T, primary, source string) step {
	t.Helper()
	inner := execStep(t)
	return func(ctx context.Context, args ...string) error {
		rewritten := append([]string(nil), args...)
		for i, a := range rewritten {
			if a == primary {
				rewritten[i] = source
			}
		}
		return inner(ctx, rewritten...)
	}
}

// Важная находка независимого ревью (#7): предыдущие тесты проверяли только
// форму вызова через recordedStep — здесь commitLeftovers прогоняется по
// настоящему пути через fetchBranch до FetchInto, с настоящим git.
func TestCommitLeftoversReachesFetchIntoThroughFetchBranch(t *testing.T) {
	primary, fetchInto, name := setupCloneFixture(t)
	source := runGitOutput(t, primary, "remote", "get-url", "sandbox-"+name)

	if err := os.WriteFile(filepath.Join(source, "leftover.txt"), []byte("забыли закоммитить\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	l := &runner.Launch{
		Workspaces: []runner.Workspace{{Path: primary}},
		Clone:      &runner.CloneSync{FetchInto: fetchInto, Branch: "task-1"},
	}

	if err := cloneSyncOut(context.Background(), name, l, realSandboxStep(t, primary, source), nil, io.Discard); err != nil {
		t.Fatalf("cloneSyncOut: %v", err)
	}

	if _, err := os.Stat(filepath.Join(fetchInto, "leftover.txt")); err != nil {
		t.Fatalf("подчищенный файл не доехал до fetchInto: %v", err)
	}
	authorName := runGitOutput(t, fetchInto, "log", "-1", "--format=%an")
	if authorName != cloneSweepName {
		t.Errorf("коммит-подчистка приписан не той личности: %q, ожидалось %q", authorName, cloneSweepName)
	}
}

// Регрессия на находку независимого ревью (#2): раньше primary был
// подставлен в текст `sh -c` строкой, а не передан позиционным аргументом
// — путь с пробелом молча ломал команду (git читал бы её как «cd в
// /primary/my» с обрубленным «repo»), и commitLeftovers принимала эту
// поломку за «дерево чистое», теряя реальную незакоммиченную работу без
// единой строки в логе.
func TestCommitLeftoversHandlesPathWithSpace(t *testing.T) {
	base := t.TempDir()
	primary := filepath.Join(base, "my repo")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, primary, "init", "-q", "-b", "master", ".")
	commit(t, primary, "seed")
	if err := os.WriteFile(filepath.Join(primary, "leftover.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	if err := commitLeftovers(context.Background(), &log, "office-x", primary, nil, execStep(t)); err != nil {
		t.Fatalf("commitLeftovers на пути с пробелом: %v", err)
	}

	subject := runGitOutput(t, primary, "log", "-1", "--format=%s")
	if !strings.Contains(subject, "preserve sandbox-local changes") {
		t.Errorf("подчистка не закоммитила на пути с пробелом (HEAD: %q) — подстановка пути строкой сломала бы это молча", subject)
	}
}

// Регрессия на находку независимого ревью (#6): даже если .git/info/exclude
// внутри песочницы пуст (например, syncExcludeFile промолчала — источника
// на хосте не нашлось), конверт обмена не должен уехать в коммит-подчистку.
// add+reset на dirs (см. doc-комментарий addScript) не зависит от копии правил.
func TestCommitLeftoversExcludesNamedDirsEvenWithoutGitExclude(t *testing.T) {
	primary := t.TempDir()
	runGit(t, primary, "init", "-q", "-b", "master", ".")
	commit(t, primary, "seed")
	if err := os.MkdirAll(filepath.Join(primary, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, ".agent", "result.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, "leftover.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Намеренно без .git/info/exclude — это и есть регрессионный сценарий.

	var log bytes.Buffer
	if err := commitLeftovers(context.Background(), &log, "office-x", primary, []string{".agent"}, execStep(t)); err != nil {
		t.Fatalf("commitLeftovers: %v", err)
	}

	tracked := runGitOutput(t, primary, "show", "--stat", "--format=", "HEAD")
	if strings.Contains(tracked, ".agent") {
		t.Errorf(".agent уехал в коммит-подчистку без git-исключений: %s", tracked)
	}
	if !strings.Contains(tracked, "leftover.txt") {
		t.Errorf("leftover.txt не закоммичен: %s", tracked)
	}
}

// Регрессия на находку независимого ревью (#3): незавершённое слияние не
// должно попасть в коммит-подчистку — настоящий MERGE_HEAD/конфликтный
// путь должен остановить commitLeftovers так же, как в
// TestCommitLeftoversSkipsWhenMergeInProgress, но здесь через настоящий git.
func TestCommitLeftoversSkipsRealMergeConflict(t *testing.T) {
	base := initRepo(t)
	commit(t, base, "seed")
	runGit(t, base, "checkout", "-q", "-b", "a")
	if err := os.WriteFile(filepath.Join(base, "f.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, base, "add", "f.txt")
	commit(t, base, "a-версия")

	runGit(t, base, "checkout", "-q", "master")
	runGit(t, base, "checkout", "-q", "-b", "b")
	if err := os.WriteFile(filepath.Join(base, "f.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, base, "add", "f.txt")
	commit(t, base, "b-версия")

	// Сливаем a в b — конфликт, MERGE_HEAD остаётся на месте.
	mergeCmd := exec.Command("git", "-C", base, "merge", "a")
	_ = mergeCmd.Run() // ожидаемо ненулевой код — конфликт

	if _, err := os.Stat(filepath.Join(base, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("подготовка теста: конфликт слияния не начался: %v", err)
	}

	var log bytes.Buffer
	if err := commitLeftovers(context.Background(), &log, "office-x", base, nil, execStep(t)); err != nil {
		t.Fatalf("commitLeftovers при настоящем конфликте вернула ошибку: %v", err)
	}

	if _, err := os.Stat(filepath.Join(base, ".git", "MERGE_HEAD")); err != nil {
		t.Error("MERGE_HEAD пропал — подчистка тронула незавершённое слияние")
	}
	if strings.Contains(runGitOutput(t, base, "log", "-1", "--format=%s"), "preserve sandbox-local changes") {
		t.Error("конфликт слияния закоммичен подчисткой как будто он разрешён")
	}
}

// Critical-находка узкого повторного ревью: конфликт бывает и без единого
// файла-маркера слияния — конфликтующий `git stash pop` (implementer может
// засташить перед сверкой) оставляет ровно те же неразрешённые записи
// индекса, но ни MERGE_HEAD, ни rebase-merge/-apply не заводит. Проверка
// по mergeInProgress (сейчас — git ls-files --unmerged) обязана поймать
// именно эту разновидность конфликта, а не только «после git merge».
func TestCommitLeftoversSkipsUnmergedStashConflict(t *testing.T) {
	base := initRepo(t)
	if err := os.WriteFile(filepath.Join(base, "f.txt"), []byte("база\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, base, "add", "f.txt")
	commit(t, base, "seed")

	if err := os.WriteFile(filepath.Join(base, "f.txt"), []byte("рабочая копия\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, base, "-c", "user.email=t@t", "-c", "user.name=t", "stash", "push", "-q", "-m", "wip")
	if err := os.WriteFile(filepath.Join(base, "f.txt"), []byte("новый коммит\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, base, "add", "f.txt")
	commit(t, base, "разошлось со stash")

	// pop конфликтует с новым коммитом — ожидаемо ненулевой код.
	popCmd := exec.Command("git", "-C", base, "stash", "pop")
	_ = popCmd.Run()

	status := runGitOutput(t, base, "status", "--porcelain")
	if !strings.Contains(status, "UU") {
		t.Fatalf("подготовка теста: неразрешённый конфликт stash pop не начался (status: %q)", status)
	}
	if _, err := os.Stat(filepath.Join(base, ".git", "MERGE_HEAD")); err == nil {
		t.Fatal("подготовка теста: stash pop неожиданно завёл MERGE_HEAD — сценарий больше не отличим от обычного merge-конфликта")
	}

	var log bytes.Buffer
	if err := commitLeftovers(context.Background(), &log, "office-x", base, nil, execStep(t)); err != nil {
		t.Fatalf("commitLeftovers при конфликте stash pop вернула ошибку: %v", err)
	}

	subject := runGitOutput(t, base, "log", "-1", "--format=%s")
	if strings.Contains(subject, "preserve sandbox-local changes") {
		t.Error("конфликт stash pop закоммичен подчисткой как будто он разрешён (маркеры <<<<<<< теперь в истории)")
	}
}

// Important-находка узкого повторного ревью: `git status --porcelain`
// и `git add -A -- .` плюс `reset -q -- dirs» смотрят на разные множества —
// если всё грязное отфильтровано исключением dirs, после reset ничего не
// останется застейдженным, и без явной проверки это раньше превращалось в ложный отказ
// commitLeftovers (а через неё — во всю выгрузку --clone) на совершенно
// здоровом прогоне, которому просто нечего было спасать.
func TestCommitLeftoversNoopWhenEverythingFilteredOutReal(t *testing.T) {
	primary := t.TempDir()
	runGit(t, primary, "init", "-q", "-b", "master", ".")
	commit(t, primary, "seed")
	if err := os.MkdirAll(filepath.Join(primary, ".agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(primary, ".agent", "result.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Никакого другого незакоммиченного — только то, что исключит pathspec.

	var log bytes.Buffer
	if err := commitLeftovers(context.Background(), &log, "office-x", primary, []string{".agent"}, execStep(t)); err != nil {
		t.Fatalf("нечего коммитить после фильтрации не должно быть ошибкой: %v", err)
	}

	head := runGitOutput(t, primary, "rev-parse", "HEAD")
	seedHead := runGitOutput(t, primary, "rev-list", "--max-parents=0", "HEAD")
	if head != seedHead {
		t.Error("появился лишний коммит, хотя коммитить было нечего")
	}
	if log.Len() != 0 {
		t.Errorf("сообщение о сохранении прозвучало, хотя коммита не было: %q", log.String())
	}
}

// execStepWithEnv — execStep, но со своим окружением у самого шелл-процесса.
// Regression-находка узкого повторного ревью: сегодня у exec-вызовов
// commitLeftovers нет чужого GIT_AUTHOR_NAME (identityVars идут --env
// только на запуск самого агента, sbx.go execArgs, не на эти вызовы), но
// свойство «переменные окружения commitScript побеждают унаследованное
// окружение» должно оставаться верным и тогда, когда оно появится.
func execStepWithEnv(t *testing.T, extraEnv []string) step {
	t.Helper()
	return func(ctx context.Context, args ...string) error {
		if len(args) < 3 || args[0] != "exec" {
			t.Fatalf("execStepWithEnv умеет только exec <name> <argv...>: %q", args)
		}
		cmd := exec.CommandContext(ctx, args[2], args[3:]...)
		cmd.Env = append(os.Environ(), extraEnv...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w\n%s", args, err, out)
		}
		return nil
	}
}

func TestCommitLeftoversIdentityWinsOverAmbientEnv(t *testing.T) {
	primary := t.TempDir()
	runGit(t, primary, "init", "-q", "-b", "master", ".")
	commit(t, primary, "seed")
	if err := os.WriteFile(filepath.Join(primary, "leftover.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := []string{
		"GIT_AUTHOR_NAME=agent-implementer", "GIT_AUTHOR_EMAIL=implementer@office.local",
		"GIT_COMMITTER_NAME=agent-implementer", "GIT_COMMITTER_EMAIL=implementer@office.local",
	}
	var log bytes.Buffer
	if err := commitLeftovers(context.Background(), &log, "office-x", primary, nil, execStepWithEnv(t, env)); err != nil {
		t.Fatalf("commitLeftovers: %v", err)
	}

	got := runGitOutput(t, primary, "log", "-1", "--format=%an <%ae>")
	want := cloneSweepName + " <" + cloneSweepEmail + ">"
	if got != want {
		t.Errorf("личность коммита-подчистки = %q, ожидалось %q — окружение роли её перебило", got, want)
	}
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
