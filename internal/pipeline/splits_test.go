package pipeline

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/mock"
)

// tickAs прогоняет цикл названной роли и падает на инфраструктурной ошибке —
// как tick() в pipeline_test.go, но с явной ролью: split-предложения
// и их подтверждение приходят только от analyst, а не от implementer,
// которого использует общий tick().
func (o *office) tickAs(t *testing.T, role string) bool {
	t.Helper()
	worked, err := o.Tick(context.Background(), role)
	if err != nil {
		t.Fatalf("цикл %s не прошёл: %v", role, err)
	}
	return worked
}

// splitResult — стандартное предложение из двух детей с зависимостью:
// общий фикстур для тестов CompleteSplits.
func splitResult() runner.Result {
	return runner.Result{
		Outcome: runner.OutcomeSplit, Summary: "Постановка описывает две сущности.", NextOwner: "human",
		Questions: []runner.Question{{ID: "Q1", Text: "Разбить на 2, как предложено?"}},
		Split: &runner.Split{Children: []runner.SplitChild{
			{ID: "category-crud", Title: "Category CRUD", Description: "Модель, миграция, CRUD категорий."},
			{ID: "transaction-crud", Title: "Transaction CRUD", Description: "Модель, миграция, CRUD операций.", DependsOn: []string{"category-crud"}},
		}},
	}
}

// confirmSplit проводит OFF-1 через оба круга: предложение и подтверждение —
// тем же путём, что и реальный аналитик, до состояния «два split-маркера
// подряд, задача в Blocked».
func confirmSplit(t *testing.T, o *office) {
	t.Helper()
	if err := o.tasks.Move("OFF-1", "Analysis"); err != nil {
		t.Fatalf("подготовка не удалась: %v", err)
	}
	o.agent.result = splitResult()
	if !o.tickAs(t, "analyst") {
		t.Fatal("первое предложение не взято в работу")
	}
	if err := o.tasks.AddComment("OFF-1", "human", "Да, разбивай."); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}
	if !o.tickAs(t, "analyst") {
		t.Fatal("подтверждение не взято в работу")
	}
}

func TestCompleteSplitsCreatesAndLinksChildren(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не прошёл: %v", err)
	}

	parent := o.get(t, "OFF-1")
	if parent.Status != o.Workflow.PR.Merged {
		t.Errorf("статус родителя %q, ожидался %q", parent.Status, o.Workflow.PR.Merged)
	}
	// finish() поднял HumanFlag, отправляя подтверждённый split в Blocked
	// (workflow.yaml: split → {to: Blocked, human: true}). closeSplitParent
	// обязан его снять — иначе закрытый тикет остаётся с меткой «ждёт
	// человека» на живой доске, хотя ждать уже нечего.
	if parent.HumanFlag {
		t.Error("HumanFlag не снят при закрытии родителя")
	}

	category, err := o.tasks.Get("OFF-2")
	if err != nil {
		t.Fatalf("категория не создана: %v", err)
	}
	if category.Summary != "Category CRUD" || category.Status != "Analysis" {
		t.Errorf("категория заведена неверно: %+v", category)
	}
	if !slices.Contains(category.Labels, "split-child:OFF-1:category-crud") {
		t.Errorf("на категории нет метки: %v", category.Labels)
	}

	transaction, err := o.tasks.Get("OFF-3")
	if err != nil {
		t.Fatalf("операции не заведены: %v", err)
	}
	if !slices.Contains(transaction.DependsOn, "OFF-2") {
		t.Errorf("зависимость не записана: %v", transaction.DependsOn)
	}

	comment := lastComment(t, parent)
	if !strings.Contains(comment.Body, "OFF-2") || !strings.Contains(comment.Body, "OFF-3") {
		t.Errorf("в отчёте о закрытии нет ключей детей:\n%s", comment.Body)
	}
}

func TestCompleteSplitsSkipsUnconfirmed(t *testing.T) {
	o := newOffice(t)
	if err := o.tasks.Move("OFF-1", "Analysis"); err != nil {
		t.Fatalf("подготовка не удалась: %v", err)
	}
	o.agent.result = splitResult()
	if !o.tickAs(t, "analyst") {
		t.Fatal("первое предложение не взято в работу")
	}

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не прошёл: %v", err)
	}

	if _, err := o.tasks.Get("OFF-2"); err == nil {
		t.Error("дети не должны создаваться на первом предложении")
	}
	parent := o.get(t, "OFF-1")
	if parent.Status != "Blocked" {
		t.Errorf("статус родителя %q, ожидался Blocked", parent.Status)
	}
}

