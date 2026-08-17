package pipeline

import (
	"context"
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

	"github.com/kao73/virtual-office/runner"
	"github.com/kao73/virtual-office/tracker"
	"github.com/kao73/virtual-office/tracker/mock"
	"github.com/kao73/virtual-office/workspace"
)

var now = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// fakeAgent — поддельный прогон: возвращает заданный результат и, если велено,
// оставляет коммит в рабочей папке. Настоящий агент подключается тем же
// интерфейсом, поэтому конвейер проверяется целиком, без трат на модель.
type fakeAgent struct {
	result runner.Result
	err    error
	commit string // сообщение коммита; пусто — агент ничего не сделал

	seen Request // что конвейер отдал агенту
	runs int
}

func (f *fakeAgent) Run(_ context.Context, req Request) (runner.Result, error) {
	f.seen, f.runs = req, f.runs+1
	if f.commit != "" {
		gitIn(req.Workdir, "commit", "-q", "--allow-empty", "-m", f.commit)
	}
	return f.result, f.err
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

	o := &office{
		tasks:  tasks,
		agent:  agent,
		origin: origin,
		Office: &Office{
			Tracker:    tasks,
			Workspaces: workspace.New(t.TempDir()),
			Workflow:   wf,
			Projects: tracker.Projects{"OFF": {
				RepoURL: origin, DefaultBranch: "master", BranchPrefix: "agent/",
			}},
			Agent:      agent,
			ConfigRoot: root,
			ConfigSHA:  "5bc6a3b0",
			Now:        func() time.Time { return now },
			Log:        io.Discard,
		},
	}
	o.add("OFF-1", "Ready")
	return o
}

