package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/pipeline"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/mock"
	"github.com/kao73/virtual-office/internal/workspace"
)

// twoOffices — два офиса над файловыми трекерами во временных каталогах,
// по проекту на каждый; имена «jira» и «mock» — метки обхода, под обоими
// лежит mock. Граф — поставляемый, корень конфигурации — репозиторий:
// cycle зовёт настоящие Reap/Tick/CompleteSplits, а им нужны роли и статусы.
//
// Хозяйство рабочих папок одно на оба офиса, как на машине: уборка
// PR-прохода перечисляет папки всей машины, и только общее хозяйство
// покажет, не снесёт ли один офис папку другого.
func twoOffices(t *testing.T, out *bytes.Buffer) (*offices, *mock.Tracker, *mock.Tracker) {
	t.Helper()
	root := filepath.Join("..", "..")
	wf, err := tracker.LoadWorkflow(filepath.Join(root, tracker.WorkflowFile))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}
	ws := workspace.New(t.TempDir())
	office := func(name, project string) (namedOffice, *mock.Tracker) {
		tr := mock.New(t.TempDir())
		return namedOffice{name: name, Office: &pipeline.Office{
			Tracker:    tr,
			Trackers:   map[string]tracker.Tracker{},
			Workspaces: ws,
			Workflow:   wf,
			Projects: tracker.Projects{project: {
				Tracker: name, DefaultBranch: "master", BranchPrefix: "agent/",
			}},
			ConfigRoot: root,
			Log:        out,
		}}, tr
	}
	jira, a := office("jira", "VO")
	local, b := office("mock", "OFF")
	return &offices{list: []namedOffice{jira, local}, workspaces: ws, out: out}, a, b
}

// expiredTask заводит задачу с истёкшей арендой: единственное, что Reap
// делает наблюдаемо без агента, — возвращает такую задачу в очередь.
func expiredTask(t *testing.T, tr *mock.Tracker, key, project string) {
	t.Helper()
	if err := tr.Add(tracker.Task{Key: key, Project: project, Status: "Ready", Summary: "задача"}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	if err := tr.Claim(tracker.ClaimRequest{
		Key: key, RunID: "прогон-" + key, Owner: "implementer",
		LeaseUntil: time.Now().Add(-time.Hour), ExpectStatus: "Ready", WorkingStatus: "InProgress",
	}); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}
}

func status(t *testing.T, tr *mock.Tracker, key string) string {
	t.Helper()
	task, err := tr.Get(key)
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	return task.Status
}

// Заголовок «== трекер X ==» печатается только когда офисов больше одного:
// при одном вывод совпадает с прежним байт в байт.
func TestEachPrintsHeadersOnlyForSeveralOffices(t *testing.T) {
	var out bytes.Buffer
	all, _, _ := twoOffices(t, &out)
	note := func(no namedOffice) error {
		out.WriteString("visited " + no.name + "\n")
		return nil
	}

	if err := all.each(context.Background(), note); err != nil {
		t.Fatalf("обход не прошёл: %v", err)
	}
	want := "== трекер jira ==\nvisited jira\n== трекер mock ==\nvisited mock\n"
	if out.String() != want {
		t.Errorf("вывод обхода двух офисов:\n%q\nожидалось:\n%q", out.String(), want)
	}

	out.Reset()
	all.list = all.list[:1]
	if err := all.each(context.Background(), note); err != nil {
		t.Fatalf("обход не прошёл: %v", err)
	}
	if strings.Contains(out.String(), "== трекер") {
		t.Errorf("при одном офисе напечатан заголовок:\n%s", out.String())
	}
}

// Ошибка одного офиса не останавливает остальных: все собираются разом,
// и у каждой в префиксе имя офиса — иначе не понять, чей отказ.
func TestEachVisitsEveryOfficeAndJoinsErrors(t *testing.T) {
	var out bytes.Buffer
	all, _, _ := twoOffices(t, &out)
	var visited []string
	err := all.each(context.Background(), func(no namedOffice) error {
		visited = append(visited, no.name)
		if no.name == "jira" {
			return errors.New("поиск задач не удался")
		}
		return nil
	})

	if err == nil {
		t.Fatal("ошибка офиса потеряна")
	}
	if !strings.Contains(err.Error(), "jira: поиск задач не удался") {
		t.Errorf("ошибка не подписана именем офиса: %v", err)
	}
	if strings.Join(visited, ",") != "jira,mock" {
		t.Errorf("обойдены %v, ожидались оба офиса по порядку", visited)
	}
}

