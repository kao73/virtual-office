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
	withLookPath(t, "git", "claude")
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
	withLookPath(t, "git", "claude")
	payloadFixture(t, "v0.9.0") // office/ ещё не создан — как сразу после runner init

	// --backend local: тест про office:stale-snapshots, не про sbx.
	var out bytes.Buffer
	if err := doctorCommand([]string{"--backend", "local"}, &out); err != nil {
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
	withLookPath(t, "git", "claude")
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

func TestDoctorChecksCometToolOnlyWhenForgeConfigured(t *testing.T) {
	withLookPath(t, "git", "claude") // comet намеренно отсутствует
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
	withLookPath(t, "git", "claude")
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
	if strings.Contains(printed, "jira:") || strings.Contains(printed, "cred:") {
		t.Errorf("проверки credential/JIRA не должны были запуститься:\n%s", printed)
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

func TestCheckLinkTypeSkippedWhenNotConfigured(t *testing.T) {
	f := checkLinkType(&fakeJiraChecker{linkErr: errors.New("не должен был позваться")}, "")
	if f.level != "ok" {
		t.Errorf("пустой depends_on_link должен быть ok без вызова: %+v", f)
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
	withLookPath(t, "git", "claude")
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
	withLookPath(t, "git", "claude") // sbx намеренно отсутствует
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
	withLookPath(t, "git", "claude")
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
	withLookPath(t, "git", "claude")
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
	// fail и сломал бы точное "doctor: 1 check(s) failed".
	var out bytes.Buffer
	err := doctorCommand([]string{"--backend", "local"}, &out)
	if err == nil || err.Error() != "doctor: 1 check(s) failed" {
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
	withLookPath(t, "git", "claude", "sbx")
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
