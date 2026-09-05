package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/budget"
	"github.com/kao73/virtual-office/internal/ledger"
	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/mock"
	"github.com/kao73/virtual-office/internal/workspace"
)

var now = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// fakeAgent — поддельный прогон: возвращает заданный результат и, если велено,
// оставляет коммит в рабочей папке. Настоящий агент подключается тем же
// интерфейсом, поэтому конвейер проверяется целиком, без трат на модель.
type fakeAgent struct {
	result runner.Result
	// byRole — результат на роль: в цикле без --role за один заход работают обе,
	// и один результат на всех означал бы, что ревьюер отвечает словами автора.
	byRole map[string]runner.Result
	err    error
	commit string // сообщение коммита; пусто — агент ничего не сделал
	// work — что агент делает в рабочей папке. Пусто — ничего не делает:
	// большинству проверок конвейера содержимое папки безразлично.
	work func(req Request)
	// act — поведение прогона целиком: и работа в папке, и результат. Нужен
	// сквозному сценарию, где роль на втором прогоне делает не то же, что
	// на первом, а порядок прогонов и есть предмет проверки.
	act func(req Request, run int) runner.Result
	// runsByRole — сколько раз каждая роль уже отработала: по этому счётчику
	// act и различает прогоны.
	runsByRole map[string]int
	// usage — во что обошёлся прогон. Пустое значение законно и означает
	// «неизвестно»: так выглядит прогон, не оставивший итогового события.
	usage runner.Usage
	// termination — чем кончился прогон. Пустое значение означает обычный
	// прогон с результатом; сценариям про обрыв и усечение оно задаётся явно.
	termination runner.Termination

	seen Request // что конвейер отдал агенту
	runs int
	// context — контекст, собранный раннером, по ролям: он живёт в рабочей папке,
	// а её однажды сносит системный проход, дойдя до терминальной задачи.
	context map[string]string
}

func (f *fakeAgent) Run(_ context.Context, req Request) (AgentRun, error) {
	f.seen, f.runs = req, f.runs+1
	if raw, err := os.ReadFile(filepath.Join(req.Workdir, runner.Dir, runner.FileContext)); err == nil {
		if f.context == nil {
			f.context = map[string]string{}
		}
		f.context[req.Role.Name] = string(raw)
	}
	if f.runsByRole == nil {
		f.runsByRole = map[string]int{}
	}
	f.runsByRole[req.Role.Name]++
	if f.act != nil {
		return f.ran(f.act(req, f.runsByRole[req.Role.Name])), f.err
	}
	if f.work != nil {
		f.work(req)
	}
	if f.commit != "" {
		gitIn(req.Workdir, "commit", "-q", "--allow-empty", "-m", f.commit)
	}
	if result, found := f.byRole[req.Role.Name]; found {
		return f.ran(result), f.err
	}
	return f.ran(f.result), f.err
}

// ran — ответ подделки в том же виде, в каком его даёт настоящий прогон.
//
// Пустое termination означает обычный прогон, дошедший до конца: повторять это
// в каждом тесте незачем, а вот молчать о нём нельзя — по нему конвейер решает,
// тратить ли попытку.
func (f *fakeAgent) ran(result runner.Result) AgentRun {
	term := f.termination
	if term.Kind == "" {
		term.Kind = runner.TerminationCompleted
	}
	return AgentRun{Result: result, Usage: f.usage, Termination: term}
}

// fakeSandboxes — поддельный уборщик песочниц: запоминает, за чьими прогонами
// приходили. Настоящий уборщик подключается тем же интерфейсом, поэтому reap
// проверяется целиком, без sbx на машине.
type fakeSandboxes struct {
	removed []string
	absent  bool // песочницы прогона на этой машине нет
	err     error
}

func (f *fakeSandboxes) Remove(runID string) (bool, error) {
	f.removed = append(f.removed, runID)
	return !f.absent && f.err == nil, f.err
}

func gitIn(dir string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=агент", "GIT_AUTHOR_EMAIL=agent@office.local",
		"GIT_COMMITTER_NAME=агент", "GIT_COMMITTER_EMAIL=agent@office.local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		panic(fmt.Sprintf("git %s: %v\n%s", strings.Join(args, " "), err, out))
	}
	return strings.TrimSpace(string(out))
}

// office — конвейер над файловым трекером и локальным репозиторием.
type office struct {
	*Office
	tasks  *mock.Tracker
	agent  *fakeAgent
	origin string
	// home — хозяйство раннера этого офиса: там же лежит и реестр прогонов.
	home string
}

func newOffice(t *testing.T) *office {
	t.Helper()
	root := repoRoot(t)

	wf, err := tracker.LoadWorkflow(filepath.Join(root, tracker.WorkflowFile))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}

	origin := originRepo(t)
	tasks := mock.New(t.TempDir())
	tasks.Now = func() time.Time { return now }
	agent := &fakeAgent{result: done("сделано")}

	// Учётки — как их собирает сборка офиса: общая для системных записей
	// и по одной на роль. Собирать их иначе, чем в проде, значит проверять не то.
	trackers := map[string]tracker.Tracker{}
	accounts := []string{mock.Account}
	for _, role := range wf.Order() {
		trackers[role] = tasks.As(mock.RoleAccount(role))
		accounts = append(accounts, mock.RoleAccount(role))
	}

	home := t.TempDir()
	o := &office{
		tasks:  tasks,
		agent:  agent,
		origin: origin,
		home:   home,
		Office: &Office{
			Tracker:    tasks,
			Trackers:   trackers,
			Workspaces: workspace.New(home),
			Ledger:     ledger.New(filepath.Join(home, ledger.FileName)),
			Workflow:   wf,
			Projects: tracker.Projects{"OFF": {
				RepoURL: origin, DefaultBranch: "master", BranchPrefix: "agent/", Tracker: "mock",
			}},
			Agent:      agent,
			ConfigRoot: root,
			ConfigSHA:  "5bc6a3b0",
			Accounts:   accounts,
			Now:        func() time.Time { return now },
			Log:        io.Discard,
		},
	}
	o.add("OFF-1", "Ready")
	return o
}

// addTried заводит задачу, у которой уже есть неудачные попытки.
func (o *office) addTried(key, status string, attempts int) {
	if err := o.tasks.Add(tracker.Task{
		Key: key, Project: "OFF", Status: status, Attempts: attempts,
		Summary: "Задача " + key, Description: "Сделать что-нибудь полезное.",
	}); err != nil {
		panic(err)
	}
}

func (o *office) add(key, status string) {
	if err := o.tasks.Add(tracker.Task{
		Key: key, Project: "OFF", Status: status,
		Summary: "Задача " + key, Description: "Сделать что-нибудь полезное.",
	}); err != nil {
		panic(err)
	}
}

// useTracker подменяет трекер офиса целиком — и общий, и ролевые. Подменить
// только общий значило бы не подменить ничего там, где тик ходит под учёткой роли.
func (o *office) useTracker(tasks tracker.Tracker) {
	o.Office.Tracker = tasks
	for role := range o.Office.Trackers {
		o.Office.Trackers[role] = tasks
	}
}

func (o *office) get(t *testing.T, key string) tracker.Task {
	t.Helper()
	task, err := o.tasks.Get(key)
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	return task
}

// tickAll прогоняет цикл по всем ролям, как это делает `runner tick` без --role.
func (o *office) tickAll(t *testing.T) {
	t.Helper()
	if err := o.TickAll(context.Background()); err != nil {
		t.Fatalf("цикл не прошёл: %v", err)
	}
}

// tick прогоняет цикл и падает на инфраструктурной ошибке.
func (o *office) tick(t *testing.T) bool {
	t.Helper()
	worked, err := o.Tick(context.Background(), "implementer")
	if err != nil {
		t.Fatalf("цикл не прошёл: %v", err)
	}
	return worked
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("корень репозитория не определён: %v", err)
	}
	return root
}

// originRepo — «удалённый» репозиторий проекта-клиента с одним коммитом.
func originRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bare, seed := filepath.Join(root, "origin.git"), filepath.Join(root, "seed")

	if out, err := exec.Command("git", "init", "-q", "--bare", "-b", "master", bare).CombinedOutput(); err != nil {
		t.Fatalf("origin не создан: %v\n%s", err, out)
	}
	if out, err := exec.Command("git", "init", "-q", "-b", "master", seed).CombinedOutput(); err != nil {
		t.Fatalf("посевной репозиторий не создан: %v\n%s", err, out)
	}
	gitIn(seed, "commit", "-q", "--allow-empty", "-m", "начало")
	gitIn(seed, "remote", "add", "origin", bare)
	gitIn(seed, "push", "-q", "origin", "master")
	return bare
}

func done(summary string) runner.Result {
	return runner.Result{Outcome: runner.OutcomeDone, Summary: summary, NextOwner: "none"}
}

// lastComment — последний комментарий задачи.
func lastComment(t *testing.T, task tracker.Task) tracker.Comment {
	t.Helper()
	if len(task.Comments) == 0 {
		t.Fatalf("у %s нет комментариев", task.Key)
	}
	return task.Comments[len(task.Comments)-1]
}

// Счастливый путь целиком: задача захвачена, агент отработал, работа опубликована,
// отчёт написан, задача уехала в Review, аренда снята.
func TestTickDoneMovesTaskToReview(t *testing.T) {
	o := newOffice(t)
	o.agent.commit = "работа агента"

	if !o.tick(t) {
		t.Fatal("цикл не взял задачу")
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Review" {
		t.Errorf("статус %q, ожидался Review", task.Status)
	}
	if task.LeaseAlive(now) || task.RunID != "" {
		t.Errorf("аренда не снята: %+v", task)
	}

	comment := lastComment(t, task)
	marker, ok := tracker.MarkerOf(comment.Body)
	if !ok {
		t.Fatalf("отчёт без маркера:\n%s", comment.Body)
	}
	if marker.Role != "implementer" || marker.Outcome != "done" {
		t.Errorf("маркер отчёта: %+v", marker)
	}
	if !strings.Contains(comment.Body, "сделано") {
		t.Errorf("итога нет в отчёте:\n%s", comment.Body)
	}

	// Работа не должна остаться только в worktree: ветку публикуют всегда,
	// когда на ней есть коммиты.
	if got := gitIn(o.origin, "log", "--oneline", "-1", "agent/OFF-1"); !strings.Contains(got, "работа агента") {
		t.Errorf("ветка не опубликована: %s", got)
	}
	if !strings.Contains(comment.Body, "agent/OFF-1") {
		t.Errorf("в отчёте нет ссылки на ветку:\n%s", comment.Body)
	}
}

