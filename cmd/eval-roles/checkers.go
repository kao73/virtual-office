package main

import (
	"fmt"

	"github.com/kao73/virtual-office/internal/runner"
)

// checkers dispatches a CheckSpec.Kind to its Checker. "llm_judge" is
// deliberately absent: a dispatch miss is how the spec's "Unimplemented
// check kind" requirement is satisfied (see dispatchCheck in run.go, Task 11).
var checkers = map[string]Checker{
	"outcome": outcomeChecker{},
}

// outcomeChecker asserts Result.Outcome against spec.Expect, and — when
// expect is needs_human and questions_not_empty is set — that Questions
// is non-empty.
type outcomeChecker struct{}

func (outcomeChecker) Run(ctx CheckContext) CheckResult {
	want := runner.Outcome(ctx.Spec.Expect)
	got := ctx.Result.Outcome
	if got != want {
		return CheckResult{Pass: false, Detail: fmt.Sprintf("outcome=%q, expected %q", got, want)}
	}
	if want == runner.OutcomeNeedsHuman && ctx.Spec.QuestionsNotEmpty && len(ctx.Result.Questions) == 0 {
		return CheckResult{Pass: false, Detail: "outcome=needs_human but questions is empty"}
	}
	return CheckResult{Pass: true, Detail: fmt.Sprintf("outcome=%q as expected", got)}
}
