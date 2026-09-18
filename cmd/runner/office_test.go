package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	payload "github.com/kao73/virtual-office"
	"github.com/kao73/virtual-office/internal/runner"
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
	gitT(t, root, "init", "-q", "-b", "master")
	gitT(t, root, "commit", "-q", "--allow-empty", "-m", "конфигурация")
	if err := os.WriteFile(filepath.Join(home, tracker.ProjectsLocalFile), []byte(projectsLocal), 0o644); err != nil {
		t.Fatalf("projects.local.yaml не записан: %v", err)
	}
	t.Setenv("OFFICE_CONFIG_ROOT", root)
	t.Setenv("OFFICE_HOME", home)
	return root, home
}

// gitT зовёт git в каталоге и роняет тест на отказе; личность коммитов —
// через окружение, чтобы не зависеть от конфигурации машины.
func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@local",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// jiraFixture — конфигурация с mock- и jira-проектом, где jira смотрит
// на тестовый сервер с данным обработчиком; учётка — в окружении.
func jiraFixture(t *testing.T, handler http.HandlerFunc, password string) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	_, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, "tracker.yaml"), []byte(trackerYAML(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", password)
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
	jiraFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"errorMessages":["Login required"]}`, http.StatusUnauthorized)
	}, "неверный")

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
	jiraFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/myself") {
			fmt.Fprint(w, `{"name":"office"}`)
			return
		}
		http.NotFound(w, r)
	}, "секрет")
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

// tracker.RefuseLeftoverOfficeFile покрыт собственным юнит-тестом
// (internal/tracker), но до этого теста ничто не проверяло сам вызов
// внутри newOffices (office.go, сразу после runner.Home()) — рефакторинг
// мог бы его потерять, и ни один тест этого не заметил бы. Отказ
// проверяется до печати строки о workflow.yaml (sources.office) — то есть
// guard стоит раньше первого чтения самой конфигурации, а не где-то
// посреди сборки офисов.
func TestNewOfficesRefusesLeftoverProjectsYAML(t *testing.T) {
	root, _ := fixtureRunner(t, mockProject)
	if err := os.WriteFile(filepath.Join(root, tracker.OfficeProjectsFile), []byte("OFF: {}\n"), 0o644); err != nil {
		t.Fatalf("projects.yaml не записан: %v", err)
	}
	var out bytes.Buffer

	all, err := newOffices(flags("tick"), nil, &out)
	if err == nil {
		t.Fatal("оставшийся projects.yaml пропущен молча")
	}
	for _, want := range []string{tracker.ProjectsLocalFile, "roles/_base/base.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("отказ не назвал %q: %v", want, err)
		}
	}
	if all != nil {
		t.Error("при отказе guard'а офисы всё равно собраны")
	}
	if strings.Contains(out.String(), "workflow.yaml") {
		t.Errorf("guard сработал после того, как конфигурация уже читалась:\n%s", out.String())
	}
}

// releaseVersion подменяет версию, вшитую ldflags: раннер ведёт себя как
// релиз v…, не будучи им собран.
func releaseVersion(t *testing.T, v string) {
	t.Helper()
	prev := payload.Version
	payload.Version = v
	t.Cleanup(func() { payload.Version = prev })
}

// payloadFixture — хозяйство с одним mock-проектом и без OFFICE_CONFIG_ROOT:
// офис берётся из поставки бинарника.
func payloadFixture(t *testing.T, version string) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("OFFICE_HOME", home)
	t.Setenv("OFFICE_CONFIG_ROOT", "")
	releaseVersion(t, version)
	if err := os.WriteFile(filepath.Join(home, tracker.ProjectsLocalFile), []byte(mockProject), 0o644); err != nil {
		t.Fatalf("projects.local.yaml не записан: %v", err)
	}
	return home
}

// Без OFFICE_CONFIG_ROOT раннер распаковывает собственную поставку — настоящую,
// не фикстуру: единственный тест, где embed-директива payload.go и распаковка
// сходятся на реальном дереве.
func TestOfficesUnpackPayloadWithoutConfigRoot(t *testing.T) {
	home := payloadFixture(t, "v0.0.0-test")
	var out bytes.Buffer
	all, err := newOffices(flags("tick"), nil, &out)
	if err != nil {
		t.Fatalf("офис из поставки не собран: %v", err)
	}
	root := filepath.Join(home, runner.OfficeDir, "v0.0.0-test")
	for _, rel := range []string{
		"roles/_base/base.yaml", "roles/analyst/role.yaml", "roles/implementer/role.yaml", "roles/reviewer/role.yaml",
		"skills/comet/SKILL.md", "hooks/require-result.sh", "workflow.yaml", "budgets.yaml",
		"tracker.example.yaml", "projects.local.example.yaml",
		"sbx-kits/comet-cli/spec.yaml", "sbx-kits/bake-comet-template.sh",
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Errorf("%s не распакован: %v", rel, err)
		}
	}
	printed := out.String()
	if !strings.Contains(printed, "офис: v0.0.0-test → "+root) {
		t.Errorf("раскладка не называет офис первой строкой:\n%s", printed)
	}
	if !strings.Contains(printed, filepath.Join(root, tracker.WorkflowFile)) {
		t.Errorf("раскладка не называет файлы под распакованным офисом:\n%s", printed)
	}
	if o := all.list[0]; o.Identity != "v0.0.0-test" || o.Root != root || o.Source != runner.SourcePayload {
		t.Errorf("офис %+v, ожидались v0.0.0-test, %s, payload", o.Office, root)
	}
	// Роль с обоими хуками грузится из распакованного офиса: биты на месте.
	if _, err := runner.LoadRole(root, "implementer"); err != nil {
		t.Errorf("роль из распакованного офиса не загружена: %v", err)
	}
	// Повторный старт ничего не переписывает.
	marker := filepath.Join(root, "правка-руками")
	if err := os.WriteFile(marker, []byte("метка\n"), 0o644); err != nil {
		t.Fatalf("метка не записана: %v", err)
	}
	if _, err := newOffices(flags("tick"), nil, io.Discard); err != nil {
		t.Fatalf("повторный сбор офиса не удался: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("повторный запуск стёр метку в распакованном офисе: %v", err)
	}
}

// Два раннера разных версий на одном хозяйстве — два каталога, каждый
// читает свой.
func TestOfficesKeepVersionsSideBySide(t *testing.T) {
	home := payloadFixture(t, "v0.0.0-a")
	if _, err := newOffices(flags("tick"), nil, io.Discard); err != nil {
		t.Fatalf("офис v0.0.0-a не собран: %v", err)
	}

	releaseVersion(t, "v0.0.0-b")
	var out bytes.Buffer
	if _, err := newOffices(flags("tick"), nil, &out); err != nil {
		t.Fatalf("офис v0.0.0-b не собран: %v", err)
	}

	for _, v := range []string{"v0.0.0-a", "v0.0.0-b"} {
		if _, err := os.Stat(filepath.Join(home, runner.OfficeDir, v)); err != nil {
			t.Errorf("каталог версии %s не сохранён: %v", v, err)
		}
	}
	printed := out.String()
	if !strings.Contains(printed, filepath.Join(home, runner.OfficeDir, "v0.0.0-b", tracker.WorkflowFile)) {
		t.Errorf("раскладка не называет вторую версию:\n%s", printed)
	}
	if strings.Contains(printed, "v0.0.0-a") {
		t.Errorf("раскладка второго запуска упоминает первую версию:\n%s", printed)
	}
}

// configSHAPattern — вид личности офиса-клона: HEAD (40 hex) и, при
// незакоммиченных правках, суффикс -dirty. fixtureRunner пишет workflow.yaml
// в корень до git init — файл остаётся неотслеживаемым, и git status
// --porcelain видит его как правку: личность выходит грязной (см. Controller
// ruling R1 в task-5-brief.md — len(ConfigSHA)==40 в исходном наброске
// не выполняется никогда); форму судит runner.IsCommitIdentity.

// Обёртки bin/* задают OFFICE_CONFIG_ROOT, и тогда поставка не трогается:
// ${OFFICE_HOME}/office/ не появляется, личность — commit клона.
func TestOfficesCloneModeUnpacksNothing(t *testing.T) {
	_, home := fixtureRunner(t, mockProject)
	releaseVersion(t, "v0.0.0-test") // должна проиграть переменной

	all, err := newOffices(flags("tick"), nil, io.Discard)
	if err != nil {
		t.Fatalf("офис в режиме клона не собран: %v", err)
	}
	o := all.list[0]
	if o.Source != runner.SourceClone {
		t.Errorf("Source = %q, ожидался %q", o.Source, runner.SourceClone)
	}
	if !runner.IsCommitIdentity(o.Identity) {
		t.Errorf("Identity = %q, не похож на commit клона (с возможным -dirty)", o.Identity)
	}
	if _, err := os.Stat(filepath.Join(home, runner.OfficeDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("поставка распакована при заданном OFFICE_CONFIG_ROOT: %v", err)
	}
}