// Независимое ревью (раунд 3): предыдущий раунд отклонил эту проверку как
// непроверяемую на этом уровне, сославшись на то, что fakeAgent обходит
// runagent.terminationOf — тот код, который в проде решает по BaseCommit,
// «начинал ли прогон работу». Это была ошибка именно в этой части: работа
// не обязана заходить так далеко, чтобы проверить сам пересчёт. work()
// кладёт пересчитанный passport.BaseCommit прямо в Request, который получает
// Agent.Run, а fakeAgent.seen фиксирует этот Request как есть — значит
// значение видно тесту напрямую, до всякого terminationOf.
//
// .comet/config.yaml без блока hook: в origin — так же, как делает живая
// задача, заведшая изменение Comet Native, но ещё не подобранная
// implementer'ом или reviewer'ом (EnsureCometHookAllowPaths, роль об этом
// блоке может не знать вовсе). PrepareInput сама допишет и закоммитит этот
// блок при подготовке входа — и именно этот коммит обязан попасть
// в BaseCommit вместо снятого раньше.
func TestWorkRecomputesBaseCommitAfterPrepareInputCommit(t *testing.T) {
	o := newOffice(t)

	clone := t.TempDir()
	if out, err := exec.Command("git", "clone", "-q", o.origin, clone).CombinedOutput(); err != nil {
		t.Fatalf("клон origin не создан: %v\n%s", err, out)
	}
	if err := os.MkdirAll(filepath.Join(clone, ".comet"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, ".comet", "config.yaml"), []byte("schema: comet.project.v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(clone, "add", ".")
	gitIn(clone, "commit", "-q", "-m", "заводит .comet/config.yaml без hook:")
	gitIn(clone, "push", "-q", "origin", "master")
	configCommit := gitIn(clone, "rev-parse", "HEAD")

	o.tick(t)

	got := o.agent.seen.Passport.BaseCommit
	if got == "" {
		t.Fatal("BaseCommit не заполнен")
	}
	if got == configCommit {
		t.Fatalf("BaseCommit не пересчитан после PrepareInput: остался равен коммиту до её служебной правки (%s)", configCommit)
	}
	subject := gitIn(o.agent.seen.Workdir, "show", "-s", "--format=%s", got)
	if subject != "chore: add hook.allow_paths to .comet/config.yaml" {
		t.Errorf("BaseCommit указывает не на коммит EnsureCometHookAllowPaths: тема %q", subject)
	}
	if parent := gitIn(o.agent.seen.Workdir, "rev-parse", got+"^"); parent != configCommit {
		t.Errorf("коммит BaseCommit не идёт сразу за посевным коммитом .comet/config.yaml: родитель %s, ожидался %s", parent, configCommit)
	}
}

// spend — сколько уже потрачено по реестру офиса.
func (o *office) spend(t *testing.T, f ledger.Filter) ledger.Total {
	t.Helper()
	total, err := o.Office.Ledger.Sum(f)
	if err != nil {
		t.Fatalf("реестр не прочитан: %v", err)
	}
	return total
}

// events — события офиса в переписке задачи.
func events(task tracker.Task) []string {
	var found []string
	for _, c := range task.Comments {
		if m, ok := tracker.MarkerOf(c.Body); ok && m.Event != "" {
			found = append(found, m.Event)
		}
	}
	return found
}

// Учёт ведётся всегда и ничего не решает: строка на прогон в реестре появляется
// независимо от того, настроены ли лимиты.
// Задача 2 (docs/superpowers/specs/2026-09-02-pipeline-clone-wiring-design.md):
// SandboxAgent.Run строит одноразовый клон-источник --clone от той же ветки,
// на которой стоит рабочая папка задачи, — work() обязана донести её агенту
// в Request.Branch, а не заставлять его снова спрашивать об этом трекер.
func TestWorkPassesTaskBranchToAgent(t *testing.T) {
	o := newOffice(t)

	if !o.tick(t) {
		t.Fatal("цикл не взял задачу")
	}

	if o.agent.seen.Branch != "agent/OFF-1" {
		t.Errorf("Request.Branch = %q, ожидалась agent/OFF-1", o.agent.seen.Branch)
	}
}

func TestTickRecordsRunInLedger(t *testing.T) {
	o := newOffice(t)
	o.agent.usage = runner.Usage{CostUSD: 0.25, DurationMS: 18258, Turns: 3}

	o.tick(t)

	total := o.spend(t, ledger.Filter{Task: "OFF-1"})
	if total.Runs != 1 || total.CostUSD != 0.25 || total.Unknown != 0 {
		t.Errorf("реестр после прогона: %+v", total)
	}
	if total.ByOutcome["done"] != 1 {
		t.Errorf("исход прогона в реестре: %v", total.ByOutcome)
	}
	if total.Broken != 0 {
		t.Errorf("реестр записан нечитаемо: %+v", total)
	}
	// Цена задачи видна и человеку — в тикете, а не только в реестре: реестр
	// локален для машины, а тикет общий.
	if body := lastComment(t, o.get(t, "OFF-1")).Body; !strings.Contains(body, "$0.2500") {
		t.Errorf("цены прогона нет в отчёте:\n%s", body)
	}
}

// Improvement-находка независимого ревью (round 2): SandboxAgent.Run может
// вернуть настоящий, уже оплаченный Usage вместе с ошибкой (ErrSyncIncomplete
// на sbx — агент отработал, но --clone синхронизация вышла из песочницы не
// целиком). Прогон при этом хоронится (аренда остаётся, задачу вернёт
// reaper), но расход агента уже понесён независимо от исхода синхронизации —
// молчание о нём в реестре исказило бы бюджет.
func TestWorkAccountsUsageEvenWhenRunFails(t *testing.T) {
	o := newOffice(t)
	o.agent.usage = runner.Usage{CostUSD: 0.42, DurationMS: 9000, Turns: 5}
	o.agent.err = errors.New("прогон состоялся, но песочница отдала не всё: comet-state.yaml не подтянут")

	if _, err := o.Tick(context.Background(), "implementer"); err == nil {
		t.Fatal("Tick не заметил неудачу прогона")
	}

	total := o.spend(t, ledger.Filter{Task: "OFF-1"})
	if total.Runs != 1 || total.CostUSD != 0.42 {
		t.Errorf("расход провалившегося, но состоявшегося прогона потерян: %+v", total)
	}
}

// Прогон, не назвавший цены, в реестре всё равно есть: пропустить его значило бы
// потерять сам факт работы, а выдумать нулевую цену — соврать.
func TestTickRecordsRunWithUnknownCost(t *testing.T) {
	o := newOffice(t)

	o.tick(t)

	total := o.spend(t, ledger.Filter{Task: "OFF-1"})
	if total.Runs != 1 || total.Unknown != 1 {
		t.Errorf("реестр после прогона без цены: %+v", total)
	}
	if body := lastComment(t, o.get(t, "OFF-1")).Body; strings.Contains(body, "$") {
		t.Errorf("в отчёте названа цена, которой никто не знает:\n%s", body)
	}
}

// Режим warn ничего не останавливает: задача берётся в работу, а человек узнаёт
// о перерасходе записью в тикете.
func TestPerTaskBudgetWarnsAndKeepsWorking(t *testing.T) {
	o := newOffice(t)
	o.Office.Budgets.PerTask = budget.Limit{USD: 1, OnExceed: budget.Warn}
	o.spent(t, "OFF-1", "implementer", 1.5)

	if !o.tick(t) {
		t.Fatal("предупреждение остановило работу")
	}
	if o.agent.runs != 1 {
		t.Errorf("прогонов %d, ожидался один", o.agent.runs)
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Review" {
		t.Errorf("статус %q, ожидался Review", task.Status)
	}
	if !slices.Contains(events(task), tracker.EventBudgetExceeded) {
		t.Errorf("о перерасходе не сказано в тикете: %v", events(task))
	}
}

// Одна задача — одна запись о перерасходе. Иначе тикет с дорогой задачей
// зарастёт одинаковыми предупреждениями, и настоящий разговор в нём потеряется.
func TestPerTaskBudgetWarnsOncePerTask(t *testing.T) {
	o := newOffice(t)
	o.Office.Budgets.PerTask = budget.Limit{USD: 1, OnExceed: budget.Warn}
	o.spent(t, "OFF-1", "implementer", 1.5)

	o.tick(t)
	// Задача вернулась в очередь роли и берётся снова — предупреждение уже было.
	if err := o.tasks.Transition("OFF-1", tracker.BySystem(), "Ready"); err != nil {
		t.Fatalf("задача не возвращена в очередь: %v", err)
	}
	if !o.tick(t) {
		t.Fatal("задача не взята второй раз")
	}

	warnings := 0
	for _, event := range events(o.get(t, "OFF-1")) {
		if event == tracker.EventBudgetExceeded {
			warnings++
		}
	}
	if warnings != 1 {
		t.Errorf("записей о перерасходе %d, ожидалась одна", warnings)
	}
}

// Режим stop не даёт начать работу вовсе: ни прогона, ни рабочей папки.
// Проверка стоит до Ensure намеренно — иначе на пропускаемую задачу всё равно
// заводился бы клон и worktree.
func TestPerTaskBudgetStopsBeforeWorktree(t *testing.T) {
	o := newOffice(t)
	o.Office.Budgets.PerTask = budget.Limit{USD: 1, OnExceed: budget.Stop}
	o.spent(t, "OFF-1", "implementer", 1.5)

	if o.tick(t) {
		t.Error("цикл взял задачу с исчерпанным бюджетом")
	}
	if o.agent.runs != 0 {
		t.Errorf("агент запущен, прогонов %d", o.agent.runs)
	}
	if _, err := os.Stat(filepath.Join(o.home, workspace.ReposDir)); !os.IsNotExist(err) {
		t.Errorf("клон проекта всё-таки заведён: %v", err)
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Blocked" || !task.HumanFlag {
		t.Errorf("задача не ушла к человеку: статус %q, ожидание %v", task.Status, task.HumanFlag)
	}
	if task.RunID != "" {
		t.Errorf("задача осталась арендованной: %+v", task)
	}
	if !slices.Contains(events(task), tracker.EventBudgetExhausted) {
		t.Errorf("о исчерпании бюджета не сказано в тикете: %v", events(task))
	}
}

// stolenTask — трекер, у которого задачу успели захватить между выборкой
// и записью. Правило владения отвечает на системную запись отказом.
type stolenTask struct {
	*mock.Tracker
	moved bool
}

func (s *stolenTask) Comment(key string, by tracker.Actor, body string) error {
	return fmt.Errorf("%w: задачу держит другой прогон", tracker.ErrNotOwner)
}

func (s *stolenTask) Transition(key string, by tracker.Actor, to string) error {
	s.moved = true
	return s.Tracker.Transition(key, by, to)
}

// Предел проверяется до захвата, и задачу в этот момент может взять сосед.
// Тогда она чужая: сказать о пределе не вышло — и двигать её мы не вправе.
func TestPerTaskBudgetLeavesStolenTaskAlone(t *testing.T) {
	o := newOffice(t)
	o.Office.Budgets.PerTask = budget.Limit{USD: 1, OnExceed: budget.Stop}
	o.spent(t, "OFF-1", "implementer", 1.5)

	stolen := &stolenTask{Tracker: o.tasks}
	o.useTracker(stolen)

	if o.tick(t) {
		t.Error("цикл взял задачу с исчерпанным бюджетом")
	}
	if stolen.moved {
		t.Error("чужую задачу всё-таки подвинули")
	}
}

// Дневной предел роли в режиме stop останавливает роль, а не офис: остальные
// роли работают, и задача остаётся в своей очереди нетронутой.
func TestPerRoleDailyBudgetStopsRoleOnly(t *testing.T) {
	o := newOffice(t)
	o.Office.Budgets.PerRoleDaily = budget.Limit{USD: 10, OnExceed: budget.Stop}
	o.spent(t, "OFF-9", "implementer", 12)

	if o.tick(t) {
		t.Error("роль взяла задачу, потратив дневной бюджет")
	}
	if o.agent.runs != 0 {
		t.Errorf("агент запущен, прогонов %d", o.agent.runs)
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Errorf("статус %q: задачу тронули", task.Status)
	}
	if len(task.Comments) != 0 {
		t.Errorf("в тикет написали о хозяйстве раннера: %v", events(task))
	}

	// Вчерашний расход сегодняшнюю работу не ограничивает: предел дневной.
	o.Office.Ledger = ledger.New(filepath.Join(t.TempDir(), ledger.FileName))
	o.spentAt(t, "OFF-9", "implementer", 12, now.Add(-24*time.Hour))
	if !o.tick(t) {
		t.Error("вчерашний расход остановил сегодняшнюю работу")
	}
}

// Дневной предел в режиме warn работу не трогает — он только говорит.
func TestPerRoleDailyBudgetWarnKeepsWorking(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.Office.Log = &log
	o.Office.Budgets.PerRoleDaily = budget.Limit{USD: 10, OnExceed: budget.Warn}
	o.spent(t, "OFF-9", "implementer", 12)

	if !o.tick(t) {
		t.Fatal("предупреждение остановило роль")
	}
	if !strings.Contains(log.String(), "дневной") {
		t.Errorf("о перерасходе роли не сказано в логе:\n%s", log.String())
	}
	// Хозяйство раннера — не дело задачи: в тикет о дневном пределе не пишут.
	if len(o.get(t, "OFF-1").Comments) != 1 {
		t.Errorf("в тикете лишние записи: %v", events(o.get(t, "OFF-1")))
	}
}

// per_role_daily — свойство прода: прогоны eval-harness'а не должны в него
// попадать, иначе sweep золотых кейсов исчерпал бы дневной бюджет роли.
func TestPerRoleDailySpendExcludesEvalEntries(t *testing.T) {
	o := newOffice(t)
	if err := o.Office.Ledger.Append(ledger.Entry{
		RunID: "прод-прогон", Task: "OFF-9", Role: "implementer", Project: "OFF", Started: now,
		Usage: runner.Usage{CostUSD: 5, DurationMS: 1000, Turns: 5}, Outcome: "done",
	}); err != nil {
		t.Fatalf("расход не записан: %v", err)
	}
	if err := o.Office.Ledger.Append(ledger.Entry{
		RunID: "eval-прогон", Role: "implementer", Started: now,
		Usage: runner.Usage{CostUSD: 100, DurationMS: 1000, Turns: 5}, Outcome: "done", Eval: true,
	}); err != nil {
		t.Fatalf("eval-расход не записан: %v", err)
	}

	spent, err := o.Office.spent(ledger.Filter{Role: "implementer", Since: startOfDay(now), ExcludeEval: true})
	if err != nil {
		t.Fatalf("расход не посчитан: %v", err)
	}
	if spent != 5 {
		t.Errorf("per_role_daily = $%.2f, ожидалось $5.00 без eval-прогона на $100", spent)
	}
}

// Та же гарантия, что и выше, но по настоящему пути проверки: через тик, а не
// через вручную собранный Filter. TestPerRoleDailySpendExcludesEvalEntries сам
// строит фильтр с ExcludeEval: true и потому не заметил бы отвал этого поля
// в roleOverspent — этот тест бьёт по нему напрямую.
func TestPerRoleDailyBudgetStopsIgnoreEvalSpend(t *testing.T) {
	o := newOffice(t)
	o.Office.Budgets.PerRoleDaily = budget.Limit{USD: 10, OnExceed: budget.Stop}
	// Прод один потратил $6 — предела не хватает. Вместе с eval-прогоном на $10
	// вышло бы $16 и роль остановилась бы, если бы eval считался наравне с продом.
	o.spent(t, "OFF-9", "implementer", 6)
	if err := o.Office.Ledger.Append(ledger.Entry{
		RunID: "eval-прогон", Role: "implementer", Started: now,
		Usage: runner.Usage{CostUSD: 10, DurationMS: 1000, Turns: 5}, Outcome: "done", Eval: true,
	}); err != nil {
		t.Fatalf("eval-расход не записан: %v", err)
	}

	if !o.tick(t) {
		t.Error("роль не взяла задачу: eval-расход посчитан наравне с продом и исчерпал дневной бюджет")
	}
}

// Дорогой прогон прерывать нечем — цена известна, когда работа уже сделана.
// Поэтому per_run только предупреждает, и делает это после отчёта: замечания
// агента человек читает первыми.
func TestPerRunBudgetWarnsAfterReport(t *testing.T) {
	o := newOffice(t)
	o.Office.Budgets.PerRun = budget.Limit{USD: 0.5, OnExceed: budget.Warn}
	o.agent.usage = runner.Usage{CostUSD: 0.75, DurationMS: 60000, Turns: 30}

	o.tick(t)

	task := o.get(t, "OFF-1")
	if !slices.Contains(events(task), tracker.EventRunBudgetExceeded) {
		t.Errorf("о дорогом прогоне не сказано в тикете: %v", events(task))
	}
	// Порядок: сперва отчёт агента, потом бухгалтерия раннера.
	last := lastComment(t, task)
	if m, _ := tracker.MarkerOf(last.Body); m.Event != tracker.EventRunBudgetExceeded {
		t.Errorf("последняя запись — %+v, ожидалось предупреждение о цене", m)
	}
	if !strings.Contains(last.Body, "$0.7500") {
		t.Errorf("предупреждение не называет цены:\n%s", last.Body)
	}
}

// Дешёвый прогон о себе не рассказывает: предупреждение о цене — событие,
// а не строка отчётности.
func TestPerRunBudgetSilentWhenCheap(t *testing.T) {
	o := newOffice(t)
	o.Office.Budgets.PerRun = budget.Limit{USD: 0.5, OnExceed: budget.Warn}
	o.agent.usage = runner.Usage{CostUSD: 0.10, DurationMS: 6000, Turns: 3}

	o.tick(t)

	if got := events(o.get(t, "OFF-1")); len(got) != 0 {
		t.Errorf("дешёвый прогон породил события: %v", got)
	}
}

// Нет бюджетов — только учёт: работа идёт как обычно, в тикете ни слова
// о деньгах сверх строки в отчёте.
func TestWithoutBudgetsOnlyAccounting(t *testing.T) {
	o := newOffice(t)
	o.spent(t, "OFF-1", "implementer", 1000)

	if !o.tick(t) {
		t.Fatal("цикл не взял задачу, хотя лимитов нет")
	}
	if got := events(o.get(t, "OFF-1")); len(got) != 0 {
		t.Errorf("без лимитов появились события бюджета: %v", got)
	}
}

// spent записывает в реестр уже случившийся расход: так выглядит машина,
// на которой задача уже стоила денег.
func (o *office) spent(t *testing.T, task, role string, cost float64) {
	t.Helper()
	o.spentAt(t, task, role, cost, now)
}

func (o *office) spentAt(t *testing.T, task, role string, cost float64, at time.Time) {
	t.Helper()
	if err := o.Office.Ledger.Append(ledger.Entry{
		RunID: "прошлый-прогон", Task: task, Role: role, Project: "OFF", Started: at,
		Usage: runner.Usage{CostUSD: cost, DurationMS: 1000, Turns: 5}, Outcome: "done",
	}); err != nil {
		t.Fatalf("расход не записан: %v", err)
	}
}

// Агент получает постановку из тикета и контекст, собранный раннером:
// сам он трекера не видит.
func TestTickFeedsAgentTaskAndContext(t *testing.T) {
	o := newOffice(t)
	if err := o.tasks.AddComment("OFF-1", "human", "Учти: файл называется hello.py"); err != nil {
		t.Fatalf("комментарий не добавлен: %v", err)
	}

	o.tick(t)

	req := o.agent.seen
	if req.Passport.TaskKey != "OFF-1" || req.Passport.Role != "implementer" {
		t.Errorf("паспорт прогона: %+v", req.Passport)
	}
	if req.Workdir == "" {
		t.Fatal("рабочая папка не создана")
	}

	task, err := os.ReadFile(filepath.Join(req.Workdir, runner.Dir, runner.FileTask))
	if err != nil {
		t.Fatalf("постановка не записана: %v", err)
	}
	for _, want := range []string{"OFF-1", "Задача OFF-1", "Сделать что-нибудь полезное"} {
		if !strings.Contains(string(task), want) {
			t.Errorf("в постановке нет %q:\n%s", want, task)
		}
	}

	context, err := os.ReadFile(filepath.Join(req.Workdir, runner.Dir, runner.FileContext))
	if err != nil {
		t.Fatalf("контекст не записан: %v", err)
	}
	for _, want := range []string{"OFF-1", "hello.py", "Попытка"} {
		if !strings.Contains(string(context), want) {
			t.Errorf("в контексте нет %q:\n%s", want, context)
		}
	}
}

// Роль, дошедшая до агента, обязана нести уже смёрженные с проектом
// network/tools — слияние происходит после claim(), не сразу при загрузке
// роли, потому что до захвата задачи проект не известен.
func TestTickMergesProjectRulesBeforeAgentRun(t *testing.T) {
	o := newOffice(t)
	proj := o.Office.Projects["OFF"]
	proj.Network = []string{"project.test"}
	proj.Tools = runner.Tools{
		Allow: []string{"Bash(project-tool)"},
		Deny:  []string{"Bash(git *dangerous*)"},
	}
	o.Office.Projects["OFF"] = proj

	if !o.tick(t) {
		t.Fatal("цикл не взял задачу")
	}

	got := o.agent.seen.Role
	if !slices.Contains(got.Network.Allow, "project.test") {
		t.Errorf("network.allow агента %v не содержит project.test", got.Network.Allow)
	}
	if !slices.Contains(got.Tools.Allow, "Bash(project-tool)") {
		t.Errorf("tools.allow агента %v не содержит Bash(project-tool)", got.Tools.Allow)
	}
	if !slices.Contains(got.Tools.Deny, "Bash(git *dangerous*)") {
		t.Errorf("tools.deny агента %v не содержит Bash(git *dangerous*)", got.Tools.Deny)
	}
}

// Вопрос человеку: задача уходит в Blocked с атрибутом ожидания, вопросы видны
// в комментарии, работа всё равно опубликована.
func TestTickNeedsHumanBlocksAndFlags(t *testing.T) {
	o := newOffice(t)
	o.agent.commit = "половина работы"
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeNeedsHuman, Summary: "Нужен выбор.", NextOwner: "human",
		Questions: []runner.Question{{ID: "Q1", Text: "Какую платёжную систему?", Options: []runner.Option{
			{ID: "a", Label: "Stripe"}, {ID: "b", Label: "ЮKassa"},
		}}},
	}

	o.tick(t)

	task := o.get(t, "OFF-1")
	if task.Status != "Blocked" {
		t.Errorf("статус %q, ожидался Blocked", task.Status)
	}
	if !task.HumanFlag {
		t.Error("атрибут «ждёт человека» не выставлен")
	}
	if body := lastComment(t, task).Body; !strings.Contains(body, "Какую платёжную систему") || !strings.Contains(body, "Stripe") {
		t.Errorf("вопросов нет в комментарии:\n%s", body)
	}
	if got := gitIn(o.origin, "log", "--oneline", "-1", "agent/OFF-1"); !strings.Contains(got, "половина работы") {
		t.Errorf("незаконченная работа не опубликована: %s", got)
	}
}

// Предложение разбить задачу маршрутизируется в графе точно так же, как
// needs_human (RoleFlow.Blocked() в internal/tracker/config.go), и сама
// разбивка обязана быть видна в комментарии — иначе человеку нечего смотреть,
// отвечая на вопрос «разбить как предложено?».
func TestTickSplitBlocksAndFlags(t *testing.T) {
	o := newOffice(t)
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeSplit, Summary: "Постановка описывает две сущности.", NextOwner: "human",
		Questions: []runner.Question{{ID: "Q1", Text: "Разбить на 2, как предложено?"}},
		Split: &runner.Split{Children: []runner.SplitChild{
			{ID: "category-crud", Title: "Category CRUD", Description: "Модель, миграция, CRUD категорий."},
			{ID: "transaction-crud", Title: "Transaction CRUD", Description: "Модель, миграция, CRUD операций.", DependsOn: []string{"category-crud"}},
		}},
	}

	o.tick(t)

	task := o.get(t, "OFF-1")
	if task.Status != "Blocked" {
		t.Errorf("статус %q, ожидался Blocked", task.Status)
	}
	if !task.HumanFlag {
		t.Error("атрибут «ждёт человека» не выставлен")
	}
	if body := lastComment(t, task).Body; !strings.Contains(body, "Category CRUD") || !strings.Contains(body, "category-crud") {
		t.Errorf("разбивки нет в комментарии:\n%s", body)
	}
}

