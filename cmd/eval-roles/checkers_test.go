package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/runner"
)

func TestOutcomeChecker(t *testing.T) {
	cases := []struct {
		name string
		spec CheckSpec
		res  runner.Result
		pass bool
	}{
		{"done matches", CheckSpec{Kind: "outcome", Expect: "done"}, runner.Result{Outcome: runner.OutcomeDone}, true},
		{"done mismatch", CheckSpec{Kind: "outcome", Expect: "done"}, runner.Result{Outcome: runner.OutcomeFailed}, false},
		{"needs_human with questions", CheckSpec{Kind: "outcome", Expect: "needs_human", QuestionsNotEmpty: true}, runner.Result{Outcome: runner.OutcomeNeedsHuman, Questions: []runner.Question{{ID: "Q1", Text: "?"}}}, true},
		{"needs_human without questions", CheckSpec{Kind: "outcome", Expect: "needs_human", QuestionsNotEmpty: true}, runner.Result{Outcome: runner.OutcomeNeedsHuman}, false},
		{"blocked matches", CheckSpec{Kind: "outcome", Expect: "blocked"}, runner.Result{Outcome: runner.OutcomeBlocked}, true},
		{"failed matches", CheckSpec{Kind: "outcome", Expect: "failed"}, runner.Result{Outcome: runner.OutcomeFailed}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := outcomeChecker{}.Run(CheckContext{Spec: tc.spec, Result: tc.res})
			if result.Pass != tc.pass {
				t.Errorf("Pass=%v, want %v (%+v)", result.Pass, tc.pass, result)
			}
			if result.Err != nil {
				t.Errorf("outcome-проверка не бывает инфраструктурной бедой: %v", result.Err)
			}
		})
	}
}

func TestCheckersMapHasOutcome(t *testing.T) {
	if _, ok := checkers["outcome"]; !ok {
		t.Error(`checkers["outcome"] не зарегистрирован`)
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t.test", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "init")
}

func headOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func TestDiffScopeCheckerPassesInScopeChange(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	initial := headOf(t, dir)

	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := diffScopeChecker{}.Run(CheckContext{FixtureDir: dir, InitialCommit: initial, Spec: CheckSpec{Allow: []string{"src/**"}}})
	if !result.Pass {
		t.Errorf("in-scope change failed: %+v", result)
	}
}

func TestDiffScopeCheckerFailsOutOfScopeChange(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	initial := headOf(t, dir)

	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// This file is never `git add`-ed — it must still be caught: an
	// untracked stray write is exactly what this check exists to catch.
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := diffScopeChecker{}.Run(CheckContext{FixtureDir: dir, InitialCommit: initial, Spec: CheckSpec{Allow: []string{"src/**"}}})
	if result.Pass {
		t.Errorf("out-of-scope (untracked) change should have failed: %+v", result)
	}
}

func TestDiffScopeCheckerPassesEmptyDiffAgainstEmptyAllow(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	initial := headOf(t, dir)

	result := diffScopeChecker{}.Run(CheckContext{FixtureDir: dir, InitialCommit: initial, Spec: CheckSpec{Allow: nil}})
	if !result.Pass {
		t.Errorf("no changes at all must pass even against an empty allow list: %+v", result)
	}
}

func TestFixtureTestsCheckerPassesOnZeroExit(t *testing.T) {
	dir := t.TempDir()
	checker := fixtureTestsChecker{timeout: 5 * time.Second}
	result := checker.Run(CheckContext{FixtureDir: dir, Spec: CheckSpec{Command: "true"}})
	if !result.Pass {
		t.Errorf("успешная команда не пройдена: %+v", result)
	}
	if result.Err != nil {
		t.Errorf("успех не должен нести Err: %v", result.Err)
	}
}

func TestFixtureTestsCheckerFailsOnNonZeroExit(t *testing.T) {
	dir := t.TempDir()
	checker := fixtureTestsChecker{timeout: 5 * time.Second}
	result := checker.Run(CheckContext{FixtureDir: dir, Spec: CheckSpec{Command: "false"}})
	if result.Pass {
		t.Error("неуспешная команда сочтена пройденной")
	}
	if result.Err != nil {
		t.Errorf("провал команды — обычный Pass=false, не Err: %v", result.Err)
	}
}

func TestFixtureTestsCheckerTimesOut(t *testing.T) {
	dir := t.TempDir()
	checker := fixtureTestsChecker{timeout: 50 * time.Millisecond}
	result := checker.Run(CheckContext{FixtureDir: dir, Spec: CheckSpec{Command: "sleep 5"}})
	if result.Pass {
		t.Error("зависшая команда сочтена успехом")
	}
	if !strings.Contains(result.Detail, "timed out") {
		t.Errorf("детали не говорят о таймауте: %q", result.Detail)
	}
	if result.Err != nil {
		t.Errorf("таймаут — обычный провал проверки, не инфраструктурная беда: %v", result.Err)
	}
}