// TestCompleteSplitsResumesInterruptedBatch воспроизводит пачку, прерванную
// на середине: прошлый проход успел создать только первого ребёнка. Новый
// проход должен досоздать только недостающего, а не задвоить первого —
// идемпотентность заложена в ensureChildren задачи 11 (FindByMarker перед
// каждым CreateTask), этот тест доказывает это конкретным сценарием.
func TestCompleteSplitsResumesInterruptedBatch(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	// Прошлая попытка успела создать только первого ребёнка — так
	// выглядит пачка, прерванная на середине.
	if _, err := o.tasks.CreateTask("OFF", tracker.TaskInput{
		Summary: "Category CRUD", Description: "Модель, миграция, CRUD категорий.",
		Labels: []string{"split-child:OFF-1:category-crud"},
	}); err != nil {
		t.Fatalf("подготовка не удалась: %v", err)
	}

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не прошёл: %v", err)
	}

	category, err := o.tasks.FindByMarker("OFF", "split-child:OFF-1:category-crud")
	if err != nil {
		t.Fatalf("поиск не удался: %v", err)
	}
	if len(category) != 1 {
		t.Errorf("категория задвоена: %+v", category)
	}

	transaction, err := o.tasks.FindByMarker("OFF", "split-child:OFF-1:transaction-crud")
	if err != nil {
		t.Fatalf("поиск не удался: %v", err)
	}
	if len(transaction) != 1 {
		t.Errorf("операции не досозданы или задвоены: %+v", transaction)
	}

	linked, err := o.tasks.Get(transaction[0].Key)
	if err != nil {
		t.Fatalf("операции не прочитаны: %v", err)
	}
	if !slices.Contains(linked.DependsOn, category[0].Key) {
		t.Errorf("зависимость не связана после докатки: %v", linked.DependsOn)
	}

	parent := o.get(t, "OFF-1")
	if parent.Status != o.Workflow.PR.Merged {
		t.Errorf("статус родителя %q, ожидался %q", parent.Status, o.Workflow.PR.Merged)
	}
}

// duplicateFind добавляет к настоящему результату FindByMarker ещё одну,
// заведомо постороннюю задачу по той же метке: имитирует коллизию — то,
// что по одной метке нашлось больше одной задачи, — которую ensureChildren
// не должен проглатывать молча.
type duplicateFind struct {
	*mock.Tracker
	marker string
	extra  tracker.TaskRef
}

func (f *duplicateFind) FindByMarker(project, marker string) ([]tracker.TaskRef, error) {
	found, err := f.Tracker.FindByMarker(project, marker)
	if err != nil || marker != f.marker {
		return found, err
	}
	return append(found, f.extra), nil
}

// TestEnsureChildrenLogsWhenMarkerMatchesMultiple доказывает, что коллизия
// по метке — по одной метке нашлось больше одной задачи — попадает в лог,
// а не проглатывается молча. Поведение при этом не меняется: берётся
// по-прежнему первый найденный.
func TestEnsureChildrenLogsWhenMarkerMatchesMultiple(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	marker := splitChildMarker("OFF-1", "category-crud")
	if _, err := o.tasks.CreateTask("OFF", tracker.TaskInput{
		Summary: "Category CRUD", Description: "Модель, миграция, CRUD категорий.",
		Labels: []string{marker},
	}); err != nil {
		t.Fatalf("подготовка не удалась: %v", err)
	}

	var log strings.Builder
	o.Office.Log = &log
	o.useTracker(&duplicateFind{Tracker: o.tasks, marker: marker, extra: tracker.TaskRef{Key: "OFF-99", Project: "OFF"}})

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не должен падать: %v", err)
	}

	if !strings.Contains(log.String(), marker) {
		t.Errorf("коллизия по метке не залогирована:\n%s", log.String())
	}

	// Поведение не изменилось: связь ушла на настоящего первого найденного
	// (созданную выше "Category CRUD"), а не на постороннего OFF-99.
	transaction, err := o.tasks.FindByMarker("OFF", splitChildMarker("OFF-1", "transaction-crud"))
	if err != nil {
		t.Fatalf("поиск не удался: %v", err)
	}
	if len(transaction) != 1 {
		t.Fatalf("операции не найдены или задвоены: %+v", transaction)
	}
	linked, err := o.tasks.Get(transaction[0].Key)
	if err != nil {
		t.Fatalf("операции не прочитаны: %v", err)
	}
	if slices.Contains(linked.DependsOn, "OFF-99") || !slices.Contains(linked.DependsOn, "OFF-2") {
		t.Errorf("зависимость ушла не на того: %v, ожидался OFF-2, не OFF-99", linked.DependsOn)
	}
}

// flakyCreate роняет CreateTask для ребёнка с данным заголовком: так
// выглядит частичный сбой пакетного создания.
type flakyCreate struct {
	*mock.Tracker
	failOn string
}

func (f *flakyCreate) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error) {
	if input.Summary == f.failOn {
		return tracker.TaskRef{}, errors.New("сеть недоступна")
	}
	return f.Tracker.CreateTask(project, input)
}