// Второе предложение находит своё же вложение по attachment:<id> из тега
// последнего маркера split — тест проверяет round-trip через сам Tracker,
// а не сравнением строк.
func TestTickSplitAttachesRawChildren(t *testing.T) {
	o := newOffice(t)
	split := &runner.Split{Children: []runner.SplitChild{
		{ID: "category-crud", Title: "Category CRUD", Description: "Модель, миграция, CRUD категорий."},
		{ID: "transaction-crud", Title: "Transaction CRUD", Description: "Модель, миграция, CRUD операций.", DependsOn: []string{"category-crud"}},
	}}
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeSplit, Summary: "Постановка описывает две сущности.", NextOwner: "human",
		Questions: []runner.Question{{ID: "Q1", Text: "Разбить на 2, как предложено?"}},
		Split:     split,
	}

	o.tick(t)

	task := o.get(t, "OFF-1")
	body := lastComment(t, task).Body
	marker, ok := tracker.MarkerOf(body)
	if !ok || marker.Attachment == "" {
		t.Fatalf("в маркере нет attachment:<id>:\n%s", body)
	}
	if !strings.Contains(body, "attachment:"+marker.Attachment) {
		t.Errorf("тег комментария не содержит attachment:%s:\n%s", marker.Attachment, body)
	}

	raw, err := o.tasks.GetAttachment(task.Key, marker.Attachment)
	if err != nil {
		t.Fatalf("вложение не прочитано: %v", err)
	}
	var got runner.Split
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("вложение не разобрано: %v", err)
	}
	if len(got.Children) != 2 || got.Children[1].DependsOn[0] != "category-crud" {
		t.Errorf("вложение потеряло данные: %+v", got)
	}
}

// Неудача — обычный исход: задача возвращается в очередь с увеличенным счётчиком.
func TestTickFailedReturnsTaskWithAttempt(t *testing.T) {
	o := newOffice(t)
	o.agent.result = runner.Result{Outcome: runner.OutcomeFailed, Summary: "Не вышло.", NextOwner: "human"}

	o.tick(t)

	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Errorf("статус %q, ожидался Ready", task.Status)
	}
	if task.Attempts != 1 {
		t.Errorf("попыток %d, ожидалась 1", task.Attempts)
	}
	if task.HumanFlag {
		t.Error("человека позвали раньше времени")
	}
}

// Исчерпав попытки, задача уходит к человеку, а не крутится вечно.
func TestTickStopsAfterMaxAttempts(t *testing.T) {
	o := newOffice(t)
	o.agent.result = runner.Result{Outcome: runner.OutcomeFailed, Summary: "Опять не вышло.", NextOwner: "human"}

	max := o.Workflow.Limits.MaxAttempts
	for i := range max {
		if !o.tick(t) {
			t.Fatalf("цикл %d не взял задачу", i+1)
		}
	}

	task := o.get(t, "OFF-1")
	if task.Attempts != max {
		t.Errorf("попыток %d, ожидалось %d", task.Attempts, max)
	}
	if task.Status != "Blocked" || !task.HumanFlag {
		t.Errorf("после исчерпания попыток задача в %q, флаг %v", task.Status, task.HumanFlag)
	}
	// И больше её никто не берёт.
	if o.tick(t) {
		t.Error("задача с исчерпанными попытками снова взята в работу")
	}
}

// Ответ человека разблокирует задачу: она возвращается в очередь, флаг снимается,
// счётчик обнуляется, а следующий прогон видит ответ в контексте.
func TestHumanReplyReturnsTaskToQueue(t *testing.T) {
	o := newOffice(t)
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeNeedsHuman, Summary: "Нужен выбор.", NextOwner: "human",
		Questions: []runner.Question{{ID: "Q1", Text: "Какую платёжную систему?"}},
	}
	o.tick(t)

	if err := o.tasks.AddComment("OFF-1", "human", "Берём Stripe."); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}
	o.agent.result = done("сделал по ответу")

	if !o.tick(t) {
		t.Fatal("после ответа человека задача не взята")
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Review" {
		t.Errorf("статус %q, ожидался Review", task.Status)
	}
	if task.HumanFlag {
		t.Error("атрибут ожидания не снят")
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d, ожидался сброс", task.Attempts)
	}

	context, err := os.ReadFile(filepath.Join(o.agent.seen.Workdir, runner.Dir, runner.FileContext))
	if err != nil {
		t.Fatalf("контекст не прочитан: %v", err)
	}
	if !strings.Contains(string(context), "Берём Stripe") {
		t.Errorf("ответ человека не доехал до агента:\n%s", context)
	}
}

// Роль подписывается своей учёткой: история тикета читается людьми, а права
// в трекере разводятся. На поведение раннера это не влияет — роли он различает
// по маркеру, — но перепутанная подпись врёт человеку.
func TestRoleWritesUnderItsOwnAccount(t *testing.T) {
	o := newOffice(t)
	o.add("OFF-2", "Review")
	o.agent.result = runner.Result{Outcome: runner.OutcomeDone, Summary: "Разобрал.", NextOwner: "human"}

	if _, err := o.Tick(context.Background(), "reviewer"); err != nil {
		t.Fatalf("цикл не прошёл: %v", err)
	}

	if author := lastComment(t, o.get(t, "OFF-2")).Author; author != mock.RoleAccount("reviewer") {
		t.Errorf("отчёт подписан %q, ожидалась учётка reviewer'а", author)
	}
}

// Голос человека определяется по автору, и офис не должен принимать за него себя:
// иначе отчёт одной роли разблокировал бы задачу, которую другая отправила ждать.
func TestOfficeDoesNotMistakeItselfForHuman(t *testing.T) {
	o := newOffice(t)
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeNeedsHuman, Summary: "Нужен выбор.", NextOwner: "human",
		Questions: []runner.Question{{ID: "Q1", Text: "Какую платёжную систему?"}},
	}
	o.tick(t)

	// Записи обеих ролей поверх вопроса — ни одна не ответ человека.
	for _, role := range []string{"implementer", "reviewer"} {
		if err := o.tasks.AddComment("OFF-1", mock.RoleAccount(role), "заметка "+role); err != nil {
			t.Fatalf("запись роли не добавлена: %v", err)
		}
	}
	unblocked, err := o.HumanReplies(context.Background())
	if err != nil {
		t.Fatalf("разбор ответов не прошёл: %v", err)
	}
	if unblocked != 0 {
		t.Fatalf("офис принял свои записи за ответ человека: разблокировано %d", unblocked)
	}

	if err := o.tasks.AddComment("OFF-1", "human", "Берём Stripe."); err != nil {
		t.Fatalf("ответ человека не записан: %v", err)
	}
	if unblocked, err = o.HumanReplies(context.Background()); err != nil {
		t.Fatalf("разбор ответов не прошёл: %v", err)
	}
	if unblocked != 1 {
		t.Errorf("ответ человека не разобран: разблокировано %d", unblocked)
	}
}

// Нарезка истории по своей роли: каждому агенту в контекст едет то, что сказали
// после его последнего отчёта, и ничего сверх. Проверяется на настоящем круге
// implementer → reviewer → implementer.
func TestHistorySliceGivesEachRoleWhatItNeeds(t *testing.T) {
	o := newOffice(t)

	o.agent.result = runner.Result{Outcome: runner.OutcomeDone, Summary: "Написал mean().", NextOwner: "reviewer"}
	o.tick(t)

	o.agent.result = runner.Result{Outcome: runner.OutcomeDone, Summary: "Нет теста на пустой список.", NextOwner: "implementer"}
	if _, err := o.Tick(context.Background(), "reviewer"); err != nil {
		t.Fatalf("цикл ревьюера не прошёл: %v", err)
	}
	reviewerSaw := agentContext(t, o)
	if !strings.Contains(reviewerSaw, "Написал mean()") {
		t.Errorf("ревьюер не увидел отчёта автора:\n%s", reviewerSaw)
	}

	o.agent.result = done("добавил тест")
	if !o.tick(t) {
		t.Fatal("задача не вернулась в очередь автора")
	}
	implementerSaw := agentContext(t, o)
	if !strings.Contains(implementerSaw, "Нет теста на пустой список") {
		t.Errorf("автор не увидел замечаний ревьюера:\n%s", implementerSaw)
	}
	if strings.Contains(implementerSaw, "Написал mean()") {
		t.Errorf("автору переслали его же прошлый отчёт:\n%s", implementerSaw)
	}
}

// agentContext — контекст, собранный раннером для последнего прогона.
func agentContext(t *testing.T, o *office) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(o.agent.seen.Workdir, runner.Dir, runner.FileContext))
	if err != nil {
		t.Fatalf("контекст не прочитан: %v", err)
	}
	return string(raw)
}

// Роль, названная в графе, обязана лежать в репозитории: раннер грузит её
// по имени из workflow.yaml, и расхождение обнаружилось бы не здесь, а посреди
// цикла, уже захватив задачу.
func TestEveryGraphRoleIsShipped(t *testing.T) {
	o := newOffice(t)
	for _, name := range o.Workflow.Order() {
		if _, err := runner.LoadRole(o.ConfigRoot, name); err != nil {
			t.Errorf("роль %s есть в графе, но не загружается: %v", name, err)
		}
	}
}

// Ревьюер читает из Review и возвращает работу автору. Рабочего статуса у него нет:
// задача остаётся в Review с живой арендой, а по исходу уходит туда, куда велит
// граф по next_owner.
func TestReviewerReturnsWorkToImplementer(t *testing.T) {
	o := newOffice(t)
	o.add("OFF-2", "Review")
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeDone, Summary: "Тесты не покрывают отказ.", NextOwner: "implementer",
	}

	worked, err := o.Tick(context.Background(), "reviewer")
	if err != nil {
		t.Fatalf("цикл не прошёл: %v", err)
	}
	if !worked {
		t.Fatal("ревьюер не взял задачу из Review")
	}

	task := o.get(t, "OFF-2")
	if task.Status != "Ready" {
		t.Errorf("статус %q, ожидался Ready: работа возвращена автору", task.Status)
	}
	if task.HumanFlag {
		t.Error("возврат автору не должен звать человека")
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d: круг ревью — не провал агента", task.Attempts)
	}
}

// Попытки считают провалы текущей роли, а не возраст задачи. Передача другой
// роли графа обнуляет счётчик — в том числе возврат назад по конвейеру: иначе
// на трёх ролях два провала одной роли оставили бы следующей одну попытку.
func TestHandoverToAnotherRoleResetsAttempts(t *testing.T) {
	o := newOffice(t)
	o.addTried("OFF-2", "Review", 2)
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeDone, Summary: "Тесты не покрывают отказ.", NextOwner: "implementer",
	}

	if _, err := o.Tick(context.Background(), "reviewer"); err != nil {
		t.Fatalf("цикл не прошёл: %v", err)
	}

	if got := o.get(t, "OFF-2").Attempts; got != 0 {
		t.Errorf("счётчик попыток %d, ожидался 0: задача ушла другой роли", got)
	}
}

// `none` и `human` ролями графа не являются и работу никому не передают:
// счётчик остаётся как был. Обнулять его здесь значило бы прощать провалы
// тому, кто просто закончил разговор.
func TestFinishWithoutHandoverKeepsAttempts(t *testing.T) {
	o := newOffice(t)
	o.addTried("OFF-2", "Review", 2)
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeDone, Summary: "Работа принята.", NextOwner: "none",
	}

	if _, err := o.Tick(context.Background(), "reviewer"); err != nil {
		t.Fatalf("цикл не прошёл: %v", err)
	}

	if got := o.get(t, "OFF-2").Attempts; got != 2 {
		t.Errorf("счётчик попыток %d, ожидалось 2: владелец не сменился", got)
	}
}

// Сценарий этапа целиком, без --role: задача проходит Ready → Review → правки →
// Review → Approved → Done, каждую роль зовёт порядок обхода, а не тест.
//
// Последний шаг делает системный проход, а не роль: forge у полигона нет,
// и PR-проход для него вырождается — задача уходит туда же, куда ушла бы слитая,
// но записью `pr-skipped`, потому что слияния не было.
func TestConveyorCarriesTaskFromReadyToDone(t *testing.T) {
	o := newOffice(t)
	o.agent.commit = "работа автора"
	o.agent.byRole = map[string]runner.Result{
		"implementer": {Outcome: runner.OutcomeDone, Summary: "Написал mean().", NextOwner: "reviewer"},
		"reviewer":    {Outcome: runner.OutcomeDone, Summary: "Нет теста на пустой список.", NextOwner: "implementer"},
	}

	// Заход первый: ревьюеру смотреть нечего, автор берёт задачу и сдаёт работу.
	o.tickAll(t)
	if task := o.get(t, "OFF-1"); task.Status != "Review" {
		t.Fatalf("после первого захода статус %q, ожидался Review", task.Status)
	}

	// Заход второй: ревьюер возвращает работу с замечаниями, и автор — он идёт
	// в обходе следом — тут же забирает её обратно в работу.
	o.tickAll(t)
	if task := o.get(t, "OFF-1"); task.Status != "Review" {
		t.Fatalf("после второго захода статус %q, ожидался Review", task.Status)
	}
	if seen := o.agent.context["implementer"]; !strings.Contains(seen, "Нет теста на пустой список") {
		t.Errorf("замечания ревьюера не доехали до автора:\n%s", seen)
	}
	if seen := o.agent.context["reviewer"]; !strings.Contains(seen, "Написал mean()") {
		t.Errorf("отчёт автора не доехал до ревьюера:\n%s", seen)
	}

	// Заход третий: ревьюер доволен.
	o.agent.byRole["reviewer"] = runner.Result{
		Outcome: runner.OutcomeDone, Summary: "Тест на пустой список есть, проверил.", NextOwner: "human",
	}
	workdir := o.agent.seen.Workdir
	o.tickAll(t)

	task := o.get(t, "OFF-1")
	if task.Status != "Approved" {
		t.Fatalf("статус %q, ожидался Approved", task.Status)
	}
	if task.HumanFlag {
		t.Error("одобренная работа зовёт человека атрибутом ожидания")
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d: провалов не было", task.Attempts)
	}
	// Approved — это «разбор прошёл», а не конец: задача ждёт PR-прохода,
	// и рабочая папка ждёт вместе с ней.
	if _, err := os.Stat(workdir); err != nil {
		t.Errorf("рабочая папка одобренной задачи убрана раньше времени: %v", err)
	}
	if got := gitIn(o.origin, "log", "--oneline", "agent/OFF-1"); !strings.Contains(got, "работа автора") {
		t.Errorf("работа не опубликована: %s", got)
	}

	// Заход четвёртый: работы ролям нет, задачу двигает системный проход.
	o.tickAll(t)
	task = o.get(t, "OFF-1")
	if task.Status != "Done" {
		t.Fatalf("после прохода статус %q, ожидался Done", task.Status)
	}
	if !tracker.HasEvent(task.Comments, tracker.EventPRSkipped) {
		t.Error("проход не сказал, что forge у проекта нет")
	}
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("рабочая папка законченной задачи осталась: %v", err)
	}

	// В переписке — обе роли и их маршруты, различимые машиной.
	var handovers []string
	for _, c := range task.Comments {
		if m, ok := tracker.MarkerOf(c.Body); ok && m.Outcome != "" {
			handovers = append(handovers, m.Role+"→"+m.Next)
		}
	}
	want := []string{"implementer→reviewer", "reviewer→implementer", "implementer→reviewer", "reviewer→human"}
	if !slices.Equal(handovers, want) {
		t.Errorf("передачи в переписке %v, ожидались %v", handovers, want)
	}
}

