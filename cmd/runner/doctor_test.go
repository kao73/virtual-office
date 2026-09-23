package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
	"github.com/kao73/virtual-office/internal/tracker"
	"github.com/kao73/virtual-office/internal/tracker/jira"
)

// withLookPath подменяет lookPath на предсказуемый список инструментов — без
// этого тест зависел бы от того, что установлено на машине, где его гоняют.
func withLookPath(t *testing.T, present ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, name := range present {
		set[name] = true
	}
	prev := lookPath
	lookPath = func(file string) (string, error) {
		if set[file] {
			return "/usr/bin/" + file, nil
		}
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPath = prev })
}

// findingPrefix — начало строки, которую печатает concludeExit для находки
// с данным уровнем и check-id. Используется вместо руками посчитанных
// пробелов и не зависит от того, что именно написано в msg.
func findingPrefix(level, check string) string {
	return fmt.Sprintf("%-4s %-28s", level, check)
}

func TestDoctorReportsToolPresence(t *testing.T) {
	withLookPath(t, "git", "claude")
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	if err := doctorCommand(nil, &out); err != nil {
		t.Fatalf("доктор отказал на исправной машине: %v\n%s", err, out.String())
	}
	for _, want := range []string{findingPrefix("ok", "tool:git"), findingPrefix("ok", "tool:claude")} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("нет строки %q:\n%s", want, out.String())
		}
	}
}

func TestDoctorNamesEachMissingToolWithoutStoppingAtFirst(t *testing.T) {
	withLookPath(t /* ни одного инструмента */)
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	err := doctorCommand(nil, &out)
	if err == nil {
		t.Fatal("отсутствующие инструменты не провалили доктора")
	}
	for _, want := range []string{findingPrefix("fail", "tool:git"), findingPrefix("fail", "tool:claude")} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("нет строки %q:\n%s", want, out.String())
		}
	}
}

func TestDoctorChecksSbxToolOnlyOnSbxBackend(t *testing.T) {
	withLookPath(t, "git", "claude") // sbx отсутствует
	fixtureRunner(t, mockProject)

	var outLocal bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &outLocal); err != nil {
		t.Fatalf("local не должен спрашивать sbx: %v\n%s", err, outLocal.String())
	}
	if strings.Contains(outLocal.String(), "tool:sbx") {
		t.Errorf("local всё равно упомянул sbx:\n%s", outLocal.String())
	}

	var outSbx bytes.Buffer
	err := doctorCommand([]string{"--backend", "sbx"}, &outSbx)
	if err == nil || !strings.Contains(outSbx.String(), findingPrefix("fail", "tool:sbx")) {
		t.Errorf("sbx-бэкенд не проверил отсутствующий sbx: %v\n%s", err, outSbx.String())
	}
}

// withSbxNotice подменяет sbxNotice: BasePolicy.run приватен пакету sbx,
// и подменить настоящий вызов sbx нечем иначе.
func withSbxNotice(t *testing.T, notice string, err error) {
	t.Helper()
	prev := sbxNotice
	sbxNotice = func() (string, error) { return notice, err }
	t.Cleanup(func() { sbxNotice = prev })
}

func TestDoctorReportsOpenSandboxNetworkAsWarnNotFail(t *testing.T) {
	withLookPath(t, "git", "claude", "sbx")
	withSbxNotice(t, "сеть машины открыта: example.com разрешён базовой политикой.", nil)
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "sbx"}, &out); err != nil {
		t.Fatalf("открытая сеть не должна ронять доктора: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), findingPrefix("warn", "sbx:network")) {
		t.Errorf("открытая сеть не отмечена как warn:\n%s", out.String())
	}
}

func TestDoctorSkipsSandboxNetworkWhenSbxToolMissing(t *testing.T) {
	withLookPath(t, "git", "claude") // sbx отсутствует
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	_ = doctorCommand([]string{"--backend", "sbx"}, &out)
	if strings.Contains(out.String(), "sbx:network") {
		t.Errorf("без sbx нечем было спрашивать сеть:\n%s", out.String())
	}
}

func TestDoctorSkipsSandboxNetworkOnLocalBackend(t *testing.T) {
	withLookPath(t, "git", "claude", "sbx")
	withSbxNotice(t, "сеть машины открыта", nil)
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("local не должен спрашивать сеть песочницы: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "sbx:network") {
		t.Errorf("local всё равно упомянул sbx:network:\n%s", out.String())
	}
}

