package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/mock"
)

// run прогоняет подкоманду mock и возвращает её вывод.
func run(t *testing.T, tr *mock.Tracker, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if err := mockCommand(tr, args, &out); err != nil {
		t.Fatalf("runner mock %s: %v", strings.Join(args, " "), err)
	}
	return out.String()
}

// CLI нужен, чтобы гонять сценарии руками, не поднимая JIRA. Проверяем весь
// круг: завести, увидеть в списке, прочитать, ответить комментарием.
func TestMockCLIRoundTrip(t *testing.T) {
	tr := mock.New(t.TempDir())

	run(t, tr, "add", "OFF-1", "--summary", "Первая задача", "--description", "Сделай полезное")

	// Проект берётся из ключа: набирать его вторым флагом незачем.
	task, err := tr.Get("OFF-1")
	if err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	if task.Project != "OFF" || task.Status != "Ready" || task.Summary != "Первая задача" {
		t.Errorf("задача заведена как %+v", task)
	}

	if out := run(t, tr, "ls"); !strings.Contains(out, "OFF-1") || !strings.Contains(out, "Ready") {
		t.Errorf("задачи нет в списке:\n%s", out)
	}

	run(t, tr, "comment", "OFF-1", "--author", "human", "Берём Stripe.")
	out := run(t, tr, "show", "OFF-1")
	for _, want := range []string{"OFF-1", "Сделай полезное", "human", "Берём Stripe."} {
		if !strings.Contains(out, want) {
			t.Errorf("в выводе show нет %q:\n%s", want, out)
		}
	}
}

func TestMockCLIListFilters(t *testing.T) {
	tr := mock.New(t.TempDir())
	run(t, tr, "add", "OFF-1", "--summary", "первая")
	run(t, tr, "add", "OFF-2", "--summary", "вторая", "--status", "Review")
	run(t, tr, "add", "OTH-1", "--summary", "чужая")

	out := run(t, tr, "ls", "--project", "OFF", "--status", "Ready")
	if !strings.Contains(out, "OFF-1") {
		t.Errorf("своей задачи нет в выводе:\n%s", out)
	}
	if strings.Contains(out, "OFF-2") || strings.Contains(out, "OTH-1") {
		t.Errorf("в выводе лишние задачи:\n%s", out)
	}
}

// Перевод задачи рукой человека — первый шаг любого сценария: в очередь роли
// задачу кладёт он, а не офис. Заодно снимается метка ожидания: человек
// ответил делом, а не словом, и задача больше его не ждёт.
func TestMockCLIMovesTaskAndClearsHumanFlag(t *testing.T) {
	tr := mock.New(t.TempDir())
	run(t, tr, "add", "OFF-1", "--summary", "первая", "--status", "Blocked")
	if err := tr.SetHumanFlag("OFF-1", tracker.BySystem(), true); err != nil {
		t.Fatalf("метка не выставлена: %v", err)
	}

	if out := run(t, tr, "move", "OFF-1", "Analysis"); !strings.Contains(out, "Analysis") {
		t.Errorf("перевод не подтверждён:\n%s", out)
	}

	task, err := tr.Get("OFF-1")
	if err != nil {
		t.Fatalf("задача не прочитана: %v", err)
	}
	if task.Status != "Analysis" {
		t.Errorf("статус %q, ожидался Analysis", task.Status)
	}
	if task.HumanFlag {
		t.Error("метка ожидания осталась: задача так и числится ждущей человека")
	}
}

// Ошибки CLI должны быть внятными: им пользуется человек, а не раннер.
func TestMockCLIRejectsNonsense(t *testing.T) {
	tr := mock.New(t.TempDir())

	cases := [][]string{
		nil,                             // без подкоманды
		{"нет-такой"},                   // неизвестная подкоманда
		{"add"},                         // без ключа
		{"add", "OFF-1"},                // без темы
		{"show", "OFF-404"},             // нет такой задачи
		{"comment", "OFF-404", "текст"}, // нет такой задачи
		{"comment", "OFF-1"},            // нечего писать
		{"move", "OFF-1"},               // без статуса
		{"move", "OFF-404", "Ready"},    // нет такой задачи
	}
	for _, args := range cases {
		var out bytes.Buffer
		if err := mockCommand(tr, args, &out); err == nil {
			t.Errorf("runner mock %s прошла без ошибки", strings.Join(args, " "))
		}
	}
}