// round прогоняет один круг «правки → ревью»: автор сдаёт работу, ревьюер
// возвращает её с замечаниями.
func (o *office) round(t *testing.T, note string) {
	t.Helper()
	o.agent.result = runner.Result{Outcome: runner.OutcomeDone, Summary: "правки", NextOwner: "reviewer"}
	if !o.tick(t) {
		t.Fatal("автор не взял задачу")
	}
	o.agent.result = runner.Result{Outcome: runner.OutcomeDone, Summary: note, NextOwner: "implementer"}
	if worked, err := o.Tick(context.Background(), "reviewer"); err != nil || !worked {
		t.Fatalf("ревьюер не взял задачу: worked=%v, err=%v", worked, err)
	}
}

// Круги «правки → ревью» ограничены: не сойдясь за отведённое число, роли зовут
// человека. Без предела задача ходила бы между ними вечно, тратя деньги молча.
func TestReturnRoundsExhaustedCallsHuman(t *testing.T) {
	o := newOffice(t)
	limit := o.Workflow.Limits.MaxReturnRounds

	for i := 1; i < limit; i++ {
		o.round(t, fmt.Sprintf("замечание %d", i))
		if task := o.get(t, "OFF-1"); task.Status != "Ready" {
			t.Fatalf("после круга %d статус %q, ожидался Ready", i, task.Status)
		}
	}
	o.round(t, "замечание, после которого круги кончились")

	task := o.get(t, "OFF-1")
	if task.Status != "Blocked" {
		t.Errorf("статус %q, ожидался Blocked: круги исчерпаны", task.Status)
	}
	if !task.HumanFlag {
		t.Error("человека не позвали")
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d: круг ревью — не провал агента", task.Attempts)
	}

	// Человеку нужно объяснение, а не только статус: сколько кругов и почему.
	last := lastComment(t, task)
	marker, ok := tracker.MarkerOf(last.Body)
	if !ok || marker.Event != tracker.EventReturnRoundsExhausted {
		t.Fatalf("последняя запись не объясняет остановку:\n%s", last.Body)
	}
	if !strings.Contains(last.Body, fmt.Sprint(limit)) {
		t.Errorf("в объяснении нет числа кругов:\n%s", last.Body)
	}
	// Сами замечания обязаны остаться в переписке: человек разбирает спор по ним.
	if len(task.Comments) < 2 {
		t.Fatal("отчёта ревьюера нет в переписке")
	}
	if !strings.Contains(task.Comments[len(task.Comments)-2].Body, "круги кончились") {
		t.Errorf("последний разбор ревьюера потерян:\n%s", task.Comments[len(task.Comments)-2].Body)
	}
}

// План разошёлся с кодом — задача возвращается аналитику, а не правится по месту.
// Маршрут задаёт граф: агент лишь называет владельца.
func TestImplementerReturnsTaskToAnalyst(t *testing.T) {
	o := newOffice(t)
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeDone, Summary: "План велит править ридер, а его в проекте нет.",
		NextOwner: "analyst",
	}

	if !o.tick(t) {
		t.Fatal("автор не взял задачу")
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Analysis" {
		t.Errorf("статус %q, ожидался Analysis: план правит аналитик", task.Status)
	}
	if task.HumanFlag {
		t.Error("возврат аналитику не должен звать человека")
	}
	if _, err := os.Stat(o.agent.seen.Workdir); err != nil {
		t.Errorf("рабочая папка не пережила возврат: %v", err)
	}
}

// Ответ человека возвращает задачу той роли, которая спрашивала, — без
// промежуточных: спросил аналитик, аналитику и продолжать. Очередь его известна
// из графа, запасной маршрут тут ни при чём.
func TestHumanReplyReturnsTaskToAnalyst(t *testing.T) {
	o := newOffice(t)
	if err := o.tasks.Add(tracker.Task{
		Key: "OFF-2", Project: "OFF", Status: "Blocked", Summary: "противоречивая постановка",
		HumanFlag: true,
	}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	marker := tracker.Marker{RunID: "аналитик-1", Role: "analyst", Outcome: "needs_human", ConfigSHA: "5bc6a3b0"}
	if err := o.tasks.AddComment("OFF-2", mock.RoleAccount("analyst"), marker.String()+"\nQ1: идемпотентность или скорость?"); err != nil {
		t.Fatalf("вопрос не записан: %v", err)
	}
	if err := o.tasks.AddComment("OFF-2", "human", "Идемпотентность."); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}

	if replies, err := o.HumanReplies(context.Background()); err != nil || replies != 1 {
		t.Fatalf("разобрано ответов %d, %v", replies, err)
	}

	task := o.get(t, "OFF-2")
	if task.Status != "Analysis" {
		t.Errorf("статус %q, ожидался Analysis: спрашивал аналитик", task.Status)
	}
	if task.HumanFlag {
		t.Error("атрибут ожидания не снят")
	}
}

// Порядок обхода — обратный ходу конвейера: сначала разгрузить его с конца.
// Проверяется он здесь, а не в графе, потому что цикл без --role исполняет
// именно этот список, и роль, забытая в нём, просто не получит работы.
func TestTickAllWalksThreeRoles(t *testing.T) {
	o := newOffice(t)
	if got := o.Workflow.Order(); !slices.Equal(got, []string{"reviewer", "implementer", "analyst"}) {
		t.Errorf("порядок обхода %v", got)
	}
}

// Одобрение уводит задачу в очередь PR-прохода: разбор пройден, дальше офис
// откроет pull request, а сливает человек. Терминальным этот статус не является
// — задача живёт до слияния, и рабочая папка живёт вместе с ней.
func TestReviewerApprovalMovesTaskToApproved(t *testing.T) {
	o := newOffice(t)
	o.add("OFF-2", "Review")
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeDone, Summary: "Сделано по постановке, тесты зелёные.", NextOwner: "human",
	}

	if _, err := o.Tick(context.Background(), "reviewer"); err != nil {
		t.Fatalf("цикл не прошёл: %v", err)
	}

	if task := o.get(t, "OFF-2"); task.Status != "Approved" {
		t.Errorf("статус %q, ожидался Approved", task.Status)
	}
}

// rejectPush заставляет origin отвергать публикацию, не мешая чтению: так выглядит
// кончившийся токен, отобранный доступ или защищённая ветка. Ломать сам репозиторий
// нельзя — тогда не прошёл бы и fetch на захвате, и проверялось бы не то.
func rejectPush(t *testing.T, origin string) {
	t.Helper()
	hook := filepath.Join(origin, "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'ветка отвергнута' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("хук origin не записан: %v", err)
	}
}

// Неопубликованная работа не двигает задачу вперёд: следующая роль искала бы
// в origin ветку, которой там нет. Задача возвращается в очередь той же роли,
// а рабочая папка остаётся — в ней всё сделанное.
func TestPushFailureReturnsTaskToItsQueue(t *testing.T) {
	o := newOffice(t)
	o.agent.commit = "работа автора"
	o.agent.usage = runner.Usage{CostUSD: 0.25, DurationMS: 18258, Turns: 3}
	rejectPush(t, o.origin)

	if !o.tick(t) {
		t.Fatal("цикл не взял задачу")
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Errorf("статус %q, ожидался Ready: публиковать было нечем, вперёд задача не идёт", task.Status)
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d: публикует ветку раннер, а не агент", task.Attempts)
	}
	if task.LeaseAlive(now) || task.RunID != "" {
		t.Errorf("аренда не снята: %+v", task)
	}

	last := lastComment(t, task)
	marker, ok := tracker.MarkerOf(last.Body)
	if !ok || marker.Event != tracker.EventPushFailed {
		t.Fatalf("последняя запись не объясняет неудачу пуша:\n%s", last.Body)
	}
	// Отчёта агента быть не должно: он объявил бы передачу, которой не было.
	for _, c := range task.Comments {
		if m, _ := tracker.MarkerOf(c.Body); m.Outcome != "" {
			t.Errorf("написан отчёт о передаче, которой не было:\n%s", c.Body)
		}
	}
	// Итог прогона при этом не потерян: его пересказывает сама запись — вместе
	// с ценой. Отчёта здесь нет, и это единственное место, где человек её увидит,
	// не заглядывая в реестр.
	if !strings.Contains(last.Body, "сделано") {
		t.Errorf("в записи нет итога прогона:\n%s", last.Body)
	}
	if !strings.Contains(last.Body, "$0.2500") {
		t.Errorf("в записи нет цены прогона:\n%s", last.Body)
	}
	// Папка остаётся: следующий прогон продолжит с той же работы.
	if _, err := os.Stat(o.agent.seen.Workdir); err != nil {
		t.Errorf("рабочая папка снесена вместе с неопубликованной работой: %v", err)
	}
}

// Серия неудачных пушей означает, что дело не в задаче: смотреть надо на доступ.
// Крутить её бесконечно — тратить прогоны на то, что раннер починить не может.
func TestPushFailuresExhaustedCallHuman(t *testing.T) {
	o := newOffice(t)
	o.agent.commit = "работа автора"
	rejectPush(t, o.origin)

	limit := o.Workflow.Limits.MaxPushFailures
	for i := 1; i <= limit; i++ {
		if !o.tick(t) {
			t.Fatalf("цикл %d не взял задачу", i)
		}
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Blocked" {
		t.Errorf("статус %q, ожидался Blocked после %d неудач", task.Status, limit)
	}
	if !task.HumanFlag {
		t.Error("человека не позвали")
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d: неудачный пуш — не попытка агента", task.Attempts)
	}
	last := lastComment(t, task)
	if marker, ok := tracker.MarkerOf(last.Body); !ok || marker.Event != tracker.EventPushFailuresExhausted {
		t.Fatalf("последняя запись не объясняет остановку:\n%s", last.Body)
	}
}

// idle настраивает подделку на прогон, не дошедший до результата: раннер в этом
// случае сам сочиняет синтетический failed, и верить ему нельзя — смотреть надо
// на Termination. Подделка воспроизводит это в точности.
func (o *office) idle(kind runner.TerminationKind, detail string) {
	o.agent.result = runner.FailedResult("результата нет: " + detail)
	o.agent.termination = runner.Termination{Kind: kind, Detail: detail}
}

// Прогон, который не начинался, — беда сети и API, а не работы. Задача
// возвращается своей роли, попытка не тратится, отчёта агента нет: он ничего
// не сказал, и пересказывать за него было бы выдумкой раннера.
//
// Живой образец: f4705aba — десять повторов CLI, ECONNRESET, два шага, $0.05
// и ни строки в рабочей папке (docs/notes/stage-4-load.md).
func TestNotStartedRunReturnsTaskWithoutSpendingAttempt(t *testing.T) {
	o := newOffice(t)
	o.idle(runner.TerminationNotStarted, "API Error: Unable to connect to API (ECONNRESET)")
	o.agent.usage = runner.Usage{CostUSD: 0.0495, DurationMS: 183782, Turns: 2}

	if !o.tick(t) {
		t.Fatal("цикл не взял задачу")
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Errorf("статус %q, ожидался Ready: прогона не было, задача остаётся у своей роли", task.Status)
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d: до работы дело не дошло, тратить попытку не за что", task.Attempts)
	}
	if task.LeaseAlive(now) || task.RunID != "" {
		t.Errorf("аренда не снята: %+v", task)
	}

	last := lastComment(t, task)
	if marker, ok := tracker.MarkerOf(last.Body); !ok || marker.Event != tracker.EventAgentUnavailable {
		t.Fatalf("последняя запись не объясняет недоступность агента:\n%s", last.Body)
	}
	// Слова агента о причине человек должен видеть в тикете, не заглядывая
	// в лог прогона на чужой машине.
	if !strings.Contains(last.Body, "ECONNRESET") {
		t.Errorf("в записи нет причины:\n%s", last.Body)
	}
	// Прогон оплачен, даже если работы не было: цена названа здесь, потому что
	// отчёта нет вовсе.
	if !strings.Contains(last.Body, "$0.0495") {
		t.Errorf("в записи нет цены прогона:\n%s", last.Body)
	}
	for _, c := range task.Comments {
		if m, _ := tracker.MarkerOf(c.Body); m.Outcome != "" {
			t.Errorf("написан отчёт агента, который ничего не сказал:\n%s", c.Body)
		}
	}
}

// Усечение — «продолжить», а не провал: агент работал и не успел отчитаться.
// Задача выходит из рабочего статуса обратно в очередь своей роли, попытка
// не тратится, а рабочая папка сохраняется — в ней продолжение.
//
// Живой образец: 8dfc3e3e — 51 шаг при max_turns 50, $1.78, result.json
// не написан (docs/notes/stage-4-load-2.md).
func TestTruncatedRunKeepsWorkAndReturnsTask(t *testing.T) {
	o := newOffice(t)
	o.agent.commit = "половина работы"
	o.idle(runner.TerminationTruncated, "предел шагов роли исчерпан на 51-м шаге, отчитаться агент не успел")

	if !o.tick(t) {
		t.Fatal("цикл не взял задачу")
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Errorf("статус %q, ожидался Ready: из рабочего статуса задача обязана выйти", task.Status)
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d: усечение — не провал агента", task.Attempts)
	}

	last := lastComment(t, task)
	if marker, ok := tracker.MarkerOf(last.Body); !ok || marker.Event != tracker.EventRunTruncated {
		t.Fatalf("последняя запись не объясняет усечение:\n%s", last.Body)
	}
	// Папка остаётся: следующий прогон продолжает с того же места, а не заново.
	if _, err := os.Stat(o.agent.seen.Workdir); err != nil {
		t.Errorf("рабочая папка снесена вместе с продолжением: %v", err)
	}
	// Сделанное до обрыва обязано быть опубликовано: worktree однажды удалят,
	// а коммит на пункт — настоящая работа.
	if out := gitIn(o.origin, "log", "--oneline", "--all"); !strings.Contains(out, "половина работы") {
		t.Errorf("работа усечённого прогона не опубликована:\n%s", out)
	}
}

