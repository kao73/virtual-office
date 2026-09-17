package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/pipeline"
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
// выглядит протухшая строка в projects.local.yaml: проект описан, а в трекере его
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
	// Молчать о пропуске нельзя: строка в projects.local.yaml выглядит рабочей,
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
	// Fix round 2, Finding 6: зависимость — хвост строки, после summary, а
	// не вставка между waiting и age. Мидроу-вставка сдвигала бы вправо
	// все колонки строки, у которых есть незакрытая зависимость (age,
	// summary), и делала бы доску нечитаемой построчно.
	summaryAt := strings.Index(off2Line, "Вторая часть")
	dependsAt := strings.Index(off2Line, "ждёт: OFF-1 (Ready)")
	if summaryAt == -1 || dependsAt == -1 || dependsAt < summaryAt {
		t.Errorf("зависимость должна идти хвостом после summary, а не перед ним:\n%s", off2Line)
	}
}

// TestDependsColumnNamesMissingDependencyByGraphAbsence — dependsColumn
// обязан описывать пропавшую зависимость так же, как describeUnmet
// (internal/pipeline/deps.go): не как утверждение, что задачи не
// существует, а как «её нет среди статусов графа» — деп может быть в
// статусе вне графа проекта или в чужом проекте, а не только удалён
// (fix round 2, Finding 3 — формулировка должна совпадать в обоих
// местах, board.go и deps.go, а не только логика).
func TestDependsColumnNamesMissingDependencyByGraphAbsence(t *testing.T) {
	got := dependsColumn([]tracker.TaskRef{{}})
	if !strings.Contains(got, "не найдена в статусах графа") {
		t.Errorf("dependsColumn = %q, ожидалась формулировка про отсутствие в статусах графа", got)
	}
	if strings.Contains(got, "неизвестная") {
		t.Errorf("dependsColumn всё ещё утверждает несуществование, которое не может проверить: %q", got)
	}
}

// boardOffices — два офиса с задачей в каждом, поверх файловых трекеров.
func boardOffices(t *testing.T, out *bytes.Buffer) *offices {
	t.Helper()
	office := func(name, project, key string) namedOffice {
		tr := mock.New(t.TempDir())
		tr.Now = func() time.Time { return boardNow }
		if err := tr.Add(tracker.Task{Key: key, Project: project, Status: "Ready", Summary: "задача " + key}); err != nil {
			t.Fatalf("задача не создана: %v", err)
		}
		return namedOffice{name: name, Office: &pipeline.Office{
			Tracker:  tr,
			Workflow: tracker.Workflow{Statuses: []string{"Ready", "Done"}, Terminal: []string{"Done"}},
			Projects: tracker.Projects{project: {Tracker: name}},
		}}
	}
	return &offices{list: []namedOffice{office("jira", "VO", "VO-1"), office("mock", "OFF", "OFF-1")}, out: out}
}

// Два трекера — доска каждого под его именем; конфигурацию печатает
// конструктор, здесь её нет. Один трекер — прежний вывод, без заголовка.
func TestBoardsListTasksPerTracker(t *testing.T) {
	var out bytes.Buffer
	all := boardOffices(t, &out)

	if err := printBoards(all, "", boardNow, &out); err != nil {
		t.Fatalf("доски не напечатаны: %v", err)
	}
	got := out.String()
	jira, vo := strings.Index(got, "== трекер jira =="), strings.Index(got, "VO-1")
	local, off := strings.Index(got, "== трекер mock =="), strings.Index(got, "OFF-1")
	if !(jira >= 0 && jira < vo && vo < local && local < off) {
		t.Errorf("задачи не под заголовками своих трекеров:\n%s", got)
	}

	out.Reset()
	all.list = all.list[1:]
	if err := printBoards(all, "", boardNow, &out); err != nil {
		t.Fatalf("доска не напечатана: %v", err)
	}
	if strings.Contains(out.String(), "== трекер") || !strings.Contains(out.String(), "OFF-1") {
		t.Errorf("при одном трекере вывод изменился:\n%s", out.String())
	}
}

// --project — доска только его офиса; чужой проект — отказ с именем файла.
//
// Спрашивается OFF — проект второго офиса, не первого: ls, который берёт
// первый попавшийся офис вместо офиса проекта, напечатал бы VO-1 и провалил
// тест. Спроси VO — и такой ls прошёл бы его, не найдя ничего.
func TestBoardsProjectFlagPicksTheOwningOffice(t *testing.T) {
	var out bytes.Buffer
	all := boardOffices(t, &out)

	if err := printBoards(all, "OFF", boardNow, &out); err != nil {
		t.Fatalf("доска проекта не напечатана: %v", err)
	}
	if !strings.Contains(out.String(), "OFF-1") || strings.Contains(out.String(), "VO-1") || strings.Contains(out.String(), "== трекер") {
		t.Errorf("--project OFF показал не только OFF:\n%s", out.String())
	}
	if err := printBoards(all, "NOPE", boardNow, &out); err == nil || !strings.Contains(err.Error(), tracker.ProjectsLocalFile) {
		t.Errorf("неизвестный проект не отвергнут с именем файла: %v", err)
	}
}
