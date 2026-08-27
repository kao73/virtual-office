package main

import (
	"fmt"

	"github.com/kao73/virtual-office/internal/runner"
)

// dispatchCheck resolves ctx.Spec.Kind through the checkers map and runs it.
// A dispatch miss (e.g. "llm_judge") fails the check explicitly instead of
// silently skipping it.
func dispatchCheck(ctx CheckContext) CheckResult {
	checker, ok := checkers[ctx.Spec.Kind]
	if !ok {
		return CheckResult{Pass: false, Detail: fmt.Sprintf("check kind %q not implemented", ctx.Spec.Kind)}
	}
	return checker.Run(ctx)
}

// runChecks evaluates every declared check — not just until the first
// failure, so a case with several problems reports all of them in one sweep.
func runChecks(fixtureDir, initialCommit string, result runner.Result, specs []CheckSpec) []CheckResult {
	results := make([]CheckResult, 0, len(specs))
	for _, spec := range specs {
		results = append(results, dispatchCheck(CheckContext{
			FixtureDir:    fixtureDir,
			InitialCommit: initialCommit,
			Result:        result,
			Spec:          spec,
		}))
	}
	return results
}