// Счётчик у «не начинал» и «не успел» общий, и это главное в нём. Два
// раздельных счётчика чередование обошло бы: прогон не начался, следующий
// оборвался, третий опять не начался — и ни один не дошёл бы до предела,
// пока задача крутится вечно.
func TestIdleRunsExhaustedCallHumanEvenWhenKindsAlternate(t *testing.T) {
	o := newOffice(t)
	limit := o.Workflow.Limits.MaxIdleRuns
	if limit < 3 {
		t.Fatalf("предел %d: сценарий чередования требует хотя бы трёх прогонов", limit)
	}

	kinds := []runner.TerminationKind{runner.TerminationNotStarted, runner.TerminationTruncated}
	for i := 0; i < limit; i++ {
		o.idle(kinds[i%len(kinds)], "прогон без результата")
		if !o.tick(t) {
			t.Fatalf("цикл %d не взял задачу", i+1)
		}
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Blocked" {
		t.Errorf("статус %q, ожидался Blocked после %d пустых прогонов вперемешку", task.Status, limit)
	}
	if !task.HumanFlag {
		t.Error("человека не позвали")
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d: ни один из этих прогонов не был провалом агента", task.Attempts)
	}
	last := lastComment(t, task)
	if marker, ok := tracker.MarkerOf(last.Body); !ok || marker.Event != tracker.EventIdleRunsExhausted {
		t.Fatalf("последняя запись не объясняет остановку:\n%s", last.Body)
	}
	// В переписке видно, что именно было: события разные, счётчик общий.
	var unavailable, truncated bool
	for _, c := range task.Comments {
		switch m, _ := tracker.MarkerOf(c.Body); m.Event {
		case tracker.EventAgentUnavailable:
			unavailable = true
		case tracker.EventRunTruncated:
			truncated = true
		}
	}
	if !unavailable || !truncated {
		t.Error("в переписке не различить, какие именно прогоны были пустыми")
	}
}

// Прогон, дошедший до результата, серию пустых обрывает: прежние беды стали
// прошлым. Иначе задача, однажды оборвавшаяся, копила бы счёт через всю жизнь.
func TestSuccessfulRunBreaksIdleSeries(t *testing.T) {
	o := newOffice(t)
	limit := o.Workflow.Limits.MaxIdleRuns

	for i := 0; i < limit-1; i++ {
		o.idle(runner.TerminationNotStarted, "обрыв связи")
		if !o.tick(t) {
			t.Fatalf("цикл %d не взял задачу", i+1)
		}
	}

	// Прогон с результатом: задача уезжает вперёд, серия обрывается.
	o.agent.termination = runner.Termination{}
	o.agent.result = done("сделал")
	o.agent.commit = "работа"
	if !o.tick(t) {
		t.Fatal("цикл не взял задачу после успешного прогона")
	}
	if task := o.get(t, "OFF-1"); task.Status != "Review" {
		t.Fatalf("статус %q, ожидался Review", task.Status)
	}

	if idle := tracker.IdleRuns(o.get(t, "OFF-1").Comments, "implementer"); idle != 0 {
		t.Errorf("серия пустых прогонов %d, ожидалась оборванной отчётом", idle)
	}
}

// Реестр обязан различать прогон без результата и настоящий провал: исход
// у первого синтетический и врёт. По этим же строкам подбирается max_turns.
func TestLedgerCarriesTermination(t *testing.T) {
	o := newOffice(t)
	o.idle(runner.TerminationTruncated, "предел шагов исчерпан")
	o.agent.usage = runner.Usage{CostUSD: 1.77, DurationMS: 432672, Turns: 51}

	if !o.tick(t) {
		t.Fatal("цикл не взял задачу")
	}

	lines := o.ledgerLines(t)
	if len(lines) == 0 {
		t.Fatal("прогон не попал в реестр, хотя был оплачен")
	}
	last := lines[len(lines)-1]
	if got := last["termination"]; got != string(runner.TerminationTruncated) {
		t.Errorf("в реестре termination=%v, ожидалось %q", got, runner.TerminationTruncated)
	}
	if got := last["cost_usd"]; got != 1.77 {
		t.Errorf("в реестре cost_usd=%v: прогон без результата всё равно оплачен", got)
	}
}

// В терминальном статусе жизнь задачи кончается: работа слита, и рабочая папка
// больше не нужна. Не убирать её — копить худший вид мусора: тот, который
// выглядит рабочим.
//
// Убирает её системный проход, а не прогон: прогон доводит задачу до Approved,
// а это ещё не конец — задача ждёт слияния.
func TestTerminalColumnRemovesWorktree(t *testing.T) {
	o := newOffice(t)
	o.add("OFF-2", "Review")
	o.agent.commit = "работа автора"
	o.agent.result = runner.Result{Outcome: runner.OutcomeDone, Summary: "Принято.", NextOwner: "human"}

	if _, err := o.Tick(context.Background(), "reviewer"); err != nil {
		t.Fatalf("цикл не прошёл: %v", err)
	}

	if task := o.get(t, "OFF-2"); task.Status != "Approved" {
		t.Fatalf("статус %q, ожидался Approved", task.Status)
	}
	workdir := o.agent.seen.Workdir
	if _, err := os.Stat(workdir); err != nil {
		t.Fatalf("рабочая папка одобренной задачи убрана раньше слияния: %v", err)
	}

	if err := o.PRPass(context.Background()); err != nil {
		t.Fatalf("системный проход не прошёл: %v", err)
	}
	if task := o.get(t, "OFF-2"); task.Status != "Done" {
		t.Fatalf("статус %q, ожидался Done", task.Status)
	}
	if _, err := os.Stat(workdir); !os.IsNotExist(err) {
		t.Errorf("рабочая папка терминальной задачи осталась: %v", err)
	}
	// Ветка удаление переживает: worktree эфемерен, работа — нет.
	if got := gitIn(o.origin, "log", "--oneline", "-1", "agent/OFF-2"); !strings.Contains(got, "работа автора") {
		t.Errorf("работа пропала вместе с папкой: %s", got)
	}
}

// В нетерминальном статусе папка обязана остаться: следующая роль продолжит
// с той же незаконченной работы.
func TestNonTerminalColumnKeepsWorktree(t *testing.T) {
	o := newOffice(t)
	o.tick(t)

	if task := o.get(t, "OFF-1"); task.Status != "Review" {
		t.Fatalf("статус %q, ожидался Review", task.Status)
	}
	if _, err := os.Stat(o.agent.seen.Workdir); err != nil {
		t.Errorf("рабочая папка задачи в работе снесена: %v", err)
	}
}

// Имена веток знает раннер, и он обязан их назвать: агент в рабочей папке видит
// только HEAD, а от чего тот отведён — уже нет.
func TestContextNamesTaskAndBaseBranch(t *testing.T) {
	o := newOffice(t)
	o.tick(t)

	context, err := os.ReadFile(filepath.Join(o.agent.seen.Workdir, runner.Dir, runner.FileContext))
	if err != nil {
		t.Fatalf("контекст не прочитан: %v", err)
	}
	for _, want := range []string{"agent/OFF-1", "origin/master"} {
		if !strings.Contains(string(context), want) {
			t.Errorf("в контексте нет %q:\n%s", want, context)
		}
	}
}

// Разобранный ответ не разбирается повторно: иначе задача возвращалась бы
// в очередь на каждом цикле.
func TestHumanReplyIsProcessedOnce(t *testing.T) {
	o := newOffice(t)
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeNeedsHuman, Summary: "Нужен выбор.", NextOwner: "human",
		Questions: []runner.Question{{ID: "Q1", Text: "Какую платёжную систему?"}},
	}
	o.tick(t)
	if err := o.tasks.AddComment("OFF-1", "human", "Берём Stripe."); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}

	// Первый разбор возвращает задачу в очередь, второй не должен найти работы.
	replies, err := o.HumanReplies(context.Background())
	if err != nil {
		t.Fatalf("разбор ответов не прошёл: %v", err)
	}
	if replies != 1 {
		t.Fatalf("разобрано ответов %d, ожидался 1", replies)
	}
	if replies, err = o.HumanReplies(context.Background()); err != nil || replies != 0 {
		t.Errorf("повторный разбор дал %d, %v", replies, err)
	}
}

// Задачу в ожидание отправляет не только вопрос агента: раннер делает это сам,
// исчерпав попытки. Ответ человека обязан возвращать её в работу и в этом случае,
// иначе она остаётся в Blocked навсегда — снять флаг ожидания больше некому.
func TestHumanReplyReturnsTaskBlockedByRunner(t *testing.T) {
	o := newOffice(t)
	o.agent.result = runner.Result{Outcome: runner.OutcomeFailed, Summary: "не вышло", NextOwner: "human"}
	for i := 0; i < o.Workflow.Limits.MaxAttempts; i++ {
		o.tick(t)
	}
	if task := o.get(t, "OFF-1"); task.Status != "Blocked" || !task.HumanFlag {
		t.Fatalf("задача не ушла к человеку после исчерпания попыток: %+v", task)
	}

	if err := o.tasks.AddComment("OFF-1", "human", "Попробуй иначе."); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}
	o.agent.result = done("сделал по подсказке")

	if !o.tick(t) {
		t.Fatal("после ответа человека задача не взята")
	}
	task := o.get(t, "OFF-1")
	if task.Status != "Review" {
		t.Errorf("статус %q, ожидался Review", task.Status)
	}
	if task.HumanFlag {
		t.Error("атрибут ожидания не снят")
	}
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d, ожидался сброс: человек снял причину", task.Attempts)
	}
}

// Роль, говорившая последней, могла исчезнуть из графа — её убрали или
// переименовали, а задача с её вопросом осталась. Возвращать такую задачу
// в очередь несуществующей роли некуда, на это в графе есть запасной маршрут.
func TestHumanReplyFallsBackWhenRoleIsGone(t *testing.T) {
	o := newOffice(t)
	if err := o.tasks.Add(tracker.Task{
		Key: "OFF-2", Project: "OFF", Status: "Blocked", Summary: "спрашивал исчезнувший",
		HumanFlag: true,
	}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	marker := tracker.Marker{RunID: "мертвец-1", Role: "planner", Outcome: "needs_human", ConfigSHA: "5bc6a3b0"}
	if err := o.tasks.AddComment("OFF-2", mock.Account, marker.String()+"\nНужно решение."); err != nil {
		t.Fatalf("вопрос не записан: %v", err)
	}
	if err := o.tasks.AddComment("OFF-2", "human", "Делаем первое."); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}

	if replies, err := o.HumanReplies(context.Background()); err != nil || replies != 1 {
		t.Fatalf("разобрано ответов %d, %v", replies, err)
	}
	task := o.get(t, "OFF-2")
	if task.Status != o.Workflow.HumanReply.Fallback {
		t.Errorf("статус %q, ожидался запасной маршрут %q", task.Status, o.Workflow.HumanReply.Fallback)
	}
	if task.HumanFlag {
		t.Error("атрибут ожидания не снят")
	}
}

// Задача проекта, которого нет в projects.yaml, не берётся: раннеру негде взять
// репозиторий и некуда пушить.
func TestTickSkipsUnknownProject(t *testing.T) {
	o := newOffice(t)
	if err := o.tasks.Add(tracker.Task{Key: "OTH-1", Project: "OTH", Status: "Ready", Summary: "чужая"}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}

	o.tick(t)
	if o.agent.seen.Passport.TaskKey != "OFF-1" {
		t.Errorf("в работу ушла %q, ожидалась OFF-1", o.agent.seen.Passport.TaskKey)
	}
	if task := o.get(t, "OTH-1"); task.Status != "Ready" || task.RunID != "" {
		t.Errorf("чужой проект тронут: %+v", task)
	}
}

// flakyClaim отдаёт ErrClaimLost на первый захват задачи: так выглядит гонка,
// когда конкурент успел первым.
type flakyClaim struct {
	*mock.Tracker
	lost map[string]bool
}

func (f *flakyClaim) Claim(req tracker.ClaimRequest) error {
	if f.lost[req.Key] {
		delete(f.lost, req.Key)
		return fmt.Errorf("%w: конкурент успел первым", tracker.ErrClaimLost)
	}
	return f.Tracker.Claim(req)
}

// Проигранный захват — не ошибка цикла: берём следующего кандидата.
func TestTickTakesNextTaskAfterLostClaim(t *testing.T) {
	o := newOffice(t)
	o.add("OFF-2", "Ready")
	o.useTracker(&flakyClaim{Tracker: o.tasks, lost: map[string]bool{"OFF-1": true}})

	if !o.tick(t) {
		t.Fatal("цикл не взял ни одной задачи")
	}
	if o.agent.seen.Passport.TaskKey != "OFF-2" {
		t.Errorf("в работу ушла %q, ожидалась OFF-2", o.agent.seen.Passport.TaskKey)
	}
	if task := o.get(t, "OFF-1"); task.Status != "Ready" {
		t.Errorf("проигранная задача сдвинулась: %+v", task)
	}

	// Замок на папке проигранной задачи взят до захвата, и его обязаны снять:
	// иначе победитель гонки упрётся в чужой замок на пустом месте.
	lost, err := o.Office.Workspaces.Ensure(
		tracker.TaskRef{Key: "OFF-1", Project: "OFF"}, o.Office.Projects["OFF"])
	if err != nil {
		t.Fatalf("папка проигранной задачи осталась запертой: %v", err)
	}
	lost.Unlock()
}

// Прогон, доживший до конца после reap, не пишет в трекер ничего, кроме
// предупреждения: задачу уже могли отдать другому.
func TestTickWithLostLeaseOnlyWarns(t *testing.T) {
	o := newOffice(t)
	o.agent.commit = "работа агента"
	// Пока агент работает, аренда истекает и reaper возвращает задачу.
	o.agent.result = done("успел доделать")
	o.Office.Agent = &fakeAgent{
		result: done("успел доделать"), commit: "работа агента",
		usage: runner.Usage{CostUSD: 0.25, DurationMS: 18258, Turns: 3},
	}
	stealAgent := o.Office.Agent.(*fakeAgent)
	o.Office.Agent = agentFunc(func(ctx context.Context, req Request) (AgentRun, error) {
		run, err := stealAgent.Run(ctx, req)
		later := now.Add(2 * time.Hour) // аренда истекла, пока агент работал
		o.tasks.Now = func() time.Time { return later }
		o.Office.Now = func() time.Time { return later }
		if err := o.Reap(context.Background()); err != nil {
			t.Fatalf("reap не прошёл: %v", err)
		}
		return run, err
	})

	o.tick(t)

	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Errorf("статус %q: прогон без аренды всё-таки сдвинул задачу", task.Status)
	}
	body := lastComment(t, task).Body
	marker, ok := tracker.MarkerOf(body)
	if !ok || marker.Event != tracker.EventLeaseLost {
		t.Errorf("последняя запись не предупреждение о потере аренды:\n%s", body)
	}
	// Работа при этом не потеряна: ветка опубликована.
	if got := gitIn(o.origin, "log", "--oneline", "-1", "agent/OFF-1"); !strings.Contains(got, "работа агента") {
		t.Errorf("работа потерянного прогона не опубликована: %s", got)
	}
	// Прогон состоялся и был оплачен — цену человек видит здесь: отчёта не будет.
	if !strings.Contains(body, "$0.2500") {
		t.Errorf("в предупреждении нет цены прогона:\n%s", body)
	}
	// И в реестре она есть: учёт не зависит от того, чем кончилась аренда.
	if total := o.spend(t, ledger.Filter{Task: "OFF-1"}); total.Runs != 1 || total.CostUSD != 0.25 {
		t.Errorf("прогон без аренды не учтён: %+v", total)
	}
}

type agentFunc func(context.Context, Request) (AgentRun, error)

func (f agentFunc) Run(ctx context.Context, req Request) (AgentRun, error) {
	return f(ctx, req)
}

// Reaper возвращает зависшую задачу в очередь, объясняя это в тикете.
func TestReapReturnsExpiredTask(t *testing.T) {
	o := newOffice(t)
	o.Office.Agent = agentFunc(func(context.Context, Request) (AgentRun, error) {
		return AgentRun{}, errors.New("раннера убили посреди прогона")
	})

	if _, err := o.Tick(context.Background(), "implementer"); err == nil {
		t.Fatal("смерть агента не замечена")
	}
	if task := o.get(t, "OFF-1"); !task.LeaseAlive(now) {
		t.Fatalf("задача осталась без аренды: %+v", task)
	}

	// Аренда истекла, приходит reaper.
	later := now.Add(2 * time.Hour)
	o.tasks.Now = func() time.Time { return later }
	o.Office.Now = func() time.Time { return later }
	if err := o.Reap(context.Background()); err != nil {
		t.Fatalf("reap не прошёл: %v", err)
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Errorf("статус %q, ожидался Ready", task.Status)
	}
	if task.LeaseAlive(later) || task.RunID != "" {
		t.Errorf("аренда не снята: %+v", task)
	}
	// Смерть раннера — не провал агента, и счётчик попыток она не трогает:
	// перезагрузили машину, кончилось место, уронили процесс. Серия смертей
	// считается отдельно, по маркерам в тикете.
	if task.Attempts != 0 {
		t.Errorf("попыток %d: смерть раннера засчитана агенту как провал", task.Attempts)
	}
	marker, ok := tracker.MarkerOf(lastComment(t, task).Body)
	if !ok || marker.Event != tracker.EventLeaseExpired {
		t.Errorf("reaper не объяснил себя в тикете: %+v", marker)
	}

	// Следующий цикл подхватывает существующий worktree, не теряя сделанного.
	o.Office.Agent = &fakeAgent{result: done("доделал после reap")}
	if !o.tick(t) {
		t.Error("после reap задача не взята заново")
	}
}

