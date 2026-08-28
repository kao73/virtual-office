package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withRoles делает root похожим на настоящий корень конфиг-репозитория —
// officeRoot() требует roles/, иначе синтетический тестовый root отвергается
// как непохожий на корень.
func withRoles(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "roles"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRunReportsZeroCasesFound(t *testing.T) {
	root := t.TempDir()
	withRoles(t, root)
	t.Setenv("OFFICE_CONFIG_ROOT", root)

	var stdout, stderr bytes.Buffer
	code, err := run(nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run завершился ошибкой: %v", err)
	}
	if code != 0 {
		t.Errorf("код %d, ожидался 0", code)
	}
	if !strings.Contains(stdout.String(), "0 cases found") {
		t.Errorf("сводка не сказала '0 cases found':\n%s", stdout.String())
	}
}

// tasks.md 2.4: «прогон харнесса на смеси проходящих и намеренно
// проваливающихся кейсов печатает сводку, которая верно считает и те, и другие».
func TestRunReportsMixOfPassingAndFailingCases(t *testing.T) {
	root := t.TempDir()
	withRoles(t, root)
	seed := func(caseID, resultJSON string) {
		dir := filepath.Join(root, "evals", "testrole", caseID)
		if err := os.MkdirAll(filepath.Join(dir, "fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "fixture", ".fake-result.json"), []byte(resultJSON), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "task.md"), []byte("задача\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "expect.yaml"), []byte("role: testrole\nchecks:\n  - kind: outcome\n    expect: done\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	seed("pass-case", `{"outcome":"done","summary":"готово","next_owner":"none"}`)
	seed("fail-case", `{"outcome":"failed","summary":"не вышло","next_owner":"human"}`)

	t.Setenv(runAgentBinEnv, buildFakeAgent(t))
	t.Setenv("OFFICE_CONFIG_ROOT", root)

	var stdout, stderr bytes.Buffer
	code, err := run([]string{"--role", "testrole"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run завершился ошибкой: %v", err)
	}
	if code != 1 {
		t.Errorf("код %d, ожидался 1 (есть failed-кейс): %s", code, stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "pass-case") || !strings.Contains(out, "fail-case") {
		t.Errorf("не все кейсы в сводке:\n%s", out)
	}
	if !strings.Contains(out, "2 cases: 1 passed, 1 failed, 0 errored") {
		t.Errorf("итоговая строка неверна:\n%s", out)
	}
}

// Фиксирует текущее поведение: в отличие от любого провала уровня кейса
// (нет fixture/task.md, инфраструктурная беда run-agent'а), который
// evaluateCase ловит и отражает одной строкой "errored", сломанный
// expect.yaml обрывает весь прогон до того, как выполнится хоть один кейс —
// good-case вообще не получает строки PASS/FAIL.
func TestRunAbortsWholeSweepOnOneBadCase(t *testing.T) {
	root := t.TempDir()
	withRoles(t, root)

	goodDir := filepath.Join(root, "evals", "testrole", "good-case")
	if err := os.MkdirAll(filepath.Join(goodDir, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goodDir, "fixture", ".fake-result.json"), []byte(`{"outcome":"done","summary":"ok","next_owner":"none"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goodDir, "task.md"), []byte("задача\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(goodDir, "expect.yaml"), []byte("role: testrole\nchecks:\n  - kind: outcome\n    expect: done\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// role в expect.yaml не совпадает с каталогом роли — LoadCase её отвергнет.
	badDir := filepath.Join(root, "evals", "testrole", "bad-case")
	if err := os.MkdirAll(filepath.Join(badDir, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(badDir, "expect.yaml"), []byte("role: other\nchecks:\n  - kind: outcome\n    expect: done\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(runAgentBinEnv, buildFakeAgent(t))
	t.Setenv("OFFICE_CONFIG_ROOT", root)

	var stdout, stderr bytes.Buffer
	if _, err := run([]string{"--role", "testrole"}, &stdout, &stderr); err == nil {
		t.Fatal("сломанный expect.yaml одного кейса должен был оборвать весь прогон")
	}
	if strings.Contains(stdout.String(), "good-case") {
		t.Errorf("good-case не должен был выполниться раньше прерывания прогона:\n%s", stdout.String())
	}
}

// Раньше OFFICE_CONFIG_ROOT без roles/ (или его отсутствие вовсе — см. тест
// ниже) молча приводил к "0 cases found" и коду 0 вместо жёсткой ошибки —
// тот же класс беды, что уже закрыт для опечатки в --role/--case.
func TestRunFailsLoudlyWhenConfigRootIsNotOfficeRepo(t *testing.T) {
	root := t.TempDir() // без roles/
	t.Setenv("OFFICE_CONFIG_ROOT", root)

	var stdout, stderr bytes.Buffer
	if _, err := run(nil, &stdout, &stderr); err == nil {
		t.Fatalf("OFFICE_CONFIG_ROOT без roles/ должен быть ошибкой; stdout=%s", stdout.String())
	}
}

// Без OFFICE_CONFIG_ROOT harness использует os.Getwd() — ровно сценарий
// "cd internal && go run ../cmd/eval-roles" из ревью: запуск не из корня
// конфиг-репозитория обязан провалиться, а не напечатать "0 cases found".
func TestRunFailsLoudlyWhenGetwdIsNotOfficeRepo(t *testing.T) {
	t.Setenv("OFFICE_CONFIG_ROOT", "") // не зависеть от окружения, в котором запущен сам go test
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	if _, err := run(nil, &stdout, &stderr); err == nil {
		t.Fatalf("запуск не из конфиг-репозитория офиса должен быть ошибкой; stdout=%s", stdout.String())
	}
}

// Подтверждает, что --keep-failed доходит через run() до evaluateCase, а не
// только работает на уровне отдельного вызова evaluateCase в run_test.go.
func TestRunKeepFailedFlagPreservesFixtureDir(t *testing.T) {
	root := t.TempDir()
	withRoles(t, root)
	dir := filepath.Join(root, "evals", "testrole", "fail-case")
	if err := os.MkdirAll(filepath.Join(dir, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fixture", ".fake-result.json"), []byte(`{"outcome":"failed","summary":"не вышло","next_owner":"human"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task.md"), []byte("задача\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "expect.yaml"), []byte("role: testrole\nchecks:\n  - kind: outcome\n    expect: done\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv(runAgentBinEnv, buildFakeAgent(t))
	t.Setenv("OFFICE_CONFIG_ROOT", root)

	var stdout, stderr bytes.Buffer
	if _, err := run([]string{"--role", "testrole", "--keep-failed"}, &stdout, &stderr); err != nil {
		t.Fatalf("run завершился ошибкой: %v", err)
	}
	if !strings.Contains(stderr.String(), "рабочий каталог сохранён") {
		t.Errorf("--keep-failed не дошёл до evaluateCase, stderr:\n%s", stderr.String())
	}
}

func TestRunRejectsCaseFlagWithoutRole(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if _, err := run([]string{"--case", "x"}, &stdout, &stderr); err == nil {
		t.Error("--case без --role должен быть отвергнут")
	}
}

// Опечатавшийся --role/--case, не совпавший ни с чем, обязан быть жёсткой
// ошибкой, а не молчаливым зелёным выходом «0 cases found» — этот зелёный
// выход зарезервирован за по-настоящему пустым деревом evals/ вообще без
// применённых фильтров.
func TestRunFailsWhenRoleFilterMatchesNothing(t *testing.T) {
	root := t.TempDir()
	withRoles(t, root)
	t.Setenv("OFFICE_CONFIG_ROOT", root)

	var stdout, stderr bytes.Buffer
	code, err := run([]string{"--role", "no-such-role"}, &stdout, &stderr)
	if err == nil {
		t.Fatalf("--role без совпадений должен быть ошибкой; code=%d, stdout=%s", code, stdout.String())
	}
	if !strings.Contains(err.Error(), "no-such-role") {
		t.Errorf("сообщение об ошибке не называет фильтр: %v", err)
	}
}
