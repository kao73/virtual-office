package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kao73/virtual-office/internal/forge"
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
// с данным уровнем и check-id: уровень, поле шириной 4 (единственное
// действительно фиксированное — "ok"/"fail"/"warn" короче), один пробел,
// сырой check-id без выравнивающих пробелов после. concludeExit сам
// довыравнивает check-id до ширины самого длинного в конкретном прогоне
// (jira:workflow:<проект>:<роль> растёт вместе с ключом проекта), так что
// фиксированную ширину здесь предполагать нельзя — а без неё эта строка
// всё равно остаётся точным префиксом настоящей строки находки и не
// зависит от того, что написано в msg или в чужом check-id.
func findingPrefix(level, check string) string {
	return fmt.Sprintf("%-4s %s", level, check)
}

// findingMessage — точный msg находки с данным level/check в printed
// выводе (первые два whitespace-разделённых токена строки), или "" и
// false, если такой строки нет. Нужен там, где мало знать, что находка
// есть, — важен и её текст, а строить его руками через findingPrefix+" "+…
// сломалось бы о динамическую ширину колонки check-id в concludeExit.
func findingMessage(t *testing.T, printed, level, check string) (string, bool) {
	t.Helper()
	for _, line := range strings.Split(printed, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == level && fields[1] == check {
			return strings.Join(fields[2:], " "), true
		}
	}
	return "", false
}

func TestDoctorReportsToolPresence(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	fixtureRunner(t, mockProject)

	// --backend local: тест про git/claude, не про sbx-бэкенд по умолчанию
	// (Finding 2) — с ним отсутствующий на этой машине sbx дал бы tool:sbx
	// fail и уронил бы доктора мимо темы теста.
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
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

	// --backend local: тест про git/claude конкретно, sbx-бэкенд по
	// умолчанию (Finding 2) тут ни при чём — пин делает это явным, а не
	// побочным эффектом того, что sbx и так отсутствует.
	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
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
	withLookPath(t, "git", "claude", "go", "comet") // sbx отсутствует
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

// TestDoctorRejectsUnknownBackend — опечатка в --backend (например,
// "sbxx") не должна тихо читаться как local и не проверять песочницу
// вовсе: runner tick --backend sbxx на этом же имени отказывает через
// runagent.SandboxesOf, и doctor обязан вести себя так же, а не иначе.
func TestDoctorRejectsUnknownBackend(t *testing.T) {
	withLookPath(t, "git", "claude")
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "bogus"}, &out)
	if err == nil {
		t.Fatal("неизвестный бэкенд должен быть отказом, а не тихим local")
	}
	if out.Len() != 0 {
		t.Errorf("доктор не должен печатать находки при неизвестном --backend:\n%s", out.String())
	}
}

// TestDoctorDefaultBackendIsSbx — регрессия ревью на Finding 2: без
// --backend доктор обязан проверять то же самое, что и голый runner tick
// (sbx), а не тихо откатываться к local. До этого фикса --backend по
// умолчанию был local, и эта проверка ловит именно возврат к старому
// умолчанию, а не только текущее значение runagent.DefaultBackend.
func TestDoctorDefaultBackendIsSbx(t *testing.T) {
	withLookPath(t, "git", "claude") // sbx намеренно отсутствует
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	err := doctorCommand(nil, &out)
	if err == nil || !strings.Contains(out.String(), findingPrefix("fail", "tool:sbx")) {
		t.Errorf("бэкенд по умолчанию должен быть sbx: %v\n%s", err, out.String())
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
	withLookPath(t, "git", "claude", "sbx", "go", "comet")
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
	withLookPath(t, "git", "claude", "sbx", "go", "comet")
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
	withLookPath(t, "git", "claude", "comet")
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

	// --backend local: тест про office:stale-snapshots, не про sbx.
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
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
	withLookPath(t, "git", "claude", "go", "comet")
	fixtureRunner(t, mockProject) // задаёт OFFICE_CONFIG_ROOT -> режим клона

	// --backend local: тест про режим клона office:stale-snapshots, не про sbx.
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("режим клона не должен ронять доктора: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), findingPrefix("ok", "office:stale-snapshots")) {
		t.Errorf("режим клона не отмечен как неприменимый:\n%s", out.String())
	}
}

func TestDoctorStaleSnapshotsOkWhenOfficeDirMissing(t *testing.T) {
	withLookPath(t, "git", "claude", "comet")
	payloadFixture(t, "v0.9.0") // office/ ещё не создан — как сразу после runner init

	// --backend local: тест про office:stale-snapshots, не про sbx.
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("свежая машина без office/ не должна ронять доктора: %v\n%s", err, out.String())
	}
	if msg, ok := findingMessage(t, out.String(), "ok", "office:stale-snapshots"); !ok || msg != "нет" {
		t.Errorf("свежий office/ должен читаться как «нет», не как отказ (%q):\n%s", msg, out.String())
	}
}

func TestDoctorFreshOfficeWithoutProjectsFileStillReportsIndependentChecks(t *testing.T) {
	withLookPath(t, "git", "claude")
	_, home := fixtureRunner(t, "OFF:\n  repo_url: https://example.test/o.git\n  tracker: mock\n  default_branch: master\n")
	if err := os.Remove(filepath.Join(home, tracker.ProjectsLocalFile)); err != nil {
		t.Fatalf("файл не убран: %v", err)
	}

	// --backend local: тест про отсутствующий projects.local.yaml, не про sbx.
	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
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
	withLookPath(t, "git", "claude", "go", "comet")
	fixtureRunner(t, "OFF:\n  repo_url: https://example.test/o.git\n  tracker: mock\n  default_branch: master\n") // ни одного jira-проекта, tracker.yaml на диске нет

	// --backend local: тест про mock-only офис/tracker.yaml, не про sbx.
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("mock-only офис не должен отказывать: %v\n%s", err, out.String())
	}
	printed := out.String()
	if strings.Contains(printed, "tracker.yaml") || strings.Contains(printed, "jira:") {
		t.Errorf("mock-only офис не должен трогать jira/tracker.yaml:\n%s", printed)
	}
}

