package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/tracker"
	"github.com/kao73/virtual-office/tracker/mock"
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
	columns := []string{"Ready", "InProgress", "Review", "Blocked"}
	if err := printBoard(tr, []string{"OFF"}, columns, boardNow, &out); err != nil {
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
	if err := printBoard(tr, []string{"OFF"}, []string{"Ready"}, boardNow, &out); err != nil {
		t.Fatalf("доска не напечатана: %v", err)
	}
	if !strings.Contains(out.String(), "задач нет") {
		t.Errorf("о пустой доске не сказано:\n%s", out.String())
	}
}

// Возраст читается человеком, а не машиной: секунды в колонке «сколько висит»
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
