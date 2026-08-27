package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

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

// evaluateCase runs one golden case end to end: materialize its fixture,
// invoke the role through run-agent, run every declared check, and
// aggregate the verdict.
func evaluateCase(runAgentBin, repoRoot string, c Case) CaseOutcome {
	name := c.Role + "/" + c.id()

	fixtureDir, initialCommit, err := materializeFixture(c.dir)
	if err != nil {
		return CaseOutcome{Case: name, Status: "errored", Err: fmt.Errorf("фикстура не подготовлена: %w", err)}
	}
	defer func() { _ = os.RemoveAll(fixtureDir) }()

	taskPath := filepath.Join(c.dir, "task.md")
	if _, err := os.Stat(taskPath); err != nil {
		return CaseOutcome{Case: name, Status: "errored", Err: fmt.Errorf("task.md не найден: %w", err)}
	}

	result, _, err := runRoleAgent(runAgentBin, repoRoot, c.Role, fixtureDir, taskPath)
	if err != nil {
		return CaseOutcome{Case: name, Status: "errored", Err: err}
	}

	checks := runChecks(fixtureDir, initialCommit, result, c.Checks)

	status := "passed"
	var errs []error
	for _, r := range checks {
		switch {
		case r.Err != nil:
			status = "errored"
			errs = append(errs, r.Err)
		case !r.Pass && status != "errored":
			status = "failed"
		}
	}
	var combined error
	if status == "errored" {
		combined = errors.Join(errs...)
	}
	return CaseOutcome{Case: name, Status: status, Checks: checks, Err: combined}
}