// Зависшая аренда и зависшая песочница убираются одним механизмом: раннера
// убили, песочница осталась работающей и держит память и диск, пока reap
// не придёт за ней с тем же run_id.
func TestReapRemovesSandboxOfDeadRun(t *testing.T) {
	o := newOffice(t)
	sandboxes := &fakeSandboxes{}
	o.Office.Sandboxes = sandboxes
	o.Office.Agent = agentFunc(func(context.Context, Request) (AgentRun, error) {
		return AgentRun{}, errors.New("раннера убили посреди прогона")
	})

	if _, err := o.Tick(context.Background(), "implementer"); err == nil {
		t.Fatal("смерть раннера не замечена")
	}
	runID := o.get(t, "OFF-1").RunID
	if runID == "" {
		t.Fatal("задача осталась без аренды: убирать станет нечего")
	}

	later := now.Add(2 * time.Hour)
	o.tasks.Now = func() time.Time { return later }
	o.Office.Now = func() time.Time { return later }
	if err := o.Reap(context.Background()); err != nil {
		t.Fatalf("reap не прошёл: %v", err)
	}

	if !slices.Equal(sandboxes.removed, []string{runID}) {
		t.Errorf("убраны песочницы %q, ожидалась одна — прогона %s", sandboxes.removed, runID)
	}
}

// Песочница живого прогона — не мусор, а законная собственность своего владельца.
//
// Проверяется гонка, ради которой уборка стоит последней: список истёкших аренд —
// снимок, и задачу мог перезахватить соседний tick, пока reap до неё шёл. Тогда
// у задачи уже новый прогон с живой арендой, первая же запись reap упирается
// в правило владения, задача пропускается — и песочница остаётся целой.
// Убирай reap раньше записи, здесь погибла бы песочница нового, живого прогона.
func TestReapKeepsSandboxOfReclaimedTask(t *testing.T) {
	o := newOffice(t)
	sandboxes := &fakeSandboxes{}
	o.Office.Sandboxes = sandboxes
	o.Office.Agent = agentFunc(func(context.Context, Request) (AgentRun, error) {
		return AgentRun{}, errors.New("раннера убили посреди прогона")
	})
	if _, err := o.Tick(context.Background(), "implementer"); err == nil {
		t.Fatal("смерть раннера не замечена")
	}

	later := now.Add(2 * time.Hour)
	o.tasks.Now = func() time.Time { return later }
	o.Office.Now = func() time.Time { return later }

	const reclaimed = "e7c0ffee-0000-4000-8000-000000000001"
	o.Office.Tracker = afterList{Tracker: o.tasks, then: func() {
		if err := o.tasks.Claim(tracker.ClaimRequest{
			Key: "OFF-1", RunID: reclaimed, Owner: "implementer",
			LeaseUntil:   later.Add(time.Hour),
			ExpectStatus: "InProgress", WorkingStatus: "InProgress",
		}); err != nil {
			t.Fatalf("задачу не перезахватили: %v", err)
		}
	}}

	if err := o.Reap(context.Background()); err != nil {
		t.Fatalf("reap не прошёл: %v", err)
	}
	if len(sandboxes.removed) != 0 {
		t.Errorf("reaper снёс песочницу живого прогона: %q", sandboxes.removed)
	}
	if task := o.get(t, "OFF-1"); task.RunID != reclaimed || !task.LeaseAlive(later) {
		t.Errorf("reaper отобрал задачу у нового прогона: %+v", task)
	}
}

// afterList — трекер, в котором что-то происходит сразу после выборки истёкших
// аренд: так выглядит соседний прогон, вклинившийся между снимком и действием.
type afterList struct {
	tracker.Tracker
	then func()
}

func (a afterList) ListExpired(project string, now time.Time) ([]tracker.TaskRef, error) {
	refs, err := a.Tracker.ListExpired(project, now)
	a.then()
	return refs, err
}

// Дело reap — вернуть задачи. Не убравшаяся песочница об этом громко сообщает,
// но не отменяет возврата и не мешает следующим задачам списка.
func TestReapReturnsTaskWhenSandboxSurvives(t *testing.T) {
	o := newOffice(t)
	o.Office.Sandboxes = &fakeSandboxes{err: errors.New("sbx не отвечает")}
	o.Office.Agent = agentFunc(func(context.Context, Request) (AgentRun, error) {
		return AgentRun{}, errors.New("раннера убили посреди прогона")
	})
	if _, err := o.Tick(context.Background(), "implementer"); err == nil {
		t.Fatal("смерть раннера не замечена")
	}

	later := now.Add(2 * time.Hour)
	o.tasks.Now = func() time.Time { return later }
	o.Office.Now = func() time.Time { return later }
	if err := o.Reap(context.Background()); err != nil {
		t.Fatalf("отказ уборки уронил reap: %v", err)
	}

	if task := o.get(t, "OFF-1"); task.Status != "Ready" || task.LeaseAlive(later) {
		t.Errorf("задача не вернулась в очередь: %+v", task)
	}
}

// Живую аренду reaper не трогает.
func TestReapKeepsLiveLease(t *testing.T) {
	o := newOffice(t)
	o.Office.Agent = agentFunc(func(context.Context, Request) (AgentRun, error) {
		return AgentRun{}, errors.New("прогон идёт")
	})
	if _, err := o.Tick(context.Background(), "implementer"); err == nil {
		t.Fatal("ошибка прогона не замечена")
	}

	if err := o.Reap(context.Background()); err != nil {
		t.Fatalf("reap не прошёл: %v", err)
	}
	if task := o.get(t, "OFF-1"); !task.LeaseAlive(now) {
		t.Errorf("reaper отобрал живую аренду: %+v", task)
	}
}

// Пустая очередь — не повод для тревоги: цикл проходит и ничего не делает.
func TestTickWithoutTasks(t *testing.T) {
	o := newOffice(t)
	if err := o.tasks.Transition("OFF-1", tracker.BySystem(), "Done"); err != nil {
		t.Fatalf("задача не убрана из очереди: %v", err)
	}

	if o.tick(t) {
		t.Error("цикл нашёл работу там, где её нет")
	}
	if o.agent.runs != 0 {
		t.Errorf("агент запускался %d раз при пустой очереди", o.agent.runs)
	}
}

// Задача, на которой раннер умирает раз за разом, крутилась бы вечно, если бы
// смерти не считались вовсе. Поэтому они считаются — но отдельно от попыток
// агента, и человека зовут с другой формулировкой: чинить тут нечего, надо
// смотреть, почему прогон не доживает.
func TestReapCallsHumanAfterStreakOfDeaths(t *testing.T) {
	o := newOffice(t)
	o.Office.Agent = agentFunc(func(context.Context, Request) (AgentRun, error) {
		return AgentRun{}, errors.New("раннера убили посреди прогона")
	})

	limit := o.Workflow.Limits.MaxLeaseExpiries
	if limit < 2 {
		t.Fatalf("предел смертей %d: проверять нечего", limit)
	}

	at := now
	for death := 1; death <= limit; death++ {
		o.tasks.Now = func() time.Time { return at }
		o.Office.Now = func() time.Time { return at }
		if _, err := o.Tick(context.Background(), "implementer"); err == nil {
			t.Fatalf("смерть %d не замечена", death)
		}

		at = at.Add(2 * time.Hour) // аренда истекла
		o.tasks.Now = func() time.Time { return at }
		o.Office.Now = func() time.Time { return at }
		if err := o.Reap(context.Background()); err != nil {
			t.Fatalf("reap %d не прошёл: %v", death, err)
		}

		task := o.get(t, "OFF-1")
		if task.Attempts != 0 {
			t.Errorf("после смерти %d попыток %d, ожидался ноль", death, task.Attempts)
		}
		if death < limit && task.Status != "Ready" {
			t.Fatalf("после смерти %d статус %q, ожидался Ready", death, task.Status)
		}
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Blocked" || !task.HumanFlag {
		t.Fatalf("после %d смертей подряд человека не позвали: %s, флаг %v", limit, task.Status, task.HumanFlag)
	}
	body := lastComment(t, task).Body
	if !strings.Contains(body, "роняет") {
		t.Errorf("человеку не объяснили, что дело не в агенте:\n%s", body)
	}
	if strings.Contains(body, "Попытка") || strings.Contains(body, "попытка") {
		t.Errorf("человеку рассказали про попытки, которых не было:\n%s", body)
	}
}

// Успешный отчёт означает, что прогон дошёл до конца: прежние смерти становятся
// прошлым, и серия начинается заново.
func TestReapStreakResetsAfterSuccessfulRun(t *testing.T) {
	o := newOffice(t)
	limit := o.Workflow.Limits.MaxLeaseExpiries

	die := func(at time.Time) time.Time {
		t.Helper()
		o.Office.Agent = agentFunc(func(context.Context, Request) (AgentRun, error) {
			return AgentRun{}, errors.New("раннера убили")
		})
		o.tasks.Now = func() time.Time { return at }
		o.Office.Now = func() time.Time { return at }
		if _, err := o.Tick(context.Background(), "implementer"); err == nil {
			t.Fatal("смерть не замечена")
		}
		at = at.Add(2 * time.Hour)
		o.tasks.Now = func() time.Time { return at }
		o.Office.Now = func() time.Time { return at }
		if err := o.Reap(context.Background()); err != nil {
			t.Fatalf("reap не прошёл: %v", err)
		}
		return at
	}

	at := now
	for range limit - 1 {
		at = die(at)
	}

	// Прогон дошёл до конца — и вернул задачу в работу через Blocked… нет, через
	// исход failed: он оставляет задачу в Ready, где её снова можно убить.
	o.Office.Agent = &fakeAgent{result: runner.Result{
		Outcome: runner.OutcomeFailed, Summary: "не вышло, но отчитался", NextOwner: "human",
	}}
	if !o.tick(t) {
		t.Fatal("задача не взята после серии смертей")
	}

	// Ещё одна смерть: она первая в новой серии, человека звать рано.
	at = die(at.Add(time.Hour))

	task := o.get(t, "OFF-1")
	if task.Status != "Ready" || task.HumanFlag {
		t.Errorf("серия не обнулилась отчётом: %s, флаг %v", task.Status, task.HumanFlag)
	}
}

// Живая проверка поймала то, чего не видели тесты: reap сообщал «песочница
// прогона X убрана» там, где песочницы не было вовсе — прогон жил на другой
// машине. Лог, утверждающий несделанное, хуже молчания: по нему потом судят,
// что происходило.
func TestReapDoesNotClaimRemovalOfAbsentSandbox(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.Office.Log = &log
	o.Office.Sandboxes = &fakeSandboxes{absent: true}
	o.Office.Agent = agentFunc(func(context.Context, Request) (AgentRun, error) {
		return AgentRun{}, errors.New("раннера убили посреди прогона")
	})

	if _, err := o.Tick(context.Background(), "implementer"); err == nil {
		t.Fatal("смерть раннера не замечена")
	}
	later := now.Add(2 * time.Hour)
	o.tasks.Now = func() time.Time { return later }
	o.Office.Now = func() time.Time { return later }
	if err := o.Reap(context.Background()); err != nil {
		t.Fatalf("reap не прошёл: %v", err)
	}

	if strings.Contains(log.String(), "убрана") {
		t.Errorf("лог сообщает об уборке, которой не было:\n%s", log.String())
	}
	if !strings.Contains(log.String(), "на этой машине нет") {
		t.Errorf("лог молчит о том, что песочницы не нашлось:\n%s", log.String())
	}
}

// Барьер рабочей папки берётся **до** захвата, и это не косметика: захватив
// задачу первым, tick успевал бы перезаписать аренду соседа, прежде чем упереться
// в замок. Занята папка — в трекере не должно измениться ничего.
func TestTickDoesNotTouchTrackerWhenWorktreeIsBusy(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.Office.Log = &log

	busy, err := o.Office.Workspaces.Ensure(
		tracker.TaskRef{Key: "OFF-1", Project: "OFF"}, o.Office.Projects["OFF"])
	if err != nil {
		t.Fatalf("папка не занята: %v", err)
	}
	defer busy.Unlock()

	if o.tick(t) {
		t.Fatal("цикл взял задачу, рабочая папка которой занята")
	}
	if o.agent.runs != 0 {
		t.Errorf("агент запущен в чужой рабочей папке: прогонов %d", o.agent.runs)
	}

	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Errorf("статус %q, ожидался Ready: задача не должна была быть захвачена", task.Status)
	}
	if task.RunID != "" {
		t.Errorf("аренда записана поверх чужой работы: %+v", task)
	}
	if !strings.Contains(log.String(), "занята") {
		t.Errorf("отступление не объяснено в логе:\n%s", log.String())
	}
}

// Занятая папка — не конец цикла: берём следующего кандидата. Прежде обход
// обрывался на первой же занятой задаче, потому что замок брался после захвата
// и упирался в него уже сам прогон.
func TestTickTakesNextTaskWhenWorktreeIsBusy(t *testing.T) {
	o := newOffice(t)
	o.add("OFF-2", "Ready")

	busy, err := o.Office.Workspaces.Ensure(
		tracker.TaskRef{Key: "OFF-1", Project: "OFF"}, o.Office.Projects["OFF"])
	if err != nil {
		t.Fatalf("папка не занята: %v", err)
	}
	defer busy.Unlock()

	if !o.tick(t) {
		t.Fatal("цикл не взял ни одной задачи")
	}
	if o.agent.seen.Passport.TaskKey != "OFF-2" {
		t.Errorf("в работу ушла %q, ожидалась OFF-2", o.agent.seen.Passport.TaskKey)
	}
}

// withWorkflow подменяет граф офиса синтетическим. Маршруты, которых в рабочем
// workflow.yaml ещё нет, проверяются на нём: роль по-прежнему implementer —
// каталог роли для прогона нужен настоящий.
func (o *office) withWorkflow(t *testing.T, yaml string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), tracker.WorkflowFile)
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("граф не записан: %v", err)
	}
	wf, err := tracker.LoadWorkflow(path)
	if err != nil {
		t.Fatalf("синтетический граф не загружен: %v", err)
	}
	o.Office.Workflow = wf
}

// routingWorkflow — граф, в котором исход `done` разветвляется по next_owner.
const routingWorkflow = `statuses: [Ready, InProgress, Review, Blocked, Done]
roles:
  implementer:
    reads_from: Ready
    working: InProgress
    outcomes:
      done:
        to: Review
        by_next_owner:
          human: Blocked
      needs_human: { to: Blocked, human: true }
      split:       { to: Blocked, human: true }
      blocked:     { to: Ready, attempts: +1 }
      failed:      { to: Ready, attempts: +1 }
limits:
  max_attempts: 3
  max_lease_expiries: 3
  max_push_failures: 3
  max_idle_runs: 3
  max_return_rounds: 3
  lease_margin_sec: 300
human_reply:
  fallback: Ready
  reset_attempts: true
`

// Маршрут исхода зависит от того, кому агент передал задачу. Решение принимает
// граф, а не агент: он лишь называет следующего владельца, а карту маршрутов
// пишет человек (DESIGN §2.1).
func TestFinishRoutesByNextOwner(t *testing.T) {
	cases := []struct {
		next string
		want string
	}{
		{next: "human", want: "Blocked"}, // назван в карте
		{next: "none", want: "Review"},   // в карте нет — маршрут по умолчанию
	}

	for _, tc := range cases {
		t.Run(tc.next, func(t *testing.T) {
			o := newOffice(t)
			o.withWorkflow(t, routingWorkflow)
			o.agent.result = runner.Result{Outcome: runner.OutcomeDone, Summary: "готово", NextOwner: tc.next}

			o.tick(t)
			task := o.get(t, "OFF-1")
			if task.Status != tc.want {
				t.Errorf("статус %q, ожидался %q", task.Status, tc.want)
			}

			// Кому передана задача — часть шапки отчёта: без неё круги
			// «правки → ревью» в переписке не сосчитать.
			marker, ok := tracker.MarkerOf(lastComment(t, task).Body)
			if !ok {
				t.Fatalf("у отчёта нет маркера: %q", lastComment(t, task).Body)
			}
			if marker.Next != tc.next {
				t.Errorf("в маркере next=%q, ожидался %q", marker.Next, tc.next)
			}
		})
	}
}