func (o *office) add(key, status string) {
	if err := o.tasks.Add(tracker.Task{
		Key: key, Project: "OFF", Status: status,
		Summary: "Задача " + key, Description: "Сделать что-нибудь полезное.",
	}); err != nil {
		panic(err)
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
	root, err := filepath.Abs("..")
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

// Вопрос человеку: задача уходит в Blocked с атрибутом ожидания, вопросы видны
// в комментарии, работа всё равно опубликована.
func TestTickNeedsHumanBlocksAndFlags(t *testing.T) {
	o := newOffice(t)
	o.agent.commit = "половина работы"
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeNeedsHuman, Summary: "Нужен выбор.", NextOwner: "human",
		Questions: []runner.Question{{Text: "Какую платёжную систему?", Options: []string{"Stripe", "ЮKassa"}}},
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
		Questions: []runner.Question{{Text: "Какую платёжную систему?"}},
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

// Разобранный ответ не разбирается повторно: иначе задача возвращалась бы
// в очередь на каждом цикле.
func TestHumanReplyIsProcessedOnce(t *testing.T) {
	o := newOffice(t)
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeNeedsHuman, Summary: "Нужен выбор.", NextOwner: "human",
		Questions: []runner.Question{{Text: "Какую платёжную систему?"}},
	}
	o.tick(t)
	if err := o.tasks.AddComment("OFF-1", "human", "Берём Stripe."); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}

	// Первый разбор возвращает задачу в очередь, второй не должен найти работы.
	replies, err := o.HumanReplies(context.Background(), "implementer")
	if err != nil {
		t.Fatalf("разбор ответов не прошёл: %v", err)
	}
	if replies != 1 {
		t.Fatalf("разобрано ответов %d, ожидался 1", replies)
	}
	if replies, err = o.HumanReplies(context.Background(), "implementer"); err != nil || replies != 0 {
		t.Errorf("повторный разбор дал %d, %v", replies, err)
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
	o.Office.Tracker = &flakyClaim{Tracker: o.tasks, lost: map[string]bool{"OFF-1": true}}

	if !o.tick(t) {
		t.Fatal("цикл не взял ни одной задачи")
	}
	if o.agent.seen.Passport.TaskKey != "OFF-2" {
		t.Errorf("в работу ушла %q, ожидалась OFF-2", o.agent.seen.Passport.TaskKey)
	}
	if task := o.get(t, "OFF-1"); task.Status != "Ready" {
		t.Errorf("проигранная задача сдвинулась: %+v", task)
	}
}

// Прогон, доживший до конца после reap, не пишет в трекер ничего, кроме
// предупреждения: задачу уже могли отдать другому.
func TestTickWithLostLeaseOnlyWarns(t *testing.T) {
	o := newOffice(t)
	o.agent.commit = "работа агента"
	// Пока агент работает, аренда истекает и reaper возвращает задачу.
	o.agent.result = done("успел доделать")
	o.Office.Agent = &fakeAgent{result: done("успел доделать"), commit: "работа агента"}
	stealAgent := o.Office.Agent.(*fakeAgent)
	o.Office.Agent = agentFunc(func(ctx context.Context, req Request) (runner.Result, error) {
		result, err := stealAgent.Run(ctx, req)
		later := now.Add(2 * time.Hour) // аренда истекла, пока агент работал
		o.tasks.Now = func() time.Time { return later }
		o.Office.Now = func() time.Time { return later }
		if err := o.Reap(context.Background()); err != nil {
			t.Fatalf("reap не прошёл: %v", err)
		}
		return result, err
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
}

type agentFunc func(context.Context, Request) (runner.Result, error)

func (f agentFunc) Run(ctx context.Context, req Request) (runner.Result, error) { return f(ctx, req) }

// Reaper возвращает зависшую задачу в очередь, объясняя это в тикете.
func TestReapReturnsExpiredTask(t *testing.T) {
	o := newOffice(t)
	o.Office.Agent = agentFunc(func(context.Context, Request) (runner.Result, error) {
		return runner.Result{}, errors.New("раннера убили посреди прогона")
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
	o.Office.Agent = agentFunc(func(context.Context, Request) (runner.Result, error) {
		return runner.Result{}, errors.New("раннера убили посреди прогона")
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
	o.Office.Agent = agentFunc(func(context.Context, Request) (runner.Result, error) {
		return runner.Result{}, errors.New("раннера убили посреди прогона")
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
	o.Office.Agent = agentFunc(func(context.Context, Request) (runner.Result, error) {
		return runner.Result{}, errors.New("раннера убили посреди прогона")
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
	o.Office.Agent = agentFunc(func(context.Context, Request) (runner.Result, error) {
		return runner.Result{}, errors.New("прогон идёт")
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
	o.Office.Agent = agentFunc(func(context.Context, Request) (runner.Result, error) {
		return runner.Result{}, errors.New("раннера убили посреди прогона")
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
		o.Office.Agent = agentFunc(func(context.Context, Request) (runner.Result, error) {
			return runner.Result{}, errors.New("раннера убили")
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
	o.Office.Agent = agentFunc(func(context.Context, Request) (runner.Result, error) {
		return runner.Result{}, errors.New("раннера убили посреди прогона")
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

// Барьер рабочей папки — второй после трекера, и он не зависит от workflow:
// в папке OFF-1 уже работает другой прогон. tick обязан отступить, не тронув
// аренду: сняв её, он вернул бы задачу в очередь, где следующий заход упёрся бы
// в тот же замок.
func TestTickStepsAsideWhenWorktreeIsBusy(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.Office.Log = &log

	busy, err := o.Office.Workspaces.Ensure(
		tracker.TaskRef{Key: "OFF-1", Project: "OFF"}, o.Office.Projects["OFF"])
	if err != nil {
		t.Fatalf("папка не занята: %v", err)
	}
	defer busy.Unlock()

	if !o.tick(t) {
		t.Fatal("цикл не взял задачу")
	}
	if o.agent.runs != 0 {
		t.Errorf("агент запущен в чужой рабочей папке: прогонов %d", o.agent.runs)
	}

	task := o.get(t, "OFF-1")
	if task.Status != "InProgress" {
		t.Errorf("статус %q: задача не оставлена как есть", task.Status)
	}
	if !task.LeaseAlive(now) {
		t.Errorf("аренда снята — задача вернётся в очередь и упрётся в тот же замок: %+v", task)
	}
	if !strings.Contains(log.String(), "занята") {
		t.Errorf("отступление не объяснено в логе:\n%s", log.String())
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
func TestCheckWorkflowWarnsAboutTwoOwners(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.Office.Log = &log
	o.Office.Tracker = knownWorkflow{
		Tracker: o.tasks,
		check:   tracker.WorkflowCheck{Sample: "OFF-1", SelfEntry: true},
	}

	o.Office.CheckWorkflow()

	if !strings.Contains(log.String(), "двух владельцев") {
		t.Errorf("раннер не сказал, чем грозит такой workflow:\n%s", log.String())
	}
	if !strings.Contains(log.String(), "tracker-protocol.md") {
		t.Errorf("предупреждение не говорит, где читать:\n%s", log.String())
	}
}

// Проверить workflow не на чем, пока в рабочем статусе нет ни одной задачи:
// переходы JIRA показывает только у конкретной задачи. Молчание в этом месте
// читалось бы как «проверил, всё в порядке».
func TestCheckWorkflowSaysWhenItCouldNotRun(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.Office.Log = &log
	o.Office.Tracker = knownWorkflow{Tracker: o.tasks}

	o.Office.CheckWorkflow()

	if !strings.Contains(log.String(), "проверка workflow не выполнена") {
		t.Errorf("невыполненная проверка выдана за успешную:\n%s", log.String())
	}
}

// У файлового трекера workflow нет вовсе, и жаловаться не на что: проверка
// молча пропускается, а не превращается в шум на каждом запуске.
func TestCheckWorkflowSilentForTrackerWithoutWorkflow(t *testing.T) {
	var log strings.Builder
	o := newOffice(t)
	o.Office.Log = &log

	o.Office.CheckWorkflow()

	if log.String() != "" {
		t.Errorf("трекер без workflow вызвал жалобу:\n%s", log.String())
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
	o.Office.Tracker = unknownProject{Tracker: o.tasks, missing: "AAA"}

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
	o.Office.Agent = agentFunc(func(context.Context, Request) (runner.Result, error) {
		return runner.Result{}, errors.New("раннера убили посреди прогона")
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
	o.Office.Tracker = unknownProject{Tracker: o.tasks, missing: "AAA"}

	if err := o.Reap(context.Background()); err != nil {
		t.Fatalf("reap бросил работу из-за незнакомого проекта: %v", err)
	}
	if task := o.get(t, "OFF-1"); task.Status != "Ready" {
		t.Errorf("зависшая задача годного проекта не возвращена: %s", task.Status)
	}
}
