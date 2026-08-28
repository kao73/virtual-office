// Команда eval-roles прогоняет фикстуры golden case против ролей офиса через
// существующий путь cmd/run-agent и выдаёт детерминированные pass/fail
// результаты. Запуск всегда ручной: ни CI, ни git-хук её не вызывают.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() {
	code, err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval-roles:", err)
		os.Exit(2)
	}
	os.Exit(code)
}

func run(args []string, stdout, _ io.Writer) (int, error) {
	fs := flag.NewFlagSet("eval-roles", flag.ContinueOnError)
	roleFlag := fs.String("role", "", "run only cases for this role")
	caseFlag := fs.String("case", "", "run only this case id (requires --role)")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if *caseFlag != "" && *roleFlag == "" {
		return 0, errors.New("--case requires --role")
	}

	repoRoot, err := officeRoot()
	if err != nil {
		return 0, err
	}

	dirs, err := discoverCases(filepath.Join(repoRoot, "evals"), *roleFlag, *caseFlag)
	if err != nil {
		return 0, err
	}
	if len(dirs) == 0 {
		if *roleFlag != "" || *caseFlag != "" {
			// Явный фильтр, не совпавший ни с чем, — это опечатка, а не
			// пустое дерево; молчаливый зелёный выход «0 cases found»
			// спрятал бы её.
			return 2, fmt.Errorf("no cases matched --role=%q --case=%q", *roleFlag, *caseFlag)
		}
		fmt.Fprintln(stdout, "0 cases found")
		return 0, nil
	}

	cases := make([]Case, 0, len(dirs))
	for _, dir := range dirs {
		c, err := LoadCase(dir)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", dir, err)
		}
		cases = append(cases, c)
	}

	binDir, err := os.MkdirTemp("", "eval-roles-bin-*")
	if err != nil {
		return 0, err
	}
	defer func() {
		if err := os.RemoveAll(binDir); err != nil {
			fmt.Fprintln(os.Stderr, "eval-roles: временный каталог сборки не убран:", err)
		}
	}()

	runAgentBin, err := resolveRunAgentBin(repoRoot, binDir)
	if err != nil {
		return 0, err
	}

	outcomes := make([]CaseOutcome, 0, len(cases))
	for _, c := range cases {
		outcomes = append(outcomes, evaluateCase(runAgentBin, repoRoot, c))
	}

	printSummary(stdout, outcomes)
	return exitCode(outcomes), nil
}

func officeRoot() (string, error) {
	if root := os.Getenv("OFFICE_CONFIG_ROOT"); root != "" {
		return root, nil
	}
	return os.Getwd()
}