// strayRouteWorkflow — граф, в котором ревьюер вправе вернуть работу автору,
// но про аналитика у его исхода не сказано ничего. Так выглядит недосмотр
// человека, пишущего карту маршрутов, и именно его правило и ловит.
const strayRouteWorkflow = `statuses: [Analysis, Ready, InProgress, Review, Blocked, Approved]
terminal: [Approved]
tick_order: [reviewer, implementer, analyst]
roles:
  analyst:
    reads_from: Analysis
    outcomes:
      done:        { to: Ready, by_next_owner: { implementer: Ready } }
      needs_human: { to: Blocked, human: true }
      split:       { to: Blocked, human: true }
      blocked:     { to: Analysis, attempts: +1 }
      failed:      { to: Analysis, attempts: +1 }
  implementer:
    reads_from: Ready
    working: InProgress
    outcomes:
      done:        { to: Review }
      needs_human: { to: Blocked, human: true }
      split:       { to: Blocked, human: true }
      blocked:     { to: Ready, attempts: +1 }
      failed:      { to: Ready, attempts: +1 }
  reviewer:
    reads_from: Review
    outcomes:
      done:
        to: Approved
        by_next_owner:
          implementer: Ready
      needs_human: { to: Blocked, human: true }
      split:       { to: Blocked, human: true }
      blocked:     { to: Review, attempts: +1 }
      failed:      { to: Review, attempts: +1 }
limits:
  max_attempts: 3
  max_lease_expiries: 3
  max_push_failures: 3
  max_idle_runs: 3
  max_return_rounds: 3
  lease_margin_sec: 300
human_reply:
  fallback: Ready
  reset_attempts: true
`

// Роль графа, названную владельцем и не описанную в карте исхода, раннер
// по умолчанию не везёт. Умолчание уводит работу вперёд по конвейеру — здесь,
// в графе этого теста, прямо в терминал, — хотя её как раз вернули на переделку.
func TestUnknownRouteCallsHumanAndKeepsWork(t *testing.T) {
	o := newOffice(t)
	o.withWorkflow(t, strayRouteWorkflow)
	o.addTried("OFF-2", "Review", 1)
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeDone, Summary: "План неверен, дело не в коде.", NextOwner: "analyst",
	}

	if worked, err := o.Tick(context.Background(), "reviewer"); err != nil || !worked {
		t.Fatalf("ревьюер не взял задачу: worked=%v, err=%v", worked, err)
	}

	task := o.get(t, "OFF-2")
	if task.Status != "Blocked" {
		t.Errorf("статус %q, ожидался Blocked: маршрута для аналитика в карте нет", task.Status)
	}
	if !task.HumanFlag {
		t.Error("человека не позвали, а решать ему")
	}
	// Попытку это не тратит и счётчик не обнуляет: передачи не было вовсе.
	if task.Attempts != 1 {
		t.Errorf("счётчик попыток %d, ожидалась прежняя единица", task.Attempts)
	}
	if _, err := os.Stat(o.agent.seen.Workdir); err != nil {
		t.Errorf("рабочая папка не пережила остановку: %v", err)
	}

	last := lastComment(t, task)
	marker, ok := tracker.MarkerOf(last.Body)
	if !ok || marker.Event != tracker.EventRouteUnknown {
		t.Fatalf("последняя запись не объясняет остановку:\n%s", last.Body)
	}
	if !strings.Contains(last.Body, "analyst") {
		t.Errorf("в объяснении не назван владелец:\n%s", last.Body)
	}
}

// `human` и `none` ролями графа не являются: для них маршрут по умолчанию —
// это и есть маршрут, а не недосмотр. Правило их не касается.
func TestUnknownRouteIgnoresReservedOwners(t *testing.T) {
	o := newOffice(t)
	o.withWorkflow(t, strayRouteWorkflow)
	o.add("OFF-2", "Review")
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeDone, Summary: "Принято.", NextOwner: "none",
	}

	if worked, err := o.Tick(context.Background(), "reviewer"); err != nil || !worked {
		t.Fatalf("ревьюер не взял задачу: worked=%v, err=%v", worked, err)
	}

	if task := o.get(t, "OFF-2"); task.Status != "Approved" {
		t.Errorf("статус %q, ожидался Approved: маршрут по умолчанию", task.Status)
	}
}

// spyTransitions считает переводы. Нужен там, где важно не то, куда задача
// уехала, а то, что её никуда не двигали.
type spyTransitions struct {
	*mock.Tracker
	calls []string
}

func (s *spyTransitions) Transition(key string, by tracker.Actor, to string) error {
	s.calls = append(s.calls, to)
	return s.Tracker.Transition(key, by, to)
}

// noWorkingWorkflow — граф роли без рабочего статуса: она читает и работает
// в одной и той же.
const noWorkingWorkflow = `statuses: [Ready, InProgress, Review, Blocked, Done]
roles:
  implementer:
    reads_from: Ready
    outcomes:
      done:        { to: Review }
      needs_human: { to: Blocked, human: true }
      split:       { to: Blocked, human: true }
      blocked:     { to: Ready, attempts: +1 }
      failed:      { to: Ready, attempts: +1 }
limits:
  max_attempts: 3
  max_lease_expiries: 3
  max_push_failures: 3
  max_idle_runs: 3
  max_return_rounds: 3
  lease_margin_sec: 300
human_reply:
  fallback: Ready
  reset_attempts: true
`

// Перевод в тот статус, в котором задача и так лежит, не делается вовсе.
// Это общее правило раннера, а не частный случай: у роли без рабочего статуса
// туда ведут и reap, и возвраты по исходам, а в JIRA перехода «в себя»
// может не быть в workflow вовсе — и захват падал бы на ровном месте.
func TestFinishSkipsTransitionToSameStatus(t *testing.T) {
	o := newOffice(t)
	o.withWorkflow(t, noWorkingWorkflow)
	spy := &spyTransitions{Tracker: o.tasks}
	o.useTracker(spy)
	o.agent.result = runner.Result{Outcome: runner.OutcomeFailed, Summary: "не вышло", NextOwner: "human"}

	o.tick(t)

	task := o.get(t, "OFF-1")
	if task.Status != "Ready" {
		t.Errorf("статус %q, ожидался Ready", task.Status)
	}
	if task.Attempts != 1 {
		t.Errorf("попытки %d, ожидалась одна: провал считается и без перехода", task.Attempts)
	}
	if len(spy.calls) != 0 {
		t.Errorf("переводы: %v, ожидалось ни одного — задача и так в Ready", spy.calls)
	}
}

// knownWorkflow — трекер, умеющий рассказать о своём workflow. Так отвечает jira;
// файловый трекер этого интерфейса не реализует вовсе.
type knownWorkflow struct {
	tracker.Tracker
	check tracker.WorkflowCheck
	err   error
}

func (k knownWorkflow) CheckWorkflow(string, string) (tracker.WorkflowCheck, error) {
	return k.check, k.err
}

// Рабочий статус, доступный переходом из него самого, позволяет двум прогонам
// захватить одну задачу. С одним раннером на проект это безопасно — потому
// раннер и предупреждает, а не отказывается работать.
func TestClaimWarnsAboutWorkflowWithTwoOwners(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.Office.Log = &log
	o.useTracker(knownWorkflow{Tracker: o.tasks, check: tracker.WorkflowCheck{Sample: "OFF-1", SelfEntry: true}})

	if _, err := o.Tick(context.Background(), "implementer"); err != nil {
		t.Fatalf("тик не прошёл: %v", err)
	}

	if !strings.Contains(log.String(), "двух владельцев") {
		t.Errorf("раннер не сказал, чем грозит такой workflow:\n%s", log.String())
	}
	if !strings.Contains(log.String(), "tracker-protocol.md") {
		t.Errorf("предупреждение не говорит, где читать:\n%s", log.String())
	}
}

// Спрашивается workflow при захвате, а не при старте, потому что переходы JIRA
// показывает только у конкретной задачи: до захвата рабочий статус может быть
// пуст, и ответа не будет вовсе. Захват же сам кладёт туда задачу — образец
// появляется ровно к моменту вопроса.
//
// Отсюда главное для живого лога: тик без работы про workflow не говорит ничего
// и ничего не спрашивает. Раньше строка «проверка не выполнена» печаталась
// на каждый заход и утапливала ту, ради которой всё затевалось.
func TestIdleTickSaysNothingAboutWorkflow(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.useTracker(knownWorkflow{Tracker: o.tasks, check: tracker.WorkflowCheck{Sample: "OFF-1", SelfEntry: true}})

	// Единственную задачу забирает первый тик; второму работы уже нет.
	if _, err := o.Tick(context.Background(), "implementer"); err != nil {
		t.Fatalf("тик не прошёл: %v", err)
	}
	o.Office.Log = &log
	worked, err := o.Tick(context.Background(), "implementer")
	if err != nil {
		t.Fatalf("тик не прошёл: %v", err)
	}
	if worked {
		t.Fatal("работа нашлась там, где её не должно быть: тест проверяет не то")
	}

	if strings.Contains(log.String(), "workflow") {
		t.Errorf("тик без работы шумит про workflow:\n%s", log.String())
	}
}

// Workflow меняют руками и редко, а захватов за час бывают десятки. Повторять
// одно и то же предупреждение на каждый — то же самое, что не предупреждать:
// строку перестают читать. Память живёт столько же, сколько процесс.
func TestWorkflowWarningSaidOncePerProcess(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.Office.Log = &log
	o.add("OFF-2", "Ready")
	o.useTracker(knownWorkflow{Tracker: o.tasks, check: tracker.WorkflowCheck{Sample: "OFF-1", SelfEntry: true}})

	for range 2 {
		if _, err := o.Tick(context.Background(), "implementer"); err != nil {
			t.Fatalf("тик не прошёл: %v", err)
		}
	}

	if got := strings.Count(log.String(), "двух владельцев"); got != 1 {
		t.Errorf("предупреждений %d, ожидалось одно:\n%s", got, log.String())
	}
}

// У файлового трекера workflow нет вовсе, и жаловаться не на что: проверка
// молча пропускается, а не превращается в шум на каждом захвате.
func TestCheckWorkflowSilentForTrackerWithoutWorkflow(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.Office.Log = &log

	if _, err := o.Tick(context.Background(), "implementer"); err != nil {
		t.Fatalf("тик не прошёл: %v", err)
	}

	if strings.Contains(log.String(), "workflow") {
		t.Errorf("трекер без workflow вызвал жалобу:\n%s", log.String())
	}
}

// Роль без рабочего статуса проверять нечего: захват её задачи статуса
// не меняет, и лазейка «вход в рабочий статус из него самого» к ней
// не относится вовсе. Спрашивать о ней трекер — значит спрашивать о пустом
// статусе.
func TestCheckWorkflowSilentForRoleWithoutWorking(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.withWorkflow(t, noWorkingWorkflow)
	o.Office.Log = &log
	o.useTracker(knownWorkflow{Tracker: o.tasks, check: tracker.WorkflowCheck{Sample: "OFF-1", SelfEntry: true}})

	if _, err := o.Tick(context.Background(), "implementer"); err != nil {
		t.Fatalf("тик не прошёл: %v", err)
	}

	if strings.Contains(log.String(), "workflow") {
		t.Errorf("роль без рабочего статуса вызвала жалобу:\n%s", log.String())
	}
}

// unknownProject — трекер, не знающий одного из проектов конфигурации.
// Так выглядит протухшая строка в projects.yaml: проект описан, а в трекере
// его нет и никогда не было.
type unknownProject struct {
	tracker.Tracker
	missing string
}

func (u unknownProject) ListReady(project, status string) ([]tracker.TaskRef, error) {
	if project == u.missing {
		return nil, fmt.Errorf("%w: %s", tracker.ErrNoProject, project)
	}
	return u.Tracker.ListReady(project, status)
}

func (u unknownProject) ListExpired(project string, now time.Time) ([]tracker.TaskRef, error) {
	if project == u.missing {
		return nil, fmt.Errorf("%w: %s", tracker.ErrNoProject, project)
	}
	return u.Tracker.ListExpired(project, now)
}

// Проекты обходятся по порядку, и раньше первый же незнакомый трекеру проект
// бросал весь цикл — работа по остальным вставала. Заглушка в projects.yaml
// останавливала reap на полигоне до настоящего проекта; поймано живой проверкой.
func TestTickSkipsProjectUnknownToTracker(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.Office.Log = &log
	// Имя нарочно раньше OFF по алфавиту: обход дойдёт до него первым.
	o.Office.Projects["AAA"] = tracker.Project{
		RepoURL: o.origin, DefaultBranch: "master", BranchPrefix: "agent/",
	}
	o.useTracker(unknownProject{Tracker: o.tasks, missing: "AAA"})

	if !o.tick(t) {
		t.Fatal("цикл бросил работу из-за проекта, которого трекер не знает")
	}
	if task := o.get(t, "OFF-1"); task.Status != "Review" {
		t.Errorf("задача годного проекта не сделана: %s", task.Status)
	}
	if !strings.Contains(log.String(), "AAA") {
		t.Errorf("пропуск проекта не объяснён в логе:\n%s", log.String())
	}
}

// То же для reap: его гоняет планировщик, и там некому заметить, что цикл
// перестал доходить до половины проектов.
func TestReapSkipsProjectUnknownToTracker(t *testing.T) {
	o := newOffice(t)
	o.Office.Log = io.Discard
	o.Office.Agent = agentFunc(func(context.Context, Request) (AgentRun, error) {
		return AgentRun{}, errors.New("раннера убили посреди прогона")
	})
	if _, err := o.Tick(context.Background(), "implementer"); err == nil {
		t.Fatal("смерть раннера не замечена")
	}

	o.Office.Projects["AAA"] = tracker.Project{
		RepoURL: o.origin, DefaultBranch: "master", BranchPrefix: "agent/",
	}
	later := now.Add(2 * time.Hour)
	o.tasks.Now = func() time.Time { return later }
	o.Office.Now = func() time.Time { return later }
	o.useTracker(unknownProject{Tracker: o.tasks, missing: "AAA"})

	if err := o.Reap(context.Background()); err != nil {
		t.Fatalf("reap бросил работу из-за незнакомого проекта: %v", err)
	}
	if task := o.get(t, "OFF-1"); task.Status != "Ready" {
		t.Errorf("зависшая задача годного проекта не возвращена: %s", task.Status)
	}
}

// tickRoleOnce прогоняет цикл одной роли и падает на инфраструктурной ошибке.
func (o *office) tickRoleOnce(t *testing.T, role string) bool {
	t.Helper()
	worked, err := o.Tick(context.Background(), role)
	if err != nil {
		t.Fatalf("цикл роли %s не прошёл: %v", role, err)
	}
	return worked
}

// ledgerLines — строки реестра этого офиса, разобранные по порядку.
func (o *office) ledgerLines(t *testing.T) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(o.home, ledger.FileName))
	if err != nil {
		t.Fatalf("реестр не прочитан: %v", err)
	}
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("строка реестра не разобрана: %v", err)
		}
		lines = append(lines, entry)
	}
	return lines
}

// findMarker ищет в переписке запись с таким событием.
func findMarker(task tracker.Task, event string) (tracker.Comment, bool) {
	for _, comment := range task.Comments {
		if m, ok := tracker.MarkerOf(comment.Body); ok && m.Event == event {
			return comment, true
		}
	}
	return tracker.Comment{}, false
}

