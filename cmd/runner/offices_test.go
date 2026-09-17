package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
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
func twoOffices(t *testing.T, out *bytes.Buffer) (*offices, *mock.Tracker, *mock.Tracker) {
	t.Helper()
	root := filepath.Join("..", "..")
	wf, err := tracker.LoadWorkflow(filepath.Join(root, tracker.WorkflowFile))
	if err != nil {
		t.Fatalf("граф не загружен: %v", err)
	}
	office := func(name, project string) (namedOffice, *mock.Tracker) {
		tr := mock.New(t.TempDir())
		return namedOffice{name: name, Office: &pipeline.Office{
			Tracker:  tr,
			Trackers: map[string]tracker.Tracker{},
			// Tick гонит PR-проход, а тот уборкой перечисляет рабочие папки
			// машины: хозяйство нужно хотя бы пустое — на nil упало бы раньше,
			// чем дело дошло до очереди.
			Workspaces: workspace.New(t.TempDir()),
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
	return &offices{list: []namedOffice{jira, local}, out: out}, a, b
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
	visit := func(no namedOffice) error {
		out.WriteString("visited " + no.name + "\n")
		return nil
	}

	if err := all.each(context.Background(), visit); err != nil {
		t.Fatalf("обход не прошёл: %v", err)
	}
	want := "== трекер jira ==\nvisited jira\n== трекер mock ==\nvisited mock\n"
	if out.String() != want {
		t.Errorf("вывод обхода двух офисов:\n%q\nожидалось:\n%q", out.String(), want)
	}

	out.Reset()
	all.list = all.list[:1]
	if err := all.each(context.Background(), visit); err != nil {
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

// Сигнал во время прогона первого офиса: его заход дорабатывает, второй
// офис не начинается — стоп между прогонами, а не посреди.
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
