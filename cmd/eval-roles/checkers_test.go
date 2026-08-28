package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// The role's own Summary is the explanation of what it did — on a mismatch
// it belongs in Detail so a failed case can be debugged without re-running.
func TestOutcomeCheckerIncludesSummaryOnMismatch(t *testing.T) {
	result := outcomeChecker{}.Run(CheckContext{
		Spec:   CheckSpec{Kind: "outcome", Expect: "done"},
		Result: runner.Result{Outcome: runner.OutcomeFailed, Summary: "не хватило прав на запись"},
	})
	if result.Pass {
		t.Fatal("mismatch должен провалиться")
	}
	if !strings.Contains(result.Detail, "не хватило прав на запись") {
		t.Errorf("Detail не содержит Summary роли: %q", result.Detail)
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

// changedPaths concatenates two independently-sorted command outputs
// (tracked via `git diff`, untracked via `git ls-files --others`); without
// its own final sort, the union isn't globally sorted whenever a tracked
// path sorts after an untracked one.
func TestChangedPathsReturnsSortedPaths(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	initial := headOf(t, dir)

	// Tracked (staged) change sorts after the untracked one below — a naive
	// tracked-then-untracked concatenation would return them out of order.
	if err := os.WriteFile(filepath.Join(dir, "zzz.txt"), []byte("z\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "add", "zzz.txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "aaa.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths, err := changedPaths(dir, initial)
	if err != nil {
		t.Fatalf("changedPaths: %v", err)
	}
	want := []string{"aaa.txt", "zzz.txt"}
	if !slices.Equal(paths, want) {
		t.Errorf("paths = %v, want %v (sorted)", paths, want)
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
