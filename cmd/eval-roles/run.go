package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kao73/virtual-office/internal/runner"
)

// dispatchCheck находит ctx.Spec.Kind в карте checkers и запускает её.
// Промах диспетчера (например, "llm_judge") явно проваливает проверку, а не
// молча её пропускает.
func dispatchCheck(ctx CheckContext) CheckResult {
	checker, ok := checkers[ctx.Spec.Kind]
	if !ok {
		return CheckResult{Pass: false, Detail: fmt.Sprintf("check kind %q not implemented", ctx.Spec.Kind)}
	}
	return checker.Run(ctx)
}

// runChecks выполняет каждую объявленную проверку — не только до первого
// провала, так что кейс с несколькими проблемами сообщит их все за один проход.
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

// evaluateCase прогоняет один golden case целиком: материализует его
// фикстуру, вызывает роль через run-agent, выполняет каждую объявленную
// проверку и сводит их в итоговый вердикт.
// keepFailed, когда true, не убирает fixtureDir не-passed кейса — иначе
// разобраться в FAIL можно только повторным (платным) прогоном роли; путь
// сохранённого каталога печатается в stderr.
// clone включает --clone бэкенда sbx для этого прогона (см. runRoleAgent).
func evaluateCase(runAgentBin, repoRoot string, c Case, stderr io.Writer, keepFailed, clone bool) (outcome CaseOutcome) {
	name := c.Role + "/" + c.id()

	fixtureDir, initialCommit, err := materializeFixture(c.dir)
	if err != nil {
		return CaseOutcome{Case: name, Status: "errored", Err: fmt.Errorf("фикстура не подготовлена: %w", err)}
	}
	defer func() {
		if keepFailed && outcome.Status != "passed" {
			fmt.Fprintf(stderr, "eval-roles: %s: рабочий каталог сохранён — %s\n", name, fixtureDir)
			return
		}
		if err := os.RemoveAll(fixtureDir); err != nil {
			fmt.Fprintln(stderr, "eval-roles: временная фикстура не убрана:", err)
		}
	}()

	taskPath := filepath.Join(c.dir, "task.md")
	if _, err := os.Stat(taskPath); err != nil {
		return CaseOutcome{Case: name, Status: "errored", Err: fmt.Errorf("task.md не найден: %w", err)}
	}

	taskKey, warning := discoverFixtureTaskKey(fixtureDir)
	if warning != "" {
		fmt.Fprintf(stderr, "eval-roles: %s: %s\n", name, warning)
	}
	result, _, err := runRoleAgent(runAgentBin, repoRoot, c.Role, fixtureDir, taskPath, taskKey, clone)
	if err != nil {
		return CaseOutcome{Case: name, Status: "errored", Err: err}
	}

	checks := runChecks(fixtureDir, initialCommit, result, c.Checks)
	status, combined := aggregateStatus(checks)
	return CaseOutcome{Case: name, Status: status, Checks: checks, Err: combined}
}

// aggregateStatus сводит результаты проверок кейса к одному вердикту: любой
// Err (инфраструктура) поднимает статус до "errored" и держит его там, даже
// если следующая проверка всего лишь Pass:false — незапустившаяся проверка
// весомее проверки, которая запустилась и нашла роль неправой.
func aggregateStatus(checks []CheckResult) (string, error) {
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
	return status, combined
}