func TestCompleteSplitsRecordsFailureNoticeAndContinues(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	// Второй, независимый подтверждённый тикет — доказывает, что беда
	// на OFF-1 не роняет весь проход CompleteSplits.
	o.add("OFF-9", "Analysis")
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeSplit, Summary: "Другая постановка.", NextOwner: "human",
		Questions: []runner.Question{{ID: "Q1", Text: "Разбить на 2, как предложено?"}},
		Split: &runner.Split{Children: []runner.SplitChild{
			{ID: "one", Title: "One", Description: "Первая половина."},
			{ID: "two", Title: "Two", Description: "Вторая половина."},
		}},
	}
	worked, err := o.Tick(context.Background(), "analyst")
	if err != nil || !worked {
		t.Fatalf("предложение OFF-9 не взято в работу: worked=%v err=%v", worked, err)
	}
	if err := o.tasks.AddComment("OFF-9", "human", "Да, разбивай."); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}
	if worked, err := o.Tick(context.Background(), "analyst"); err != nil || !worked {
		t.Fatalf("подтверждение OFF-9 не взято в работу: worked=%v err=%v", worked, err)
	}

	o.useTracker(&flakyCreate{Tracker: o.tasks, failOn: "Category CRUD"})

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("проход не должен падать целиком: %v", err)
	}

	parent := o.get(t, "OFF-1")
	if parent.Status != "Blocked" {
		t.Errorf("статус родителя %q, ожидался Blocked — сбой не должен двигать задачу", parent.Status)
	}
	comment := lastComment(t, parent)
	marker, ok := tracker.MarkerOf(comment.Body)
	if !ok || marker.Event != tracker.EventSplitCreateFailed {
		t.Errorf("нет записи о сбое:\n%s", comment.Body)
	}

	other := o.get(t, "OFF-9")
	if other.Status != o.Workflow.PR.Merged {
		t.Errorf("вторая задача %q, ожидался %q — беда первой не должна её касаться",
			other.Status, o.Workflow.PR.Merged)
	}
}

// flakyClose роняет Transition для ключа задачи-родителя: так выглядит сбой
// самого последнего шага completeSplit — закрытия родителя в
// closeSplitParent, — уже после того как дети созданы, связаны и отчёт
// о них записан. Task 11 обернул splitFailed'ом только ранние шаги; этот
// тест — на сбой именно здесь.
type flakyClose struct {
	*mock.Tracker
	failOn string
}

func (f *flakyClose) Transition(key string, by tracker.Actor, toStatus string) error {
	if key == f.failOn {
		return errors.New("сеть недоступна")
	}
	return f.Tracker.Transition(key, by, toStatus)
}

// TestCompleteSplitsRecordsCloseFailureNoticeAndContinues доказывает, что
// сбой именно на закрытии родителя (Transition после того, как дети уже
// созданы и связаны) тоже уходит через splitFailed, а не наружу из
// CompleteSplits — до этой задачи closeSplitParent не был обёрнут, и такой
// сбой ронял бы весь проход, не давая дойти до OFF-9.
func TestCompleteSplitsRecordsCloseFailureNoticeAndContinues(t *testing.T) {
	o := newOffice(t)
	confirmSplit(t, o)

	// Второй, независимый подтверждённый тикет — доказывает, что беда
	// на закрытии OFF-1 не роняет весь проход CompleteSplits.
	o.add("OFF-9", "Analysis")
	o.agent.result = runner.Result{
		Outcome: runner.OutcomeSplit, Summary: "Другая постановка.", NextOwner: "human",
		Questions: []runner.Question{{ID: "Q1", Text: "Разбить на 2, как предложено?"}},
		Split: &runner.Split{Children: []runner.SplitChild{
			{ID: "one", Title: "One", Description: "Первая половина."},
			{ID: "two", Title: "Two", Description: "Вторая половина."},
		}},
	}
	worked, err := o.Tick(context.Background(), "analyst")
	if err != nil || !worked {
		t.Fatalf("предложение OFF-9 не взято в работу: worked=%v err=%v", worked, err)
	}
	if err := o.tasks.AddComment("OFF-9", "human", "Да, разбивай."); err != nil {
		t.Fatalf("ответ не записан: %v", err)
	}
	if worked, err := o.Tick(context.Background(), "analyst"); err != nil || !worked {
		t.Fatalf("подтверждение OFF-9 не взято в работу: worked=%v err=%v", worked, err)
	}

	o.useTracker(&flakyClose{Tracker: o.tasks, failOn: "OFF-1"})

	if err := o.CompleteSplits(context.Background()); err != nil {
		t.Fatalf("сбой закрытия родителя не должен ронять весь проход: %v", err)
	}

	parent := o.get(t, "OFF-1")
	if parent.Status != "Blocked" {
		t.Errorf("статус родителя %q, ожидался Blocked — сбой закрытия не должен двигать задачу", parent.Status)
	}
	comment := lastComment(t, parent)
	marker, ok := tracker.MarkerOf(comment.Body)
	if !ok || marker.Event != tracker.EventSplitCreateFailed {
		t.Errorf("нет записи о сбое закрытия:\n%s", comment.Body)
	}

	other := o.get(t, "OFF-9")
	if other.Status != o.Workflow.PR.Merged {
		t.Errorf("вторая задача %q, ожидался %q — беда закрытия первой не должна её касаться",
			other.Status, o.Workflow.PR.Merged)
	}
}