// Контекст проверяется между офисами: сигнал, пришедший во время прогона
// в первом, останавливает обход перед вторым, не дожидаясь его.
func TestEachStopsBetweenOfficesOnCancel(t *testing.T) {
	var out bytes.Buffer
	all, _, _ := twoOffices(t, &out)
	ctx, cancel := context.WithCancel(context.Background())
	var visited []string
	err := all.each(ctx, func(no namedOffice) error {
		visited = append(visited, no.name)
		cancel()
		return nil
	})

	if err != nil {
		t.Fatalf("обход вернул ошибку: %v", err)
	}
	if strings.Join(visited, ",") != "jira" {
		t.Errorf("обойдены %v, ожидался только первый офис", visited)
	}
}

// У задачи есть проект, у проекта — трекер, у трекера — офис.
func TestByProjectFindsTheOwningOffice(t *testing.T) {
	var out bytes.Buffer
	all, _, _ := twoOffices(t, &out)

	no, err := all.byProject("VO")
	if err != nil || no.name != "jira" {
		t.Errorf("byProject(VO) = %q, %v; ожидался офис jira", no.name, err)
	}
	no, err = all.byProject("OFF")
	if err != nil || no.name != "mock" {
		t.Errorf("byProject(OFF) = %q, %v; ожидался офис mock", no.name, err)
	}
	if _, err := all.byProject("NOPE"); err == nil || !strings.Contains(err.Error(), tracker.ProjectsLocalFile) {
		t.Errorf("неизвестный проект не отвергнут с именем файла: %v", err)
	}
}

// Один заход цикла делает reap в каждом офисе: зависшая задача возвращается
// в очередь и там, и там. Роль для tick — reviewer: в её очереди пусто,
// и агент не нужен.
func TestCycleReapsEveryOffice(t *testing.T) {
	var out bytes.Buffer
	all, a, b := twoOffices(t, &out)
	expiredTask(t, a, "VO-1", "VO")
	expiredTask(t, b, "OFF-1", "OFF")

	all.cycle(context.Background(), "reviewer")

	if got := status(t, a, "VO-1"); got != "Ready" {
		t.Errorf("VO-1 в %q, ожидался Ready: reap не дошёл до офиса jira", got)
	}
	if got := status(t, b, "OFF-1"); got != "Ready" {
		t.Errorf("OFF-1 в %q, ожидался Ready: reap не дошёл до офиса mock", got)
	}
}

// cancelOnExpired отменяет контекст первым же ListExpired: так выглядит
// сигнал, пришедший во время прогона первого офиса.
type cancelOnExpired struct {
	tracker.Tracker
	cancel context.CancelFunc
}

func (c cancelOnExpired) ListExpired(project string, now time.Time) ([]tracker.TaskRef, error) {
	c.cancel()
	return c.Tracker.ListExpired(project, now)
}

// Сигнал в первом офисе: его шаг дорабатывает (reap не смотрит на ctx),
// второй офис не начинается — стоп между офисами.
func TestCycleStopsBeforeNextOfficeOnCancel(t *testing.T) {
	var out bytes.Buffer
	all, a, b := twoOffices(t, &out)
	expiredTask(t, a, "VO-1", "VO")
	expiredTask(t, b, "OFF-1", "OFF")
	ctx, cancel := context.WithCancel(context.Background())
	all.list[0].Tracker = cancelOnExpired{Tracker: a, cancel: cancel}

	all.cycle(ctx, "reviewer")

	if got := status(t, a, "VO-1"); got != "Ready" {
		t.Errorf("VO-1 в %q, ожидался Ready: первый офис обязан дорабатывать заход", got)
	}
	if got := status(t, b, "OFF-1"); got != "InProgress" {
		t.Errorf("OFF-1 в %q, ожидался InProgress: второй офис не должен был начаться", got)
	}
}

