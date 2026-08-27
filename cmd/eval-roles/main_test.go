package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunReportsZeroCasesFound(t *testing.T) {
	root := t.TempDir()
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

// tasks.md 2.4: "running the harness against a mix of passing and
// deliberately-failing cases prints a summary that correctly counts both."
func TestRunReportsMixOfPassingAndFailingCases(t *testing.T) {
	root := t.TempDir()
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

func TestRunRejectsCaseFlagWithoutRole(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if _, err := run([]string{"--case", "x"}, &stdout, &stderr); err == nil {
		t.Error("--case без --role должен быть отвергнут")
	}
}
