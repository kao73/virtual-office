package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/pipeline"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/mock"
	"github.com/kao73/virtual-office/internal/workspace"
)

var moment = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

// Ветка удаление переживает — она в bare-клоне. А вот незакоммиченное
// не переживает ничего: другого места у него нет.
func TestRemovableRefusesDirtyWorktree(t *testing.T) {
	entry := workspace.Entry{Key: "OFF-1", Dirty: 3}

	err := removable(entry, tracker.Task{Key: "OFF-1"}, moment)
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

	if err := removable(workspace.Entry{Key: "OFF-1"}, task, moment); err == nil {
		t.Fatal("папка выдернута из-под работающего агента")
	}
}

// Истёкшая аренда работе не мешает: прогон мёртв, папку убирать можно.
func TestRemovableAllowsExpiredLease(t *testing.T) {
	task := tracker.Task{
		Key: "OFF-1", RunID: "прогон-1", LeaseUntil: moment.Add(-time.Hour),
	}

	if err := removable(workspace.Entry{Key: "OFF-1"}, task, moment); err != nil {
		t.Errorf("папка мёртвого прогона не убирается: %v", err)
	}
}

func TestRemovableAllowsCleanIdleWorktree(t *testing.T) {
	if err := removable(workspace.Entry{Key: "OFF-1"}, tracker.Task{Key: "OFF-1"}, moment); err != nil {
		t.Errorf("чистая ничейная папка не убирается: %v", err)
	}
}