// TestDoctorChecksCometToolForAnyProjectRegardlessOfForge — регрессия ревью:
// comet раньше проверялся только когда у проекта настроен forge, но
// internal/pipeline/prpass.go зовёт archiveIfReady (а значит и comet) что для
// forge=="" ветки, что для forge!="" — открытие PR идёт после архивирования в
// обоих случаях. Оба текущих полигона этого самого репозитория — без forge,
// то есть старая проверка не ловила самый частый случай.
func TestDoctorChecksCometToolForAnyProjectRegardlessOfForge(t *testing.T) {
	withLookPath(t, "git", "claude", "go") // comet намеренно отсутствует
	fixtureRunner(t, mockProject)          // без forge

	// --backend local: тест про tool:comet, не про sbx.
	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
	if err == nil || !strings.Contains(out.String(), findingPrefix("fail", "tool:comet")) {
		t.Errorf("проект без forge тоже должен требовать comet: %v\n%s", err, out.String())
	}
}

func TestDoctorChecksCometToolWhenForgeConfigured(t *testing.T) {
	withLookPath(t, "git", "claude", "go") // comet намеренно отсутствует
	fixtureRunner(t, "OFF:\n  repo_url: https://example.test/o.git\n  tracker: mock\n"+
		"  default_branch: master\n  forge: github\n")

	// --backend local: тест про tool:comet/forge, не про sbx.
	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
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
	withLookPath(t, "git", "claude", "go", "comet")
	_, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte("это: не: tracker.yaml: {{{"), 0o644); err != nil {
		t.Fatalf("сломанный tracker.yaml не записан: %v", err)
	}

	// --backend local: тест про сломанный tracker.yaml, не про sbx.
	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
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
	// cred:JIRA*, не голое "cred:": cred:GITHUB_TOKEN — про PR-проход
	// (projects.local.yaml), не про сломанный tracker.yaml, и ему положено
	// напечататься даже здесь.
	if strings.Contains(printed, "jira:") || strings.Contains(printed, "cred:JIRA") {
		t.Errorf("проверки credential/JIRA, зависящие от tracker.yaml, не должны были запуститься:\n%s", printed)
	}
}

// fakeJiraChecker — минимальная подделка jiraChecker: подделка отвечает на
// четыре вопроса напрямую, без HTTP-сервера. Полный tracker.Tracker (с
// Claim/Comment/Transition) доктору не нужен — он бы значил побочные
// эффекты, которые спецификация запрещает.
type fakeJiraChecker struct {
	accountErr  error
	fields      []jira.FieldCheck
	fieldsErr   error
	linkErr     error
	workflow    map[string]tracker.WorkflowCheck
	workflowErr error
}

func (f *fakeJiraChecker) CheckAccount() error                     { return f.accountErr }
func (f *fakeJiraChecker) CheckFields() ([]jira.FieldCheck, error) { return f.fields, f.fieldsErr }
func (f *fakeJiraChecker) CheckLinkType() error                    { return f.linkErr }
func (f *fakeJiraChecker) CheckWorkflow(project, workingStatus string) (tracker.WorkflowCheck, error) {
	if f.workflowErr != nil {
		return tracker.WorkflowCheck{}, f.workflowErr
	}
	return f.workflow[project+"|"+workingStatus], nil
}

func TestCheckAccountWrapsMismatchAsFail(t *testing.T) {
	f := checkAccount(&fakeJiraChecker{accountErr: errors.New("учётка office не совпадает с server")})
	if f.level != "fail" || !strings.Contains(f.msg, "server") {
		t.Errorf("несовпадение учётки не отражено: %+v", f)
	}
}

func TestCheckFieldsNamesMissingFieldByConfigAndID(t *testing.T) {
	findings := checkFields(&fakeJiraChecker{fields: []jira.FieldCheck{
		{Config: "lease_until", ID: "customfield_10003", Present: false, ExpectedType: "datetime"},
		{Config: "agent_owner", ID: "customfield_10001", Present: true, ExpectedType: "string", ActualType: "string"},
	}})
	var lease finding
	for _, f := range findings {
		if f.check == "jira:field:lease_until" {
			lease = f
		}
	}
	if lease.level != "fail" || !strings.Contains(lease.msg, "customfield_10003") || !strings.Contains(lease.msg, "lease_until") {
		t.Errorf("пропавшее поле не названо ни именем, ни id: %+v", lease)
	}
}

// TestCheckLinkTypeWarnsWhenNotConfiguredWithoutCallingTracker — регрессия
// внешнего ревью: пустой depends_on_link раньше отчитывался как ok, хотя
// связи «зависит от» без него не работают (LinkDependsOn откажет в
// настоящем прогоне) — warn честнее, а не звать трекер он по-прежнему не
// должен.
func TestCheckLinkTypeWarnsWhenNotConfiguredWithoutCallingTracker(t *testing.T) {
	f := checkLinkType(&fakeJiraChecker{linkErr: errors.New("не должен был позваться")}, "")
	if f.level != "warn" {
		t.Errorf("пустой depends_on_link должен быть warn, не ok, но без вызова трекера: %+v", f)
	}
}

func TestCheckLinkTypeFailsWhenAbsent(t *testing.T) {
	f := checkLinkType(&fakeJiraChecker{linkErr: errors.New(`issueLinkType "Depends" не найден`)}, "Depends")
	if f.level != "fail" {
		t.Errorf("отсутствующий тип связи должен быть fail: %+v", f)
	}
}

func TestCheckWorkflowWarnsOnSelfEntryWithoutFailing(t *testing.T) {
	f := checkWorkflow(&fakeJiraChecker{workflow: map[string]tracker.WorkflowCheck{
		"VO|InProgress": {Sample: "VO-3", SelfEntry: true},
	}}, "VO", "implementer", "InProgress")
	if f.level != "warn" {
		t.Errorf("двух владельцев не должно быть fatal: %+v", f)
	}
}

func TestCheckWorkflowOkWithoutSample(t *testing.T) {
	f := checkWorkflow(&fakeJiraChecker{}, "VO", "implementer", "InProgress")
	if f.level != "ok" {
		t.Errorf("нечего проверять — не отказ: %+v", f)
	}
}

