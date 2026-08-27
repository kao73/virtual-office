package main

import (
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
		{Kind: "outcome", Expect: "done"},        // will fail (result below is failed)
		{Kind: "llm_judge"},                      // will fail (unimplemented)
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

func TestEvaluateCaseAggregatesPassed(t *testing.T) {
	bin := buildFakeAgent(t)
	t.Setenv(runAgentBinEnv, bin)
	t.Setenv("FAKE_AGENT_RESULT", `{"outcome":"done","summary":"ok","next_owner":"none"}`)

	root := t.TempDir()
	caseDir := filepath.Join(root, "testrole", "ok-case")
	if err := os.MkdirAll(filepath.Join(caseDir, "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	// materializeFixture (Task 5) commits the fixture tree via `git add -A &&
	// git commit`, which fails on a truly empty tree ("nothing to commit") —
	// so, unlike the brief's literal listing, the fixture needs at least one
	// file. Matches the seeding pattern in fixture_test.go.
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