// --force — осознанный выбор человека, и он снимает обе оговорки разом:
// грязную папку с живой арендой rm сносит, трекер об аренде не спрашивая.
func TestRemoveWorktreeForceOverridesBoth(t *testing.T) {
	ws := workspace.New(t.TempDir())
	w, project := ensureWorktree(t, ws, "OFF-1", "OFF", "mock")
	if err := os.WriteFile(filepath.Join(w.Dir, "недоделка.txt"), []byte("грязь"), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}
	tr := mock.New(t.TempDir())
	leasedTask(t, tr, "OFF-1", "OFF", moment.Add(time.Hour))

	var out bytes.Buffer
	all := &offices{
		list:       []namedOffice{{name: "mock", Office: &pipeline.Office{Tracker: tr, Projects: tracker.Projects{"OFF": project}}}},
		workspaces: ws,
		out:        &out,
	}
	if err := removeWorktree(all, "OFF-1", true, moment); err != nil {
		t.Fatalf("--force не снял оговорки: %v", err)
	}
	if entries, _ := ws.List(); len(entries) != 0 {
		t.Errorf("рабочая папка осталась: %+v", entries)
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

// bareOrigin — bare-репозиторий с одним коммитом в master: Ensure ответвляет
// ветку задачи от origin/master, и без коммита такой ссылки нет.
func bareOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bare, seed := filepath.Join(root, "client.git"), filepath.Join(root, "seed")
	gitT(t, root, "init", "-q", "--bare", "-b", "master", bare)
	gitT(t, root, "init", "-q", "-b", "master", seed)
	gitT(t, seed, "commit", "-q", "--allow-empty", "-m", "начало")
	gitT(t, seed, "remote", "add", "origin", bare)
	gitT(t, seed, "push", "-q", "origin", "master")
	return bare
}

// ensureWorktree заводит рабочую папку задачи под свежим bare-репозиторием
// и снимает барьер Ensure: прогон кончился, папка свободна — иначе уборка
// молча обошла бы её как занятую.
func ensureWorktree(t *testing.T, ws *workspace.Manager, key, project, trackerName string) (workspace.Workspace, tracker.Project) {
	t.Helper()
	p := tracker.Project{RepoURL: bareOrigin(t), DefaultBranch: "master", BranchPrefix: "agent/", Tracker: trackerName}
	w, err := ws.Ensure(tracker.TaskRef{Key: key, Project: project}, p)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	if err := w.Unlock(); err != nil {
		t.Fatalf("барьер не снят: %v", err)
	}
	return w, p
}

// Аренду спрашивают у трекера, в котором задача живёт. В «чужом» офисе та же
// задача арендована живым прогоном: спроси rm не тот трекер — и получил бы
// отказ «над VO-1 работает прогон», хотя свой офис знает её свободной.
//
// Чужой офис стоит в списке первым, свой — вторым, и это не случайность:
// rm, который берёт первый попавшийся трекер вместо офиса проекта, упёрся бы
// в живую аренду и отказал — тест ловит именно эту регрессию. Стой свой офис
// первым, такой rm прошёл бы тест, не найдя ничего.
func TestRemoveWorktreeAsksTheOfficeOwningTheProject(t *testing.T) {
	ws := workspace.New(t.TempDir())
	_, project := ensureWorktree(t, ws, "VO-1", "VO", "jira")

	own := mock.New(t.TempDir())
	if err := own.Add(tracker.Task{Key: "VO-1", Project: "VO", Status: "Ready", Summary: "задача"}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	foreign := mock.New(t.TempDir())
	leasedTask(t, foreign, "VO-1", "VO", moment.Add(time.Hour))

	var out bytes.Buffer
	all := &offices{
		list: []namedOffice{
			{name: "mock", Office: &pipeline.Office{Tracker: foreign, Projects: tracker.Projects{"OFF": {Tracker: "mock"}}}},
			{name: "jira", Office: &pipeline.Office{Tracker: own, Projects: tracker.Projects{"VO": project}}},
		},
		workspaces: ws,
		out:        &out,
	}
	if err := removeWorktree(all, "VO-1", false, moment); err != nil {
		t.Fatalf("папка не удалена: %v", err)
	}
	if entries, _ := ws.List(); len(entries) != 0 {
		t.Errorf("рабочая папка осталась: %+v", entries)
	}
	if !strings.Contains(out.String(), "удалена") {
		t.Errorf("об удалении не сказано:\n%s", out.String())
	}
}

// Папка проекта, которого в projects.local.yaml больше нет: без --force —
// отказ с именем файла (некому спросить трекер об аренде), с --force — снос:
// флаг перекрывает всё, о чём спрашивают трекер, и трекер под ним не нужен
// вовсе. Иначе --force был бы обесценен ровно там, где его зовут, —
// когда на машине что-то уже сломано.
func TestRemoveWorktreeForProjectGoneFromConfig(t *testing.T) {
	setup := func(t *testing.T) (*offices, *workspace.Manager) {
		t.Helper()
		ws := workspace.New(t.TempDir())
		ensureWorktree(t, ws, "VO-1", "VO", "jira")
		all := &offices{
			list: []namedOffice{
				{name: "mock", Office: &pipeline.Office{Tracker: mock.New(t.TempDir()), Projects: tracker.Projects{"OFF": {Tracker: "mock"}}}},
			},
			workspaces: ws,
			out:        io.Discard,
		}
		return all, ws
	}

	t.Run("без --force отказ называет файл проектов", func(t *testing.T) {
		all, ws := setup(t)
		err := removeWorktree(all, "VO-1", false, moment)
		if err == nil {
			t.Fatal("папка проекта, пропавшего из конфигурации, удалена без --force")
		}
		if !strings.Contains(err.Error(), tracker.ProjectsLocalFile) {
			t.Errorf("отказ не назвал файл проектов: %v", err)
		}
		if entries, _ := ws.List(); len(entries) != 1 {
			t.Errorf("папка не должна была исчезнуть: %+v", entries)
		}
	})

	t.Run("с --force сносится без трекера", func(t *testing.T) {
		all, ws := setup(t)
		if err := removeWorktree(all, "VO-1", true, moment); err != nil {
			t.Fatalf("папка не удалена по --force: %v", err)
		}
		if entries, _ := ws.List(); len(entries) != 0 {
			t.Errorf("рабочая папка осталась: %+v", entries)
		}
	})
}