func TestLoadWorkingStatusesReadsShippedWorkflow(t *testing.T) {
	root, _ := fixtureRunner(t, mockProject)
	o := runner.Office{Root: filepath.Join(root, runner.OfficeDir)}

	statuses, err := loadWorkingStatuses(o, nil)
	if err != nil {
		t.Fatalf("workflow.yaml не прочитан: %v", err)
	}
	if statuses["implementer"] != "InProgress" {
		t.Errorf("рабочий статус implementer не найден: %+v", statuses)
	}
}

func TestLoadWorkingStatusesPropagatesResolveError(t *testing.T) {
	sentinel := errors.New("нет личности раннера")
	if _, err := loadWorkingStatuses(runner.Office{}, sentinel); !errors.Is(err, sentinel) {
		t.Errorf("ошибка резолва не дошла: %v", err)
	}
}

// doctorTrackerYAML — tracker.yaml полное настолько, чтобы CheckAccount,
// CheckFields и CheckLinkType все нашли то, что ищут: отдельно от
// office_test.go's trackerYAML(), которое depends_on_link не настраивает
// и статус InProgress не переводит.
func doctorTrackerYAML(baseURL string) string {
	return "base_url: " + baseURL + "\nauth: { mode: basic }\n" +
		"accounts:\n  default: { user_env: JIRA_USER, secret_env: JIRA_PASSWORD }\n" +
		"status_map: { Ready: Ready, InProgress: In Progress, Review: Review, Blocked: Blocked }\n" +
		"fields:\n  agent_owner: customfield_10001\n  run_id: customfield_10002\n" +
		"  lease_until: customfield_10003\n  attempts: customfield_10004\n" +
		"human_flag_label: office-waits-human\n" +
		"depends_on_link: Depends\n"
}

// doctorJiraHandler — минимальный сервер JIRA для доктора: /myself, /field,
// /issueLinkType с ответами, совпадающими с doctorTrackerYAML, и пустой
// /search (задач в рабочем статусе ещё нет — законный результат). Любой
// другой запрос — ошибка теста: доктор не должен звать ничего лишнего.
func doctorJiraHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/rest/api/2/myself":
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "office"})
		case "/rest/api/2/field":
			_ = json.NewEncoder(w).Encode([]map[string]any{
				{"id": "customfield_10001", "schema": map[string]any{"type": "string"}},
				{"id": "customfield_10002", "schema": map[string]any{"type": "string"}},
				{"id": "customfield_10003", "schema": map[string]any{"type": "datetime"}},
				{"id": "customfield_10004", "schema": map[string]any{"type": "number"}},
			})
		case "/rest/api/2/issueLinkType":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issueLinkTypes": []map[string]any{{"name": "Depends"}},
			})
		case "/rest/api/2/search":
			_ = json.NewEncoder(w).Encode(map[string]any{"issues": []any{}})
		default:
			t.Errorf("доктор: неожиданный запрос %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}
}

func TestDoctorFullSuccessPathExitsZero(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	server := httptest.NewServer(doctorJiraHandler(t))
	t.Cleanup(server.Close)
	_, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte(doctorTrackerYAML(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	var out bytes.Buffer
	// --backend local, явно: полный счастливый путь по JIRA — про трекер, не
	// про бэкенд, а --backend по умолчанию теперь sbx (Finding 2) и на машине
	// без sbx уронил бы этот тест находкой tool:sbx, которая ему не по теме.
	// Поведение default-бэкенда отдельно проверяет TestDoctorDefaultBackendIsSbx.
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("доктор отказал на счастливом пути: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"tool:git", "tool:claude", "cred:JIRA_USER", "cred:JIRA_PASSWORD",
		"jira:account", "jira:field:agent_owner", "jira:field:run_id",
		"jira:field:lease_until", "jira:field:attempts", "jira:link-type",
		// Регрессия ревью: удаление checkWorkflow-цикла в doctorCommand не
		// ловил ни один тест — go test ./cmd/runner/... всё равно проходил.
		// VO/implementer — из jiraProject+mockProject'ного office/workflow.yaml
		// фикстуры (единственная роль с рабочим статусом, InProgress).
		"jira:workflow:VO:implementer",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("нет строки про %s:\n%s", want, out.String())
		}
	}
}

func TestDoctorLocalBackendOmitsSbxChecks(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet") // sbx намеренно отсутствует
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("доктор отказал: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "tool:sbx") || strings.Contains(out.String(), "sbx:network") {
		t.Errorf("бэкенд local не должен упоминать sbx:\n%s", out.String())
	}
}

// recordedRequest — метод и путь одного запроса к тестовому серверу JIRA;
// именованный тип нужен затем, чтобы assertNoMutatingRequests был один
// на оба варианта теста (clone и payload), а не копией цикла в каждом.
type recordedRequest struct {
	method string
	path   string
}

// assertNoMutatingRequests — общая часть обоих вариантов
// TestDoctorMakesNoMutatingRequestsOrWrites: доктору разрешён только
// read-only HTTP (GET и POST на /search — это JQL, а не запись).
func assertNoMutatingRequests(t *testing.T, requests []recordedRequest) {
	t.Helper()
	for _, req := range requests {
		// PUT, DELETE, PATCH запрещены — это мутирующие операции.
		if req.method == http.MethodPut || req.method == http.MethodDelete || req.method == http.MethodPatch {
			t.Errorf("доктор отправил %s %s — это мутирующая операция", req.method, req.path)
		}
		// POST разрешён только для /search — это read-only JQL операция в JIRA.
		if req.method == http.MethodPost && req.path != "/rest/api/2/search" {
			t.Errorf("доктор отправил POST %s — POST разрешён только для /rest/api/2/search", req.path)
		}
	}
}

// dirSnapshot — путь, размер и mtime каждого файла и каталога под root, одной
// строкой на запись. len(os.ReadDir(root)) до/после (прежняя версия этого
// теста) ловил только смену числа записей верхнего уровня — не правку
// содержимого существующего файла и не запись вглубь дерева (office/, bin/,
// runs/). filepath.WalkDir здесь обходит каждый уровень, так что новый,
// изменённый или пропавший путь где угодно в дереве превращает before в
// другую строку, чем after.
//
// WalkDir гарантированно обходит в лексическом порядке (godoc), поэтому
// сортировать строки отдельно не нужно — снимок детерминирован сам по себе.
func dirSnapshot(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		kind := "file"
		if info.IsDir() {
			kind = "dir"
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%d\t%d", kind, rel, info.Size(), info.ModTime().UnixNano()))
		return nil
	})
	if err != nil {
		t.Fatalf("снимок %s не построен: %v", root, err)
	}
	return strings.Join(lines, "\n")
}