// stepRecorder оборачивает трекер одного офиса и пишет в общий срез метку первого
// же вызова, который опознаёт шаг cycle: ListExpired бывает только у Reap,
// ListReady — только внутри Tick (HumanReplies, claim, PRPass), List — у
// CompleteSplits (и лениво у claim(), когда у кандидата есть depends_on — но
// в задаче этого теста зависимостей нет, а очередь reviewer'а пуста, так что
// в этом прогоне List зовёт только CompleteSplits).
type stepRecorder struct {
	tracker.Tracker
	calls *[]string
}

func (s stepRecorder) ListExpired(project string, now time.Time) ([]tracker.TaskRef, error) {
	*s.calls = append(*s.calls, "reap")
	return s.Tracker.ListExpired(project, now)
}

func (s stepRecorder) ListReady(project, status string) ([]tracker.TaskRef, error) {
	*s.calls = append(*s.calls, "tick")
	return s.Tracker.ListReady(project, status)
}

func (s stepRecorder) List(project string, statuses []string) ([]tracker.TaskRef, error) {
	*s.calls = append(*s.calls, "complete-splits")
	return s.Tracker.List(project, statuses)
}

// Порядок шагов внутри одного захода — reap → tick → complete-splits — нагружен
// смыслом (см. доккомент cycle): tick первым разбирает ответы человека, и реплика,
// пришедшая между заходами, обязана увести задачу из Blocked раньше, чем до неё
// доберётся CompleteSplits. Раньше этот порядок пинил в internal/pipeline
// TestLoopProcessesHumanReplyBeforeCompletingSplits, гоняя pipeline.Office.Loop
// целиком; коммит 3116974 этой ветки убрал Loop и tickOnce вместе с тем тестом —
// теперь драйвер живёт в cmd/runner (offices.go, cycle), и без этого теста тихая
// перестановка — например, CompleteSplits перед tick — не уронила бы ни одного
// теста, хотя вернула бы ту же гонку с ответом человека.
func TestCycleOrderIsReapThenTickThenCompleteSplits(t *testing.T) {
	var out bytes.Buffer
	all, a, _ := twoOffices(t, &out)
	expiredTask(t, a, "VO-1", "VO")
	var calls []string
	all.list[0].Tracker = stepRecorder{Tracker: a, calls: &calls}

	all.cycle(context.Background(), "reviewer")

	first := func(step string) int {
		for i, c := range calls {
			if c == step {
				return i
			}
		}
		t.Fatalf("шаг %q не наблюдался среди вызовов: %v", step, calls)
		return -1
	}
	reap, tick, splits := first("reap"), first("tick"), first("complete-splits")
	if !(reap < tick && tick < splits) {
		t.Errorf("порядок шагов %v, ожидался reap → tick → complete-splits", calls)
	}
}

// loop с уже отменённым контекстом не ждёт таймера и говорит, почему встал.
func TestLoopStopsOnSignalWithoutWaiting(t *testing.T) {
	var out bytes.Buffer
	all, _, _ := twoOffices(t, &out)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := all.loop(ctx, time.Hour, "reviewer"); err != nil {
		t.Fatalf("цикл вернул ошибку: %v", err)
	}
	if !strings.Contains(out.String(), "остановка по сигналу") {
		t.Errorf("о причине остановки не сказано:\n%s", out.String())
	}
}

// loop — это cycle по расписанию, и тест обязан увидеть сам cycle: заход
// с уже отменённым контекстом (тест выше) не посещает ни одного офиса,
// и loop, забывший позвать cycle, прошёл бы его. Здесь контекст отменяется
// изнутри первого же reap — как сигнал, пришедший во время захода: задача
// возвращена в очередь (cycle дошёл), а loop вернулся, не дожидаясь таймера
// в час.
func TestLoopDrivesCycleAndStopsOnSignal(t *testing.T) {
	var out bytes.Buffer
	all, a, _ := twoOffices(t, &out)
	expiredTask(t, a, "VO-1", "VO")
	// Срок — страховка, а не механизм: loop, не дошедший до cycle, никогда
	// не отменил бы контекст сам и ждал бы таймер в час; со сроком он вернётся
	// и провалит проверку статуса, а не повесит пакет тестов.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	all.list[0].Tracker = cancelOnExpired{Tracker: a, cancel: cancel}

	if err := all.loop(ctx, time.Hour, "reviewer"); err != nil {
		t.Fatalf("цикл вернул ошибку: %v", err)
	}
	if got := status(t, a, "VO-1"); got != "Ready" {
		t.Errorf("VO-1 в %q, ожидался Ready: loop не довёл заход до cycle", got)
	}
	if !strings.Contains(out.String(), "остановка по сигналу") {
		t.Errorf("о причине остановки не сказано:\n%s", out.String())
	}
}

