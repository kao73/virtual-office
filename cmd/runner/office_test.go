package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/tracker"
)

// Строка об источнике печатается **в момент разрешения пути**, а не в конце сборки.
//
// Тест на порядок, а не на текст, и он не педантизм: первая редакция копила строки
// и печатала их разом после всех загрузчиков — то есть при отказе не печатала
// ничего, ровно в том случае, ради которого печать и заведена.
//
// Проверяется здесь только configSources. Порядок вызовов внутри newOffices() тест
// не держит, и сломать его можно двумя способами: вернуть накопление до конца
// сборки или поднять все вызовы sources.* в начало функции — тогда строка про
// tracker.yaml выйдет и на машине без единого jira-проекта, где файл не открывается.
func TestConfigSourcesPrintImmediately(t *testing.T) {
	var out strings.Builder
	sources := configSources{out: &out}

	sources.office("/офис", "workflow.yaml")
	if !strings.Contains(out.String(), "workflow.yaml") {
		t.Fatalf("первая строка не напечатана сразу: %q", out.String())
	}

	// Вторая строка выходит следом, а шапка остаётся одна.
	sources.machine("/машина", "tracker.yaml")
	printed := out.String()
	if strings.Count(printed, "конфигурация:") != 1 {
		t.Errorf("шапка напечатана не один раз:\n%s", printed)
	}
	if !strings.Contains(printed, filepath.Join("/машина", "tracker.yaml")) {
		t.Errorf("вторая строка не напечатана:\n%s", printed)
	}

	// Файл, которого нет, называется тоже: «нет» — такой же ответ, как путь,
	// и для необязательных бюджетов он законный.
	if !strings.Contains(printed, "нет") {
		t.Errorf("отсутствующий файл не помечен:\n%s", printed)
	}
}

// fixtureRunner — временные корень конфигурации и хозяйство раннера.
// Корень — git-репозиторий с одним коммитом: runner.ConfigSHA читает HEAD.
// В нём копия поставляемого workflow.yaml; ролей нет — их читает tick,
// а не конструктор. Оба пути уходят в окружение, откуда их берёт newOffices().
func fixtureRunner(t *testing.T, projectsLocal string) (root, home string) {
	t.Helper()
	root, home = t.TempDir(), t.TempDir()

	wf, err := os.ReadFile(filepath.Join("..", "..", tracker.WorkflowFile))
	if err != nil {
		t.Fatalf("поставляемый граф не прочитан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, tracker.WorkflowFile), wf, 0o644); err != nil {
		t.Fatalf("граф не скопирован: %v", err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "master"},
		{"-c", "user.name=t", "-c", "user.email=t@local", "commit", "-q", "--allow-empty", "-m", "конфигурация"},
	} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(home, tracker.ProjectsLocalFile), []byte(projectsLocal), 0o644); err != nil {
		t.Fatalf("projects.local.yaml не записан: %v", err)
	}
	t.Setenv("OFFICE_CONFIG_ROOT", root)
	t.Setenv("OFFICE_HOME", home)
	return root, home
}

const (
	mockProject = "OFF:\n  repo_url: https://example.test/o.git\n  tracker: mock\n  default_branch: master\n"
	jiraProject = "VO:\n  repo_url: https://example.test/v.git\n  tracker: jira\n  default_branch: master\n"
)

// trackerYAML — минимальный tracker.yaml, смотрящий на тестовый сервер.
func trackerYAML(baseURL string) string {
	return "base_url: " + baseURL + "\nauth: { mode: basic }\n" +
		"accounts:\n  default: { user_env: JIRA_USER, secret_env: JIRA_PASSWORD }\n" +
		"status_map: { Ready: Ready }\n" +
		"fields:\n  agent_owner: customfield_10001\n  run_id: customfield_10002\n" +
		"  lease_until: customfield_10003\n  attempts: customfield_10004\n" +
		"human_flag_label: office-waits-human\n"
}