func TestDoctorMakesNoMutatingRequestsOrWrites(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	var requests []recordedRequest
	handler := doctorJiraHandler(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, recordedRequest{r.Method, r.URL.Path})
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	_, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte(doctorTrackerYAML(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	before := dirSnapshot(t, home)
	// --backend local: тест про побочные эффекты JIRA-пути, не про sbx —
	// sbx по умолчанию (Finding 2) на машине без sbx дал бы tool:sbx fail
	// и уронил бы доктора до сравнения снимков.
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("доктор отказал: %v\n%s", err, out.String())
	}
	assertNoMutatingRequests(t, requests)
	after := dirSnapshot(t, home)
	if before != after {
		t.Errorf("под ${OFFICE_HOME} что-то изменилось:\nбыло:\n%s\nстало:\n%s", before, after)
	}
}

// TestDoctorMakesNoWritesInPayloadMode — тот же вопрос, что у предыдущего
// теста, но в режиме поставки (fixtureRunner задаёт OFFICE_CONFIG_ROOT и
// проверяет только клон): checkStaleOfficeSnapshots читает дерево
// ${OFFICE_HOME}/office/<версия>/ только в режиме поставки
// (payloadFixture, без OFFICE_CONFIG_ROOT) — os.ReadDir каталога версий и
// dirSize по каждому чужому снапшоту. Без этого варианта именно то место,
// где доктор реально ходит по файловой системе, не было прикрыто вовсе.
func TestDoctorMakesNoWritesInPayloadMode(t *testing.T) {
	withLookPath(t, "git", "claude", "comet")
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

	before := dirSnapshot(t, home)
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("доктор отказал: %v\n%s", err, out.String())
	}
	after := dirSnapshot(t, home)
	if before != after {
		t.Errorf("режим поставки: под ${OFFICE_HOME} что-то изменилось:\nбыло:\n%s\nстало:\n%s", before, after)
	}
}

// doctorTrackerYAMLWithReviewerRole — как doctorTrackerYAML, плюс роль
// reviewer со своей парой учётки: нужна, чтобы протухшая переменная одной
// роли не задевала общую учётку, которой доктор открывает трекер.
func doctorTrackerYAMLWithReviewerRole(baseURL string) string {
	return "base_url: " + baseURL + "\nauth: { mode: basic }\n" +
		"accounts:\n" +
		"  default: { user_env: JIRA_USER, secret_env: JIRA_PASSWORD }\n" +
		"  roles:\n" +
		"    reviewer: { user_env: JIRA_REVIEWER_USER, secret_env: JIRA_REVIEWER_PASSWORD }\n" +
		"status_map: { Ready: Ready, InProgress: In Progress, Review: Review, Blocked: Blocked }\n" +
		"fields:\n  agent_owner: customfield_10001\n  run_id: customfield_10002\n" +
		"  lease_until: customfield_10003\n  attempts: customfield_10004\n" +
		"human_flag_label: office-waits-human\n" +
		"depends_on_link: Depends\n"
}

func TestDoctorSingleFatalCredentialFailureAmongPassesExits2(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	server := httptest.NewServer(doctorJiraHandler(t))
	t.Cleanup(server.Close)
	_, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte(doctorTrackerYAMLWithReviewerRole(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")
	t.Setenv("JIRA_REVIEWER_USER", "office-reviewer")
	// JIRA_REVIEWER_PASSWORD нарочно не задан — единственная сломанная проверка;
	// доктор открывает трекер под общей учёткой, и её кред цел.

	// --backend local: подсчёт держится ровно на одном протухшем креде;
	// sbx по умолчанию (Finding 2) на машине без sbx добавил бы ещё один
	// fail и сломал бы точное "doctor: отказавших проверок: 1".
	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
	if err == nil || err.Error() != "doctor: отказавших проверок: 1" {
		t.Fatalf("доктор не отказал ровно на одном протухшем креде: %v", err)
	}
	printed := out.String()
	if !strings.Contains(printed, "cred:JIRA_REVIEWER_PASSWORD") {
		t.Errorf("протухший кред не назван:\n%s", printed)
	}
	for _, want := range []string{"jira:account", "jira:link-type", "cred:JIRA_USER", "cred:JIRA_PASSWORD"} {
		if !strings.Contains(printed, want) {
			t.Errorf("прочие проверки не выполнены при одном отказе (%s):\n%s", want, printed)
		}
	}
}

func TestDoctorOnlyNonFatalFindingsExitsZero(t *testing.T) {
	withLookPath(t, "git", "claude", "sbx", "comet")
	withSbxNotice(t, "сеть машины открыта: example.com разрешён базовой политикой.", nil)
	home := payloadFixture(t, "v0.9.0")
	officeDir := filepath.Join(home, runner.OfficeDir)
	for _, v := range []string{"v0.9.0", "v0.8.0"} {
		if err := os.MkdirAll(filepath.Join(officeDir, v), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "sbx"}, &out); err != nil {
		t.Fatalf("только некритичные находки не должны ронять доктора: %v\n%s", err, out.String())
	}
	printed := out.String()
	if !strings.Contains(printed, findingPrefix("warn", "sbx:network")) ||
		!strings.Contains(printed, findingPrefix("warn", "office:stale-snapshots")) {
		t.Errorf("некритичные находки не напечатаны:\n%s", printed)
	}
}

// TestDoctorResolveFailureIsFatalNotOk — регрессия ревью: отказ
// runner.ResolveOffice раньше читался внутри checkStaleOfficeSnapshots как
// тот же самый "режим клона/разработки", что и легитимный клон — доктор
// печатал ok и выходил 0 на битом OFFICE_CONFIG_ROOT, хотя `runner version`
// на той же самой машине с тем же самым отказом отказывает и выходит 2.
func TestDoctorResolveFailureIsFatalNotOk(t *testing.T) {
	withLookPath(t, "git", "claude", "comet")
	badRoot := t.TempDir() // не git-репозиторий: ConfigSHA откажет
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, tracker.ProjectsLocalFile), []byte(mockProject), 0o644); err != nil {
		t.Fatalf("projects.local.yaml не записан: %v", err)
	}
	t.Setenv(runner.ConfigRootEnv, badRoot)
	t.Setenv(runner.HomeEnv, home)

	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
	if err == nil {
		t.Fatal("битый OFFICE_CONFIG_ROOT должен быть fatal, как у runner version")
	}
	printed := out.String()
	if !strings.Contains(printed, findingPrefix("fail", "office:resolve")) {
		t.Errorf("отказ резолва не назван office:resolve fail:\n%s", printed)
	}
	if !strings.Contains(printed, findingPrefix("warn", "skip:office-dependent")) {
		t.Errorf("пропуск office-зависимых проверок не назван:\n%s", printed)
	}
	// findingPrefix, не голая подстрока: сообщение skip:office-dependent само
	// упоминает "office:stale-snapshots" в тексте, и strings.Contains
	// совпал бы с ним же, а не с отдельной строкой находки.
	if strings.Contains(printed, findingPrefix("ok", "office:stale-snapshots")) ||
		strings.Contains(printed, findingPrefix("warn", "office:stale-snapshots")) {
		t.Errorf("stale-snapshots не должен печататься при отказе резолва — резолв уже провален:\n%s", printed)
	}
}

