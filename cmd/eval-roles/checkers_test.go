package main

import (
	"testing"

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
