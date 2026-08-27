package main

import (
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