// Машина, где все проекты mock, стартует без tracker.yaml, и строки о нём
// в раскладке нет: файл не открывается — значит и не упоминается.
func TestOfficesMockOnlyNeedsNoTrackerFile(t *testing.T) {
	fixtureRunner(t, mockProject)
	var out bytes.Buffer

	all, err := newOffices(flags("tick"), nil, &out)
	if err != nil {
		t.Fatalf("офис на одном mock не собран: %v", err)
	}
	if len(all.list) != 1 || all.list[0].name != "mock" {
		t.Errorf("офисы: %+v, ожидался один — mock", all.list)
	}
	if strings.Contains(out.String(), "tracker.yaml") {
		t.Errorf("tracker.yaml упомянут там, где не открывался:\n%s", out.String())
	}
	if !strings.Contains(out.String(), tracker.ProjectsLocalFile) {
		t.Errorf("projects.local.yaml не назван в раскладке:\n%s", out.String())
	}
}

// Проект jira без tracker.yaml — отказ с именем файла, до всякой работы.
func TestOfficesJiraProjectWithoutTrackerFileIsRefused(t *testing.T) {
	fixtureRunner(t, mockProject+jiraProject)
	var out bytes.Buffer

	all, err := newOffices(flags("tick"), nil, &out)
	if err == nil || !strings.Contains(err.Error(), "tracker.yaml") {
		t.Fatalf("отказ не назвал tracker.yaml: %v", err)
	}
	if all != nil {
		t.Error("при отказе одного трекера собран офис другого")
	}
	// Файл назван и в раскладке — «нет» такой же ответ, как путь, и строка
	// идёт после projects.local.yaml: список трекеров известен только после проектов.
	printed := out.String()
	if !strings.Contains(printed, "tracker.yaml") || strings.Index(printed, "tracker.yaml") < strings.Index(printed, tracker.ProjectsLocalFile) {
		t.Errorf("tracker.yaml не назван после projects.local.yaml:\n%s", printed)
	}
}

// Отвергнутый кред jira — отказ всей команды: под планировщиком это обязано
// быть отказом, а не строкой в логе, и mock-офис при этом не собирается.
func TestOfficesRejectedJiraCredentialRefusesWholeCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errorMessages":["Login required"]}`, http.StatusUnauthorized)
	}))
	defer server.Close()
	_, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, "tracker.yaml"), []byte(trackerYAML(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "неверный")

	all, err := newOffices(flags("tick"), nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "учётк") {
		t.Fatalf("отказ не про учётку: %v", err)
	}
	if all != nil {
		t.Error("при отвергнутом креде jira собран офис mock")
	}
}

// Два трекера — два офиса, по алфавиту, с общими проектами каждому своими.
func TestOfficesBuildOnePerTracker(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/myself") {
			fmt.Fprint(w, `{"name":"office"}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	_, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, "tracker.yaml"), []byte(trackerYAML(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")
	var out bytes.Buffer

	all, err := newOffices(flags("tick"), nil, &out)
	if err != nil {
		t.Fatalf("офисы не собраны: %v", err)
	}
	if len(all.list) != 2 || all.list[0].name != "jira" || all.list[1].name != "mock" {
		t.Fatalf("офисы %+v, ожидались jira, mock", all.list)
	}
	if _, ok := all.list[0].Projects["VO"]; !ok || len(all.list[0].Projects) != 1 {
		t.Errorf("офис jira видит не только VO: %v", all.list[0].Projects.Keys())
	}
	if _, ok := all.list[1].Projects["OFF"]; !ok || len(all.list[1].Projects) != 1 {
		t.Errorf("офис mock видит не только OFF: %v", all.list[1].Projects.Keys())
	}
	if all.list[0].Workspaces != all.list[1].Workspaces || all.list[0].Ledger != all.list[1].Ledger {
		t.Error("хозяйство машины не общее для офисов")
	}
}

// --tracker больше нет: неизвестный флаг, и отказ приходит раньше, чем
// раннер тронул конфигурацию.
func TestOfficesRejectTrackerFlag(t *testing.T) {
	fixtureRunner(t, mockProject)
	var out bytes.Buffer

	_, err := newOffices(flags("tick"), []string{"--tracker", "jira"}, &out)
	if err == nil || !strings.Contains(err.Error(), "tracker") {
		t.Fatalf("флаг --tracker принят: %v", err)
	}
	if strings.Contains(out.String(), "конфигурация:") {
		t.Errorf("конфигурация прочитана до разбора флагов:\n%s", out.String())
	}
}
