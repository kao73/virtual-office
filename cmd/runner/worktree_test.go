package main

import (
	"bytes"
	"os"
	"os/exec"
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
	origin := bareOrigin(t)
	ws := workspace.New(t.TempDir())
	project := tracker.Project{RepoURL: origin, DefaultBranch: "master", BranchPrefix: "agent/", Tracker: "mock"}
	w, err := ws.Ensure(tracker.TaskRef{Key: "OFF-1", Project: "OFF"}, project)
	if err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}
	if err := os.WriteFile(filepath.Join(w.Dir, "недоделка.txt"), []byte("грязь"), 0o644); err != nil {
		t.Fatalf("файл не записан: %v", err)
	}
	tr := mock.New(t.TempDir())
	if err := tr.Add(tracker.Task{Key: "OFF-1", Project: "OFF", Status: "Ready", Summary: "задача"}); err != nil {
		t.Fatalf("задача не создана: %v", err)
	}
	if err := tr.Claim(tracker.ClaimRequest{
		Key: "OFF-1", RunID: "прогон-1", Owner: "implementer",
		LeaseUntil: moment.Add(time.Hour), ExpectStatus: "Ready", WorkingStatus: "InProgress",
	}); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}

	var out bytes.Buffer
	all := &offices{
		list:       []namedOffice{{name: "mock", Office: &pipeline.Office{Tracker: tr, Projects: tracker.Projects{"OFF": project}}}},
		workspaces: ws,
		out:        &out,
	}
	if err := removeWorktree(all, "OFF-1", true, moment, &out); err != nil {
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
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@local",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@local")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--bare", "-b", "master", bare)
	run("init", "-q", "-b", "master", seed)
	run("-C", seed, "commit", "-q", "--allow-empty", "-m", "начало")
	run("-C", seed, "remote", "add", "origin", bare)
	run("-C", seed, "push", "-q", "origin", "master")
	return bare
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
	origin := bareOrigin(t)
	ws := workspace.New(t.TempDir())
	project := tracker.Project{RepoURL: origin, DefaultBranch: "master", BranchPrefix: "agent/", Tracker: "jira"}
	if _, err := ws.Ensure(tracker.TaskRef{Key: "VO-1", Project: "VO"}, project); err != nil {
		t.Fatalf("рабочая папка не создана: %v", err)
	}

	add := func(tr *mock.Tracker) {
		t.Helper()
		if err := tr.Add(tracker.Task{Key: "VO-1", Project: "VO", Status: "Ready", Summary: "задача"}); err != nil {
			t.Fatalf("задача не создана: %v", err)
		}
	}
	own := mock.New(t.TempDir())
	add(own)
	foreign := mock.New(t.TempDir())
	add(foreign)
	if err := foreign.Claim(tracker.ClaimRequest{
		Key: "VO-1", RunID: "прогон-1", Owner: "implementer",
		LeaseUntil: moment.Add(time.Hour), ExpectStatus: "Ready", WorkingStatus: "InProgress",
	}); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}

	var out bytes.Buffer
	all := &offices{
		list: []namedOffice{
			{name: "mock", Office: &pipeline.Office{Tracker: foreign, Projects: tracker.Projects{"OFF": {Tracker: "mock"}}}},
			{name: "jira", Office: &pipeline.Office{Tracker: own, Projects: tracker.Projects{"VO": project}}},
		},
		workspaces: ws,
		out:        &out,
	}
	if err := removeWorktree(all, "VO-1", false, moment, &out); err != nil {
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
	setup := func(t *testing.T) (*offices, *workspace.Manager, *bytes.Buffer) {
		t.Helper()
		origin := bareOrigin(t)
		ws := workspace.New(t.TempDir())
		project := tracker.Project{RepoURL: origin, DefaultBranch: "master", BranchPrefix: "agent/", Tracker: "jira"}
		if _, err := ws.Ensure(tracker.TaskRef{Key: "VO-1", Project: "VO"}, project); err != nil {
			t.Fatalf("рабочая папка не создана: %v", err)
		}
		var out bytes.Buffer
		all := &offices{
			list: []namedOffice{
				{name: "mock", Office: &pipeline.Office{Tracker: mock.New(t.TempDir()), Projects: tracker.Projects{"OFF": {Tracker: "mock"}}}},
			},
			workspaces: ws,
			out:        &out,
		}
		return all, ws, &out
	}

	t.Run("без --force отказ называет файл проектов", func(t *testing.T) {
		all, ws, out := setup(t)
		err := removeWorktree(all, "VO-1", false, moment, out)
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
		all, ws, out := setup(t)
		if err := removeWorktree(all, "VO-1", true, moment, out); err != nil {
			t.Fatalf("папка не удалена по --force: %v", err)
		}
		if entries, _ := ws.List(); len(entries) != 0 {
			t.Errorf("рабочая папка осталась: %+v", entries)
		}
	})
}