// failingExpired — трекер, у которого reap падает: так выглядит офис,
// чей трекер отвечает ошибкой посреди захода.
type failingExpired struct{ tracker.Tracker }

func (failingExpired) ListExpired(string, time.Time) ([]tracker.TaskRef, error) {
	return nil, errors.New("трекер недоступен")
}

// Ошибка шага одного офиса — строка в логе с именем шага, а не смерть
// захода: второй офис своё получает. «Продолжить» без «сказать» было бы
// тихим отказом: планировщик крутил бы заходы, а в логе — пусто.
func TestCycleLogsStepErrorsAndContinues(t *testing.T) {
	var out bytes.Buffer
	all, a, b := twoOffices(t, &out)
	expiredTask(t, b, "OFF-1", "OFF")
	all.list[0].Tracker = failingExpired{Tracker: a}

	all.cycle(context.Background(), "reviewer")

	// Заход loop не печатает заголовков офисов — при --every 2m это была бы
	// тысяча строк в сутки в журнале планировщика; чей шаг упал, говорит
	// префикс самой строки.
	if !strings.Contains(out.String(), "jira: reap: ") || !strings.Contains(out.String(), "трекер недоступен") {
		t.Errorf("об ошибке reap не сказано с именем офиса:\n%s", out.String())
	}
	if strings.Contains(out.String(), "== трекер") {
		t.Errorf("заход цикла печатает заголовки офисов:\n%s", out.String())
	}
	if got := status(t, b, "OFF-1"); got != "Ready" {
		t.Errorf("OFF-1 в %q, ожидался Ready: второй офис не дождался своего reap", got)
	}
}

// getRecorder запоминает, о каких задачах офис спрашивал трекер.
type getRecorder struct {
	tracker.Tracker
	asked *[]string
}

func (g getRecorder) Get(key string) (tracker.Task, error) {
	*g.asked = append(*g.asked, key)
	return g.Tracker.Get(key)
}

// Рабочие папки — хозяйство машины, и уборка каждого офиса видит их все.
// Закончённую задачу jira убирает офис jira тем же заходом; офис mock,
// стоящий первым, о чужой задаче даже не спрашивает свой трекер — иначе
// ответ «нет такой задачи» из чужого трекера решал бы судьбу чужой папки.
func TestCycleSweepsFolderOnlyInOwningOffice(t *testing.T) {
	var out bytes.Buffer
	all, a, b := twoOffices(t, &out)
	all.list[0], all.list[1] = all.list[1], all.list[0] // mock первым, jira вторым
	project := tracker.Project{RepoURL: bareOrigin(t), DefaultBranch: "master", BranchPrefix: "agent/", Tracker: "jira"}
	ws, err := all.workspaces.Ensure(tracker.TaskRef{Key: "VO-1", Project: "VO"}, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	// Ensure держит барьер до конца прогона; прогон кончился — папка свободна,
	// иначе уборка молча обошла бы её как занятую.
	if err := ws.Unlock(); err != nil {
		t.Fatalf("барьер не снят: %v", err)
	}
	if err := a.Add(tracker.Task{Key: "VO-1", Project: "VO", Status: "Done", Summary: "закрыта"}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	var asked []string
	all.list[0].Tracker = getRecorder{Tracker: b, asked: &asked}

	all.cycle(context.Background(), "reviewer")

	if entries, _ := all.workspaces.List(); len(entries) != 0 {
		t.Errorf("папка закрытой задачи осталась: %+v\n%s", entries, out.String())
	}
	if slices.Contains(asked, "VO-1") {
		t.Errorf("офис mock спрашивал свой трекер о чужой задаче VO-1: %v", asked)
	}
}
