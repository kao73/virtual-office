package pipeline

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
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
