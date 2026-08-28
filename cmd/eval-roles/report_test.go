package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestExitCode напрямую проверяет тройной контракт кода выхода: 0 — чистый
// pass, 1 — failed-но-не-errored, 2 — как только хоть один кейс errored, даже
// рядом с failed или passed кейсами.
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

// TestPrintSummaryRendersErroredCase подтверждает, что ветка ERROR
// действительно печатает что-то различимое — имя кейса и текст самой
// ошибки, — а не только то, что счётчик его учёл.
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
