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
		{"split with questions", CheckSpec{Kind: "outcome", Expect: "split", QuestionsNotEmpty: true}, runner.Result{Outcome: runner.OutcomeSplit, Questions: []runner.Question{{ID: "Q1", Text: "?"}}}, true},
		{"split without questions", CheckSpec{Kind: "outcome", Expect: "split", QuestionsNotEmpty: true}, runner.Result{Outcome: runner.OutcomeSplit}, false},
		{"split children_count_min satisfied", CheckSpec{Kind: "outcome", Expect: "split", ChildrenCountMin: 2}, runner.Result{Outcome: runner.OutcomeSplit, Split: &runner.Split{Children: []runner.SplitChild{{ID: "a"}, {ID: "b"}}}}, true},
		{"split children_count_min exceeded is fine", CheckSpec{Kind: "outcome", Expect: "split", ChildrenCountMin: 2}, runner.Result{Outcome: runner.OutcomeSplit, Split: &runner.Split{Children: []runner.SplitChild{{ID: "a"}, {ID: "b"}, {ID: "c"}}}}, true},
		{"split children_count_min unmet", CheckSpec{Kind: "outcome", Expect: "split", ChildrenCountMin: 3}, runner.Result{Outcome: runner.OutcomeSplit, Split: &runner.Split{Children: []runner.SplitChild{{ID: "a"}, {ID: "b"}}}}, false},
		{"split children_count_min unset ignores count", CheckSpec{Kind: "outcome", Expect: "split"}, runner.Result{Outcome: runner.OutcomeSplit, Split: &runner.Split{Children: []runner.SplitChild{{ID: "a"}}}}, true},
		{"blocked matches", CheckSpec{Kind: "outcome", Expect: "blocked"}, runner.Result{Outcome: runner.OutcomeBlocked}, true},
		{"failed matches", CheckSpec{Kind: "outcome", Expect: "failed"}, runner.Result{Outcome: runner.OutcomeFailed}, true},
		{"next_owner matches", CheckSpec{Kind: "outcome", Expect: "done", NextOwner: "implementer"}, runner.Result{Outcome: runner.OutcomeDone, NextOwner: "implementer"}, true},
		{"next_owner mismatches", CheckSpec{Kind: "outcome", Expect: "done", NextOwner: "implementer"}, runner.Result{Outcome: runner.OutcomeDone, NextOwner: "human"}, false},
		{"next_owner unset ignores Result.NextOwner", CheckSpec{Kind: "outcome", Expect: "done"}, runner.Result{Outcome: runner.OutcomeDone, NextOwner: "human"}, true},
		// runner.Result.Validate сравнивает через strings.TrimSpace (internal/runner/agentio.go) —
		// значение с хвостовым пробелом законно по контракту раннера, и здесь должно совпасть так же.
		{"next_owner matches despite surrounding whitespace", CheckSpec{Kind: "outcome", Expect: "done", NextOwner: "implementer"}, runner.Result{Outcome: runner.OutcomeDone, NextOwner: "implementer "}, true},
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

// Собственный Summary роли — объяснение того, что она сделала; при
// несовпадении ему место в Detail, чтобы провалившийся кейс можно было
// отладить без повторного прогона.
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
	// Этот файл никогда не проходит `git add` — и всё равно должен быть
	// пойман: именно неотслеживаемая случайная запись и есть то, что эта
	// проверка призвана ловить.
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := diffScopeChecker{}.Run(CheckContext{FixtureDir: dir, InitialCommit: initial, Spec: CheckSpec{Allow: []string{"src/**"}}})
	if result.Pass {
		t.Errorf("out-of-scope (untracked) change should have failed: %+v", result)
	}
}

// changedPaths склеивает вывод двух независимо отсортированных команд
// (отслеживаемое — через `git diff`, неотслеживаемое — через `git ls-files
// --others`); без собственной финальной сортировки объединение не
// отсортировано глобально всякий раз, когда отслеживаемый путь идёт по
// алфавиту после неотслеживаемого.
func TestChangedPathsReturnsSortedPaths(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	initial := headOf(t, dir)

	// Отслеживаемое (застейдженное) изменение идёт по алфавиту после
	// неотслеживаемого ниже — наивная склейка «сначала отслеживаемое, потом
	// неотслеживаемое» вернула бы их не по порядку.
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

// Сломанный allow-паттерн — это сломанный expect.yaml, а не роль, нарушившая
// область; он обязан всплыть как Err, а не как ложный "changed outside allow".
func TestDiffScopeCheckerReportsErrOnMalformedPattern(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	initial := headOf(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := diffScopeChecker{}.Run(CheckContext{FixtureDir: dir, InitialCommit: initial, Spec: CheckSpec{Allow: []string{"["}}})
	if result.Err == nil {
		t.Errorf("malformed pattern should have produced Err: %+v", result)
	}
}

// git по умолчанию C-квотирует не-ASCII пути (core.quotePath=true); без
// отключения этого кириллическое имя файла вернулось бы из `git diff`/`git
// ls-files` восьмеричноэкранированной строкой в кавычках, не совпадающей ни
// с одним glob'ом.
func TestDiffScopeCheckerHandlesNonASCIIFilenames(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	initial := headOf(t, dir)

	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "имя.md"), []byte("текст\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result := diffScopeChecker{}.Run(CheckContext{FixtureDir: dir, InitialCommit: initial, Spec: CheckSpec{Allow: []string{"docs/**"}}})
	if !result.Pass {
		t.Errorf("in-scope Cyrillic filename failed: %+v", result)
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

// Незапустившаяся команда (здесь: FixtureDir не существует, так что сам
// shell не запустить) — инфраструктурная беда, не вина роли; она обязана
// вернуться как Err, а не как обычный Pass:false.
func TestFixtureTestsCheckerReportsErrWhenCommandCannotStart(t *testing.T) {
	checker := fixtureTestsChecker{timeout: 5 * time.Second}
	result := checker.Run(CheckContext{FixtureDir: "/does/not/exist-xyz", Spec: CheckSpec{Command: "true"}})
	if result.Err == nil {
		t.Errorf("command that couldn't start should have produced Err: %+v", result)
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
