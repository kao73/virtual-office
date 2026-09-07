package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/mock"
)

var boardNow = time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)

// `ls` отвечает на вопрос «что сейчас на доске»: где задача, кто её взял, сколько
// она там висит. Очередь для этого не годится — в ней нет как раз того, над чем
// работают.
func TestBoardShowsWhoWorksAndForHowLong(t *testing.T) {
	tr := mock.New(t.TempDir())
	tr.Now = func() time.Time { return boardNow }

	add := func(key, status, summary string) {
		project, _, _ := strings.Cut(key, "-")
		if err := tr.Add(tracker.Task{Key: key, Project: project, Status: status, Summary: summary}); err != nil {
			t.Fatalf("задача не создана: %v", err)
		}
	}
	add("OFF-1", "InProgress", "Добавить hello.py")
	add("OFF-2", "Blocked", "Спорная постановка")
	add("OTH-1", "Ready", "Чужой проект")

	if err := tr.Claim(tracker.ClaimRequest{
		Key: "OFF-1", RunID: "прогон-1", Owner: "implementer",
		LeaseUntil: boardNow.Add(time.Hour), ExpectStatus: "InProgress", WorkingStatus: "InProgress",
	}); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}
	if err := tr.SetHumanFlag("OFF-2", tracker.BySystem(), true); err != nil {
		t.Fatalf("атрибут ожидания не выставлен: %v", err)
	}

	var out bytes.Buffer
	statuses := []string{"Ready", "InProgress", "Review", "Blocked"}
	if err := printBoard(tr, []string{"OFF"}, statuses, func(s string) bool { return s == "Done" }, boardNow, &out); err != nil {
		t.Fatalf("доска не напечатана: %v", err)
	}

	got := out.String()
	for _, want := range []string{"OFF-1", "InProgress", "implementer", "Добавить hello.py", "OFF-2", "ждёт человека"} {
		if !strings.Contains(got, want) {
			t.Errorf("в выводе нет %q:\n%s", want, got)
		}
	}
	// Проект, о котором не спрашивали, в выводе не появляется.
	if strings.Contains(got, "OTH-1") {
		t.Errorf("показан чужой проект:\n%s", got)
	}
}

// Пустая доска — законное состояние, и молчать о нём нельзя: человек не должен
// гадать, кончились задачи или сломался вывод.
func TestBoardSaysWhenEmpty(t *testing.T) {
	tr := mock.New(t.TempDir())
	tr.Now = func() time.Time { return boardNow }

	var out bytes.Buffer
	if err := printBoard(tr, []string{"OFF"}, []string{"Ready"}, func(s string) bool { return s == "Done" }, boardNow, &out); err != nil {
		t.Fatalf("доска не напечатана: %v", err)
	}
	if !strings.Contains(out.String(), "задач нет") {
		t.Errorf("о пустой доске не сказано:\n%s", out.String())
	}
}

// Возраст читается человеком, а не машиной: секунды в графе «сколько висит»
// не нужны никому.
func TestAgeIsHumanReadable(t *testing.T) {
	cases := map[time.Duration]string{
		30 * time.Second:     "только что",
		20 * time.Minute:     "20м",
		5 * time.Hour:        "5ч",
		50 * time.Hour:       "2д",
		400 * 24 * time.Hour: "400д",
	}
	for since, want := range cases {
		if got := age(boardNow.Add(-since), boardNow); got != want {
			t.Errorf("возраст %s показан как %q, ожидалось %q", since, got, want)
		}
	}
	if got := age(time.Time{}, boardNow); got != "—" {
		t.Errorf("неизвестный возраст показан как %q", got)
	}
}

// unknownProject — трекер, не знающий одного из проектов конфигурации. Так
// выглядит протухшая строка в projects.yaml: проект описан, а в трекере его
// нет и никогда не было.
type unknownProject struct {
	tracker.Tracker
	missing string
}

func (u unknownProject) List(project string, statuses []string) ([]tracker.TaskRef, error) {
	if project == u.missing {
		return nil, fmt.Errorf("%w: %s", tracker.ErrNoProject, project)
	}
	return u.Tracker.List(project, statuses)
}

// Незнакомый трекеру проект `tick` пропускает, а `ls` на нём падал: одна причина,
// два разных поведения. Доска нужна как раз затем, чтобы такую строку в конфигурации
// увидеть, — отказываться её показывать из-за неё же бессмысленно.
func TestBoardSkipsProjectUnknownToTracker(t *testing.T) {
	tr := mock.New(t.TempDir())
	tr.Now = func() time.Time { return boardNow }
	if err := tr.Add(tracker.Task{Key: "OFF-1", Project: "OFF", Status: "Ready", Summary: "Добавить hello.py"}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}

	var out bytes.Buffer
	// Имя нарочно раньше OFF по алфавиту: обход дойдёт до него первым.
	tasks := unknownProject{Tracker: tr, missing: "AAA"}
	if err := printBoard(tasks, []string{"AAA", "OFF"}, []string{"Ready"}, func(s string) bool { return s == "Done" }, boardNow, &out); err != nil {
		t.Fatalf("доска не напечатана: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "OFF-1") {
		t.Errorf("из-за чужого проекта потеряна вся доска:\n%s", got)
	}
	// Молчать о пропуске нельзя: строка в projects.yaml выглядит рабочей,
	// а задач по ней не видно — человеку нужно знать почему.
	if !strings.Contains(got, "AAA") {
		t.Errorf("о пропущенном проекте не сказано ни слова:\n%s", got)
	}
}

// TestBoardShowsBlockedDependency доказывает, что ls называет, чего
// ждёт заблокированная задача, без отдельной команды
// (pipeline-dependency-gate/spec.md, "A blocked candidate's wait is
// visible without extra tooling").
func TestBoardShowsBlockedDependency(t *testing.T) {
	tr := mock.New(t.TempDir())
	tr.Now = func() time.Time { return boardNow }

	add := func(task tracker.Task) {
		if err := tr.Add(task); err != nil {
			t.Fatalf("задача не создана: %v", err)
		}
	}
	add(tracker.Task{Key: "OFF-1", Project: "OFF", Status: "Ready", Summary: "Первая часть"})
	add(tracker.Task{Key: "OFF-2", Project: "OFF", Status: "Ready", Summary: "Вторая часть", DependsOn: []string{"OFF-1"}})

	var out bytes.Buffer
	terminal := func(s string) bool { return s == "Done" }
	if err := printBoard(tr, []string{"OFF"}, []string{"Ready"}, terminal, boardNow, &out); err != nil {
		t.Fatalf("доска не напечатана: %v", err)
	}

	var off1Line, off2Line string
	for _, line := range strings.Split(out.String(), "\n") {
		switch {
		case strings.HasPrefix(line, "OFF-1 "):
			off1Line = line
		case strings.HasPrefix(line, "OFF-2 "):
			off2Line = line
		}
	}
	if !strings.Contains(off2Line, "ждёт: OFF-1 (Ready)") {
		t.Errorf("строка OFF-2 не называет зависимость:\n%s", off2Line)
	}
	if strings.Contains(off1Line, "ждёт:") {
		t.Errorf("у задачи без зависимостей появилась колонка ожидания:\n%s", off1Line)
	}
}
