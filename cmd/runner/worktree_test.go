package main

import (
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/workspace"
)

var moment = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// Ветка удаление переживает — она в bare-клоне. А вот незакоммиченное
// не переживает ничего: другого места у него нет.
func TestRemovableRefusesDirtyWorktree(t *testing.T) {
	entry := workspace.Entry{Key: "OFF-1", Dirty: 3}

	err := removable(entry, tracker.Task{Key: "OFF-1"}, moment, false)
	if err == nil {
		t.Fatal("незакоммиченная работа снесена молча")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("отказ не говорит, чем его перебить: %v", err)
	}
}

// Рабочая папка задачи с живой арендой — это папка, в которой прямо сейчас
// сидит агент: она смонтирована в его песочницу.
func TestRemovableRefusesLeasedTask(t *testing.T) {
	task := tracker.Task{
		Key: "OFF-1", RunID: "прогон-1", LeaseUntil: moment.Add(time.Hour),
	}

	if err := removable(workspace.Entry{Key: "OFF-1"}, task, moment, false); err == nil {
		t.Fatal("папка выдернута из-под работающего агента")
	}
}

// Истёкшая аренда работе не мешает: прогон мёртв, папку убирать можно.
func TestRemovableAllowsExpiredLease(t *testing.T) {
	task := tracker.Task{
		Key: "OFF-1", RunID: "прогон-1", LeaseUntil: moment.Add(-time.Hour),
	}

	if err := removable(workspace.Entry{Key: "OFF-1"}, task, moment, false); err != nil {
		t.Errorf("папка мёртвого прогона не убирается: %v", err)
	}
}

func TestRemovableAllowsCleanIdleWorktree(t *testing.T) {
	if err := removable(workspace.Entry{Key: "OFF-1"}, tracker.Task{Key: "OFF-1"}, moment, false); err != nil {
		t.Errorf("чистая ничейная папка не убирается: %v", err)
	}
}

// --force — осознанный выбор человека, и он снимает обе оговорки разом.
func TestRemovableForceOverridesBoth(t *testing.T) {
	entry := workspace.Entry{Key: "OFF-1", Dirty: 3}
	task := tracker.Task{Key: "OFF-1", RunID: "прогон-1", LeaseUntil: moment.Add(time.Hour)}

	if err := removable(entry, task, moment, true); err != nil {
		t.Errorf("--force не снял оговорки: %v", err)
	}
}

// Строка итога попадается человеку на глаза каждый раз, и «1 папок» в ней
// выглядит небрежностью.
func TestPluralAgreesWithNumber(t *testing.T) {
	cases := map[int]string{
		0: "папок", 1: "папка", 2: "папки", 4: "папки", 5: "папок",
		11: "папок", 12: "папок", 14: "папок",
		21: "папка", 22: "папки", 25: "папок",
		101: "папка", 111: "папок",
	}
	for n, want := range cases {
		if got := plural(n); got != want {
			t.Errorf("%d %s, ожидалось %d %s", n, got, n, want)
		}
	}
}
