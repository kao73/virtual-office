package main

import (
	"fmt"
	"io"
)

// printSummary prints one line per case (PASS/FAIL/ERROR), the detail of
// every non-passing check under a failed case, the error under an errored
// case, and a final tally line.
func printSummary(out io.Writer, outcomes []CaseOutcome) {
	var passed, failed, errored int
	for _, o := range outcomes {
		switch o.Status {
		case "passed":
			passed++
			fmt.Fprintf(out, "PASS  %s\n", o.Case)
		case "failed":
			failed++
			fmt.Fprintf(out, "FAIL  %s\n", o.Case)
			for _, c := range o.Checks {
				if !c.Pass {
					fmt.Fprintf(out, "        %s\n", c.Detail)
				}
			}
		case "errored":
			errored++
			fmt.Fprintf(out, "ERROR %s: %v\n", o.Case, o.Err)
		}
	}
	fmt.Fprintf(out, "\n%d cases: %d passed, %d failed, %d errored\n", len(outcomes), passed, failed, errored)
}

// exitCode mirrors run-agent's own 1=behavioral/2=infra split, one level up:
// 0 clean pass, 1 clean sweep with at least one failed case, 2 if any case
// errored (or the harness itself couldn't run at all — see main.go).
func exitCode(outcomes []CaseOutcome) int {
	hasErrored, hasFailed := false, false
	for _, o := range outcomes {
		switch o.Status {
		case "errored":
			hasErrored = true
		case "failed":
			hasFailed = true
		}
	}
	switch {
	case hasErrored:
		return 2
	case hasFailed:
		return 1
	default:
		return 0
	}
}