func TestDoctorReportsStaleSnapshotsInPayloadMode(t *testing.T) {
	withLookPath(t, "git", "claude")
	home := payloadFixture(t, "v0.9.0")
	officeDir := filepath.Join(home, runner.OfficeDir)
	for _, v := range []string{"v0.9.0", "v0.8.0", "v0.7.0"} {
		if err := os.MkdirAll(filepath.Join(officeDir, v), 0o755); err != nil {
			t.Fatalf("снапшот %s не создан: %v", v, err)
		}
	}
	if err := os.WriteFile(filepath.Join(officeDir, "v0.8.0", "workflow.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := doctorCommand(nil, &out); err != nil {
		t.Fatalf("устаревшие снапшоты не должны ронять доктора: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), findingPrefix("warn", "office:stale-snapshots")) ||
		!strings.Contains(out.String(), "устаревших снапшотов: 2") {
		t.Errorf("не найдены два устаревших снапшота:\n%s", out.String())
	}
	for _, v := range []string{"v0.9.0", "v0.8.0", "v0.7.0"} {
		if _, err := os.Stat(filepath.Join(officeDir, v)); err != nil {
			t.Errorf("снапшот %s пропал — доктор должен быть read-only: %v", v, err)
		}
	}
}

func TestDoctorStaleSnapshotsNotApplicableInCloneMode(t *testing.T) {
	withLookPath(t, "git", "claude")
	fixtureRunner(t, mockProject) // задаёт OFFICE_CONFIG_ROOT -> режим клона

	var out bytes.Buffer
	if err := doctorCommand(nil, &out); err != nil {
		t.Fatalf("режим клона не должен ронять доктора: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), findingPrefix("ok", "office:stale-snapshots")) {
		t.Errorf("режим клона не отмечен как неприменимый:\n%s", out.String())
	}
}

func TestDoctorStaleSnapshotsOkWhenOfficeDirMissing(t *testing.T) {
	withLookPath(t, "git", "claude")
	payloadFixture(t, "v0.9.0") // office/ ещё не создан — как сразу после runner init

	var out bytes.Buffer
	if err := doctorCommand(nil, &out); err != nil {
		t.Fatalf("свежая машина без office/ не должна ронять доктора: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), findingPrefix("ok", "office:stale-snapshots")+" нет") {
		t.Errorf("свежий office/ должен читаться как «нет», не как отказ:\n%s", out.String())
	}
}

func TestDoctorFreshOfficeWithoutProjectsFileStillReportsIndependentChecks(t *testing.T) {
	withLookPath(t, "git", "claude")
	_, home := fixtureRunner(t, "OFF:\n  repo_url: https://example.test/o.git\n  tracker: mock\n  default_branch: master\n")
	if err := os.Remove(filepath.Join(home, tracker.ProjectsLocalFile)); err != nil {
		t.Fatalf("файл не убран: %v", err)
	}

	var out bytes.Buffer
	err := doctorCommand(nil, &out)
	if err == nil {
		t.Fatal("отсутствующий projects.local.yaml должен быть fatal")
	}
	printed := out.String()
	for _, want := range []string{
		findingPrefix("ok", "tool:git"), findingPrefix("ok", "tool:claude"),
		"office:stale-snapshots",
		findingPrefix("fail", "config:projects.local.yaml"),
		findingPrefix("warn", "skip:project-dependent"),
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("нет строки про %q:\n%s", want, printed)
		}
	}
}

func TestDoctorMockOnlyOfficeSkipsJiraEntirelyWithoutTrackerFile(t *testing.T) {
	withLookPath(t, "git", "claude")
	fixtureRunner(t, "OFF:\n  repo_url: https://example.test/o.git\n  tracker: mock\n  default_branch: master\n") // ни одного jira-проекта, tracker.yaml на диске нет

	var out bytes.Buffer
	if err := doctorCommand(nil, &out); err != nil {
		t.Fatalf("mock-only офис не должен отказывать: %v\n%s", err, out.String())
	}
	printed := out.String()
	if strings.Contains(printed, "tracker.yaml") || strings.Contains(printed, "jira:") {
		t.Errorf("mock-only офис не должен трогать jira/tracker.yaml:\n%s", printed)
	}
}

func TestDoctorChecksCometToolOnlyWhenForgeConfigured(t *testing.T) {
	withLookPath(t, "git", "claude") // comet намеренно отсутствует
	fixtureRunner(t, "OFF:\n  repo_url: https://example.test/o.git\n  tracker: mock\n"+
		"  default_branch: master\n  forge: github\n")

	var out bytes.Buffer
	err := doctorCommand(nil, &out)
	if err == nil || !strings.Contains(out.String(), findingPrefix("fail", "tool:comet")) {
		t.Errorf("forge-проект должен требовать comet: %v\n%s", err, out.String())
	}
}

func TestCheckCredentialsNamesUnsetVariableWithoutItsValue(t *testing.T) {
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет-которого-не-должно-быть-в-выводе")
	accounts := jira.Accounts{Default: jira.Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_MISSING"}}

	findings := checkCredentials(accounts)
	var missing finding
	for _, f := range findings {
		if f.check == "cred:JIRA_MISSING" {
			missing = f
		}
	}
	if missing.level != "fail" {
		t.Fatalf("незаданная переменная не отмечена как fail: %+v", missing)
	}
	for _, f := range findings {
		if strings.Contains(f.msg, "секрет-которого-не-должно-быть-в-выводе") {
			t.Error("значение креда попало в вывод")
		}
	}
}

func TestCheckCredentialsDedupsSharedAccount(t *testing.T) {
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")
	accounts := jira.Accounts{
		Default: jira.Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"},
		Roles: map[string]jira.Account{
			"reviewer":    {UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"}, // та же пара
			"implementer": {UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"},
		},
	}

	findings := checkCredentials(accounts)
	if len(findings) != 2 { // JIRA_USER + JIRA_PASSWORD, один раз каждая
		t.Errorf("общая учётка проверена не один раз: %+v", findings)
	}
}

func TestDoctorMalformedTrackerYamlIsolatesOnlyDependentChecks(t *testing.T) {
	withLookPath(t, "git", "claude")
	_, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte("это: не: tracker.yaml: {{{"), 0o644); err != nil {
		t.Fatalf("сломанный tracker.yaml не записан: %v", err)
	}

	var out bytes.Buffer
	err := doctorCommand(nil, &out)
	if err == nil {
		t.Fatal("сломанный tracker.yaml должен быть fatal")
	}
	printed := out.String()
	for _, want := range []string{
		findingPrefix("ok", "tool:git"), findingPrefix("ok", "tool:claude"), "office:stale-snapshots",
		findingPrefix("fail", "config:tracker.yaml"), findingPrefix("warn", "skip:jira"),
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("нет строки про %q:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "jira:") || strings.Contains(printed, "cred:") {
		t.Errorf("проверки credential/JIRA не должны были запуститься:\n%s", printed)
	}
}
