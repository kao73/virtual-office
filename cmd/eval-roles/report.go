package main

import (
	"fmt"
	"io"
)

// printSummary печатает по строке на кейс (PASS/FAIL/ERROR), детали каждой
// непройденной проверки под провалившимся кейсом, ошибку под errored-кейсом
// и итоговую строку со счётом.
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

// exitCode повторяет собственное деление run-agent'а 1=поведение/2=инфра, но
// уровнем выше: 0 — чистый pass, 1 — чистый прогон, но хоть один кейс failed,
// 2 — если хоть один кейс errored (или сам харнесс вообще не смог
// запуститься — см. main.go).
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
