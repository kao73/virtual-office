package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestExitCode exercises the three-way exit-code contract directly: 0 clean
// pass, 1 failed-but-not-errored, 2 whenever any case errored — even
// alongside failed or passed cases.
func TestExitCode(t *testing.T) {
	cases := []struct {
		name     string
		outcomes []CaseOutcome
		want     int
	}{
		{
			name:     "all passed",
			outcomes: []CaseOutcome{{Status: "passed"}, {Status: "passed"}},
			want:     0,
		},
		{
			name:     "failed, no errored",
			outcomes: []CaseOutcome{{Status: "passed"}, {Status: "failed"}},
			want:     1,
		},
		{
			name:     "errored alongside passed and failed",
			outcomes: []CaseOutcome{{Status: "passed"}, {Status: "failed"}, {Status: "errored"}},
			want:     2,
		},
		{
			name:     "only errored",
			outcomes: []CaseOutcome{{Status: "errored"}},
			want:     2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := exitCode(tc.outcomes); got != tc.want {
				t.Errorf("exitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestPrintSummaryRendersErroredCase confirms the ERROR branch actually
// prints something distinguishable — the case name and the underlying
// error text — not just that the tally counts it.
func TestPrintSummaryRendersErroredCase(t *testing.T) {
	var out bytes.Buffer
	outcomes := []CaseOutcome{
		{Case: "testrole/broken-case", Status: "errored", Err: errors.New("фикстура не подготовлена: boom")},
	}
	printSummary(&out, outcomes)

	rendered := out.String()
	if !strings.Contains(rendered, "ERROR") {
		t.Errorf("сводка не содержит ERROR-маркер:\n%s", rendered)
	}
	if !strings.Contains(rendered, "testrole/broken-case") {
		t.Errorf("сводка не называет errored-кейс:\n%s", rendered)
	}
	if !strings.Contains(rendered, "boom") {
		t.Errorf("сводка не несёт текст ошибки:\n%s", rendered)
	}
	if !strings.Contains(rendered, "1 cases: 0 passed, 0 failed, 1 errored") {
		t.Errorf("итоговая строка не учла errored-кейс:\n%s", rendered)
	}
}