// TestDoctorStaleSnapshotsUnreadableDirIsWarnNotFatal — регрессия ревью:
// ошибка чтения ${OFFICE_HOME}/office/ (права, ENOTDIR — не "каталога нет")
// печаталась как fail и в одиночку роняла доктора до exit 2, хотя
// спецификация прямо перечисляет stale-snapshots среди находок, которые
// код возврата не должны менять.
func TestDoctorStaleSnapshotsUnreadableDirIsWarnNotFatal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root игнорирует права каталога — отказа чтения не будет")
	}
	withLookPath(t, "git", "claude", "comet")
	home := payloadFixture(t, "v0.9.0")
	officeDir := filepath.Join(home, runner.OfficeDir)
	if err := os.MkdirAll(filepath.Join(officeDir, "v0.9.0"), 0o755); err != nil {
		t.Fatalf("текущая версия не создана: %v", err)
	}
	if err := os.Chmod(officeDir, 0o000); err != nil {
		t.Fatalf("права office/ не изменены: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(officeDir, 0o755) }) // иначе t.TempDir() не уберёт за собой

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("нечитаемый office/ не должен быть фатальным сам по себе: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), findingPrefix("warn", "office:stale-snapshots")) {
		t.Errorf("ошибка чтения office/ должна быть warn, не fail:\n%s", out.String())
	}
}

// TestCheckCredentialsRejectsEmptyButSetVariable — регрессия ревью:
// os.LookupEnv говорит "есть" и для `export X=` (пустое значение), хотя
// jira.OpenAs (internal/tracker/jira/jira.go) такое значение трактует как
// отсутствие креда.
func TestCheckCredentialsRejectsEmptyButSetVariable(t *testing.T) {
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "") // задана переменной, но пусто
	accounts := jira.Accounts{Default: jira.Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"}}

	findings := checkCredentials(accounts)
	var secret finding
	for _, f := range findings {
		if f.check == "cred:JIRA_PASSWORD" {
			secret = f
		}
	}
	if secret.level != "fail" {
		t.Errorf("пустое значение переменной принято за заданное: %+v", secret)
	}
}

// TestCheckCredentialsNamesUnconfiguredHalfOfRoleAccount — регрессия
// ревью: LoadConfig проверяет accounts.default целиком, но не
// accounts.roles.*, так что роль с user_env без своего secret_env (или
// наоборот) грузится без единой находки о пропавшей половине — и
// jira.OpenAs на такой роли ушла бы за секретом в переменную с пустым
// именем.
func TestCheckCredentialsNamesUnconfiguredHalfOfRoleAccount(t *testing.T) {
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")
	accounts := jira.Accounts{
		Default: jira.Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"},
		Roles: map[string]jira.Account{
			"reviewer": {UserEnv: "JIRA_REVIEWER_USER"}, // secret_env забыт
		},
	}

	findings := checkCredentials(accounts)
	var secretHalf finding
	for _, f := range findings {
		if f.check == "cred:reviewer.secret_env" {
			secretHalf = f
		}
	}
	if secretHalf.level != "fail" {
		t.Errorf("незаполненная половина учётки роли не названа: %+v", findings)
	}
}

// TestCheckFieldsWrapsTransportErrorAsSingleFinding — GET /field, упавший
// целиком, отдаёт одну находку jira:fields, а не молчит и не путает её
// с "поле не найдено".
func TestCheckFieldsWrapsTransportErrorAsSingleFinding(t *testing.T) {
	findings := checkFields(&fakeJiraChecker{fieldsErr: errors.New("GET /field: соединение разорвано")})
	if len(findings) != 1 || findings[0].check != "jira:fields" || findings[0].level != "fail" {
		t.Errorf("транспортная ошибка полей не завёрнута в одну находку: %+v", findings)
	}
}

// TestCheckWorkflowWarnsWhenTransportFails — свой отказ trk.CheckWorkflow
// (не путать с отказом чтения workflow.yaml самим доктором) должен остаться
// некритичной находкой, как и прочие workflow-варианты.
func TestCheckWorkflowWarnsWhenTransportFails(t *testing.T) {
	f := checkWorkflow(&fakeJiraChecker{workflowErr: errors.New("GET /search: таймаут")}, "VO", "implementer", "InProgress")
	if f.level != "warn" {
		t.Errorf("отказ самого запроса workflow не должен быть fatal: %+v", f)
	}
}

// TestDoctorJiraOpenFailureEmitsSkipFinding — регрессия ревью: отказ
// jira.Open (конфигурация, не сеть — реальная недоступность инстанса
// проявляется как jira:account) ронял account/fields/link-type/workflow
// молча, без единой находки о том, что они пропущены — в отличие от двух
// соседних стадий (projects.local.yaml, tracker.yaml) той же функции.
func TestDoctorJiraOpenFailureEmitsSkipFinding(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	_, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte(doctorTrackerYAML("jira.example.com")), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
	if err == nil {
		t.Fatal("base_url без схемы должен быть fatal")
	}
	printed := out.String()
	if !strings.Contains(printed, findingPrefix("fail", "jira:open")) {
		t.Errorf("jira:open не назван:\n%s", printed)
	}
	if !strings.Contains(printed, findingPrefix("warn", "skip:jira-checks")) {
		t.Errorf("пропущенные проверки не названы:\n%s", printed)
	}
	// findingPrefix, не голая подстрока: сама строка skip:jira-checks
	// перечисляет "jira:account/fields/link-type/workflow" в своём тексте,
	// и голый strings.Contains(printed, "jira:account") совпал бы с ней же.
	for _, unwanted := range []string{
		findingPrefix("ok", "jira:account"), findingPrefix("fail", "jira:account"),
		findingPrefix("ok", "jira:field:agent_owner"), findingPrefix("fail", "jira:field:agent_owner"),
		findingPrefix("ok", "jira:link-type"), findingPrefix("fail", "jira:link-type"),
	} {
		if strings.Contains(printed, unwanted) {
			t.Errorf("проверки после неоткрытого трекера не должны были запуститься (%q):\n%s", unwanted, printed)
		}
	}
}

// TestDoctorWorkflowLoadFailureEmitsSkipFindingPerProject — регрессия
// ревью: это прямой близнец бага, который команда уже ловила руками
// однажды (удаление checkWorkflow-цикла не проваливало ни один тест) — но
// на стороне ИЗОЛЯЦИИ отказа, не успеха: до этого теста ни один тест не
// приводил doctorCommand к workflowErr != nil от начала до конца.
func TestDoctorWorkflowLoadFailureEmitsSkipFindingPerProject(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	server := httptest.NewServer(doctorJiraHandler(t))
	t.Cleanup(server.Close)
	root, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte(doctorTrackerYAML(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")
	// ResolveOffice по-прежнему проходит (личность раннера цела) — ломается
	// только чтение графа: tracker.LoadWorkflow, а не ResolveOffice.
	workflowPath := filepath.Join(root, runner.OfficeDir, tracker.WorkflowFile)
	if err := os.WriteFile(workflowPath, []byte("это: не: граф: {{{"), 0o644); err != nil {
		t.Fatalf("workflow.yaml не сломан: %v", err)
	}

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("skip:workflow — не критично, доктор не должен отказывать: %v\n%s", err, out.String())
	}
	printed := out.String()
	if !strings.Contains(printed, findingPrefix("warn", "skip:workflow:VO")) {
		t.Errorf("пропуск workflow-проверки для VO не назван:\n%s", printed)
	}
	for _, want := range []string{"jira:account", "jira:field:agent_owner", "jira:link-type"} {
		if !strings.Contains(printed, want) {
			t.Errorf("проверки, не зависящие от workflow.yaml, должны были выполниться (%s):\n%s", want, printed)
		}
	}
}

// TestDoctorMalformedProjectsLocalYamlIsolatesOnlyDependentChecks —
// регрессия ревью: только "файла нет" было проверено для
// projects.local.yaml, хотя спецификация явно требует того же поведения
// и для "не разобрался" — как у tracker.yaml
// (TestDoctorMalformedTrackerYamlIsolatesOnlyDependentChecks).
func TestDoctorMalformedProjectsLocalYamlIsolatesOnlyDependentChecks(t *testing.T) {
	withLookPath(t, "git", "claude", "go")
	_, home := fixtureRunner(t, mockProject)
	if err := os.WriteFile(filepath.Join(home, tracker.ProjectsLocalFile), []byte("это: не: projects: {{{"), 0o644); err != nil {
		t.Fatalf("сломанный projects.local.yaml не записан: %v", err)
	}

	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
	if err == nil {
		t.Fatal("сломанный projects.local.yaml должен быть fatal")
	}
	printed := out.String()
	for _, want := range []string{
		findingPrefix("ok", "tool:git"), findingPrefix("ok", "tool:claude"), "office:stale-snapshots",
		findingPrefix("fail", "config:projects.local.yaml"), findingPrefix("warn", "skip:project-dependent"),
	} {
		if !strings.Contains(printed, want) {
			t.Errorf("нет строки про %q:\n%s", want, printed)
		}
	}
	if strings.Contains(printed, "tool:comet") || strings.Contains(printed, "jira:") {
		t.Errorf("проекто-зависимые проверки не должны были запуститься:\n%s", printed)
	}
}

// TestDoctorStaleTrackerYamlIgnoredWhenNoJiraProject — регрессия ревью:
// только "оба файла отсутствуют" было проверено; проект, переключённый на
// mock, но оставивший старый tracker.yaml на диске, не должен внезапно
// получить попытку его прочитать.
func TestDoctorStaleTrackerYamlIgnoredWhenNoJiraProject(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	_, home := fixtureRunner(t, mockProject) // ни одного jira-проекта
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte("это: не: tracker: {{{"), 0o644); err != nil {
		t.Fatalf("оставшийся tracker.yaml не записан: %v", err)
	}

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("оставшийся tracker.yaml не должен трогаться без jira-проекта: %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "tracker.yaml") || strings.Contains(out.String(), "jira:") {
		t.Errorf("оставшийся tracker.yaml не должен был прочитаться:\n%s", out.String())
	}
}

// TestDoctorRejectsRoleFlag и TestDoctorRejectsJSONFlag — регрессия ревью:
// спецификация явно требует отказывать эти два флага, но до этих тестов
// это держалось только на побочном эффекте flag.ContinueOnError, без
// единого теста, который заметил бы, если кто-то однажды их всё-таки заведёт.
func TestDoctorRejectsRoleFlag(t *testing.T) {
	withLookPath(t, "git", "claude")
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	if err := doctorCommand([]string{"--role", "implementer"}, &out); err == nil {
		t.Error("--role должен быть отказом: спецификация запрещает doctor'у эту привязку")
	}
}

func TestDoctorRejectsJSONFlag(t *testing.T) {
	withLookPath(t, "git", "claude")
	fixtureRunner(t, mockProject)

	var out bytes.Buffer
	if err := doctorCommand([]string{"--json"}, &out); err == nil {
		t.Error("--json должен быть отказом: спецификация требует простого текста")
	}
}

// TestDoctorConfigHomeFailureIsFatalAndNamesSkip — регрессия ревью:
// config:home не сопровождался находкой skip:, в отличие от двух других
// стадий этой же функции с тем же самым классом отказа (файл конфигурации
// не прочитан → зависящие проверки пропущены).
func TestDoctorConfigHomeFailureIsFatalAndNamesSkip(t *testing.T) {
	withLookPath(t, "git", "claude", "go")
	fixtureRunner(t, mockProject) // резолв офиса (клон) не завязан на OFFICE_HOME
	t.Setenv(runner.HomeEnv, "")
	t.Setenv("HOME", "")

	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
	if err == nil {
		t.Fatal("недоступный OFFICE_HOME должен быть fatal")
	}
	printed := out.String()
	if !strings.Contains(printed, findingPrefix("fail", "config:home")) {
		t.Errorf("config:home не назван:\n%s", printed)
	}
	if !strings.Contains(printed, findingPrefix("warn", "skip:project-dependent")) {
		t.Errorf("пропуск зависящих проверок не назван:\n%s", printed)
	}
}

// TestDoctorGithubTokenFatalForForgeProject — регрессия внешнего ревью:
// GITHUB_TOKEN не проверялся вовсе, хотя internal/forge/github.go
// отказывает без него ("без него офис не откроет pull request") — forge-
// проект без токена узнавал бы об этом только в настоящем прогоне.
func TestDoctorGithubTokenFatalForForgeProject(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	fixtureRunner(t, "OFF:\n  repo_url: https://example.test/o.git\n  tracker: mock\n"+
		"  default_branch: master\n  forge: github\n")
	t.Setenv(forge.TokenEnv, "")

	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
	if err == nil || !strings.Contains(out.String(), findingPrefix("fail", "cred:GITHUB_TOKEN")) {
		t.Errorf("forge-проект без GITHUB_TOKEN должен быть fatal: %v\n%s", err, out.String())
	}
}

// TestDoctorGithubTokenWarnForHTTPSProjectWithoutForge — тот же токен нужен
// credentialArgs (internal/workspace/workspace.go) для пуша по HTTPS даже
// без forge; без него пуш уйдёт на системные git-креды — предупреждение,
// не отказ: по ssh их и так достаточно, а HTTPS без токена иногда работает
// (публичный репозиторий, готовый .netrc).
func TestDoctorGithubTokenWarnForHTTPSProjectWithoutForge(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	fixtureRunner(t, mockProject) // https://, без forge
	t.Setenv(forge.TokenEnv, "")

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("HTTPS без GITHUB_TOKEN — предупреждение, не отказ: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), findingPrefix("warn", "cred:GITHUB_TOKEN")) {
		t.Errorf("предупреждение про GITHUB_TOKEN не напечатано:\n%s", out.String())
	}
}

func TestDoctorGithubTokenOkWhenSet(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	fixtureRunner(t, mockProject)
	t.Setenv(forge.TokenEnv, "секрет")

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("заданный GITHUB_TOKEN не должен ронять доктора: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), findingPrefix("ok", "cred:GITHUB_TOKEN")) {
		t.Errorf("заданный GITHUB_TOKEN должен быть ok:\n%s", out.String())
	}
}

// TestCheckCredentialsNamesEachUnconfiguredRoleSeparately — регрессия
// внешнего ревью: дедуп по значению jira.Account считал две РАЗНЫЕ роли,
// у которых обе половины учётки просто не настроены (одинаковый нулевой
// jira.Account{}), одной и той же общей учёткой — называлась только первая
// по алфавиту, вторая пропадала молча.
func TestCheckCredentialsNamesEachUnconfiguredRoleSeparately(t *testing.T) {
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")
	accounts := jira.Accounts{
		Default: jira.Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"},
		Roles: map[string]jira.Account{
			"analyst":  {}, // обе половины не настроены
			"reviewer": {}, // тоже — но это ДРУГАЯ беда, не общая с analyst учётка
		},
	}

	findings := checkCredentials(accounts)
	for _, role := range []string{"analyst", "reviewer"} {
		found := false
		for _, f := range findings {
			if f.check == "cred:"+role+".user_env" {
				found = true
			}
		}
		if !found {
			t.Errorf("роль %s не названа отдельно от других таких же пустых: %+v", role, findings)
		}
	}
}

// TestDoctorWorkflowNotYetUnpackedNamesItClearlyNotRawPath — регрессия
// внешнего ревью: на свежепоставленном офисе, который ни разу не тикнул,
// ResolveOffice(Resolve{}) (Unpack: false) не распаковывает
// office/<версия>/ — workflow.yaml там ещё нет, и человек в главном
// сценарии команды («поставили офис, ещё не тикали») видел бы сырой путь
// ОС вместо объяснения.
func TestDoctorWorkflowNotYetUnpackedNamesItClearlyNotRawPath(t *testing.T) {
	withLookPath(t, "git", "claude", "comet")
	server := httptest.NewServer(doctorJiraHandler(t))
	t.Cleanup(server.Close)
	home := payloadFixture(t, "v0.9.0") // office/v0.9.0/ ещё не распакован
	if err := os.WriteFile(filepath.Join(home, tracker.ProjectsLocalFile), []byte(mockProject+jiraProject), 0o644); err != nil {
		t.Fatalf("projects.local.yaml не переписан: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte(doctorTrackerYAML(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("нераспакованный офис не критичен сам по себе: %v\n%s", err, out.String())
	}
	printed := out.String()
	if !strings.Contains(printed, "офис ещё не распакован") {
		t.Errorf("сообщение не объясняет причину человеку:\n%s", printed)
	}
	if strings.Contains(printed, "no such file or directory") {
		t.Errorf("сырой путь ОС не должен был попасть в вывод:\n%s", printed)
	}
}

// TestCheckCredentialsNamesEachRoleWithSameHalfConfiguredAccountSeparately
// — регрессия внешнего ревью (круг 2): круг 1 дедупил только полностью
// пустой Account{}, но две роли с одинаковой ПОЛОВИНОЙ учётки (обе
// {user_env: JIRA_USER}, без secret_env) — тоже одинаковый непустой acc, и
// дедуп по значению Account всё равно терял вторую роль. Теперь дедуп —
// по уже напечатанному имени переменной, а не по структуре целиком.
func TestCheckCredentialsNamesEachRoleWithSameHalfConfiguredAccountSeparately(t *testing.T) {
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")
	accounts := jira.Accounts{
		Default: jira.Account{UserEnv: "JIRA_USER", SecretEnv: "JIRA_PASSWORD"},
		Roles: map[string]jira.Account{
			"analyst":  {UserEnv: "JIRA_USER"}, // secret_env забыт
			"reviewer": {UserEnv: "JIRA_USER"}, // тоже забыт — но своя, отдельная беда
		},
	}

	findings := checkCredentials(accounts)
	for _, role := range []string{"analyst", "reviewer"} {
		found := false
		for _, f := range findings {
			if f.check == "cred:"+role+".secret_env" {
				found = true
			}
		}
		if !found {
			t.Errorf("роль %s не названа отдельно, хотя её половина учётки совпадает с другой ролью: %+v", role, findings)
		}
	}
	var userEnvCount int
	for _, f := range findings {
		if f.check == "cred:JIRA_USER" {
			userEnvCount++
		}
	}
	if userEnvCount != 1 {
		t.Errorf("JIRA_USER — настоящая общая переменная, должна остаться дедуплицирована один раз: %+v", findings)
	}
}

// TestDoctorWorkflowMissingInCloneModeKeepsOriginalMessage — регрессия
// внешнего ревью (круг 2): в режиме клона ResolveOffice не проверяет
// существование <root>/office вовсе, и тот же самый fs.ErrNotExist от
// LoadWorkflow там означает настоящую пропажу графа, а не «офис ещё не
// распакован» (это верно только для режима поставки, где ResolveOffice
// умышленно не распаковывает).
func TestDoctorWorkflowMissingInCloneModeKeepsOriginalMessage(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	server := httptest.NewServer(doctorJiraHandler(t))
	t.Cleanup(server.Close)
	root, home := fixtureRunner(t, mockProject+jiraProject)
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte(doctorTrackerYAML(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")
	if err := os.Remove(filepath.Join(root, runner.OfficeDir, tracker.WorkflowFile)); err != nil {
		t.Fatalf("workflow.yaml не убран: %v", err)
	}

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("skip:workflow не критично: %v\n%s", err, out.String())
	}
	printed := out.String()
	if strings.Contains(printed, "офис ещё не распакован") {
		t.Errorf("режим клона не распаковывается никогда — сообщение про поставку вводит в заблуждение:\n%s", printed)
	}
	if !strings.Contains(printed, findingPrefix("warn", "skip:workflow:VO")) {
		t.Errorf("пропуск workflow-проверки не назван:\n%s", printed)
	}
}

// TestDoctorReportsUnknownForgeKind — регрессия внешнего ревью (круг 2):
// forgesOf (cmd/runner/office.go) валит любой tick на forge, отличном от
// "github", но круг 1's cred:GITHUB_TOKEN считал любой непустой Forge
// заявкой на токен и печатал ok, даже когда forge вообще не собран.
func TestDoctorReportsUnknownForgeKind(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	fixtureRunner(t, "OFF:\n  repo_url: https://example.test/o.git\n  tracker: mock\n"+
		"  default_branch: master\n  forge: gitlab\n")
	t.Setenv(forge.TokenEnv, "токен") // даже с токеном неизвестный forge — беда

	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
	if err == nil || !strings.Contains(out.String(), findingPrefix("fail", "forge:OFF")) {
		t.Errorf("неизвестный forge должен быть fatal: %v\n%s", err, out.String())
	}
}

// TestDoctorReportsUnparseableRepoURLForForgeProject — та же самая ранняя
// проверка, что NewGitHub (internal/forge/github.go) делает перед
// настоящим прогоном ("узнать в середине прохода было бы поздно").
func TestDoctorReportsUnparseableRepoURLForForgeProject(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	fixtureRunner(t, "OFF:\n  repo_url: https://example.test\n  tracker: mock\n"+
		"  default_branch: master\n  forge: github\n")
	t.Setenv(forge.TokenEnv, "токен")

	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
	if err == nil || !strings.Contains(out.String(), findingPrefix("fail", "forge:OFF")) {
		t.Errorf("неразбираемый repo_url должен быть fatal: %v\n%s", err, out.String())
	}
}

// TestDoctorColumnWidthScalesWithLongProjectKey — регрессия внешнего
// ревью (круг 3): захардкоженная ширина колонки check-id (сперва 28, потом
// 32) рвалась ровно на ключах проекта такой длины — jira:workflow:<проект>:<роль>
// склеивался с сообщением без разделяющего пробела. concludeExit теперь
// считает ширину из самих находок, так что "PLATFORM" (длиннее "VO",
// которым покрыты прочие тесты) обязан пройти без повторения бага.
func TestDoctorColumnWidthScalesWithLongProjectKey(t *testing.T) {
	withLookPath(t, "git", "claude", "go", "comet")
	server := httptest.NewServer(doctorJiraHandler(t))
	t.Cleanup(server.Close)
	longJiraProject := "PLATFORM:\n  repo_url: https://example.test/p.git\n  tracker: jira\n  default_branch: master\n"
	_, home := fixtureRunner(t, mockProject+longJiraProject)
	if err := os.WriteFile(filepath.Join(home, jira.TrackerFile), []byte(doctorTrackerYAML(server.URL)), 0o644); err != nil {
		t.Fatalf("tracker.yaml не записан: %v", err)
	}
	t.Setenv("JIRA_USER", "office")
	t.Setenv("JIRA_PASSWORD", "секрет")

	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
		t.Fatalf("доктор отказал: %v\n%s", err, out.String())
	}
	msg, ok := findingMessage(t, out.String(), "ok", "jira:workflow:PLATFORM:implementer")
	if !ok {
		t.Fatalf("находка для длинного ключа проекта не распознана как отдельная строка:\n%s", out.String())
	}
	if !strings.HasPrefix(msg, "нет образца") {
		t.Errorf("сообщение повреждено — check-id склеился с msg без пробела: %q", msg)
	}
}