// Ответ человека доходит до спросившей роли разобранным. Общее хранилище тут
// одно — тикет: вопросы раннер напечатал в него прошлым прогоном, оттуда же
// и читает. Между вопросом и ответом лежит другой тик, и помнить заданное негде.
func TestAnswersReachTheAskingRole(t *testing.T) {
	o := newOffice(t)
	if err := o.tasks.Add(tracker.Task{
		Key: "OFF-2", Project: "OFF", Status: "Blocked", Summary: "противоречивая постановка",
		HumanFlag: true,
	}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}

	report := tracker.ReportBody(
		tracker.Marker{RunID: "аналитик-1", Role: "analyst", Outcome: "needs_human", Next: "human", ConfigSHA: "5bc6a3b0"},
		runner.Result{
			Outcome: runner.OutcomeNeedsHuman, Summary: "Постановка допускает два прочтения.", NextOwner: "human",
			Questions: []runner.Question{
				{ID: "Q1", Text: "Идемпотентность или скорость?", Options: []runner.Option{
					{ID: "a", Label: "идемпотентность"}, {ID: "b", Label: "скорость"},
				}},
				{ID: "Q2", Text: "Какой формат даты в экспорте?"},
			},
		}, "", runner.Usage{})
	if err := o.tasks.AddComment("OFF-2", mock.RoleAccount("analyst"), report); err != nil {
		t.Fatalf("вопросы не записаны: %v", err)
	}
	if err := o.tasks.AddComment("OFF-2", "human", "Q1: b\nQ2: ISO-8601"); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}

	o.agent.byRole = map[string]runner.Result{"analyst": {
		Outcome: runner.OutcomeFailed, Summary: "Не успел.", NextOwner: "human",
	}}
	if !o.tickRoleOnce(t, "analyst") {
		t.Fatal("аналитик не взял разблокированную задачу")
	}

	context := o.agent.context["analyst"]
	for _, want := range []string{
		"## Ответы человека",
		"Q1 «Идемпотентность или скорость?» → b — скорость",
		"Q2 «Какой формат даты в экспорте?» → ISO-8601",
	} {
		if !strings.Contains(context, want) {
			t.Errorf("в контексте нет %q:\n%s", want, context)
		}
	}
}

// Вопросы, на которые уже отработали, второй раз в контекст не попадают: иначе
// на круге implementer → analyst аналитик получил бы позавчерашний выбор как
// свежий и решил бы задачу заново.
func TestSpentAnswersDoNotReturn(t *testing.T) {
	o := newOffice(t)
	o.add("OFF-2", "Analysis")

	report := tracker.ReportBody(
		tracker.Marker{RunID: "аналитик-1", Role: "analyst", Outcome: "needs_human", Next: "human", ConfigSHA: "5bc6a3b0"},
		runner.Result{
			Outcome: runner.OutcomeNeedsHuman, Summary: "Нужен выбор.", NextOwner: "human",
			Questions: []runner.Question{{ID: "Q1", Text: "Идемпотентность или скорость?", Options: []runner.Option{
				{ID: "a", Label: "идемпотентность"}, {ID: "b", Label: "скорость"},
			}}},
		}, "", runner.Usage{})
	done := tracker.ReportBody(
		tracker.Marker{RunID: "аналитик-2", Role: "analyst", Outcome: "done", Next: "implementer", ConfigSHA: "5bc6a3b0"},
		runner.Result{Outcome: runner.OutcomeDone, Summary: "План готов.", NextOwner: "implementer"}, "", runner.Usage{})

	for _, entry := range []struct{ author, body string }{
		{mock.RoleAccount("analyst"), report},
		{"human", "Q1: b"},
		{mock.RoleAccount("analyst"), done},
	} {
		if err := o.tasks.AddComment("OFF-2", entry.author, entry.body); err != nil {
			t.Fatalf("комментарий не записан: %v", err)
		}
	}

	o.agent.byRole = map[string]runner.Result{"analyst": {
		Outcome: runner.OutcomeFailed, Summary: "Не успел.", NextOwner: "human",
	}}
	if !o.tickRoleOnce(t, "analyst") {
		t.Fatal("аналитик не взял задачу")
	}

	if context := o.agent.context["analyst"]; strings.Contains(context, "Ответы человека") {
		t.Errorf("съеденные ответы вернулись в контекст:\n%s", context)
	}
}

// alone убирает с дороги задачу, которую заводит newOffice: сценарий про одну
// задачу не должен спотыкаться о вторую в той же очереди.
func (o *office) alone(t *testing.T) {
	t.Helper()
	if err := o.tasks.Move("OFF-1", "Backlog"); err != nil {
		t.Fatalf("задача не убрана с дороги: %v", err)
	}
}

// План, каким его пишет аналитик: три файла в каталоге изменения, закоммиченные
// в ветку задачи. Незакоммиченный план для следующей роли не существует.
func writePlan(t *testing.T, req Request, key, message string, items ...string) {
	t.Helper()
	dir := runner.ChangeDirRel(key)
	// Каталог изменения раньше готовил раннер по шаблонам; теперь аналитик
	// создаёт его сам, и тест воспроизводит ровно это.
	if err := os.MkdirAll(filepath.Join(req.Workdir, dir), 0o755); err != nil {
		t.Fatalf("каталог изменения не создан: %v", err)
	}

	plan := "# Что делать\n\n"
	for _, item := range items {
		plan += "- [ ] " + item + "\n"
	}
	files := map[string]string{
		runner.FileBrief:  "# Зачем\n\nПочинить оплату.\n\n## Критерии приёмки\n\n- [ ] тесты зелёные\n",
		runner.FileDesign: "# Как\n\nТрогаем billing.\n",
		runner.FileTasks:  plan,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(req.Workdir, dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("%s не записан: %v", name, err)
		}
	}
	gitIn(req.Workdir, "add", dir)
	gitIn(req.Workdir, "commit", "-q", "-m", message)
}

// markItem отмечает пункт плана — единственное, что разработчику в нём положено.
func markItem(t *testing.T, req Request, key string, n int) {
	t.Helper()
	path := filepath.Join(req.Workdir, runner.ChangeDirRel(key), runner.FileTasks)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("план не прочитан: %v", err)
	}

	lines, marked := strings.Split(string(raw), "\n"), 0
	for i, line := range lines {
		if !strings.HasPrefix(line, "- [ ] ") {
			continue
		}
		if marked++; marked == n {
			lines[i] = strings.Replace(line, "- [ ] ", "- [x] ", 1)
			break
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatalf("план не записан: %v", err)
	}
}

// Сквозной сценарий: человек переводит задачу в очередь аналитика, план проходит
// три роли, разработчик возвращает его на правку и доводит до ревью, ревьюер
// одобряет. Проверяется не каждая деталь по отдельности, а то, что они сходятся
// в один конвейер: план из ветки виден следующей роли, маршруты ведут туда, куда
// задумано, ограждения честную работу пропускают.
func TestEndToEndTaskThroughThreeRoles(t *testing.T) {
	o := newOffice(t)
	o.alone(t)
	o.add("OFF-3", "Backlog")

	o.agent.act = func(req Request, run int) runner.Result {
		switch req.Role.Name {
		case "analyst":
			if run == 1 {
				writePlan(t, req, "OFF-3", "план", "написать тест", "починить оплату")
				return runner.Result{Outcome: runner.OutcomeDone, Summary: "План готов.", NextOwner: "implementer"}
			}
			// Разработчик вернул задачу: план правит аналитик, и правка —
			// это коммит, а не слова в отчёте.
			writePlan(t, req, "OFF-3", "план поправлен", "написать тест", "починить оплату в billing.py")
			return runner.Result{Outcome: runner.OutcomeDone, Summary: "План поправлен.", NextOwner: "implementer"}
		case "implementer":
			if run == 1 {
				markItem(t, req, "OFF-3", 1)
				gitIn(req.Workdir, "add", "-A")
				gitIn(req.Workdir, "commit", "-q", "-m", "тест")
				return runner.Result{
					Outcome: runner.OutcomeDone, NextOwner: "analyst",
					Summary: "Пункт 2 плана велит править ридер, а оплата живёт в billing.py.",
				}
			}
			markItem(t, req, "OFF-3", 2)
			gitIn(req.Workdir, "add", "-A")
			gitIn(req.Workdir, "commit", "-q", "-m", "оплата")
			return runner.Result{Outcome: runner.OutcomeDone, Summary: "Сделано по плану.", NextOwner: "reviewer"}
		default:
			return runner.Result{Outcome: runner.OutcomeDone, Summary: "Сходится с планом, тесты зелёные.", NextOwner: "human"}
		}
	}

	// Backlog человеческий: сам по себе он в работу не уходит.
	o.tickAll(t)
	if task := o.get(t, "OFF-3"); task.Status != "Backlog" {
		t.Fatalf("статус %q: Backlog в работу не берут", task.Status)
	}
	if o.agent.runs != 0 {
		t.Fatalf("прогонов %d: работы в Backlog для офиса нет", o.agent.runs)
	}

	// Дальше задачу ведёт офис, а человек только перенёс её в очередь аналитика.
	if err := o.tasks.Move("OFF-3", "Analysis"); err != nil {
		t.Fatalf("задача не переведена: %v", err)
	}

	// Шаги разбираются поимённо, а не циклом TickAll: за один заход конвейер
	// успевает и вернуть задачу назад, и снова увести вперёд — порядок обхода
	// идёт с конца, — и промежуточный статус в такой проверке был бы не виден.
	for _, step := range []struct{ role, want string }{
		{"analyst", "Ready"},        // план написан и закоммичен
		{"implementer", "Analysis"}, // план разошёлся с кодом — назад к автору плана
		{"analyst", "Ready"},        // аналитик поправил план
		{"implementer", "Review"},   // работа сделана по плану
		{"reviewer", "Approved"},    // разбор сошёлся
	} {
		if !o.tickRoleOnce(t, step.role) {
			t.Fatalf("роль %s не взяла задачу", step.role)
		}
		if got := o.get(t, "OFF-3").Status; got != step.want {
			t.Fatalf("после роли %s статус %q, ожидался %q", step.role, got, step.want)
		}
	}

	task := o.get(t, "OFF-3")
	if task.Attempts != 0 {
		t.Errorf("счётчик попыток %d: провалов не было", task.Attempts)
	}
	if task.HumanFlag {
		t.Error("человека позвали, хотя спор не заходил в тупик")
	}

	// План разработчик получил из ветки, а не из отчёта: раннер называет его
	// путь только когда файл отслеживается git.
	if context := o.agent.context["implementer"]; !strings.Contains(context, "План: docs/changes/OFF-3/tasks.md") {
		t.Errorf("разработчик не знал про план:\n%s", context)
	}
	if context := o.agent.context["reviewer"]; !strings.Contains(context, "Каталог изменения: docs/changes/OFF-3") {
		t.Errorf("ревьюеру не назвали каталог изменения:\n%s", context)
	}
}

// Вопрос человеку и ответ на него: аналитик спрашивает, задача уходит в ожидание,
// человек отвечает меткой — и она возвращается спросившей роли разобранной,
// без промежуточных ролей.
func TestEndToEndQuestionAndAnswer(t *testing.T) {
	o := newOffice(t)
	o.alone(t)
	o.add("OFF-3", "Analysis")

	o.agent.act = func(req Request, run int) runner.Result {
		if run == 1 {
			return runner.Result{
				Outcome: runner.OutcomeNeedsHuman, NextOwner: "human",
				Summary: "Постановка допускает два прочтения с разной ценой ошибки.",
				Questions: []runner.Question{{ID: "Q1", Text: "Идемпотентность или скорость?", Options: []runner.Option{
					{ID: "a", Label: "идемпотентность"}, {ID: "b", Label: "скорость"},
				}}},
			}
		}
		writePlan(t, req, "OFF-3", "план", "сделать быстро")
		return runner.Result{Outcome: runner.OutcomeDone, Summary: "План готов.", NextOwner: "implementer"}
	}

	o.tickAll(t)
	task := o.get(t, "OFF-3")
	if task.Status != "Blocked" || !task.HumanFlag {
		t.Fatalf("задача не ушла в ожидание: %s, флаг %v", task.Status, task.HumanFlag)
	}
	// Вопрос попал в тикет по грамматике протокола — оттуда его и прочитает
	// следующий тик, другого общего хранилища нет.
	if body := lastComment(t, task).Body; !strings.Contains(body, "Q1: Идемпотентность или скорость?") ||
		!strings.Contains(body, "  b) скорость") {
		t.Fatalf("вопрос записан не по грамматике:\n%s", body)
	}

	if err := o.tasks.AddComment("OFF-3", "человек", "Q1: b"); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}

	o.tickAll(t)
	if got := o.get(t, "OFF-3").Status; got != "Ready" {
		t.Fatalf("статус %q, ожидался Ready: ответ получен, план написан", got)
	}
	if context := o.agent.context["analyst"]; !strings.Contains(context, "Q1 «Идемпотентность или скорость?» → b — скорость") {
		t.Errorf("ответ не дошёл до спросившей роли разобранным:\n%s", context)
	}
}

// Круги возврата ограничены, но реплика человека обрывает серию: он вмешался
// в спор, и считать прежние круги дальше — значит звать его же обратно.
func TestReturnRoundsBrokenByHumanWord(t *testing.T) {
	o := newOffice(t)
	o.alone(t)
	o.add("OFF-3", "Review")
	o.agent.byRole = map[string]runner.Result{
		"reviewer":    {Outcome: runner.OutcomeDone, Summary: "Верните автору.", NextOwner: "implementer"},
		"implementer": {Outcome: runner.OutcomeDone, Summary: "Готово, смотрите снова.", NextOwner: "reviewer"},
	}
	limit := o.Workflow.Limits.MaxReturnRounds

	// Два круга подряд — предел ещё не достигнут.
	for range limit - 1 {
		o.tickAll(t)
	}
	if task := o.get(t, "OFF-3"); task.HumanFlag {
		t.Fatalf("человека позвали на %d круге из %d", limit-1, limit)
	}

	if err := o.tasks.AddComment("OFF-3", "человек", "Смотрю, разбирайтесь дальше сами."); err != nil {
		t.Fatalf("реплика не записана: %v", err)
	}

	// Ещё столько же кругов: серия началась заново, и предел не сработал.
	for range limit - 1 {
		o.tickAll(t)
	}
	task := o.get(t, "OFF-3")
	if task.HumanFlag {
		t.Errorf("серия не оборвалась репликой человека: человека позвали снова")
	}
	if _, found := findMarker(task, tracker.EventReturnRoundsExhausted); found {
		t.Error("круги посчитаны через реплику человека")
	}
}

// Крупную задачу офис не режет сам. Аналитик предлагает разбиение в brief.md
// и спрашивает; подзадачи заводит человек — и он же уводит родителя из очереди,
// а не отвечает комментарием: тик между ответом и переносом вернул бы задачу
// аналитику, и тот распланировал бы её заново.
func TestOversizedTaskGoesBackToHuman(t *testing.T) {
	o := newOffice(t)
	o.alone(t)
	o.add("OFF-3", "Analysis")
	o.agent.act = func(req Request, _ int) runner.Result {
		writePlan(t, req, "OFF-3", "разбиение", "часть 1", "часть 2", "часть 3")
		return runner.Result{
			Outcome: runner.OutcomeNeedsHuman, NextOwner: "human",
			Summary: "Задача на три самостоятельные части, разбиение — в brief.md.",
			Questions: []runner.Question{{ID: "Q1", Text: "Разбить на три задачи, как в brief.md?", Options: []runner.Option{
				{ID: "yes", Label: "да, заведу подзадачи"}, {ID: "no", Label: "нет, делаем целиком"},
			}}},
		}
	}

	o.tickAll(t)
	task := o.get(t, "OFF-3")
	if task.Status != "Blocked" || !task.HumanFlag {
		t.Fatalf("задача не ушла к человеку: %s, флаг %v", task.Status, task.HumanFlag)
	}

	// Подзадач офис не заводит: в трекере ровно те задачи, что были.
	refs, err := o.tasks.List("OFF", o.Workflow.Statuses)
	if err != nil {
		t.Fatalf("список задач не прочитан: %v", err)
	}
	if len(refs) != 2 {
		t.Errorf("задач в проекте %d, ожидалось 2: офис в трекере ничего не создаёт", len(refs))
	}

	// Человек завёл подзадачи сам и увёл родителя из очереди — переносом,
	// а не словом: перенос снимает и метку ожидания.
	if err := o.tasks.Move("OFF-3", "Backlog"); err != nil {
		t.Fatalf("родитель не уведён: %v", err)
	}
	runs := o.agent.runs
	o.tickAll(t)
	if o.agent.runs != runs {
		t.Errorf("прогонов стало %d: задача из Backlog снова взята в работу", o.agent.runs)
	}
	if got := o.get(t, "OFF-3"); got.HumanFlag {
		t.Error("метка ожидания пережила перенос")
	}
}
