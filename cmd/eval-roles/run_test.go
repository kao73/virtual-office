package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kao73/virtual-office/internal/runner"
)

func TestDispatchCheckRejectsUnimplementedKind(t *testing.T) {
	result := dispatchCheck(CheckContext{Spec: CheckSpec{Kind: "llm_judge", Criteria: "x"}})
	if result.Pass {
		t.Error("llm_judge сочтён пройденным")
	}
	if result.Detail != `check kind "llm_judge" not implemented` {
		t.Errorf("детали = %q", result.Detail)
	}
	if result.Err != nil {
		t.Errorf("нереализованный вид — не инфраструктурная беда: %v", result.Err)
	}
}

func TestDispatchCheckRunsKnownKind(t *testing.T) {
	result := dispatchCheck(CheckContext{
		Spec:   CheckSpec{Kind: "outcome", Expect: "done"},
		Result: doneResultForTest(),
	})
	if !result.Pass {
		t.Errorf("известный вид не отработал: %+v", result)
	}
}

func TestRunChecksRunsEveryCheckNotJustFirstFailure(t *testing.T) {
	specs := []CheckSpec{
		{Kind: "outcome", Expect: "done"}, // провалится (результат ниже — failed)
		{Kind: "llm_judge"},               // провалится (не реализован)
	}
	results := runChecks("", "", failedResultForTest(), specs)
	if len(results) != 2 {
		t.Fatalf("получено %d результатов, ожидалось 2 (оба check'а обязаны отработать)", len(results))
	}
	if results[0].Pass || results[1].Pass {
		t.Errorf("оба check'а должны провалиться: %+v", results)
	}
}

func doneResultForTest() runner.Result   { return runner.Result{Outcome: runner.OutcomeDone} }
func failedResultForTest() runner.Result { return runner.Result{Outcome: runner.OutcomeFailed} }

// Каждый golden case в evals/ объявляет несколько проверок и на практике может
// внутри одного кейса иметь и прошедшие, и провалившиеся — но эта смесь была
// не покрыта на уровне юнит-теста (TestRunChecksRunsEveryCheckNotJustFirstFailure
// выше проваливает обе проверки). gitInit/headOf — из checkers_test.go.
func TestRunChecksMixedPassAndFail(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir)
	initial := headOf(t, dir)
	// Случайный неотслеживаемый файл вне allow — diff_scope обязана
	// провалиться, а outcome, оцененная по тому же результату, — пройти.
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	specs := []CheckSpec{
		{Kind: "outcome", Expect: "done"},
		{Kind: "diff_scope", Allow: []string{"src/**"}},
	}
	results := runChecks(dir, initial, doneResultForTest(), specs)
	if len(results) != 2 {
		t.Fatalf("получено %d результатов, ожидалось 2", len(results))
	}
	if !results[0].Pass {
		t.Errorf("outcome check должен был пройти: %+v", results[0])
	}
	if results[1].Pass {
		t.Errorf("diff_scope check должен был провалиться: %+v", results[1])
	}
}

func TestAggregateStatusErroredWhenAnyCheckErrs(t *testing.T) {
	status, err := aggregateStatus([]CheckResult{
		{Pass: true},
		{Err: errors.New("boom")},
	})
	if status != "errored" {
		t.Errorf("status=%q, ожидался errored", status)
	}
	if err == nil {
		t.Error("errored-статус обязан нести объединённую ошибку")
	}
}

// Err ранжируется выше Pass:false: пришедший позже обычный провал не должен
// понизить уже errored-статус до failed.
func TestAggregateStatusErroredNotDowngradedByLaterFailure(t *testing.T) {
	status, _ := aggregateStatus([]CheckResult{
		{Err: errors.New("boom")},
		{Pass: false, Detail: "тоже не так"},
	})
	if status != "errored" {
		t.Errorf("status=%q, ожидался errored (не должен понизиться до failed)", status)
	}
}

func TestEvaluateCaseAggregatesPassed(t *testing.T) {
	bin := buildFakeAgent(t)
	t.Setenv(runAgentBinEnv, bin)
	t.Setenv("FAKE_AGENT_RESULT", `{"outcome":"done","summary":"ok","next_owner":"none"}`)

	root := t.TempDir()
	caseDir := filepath.Join(root, "testrole", "ok-case")
	if err := os.MkdirAll(filepath.Join(caseDir, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	// materializeFixture (Task 5) коммитит дерево фикстуры через `git add -A
	// && git commit`, а это проваливается на по-настоящему пустом дереве
	// («nothing to commit») — так что, в отличие от буквального перечисления
	// в брифе, фикстуре нужен хотя бы один файл. Тот же приём заполнения,
	// что в fixture_test.go.
	if err := os.WriteFile(filepath.Join(caseDir, "fixture", "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "task.md"), []byte("задача\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte("role: testrole\nchecks:\n  - kind: outcome\n    expect: done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCase(caseDir)
	if err != nil {
		t.Fatalf("case не разобран: %v", err)
	}

	outcome := evaluateCase(bin, ".", c)
	if outcome.Status != "passed" {
		t.Errorf("status=%q, ожидался passed: %+v", outcome.Status, outcome)
	}
	if outcome.Case != "testrole/ok-case" {
		t.Errorf("case=%q, ожидался testrole/ok-case", outcome.Case)
	}
}

// Единственный путь, который не гоняет ни один другой тест: diff_scope через
// настоящий конвейер evaluateCase → materializeFixture → runRoleAgent. Он
// доказывает, что собственная запись fakeagent'а в .agent/ исключена из diff
// точно так же, как у настоящего run-agent — иначе каждый golden case,
// использующий diff_scope (4 из 6 под evals/), остался бы непроверенным
// в `go test ./...`.
func TestEvaluateCaseDiffScopeIgnoresAgentDir(t *testing.T) {
	bin := buildFakeAgent(t)
	t.Setenv(runAgentBinEnv, bin)
	t.Setenv("FAKE_AGENT_RESULT", `{"outcome":"done","summary":"ok","next_owner":"none"}`)

	root := t.TempDir()
	caseDir := filepath.Join(root, "testrole", "scope-case")
	if err := os.MkdirAll(filepath.Join(caseDir, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "fixture", "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "task.md"), []byte("задача\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// allow: [] — самая строгая из возможных областей. Проходит, только если
	// .agent/ (единственное, что пишет fakeagent) по-настоящему невидим для
	// git, а не просто случайно отсутствует.
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte("role: testrole\nchecks:\n  - kind: diff_scope\n    allow: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCase(caseDir)
	if err != nil {
		t.Fatalf("case не разобран: %v", err)
	}

	outcome := evaluateCase(bin, ".", c)
	if outcome.Status != "passed" {
		t.Errorf("status=%q, ожидался passed (.agent/ должен быть исключён из diff_scope): %+v", outcome.Status, outcome)
	}
}

func TestEvaluateCaseErrorsOnMissingFixture(t *testing.T) {
	root := t.TempDir()
	caseDir := filepath.Join(root, "testrole", "broken-case")
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(caseDir, "expect.yaml"), []byte("role: testrole\nchecks:\n  - kind: outcome\n    expect: done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadCase(caseDir)
	if err != nil {
		t.Fatalf("case не разобран: %v", err)
	}

	outcome := evaluateCase("/does/not/matter", ".", c)
	if outcome.Status != "errored" {
		t.Errorf("status=%q, ожидался errored (нет fixture/)", outcome.Status)
	}
	if outcome.Err == nil {
		t.Error("errored-исход обязан нести Err")
	}
}
