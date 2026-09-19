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

	"github.com/kao73/virtual-office/internal/runner"
)

func main() {
	code, err := run(os.Args[1:], os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval-roles:", err)
		os.Exit(2)
	}
	os.Exit(code)
}

func run(args []string, stdout, stderr io.Writer) (int, error) {
	fs := flag.NewFlagSet("eval-roles", flag.ContinueOnError)
	roleFlag := fs.String("role", "", "прогнать кейсы только этой роли")
	caseFlag := fs.String("case", "", "прогнать только этот кейс (требует --role)")
	keepFailedFlag := fs.Bool("keep-failed", false, "не удалять рабочий каталог не-passed кейсов — путь печатается в stderr")
	cloneFlag := fs.Bool("clone", false, "запускать роль через run-agent --clone (бэкенд sbx): "+
		"агент работает на клоне фикстуры внутри песочницы вместо бинд-маунта — лечит разлад "+
		"блокировок Comet Native с бинд-маунтом sbx")
	if err := fs.Parse(args); err != nil {
		return 0, err
	}
	if *caseFlag != "" && *roleFlag == "" {
		return 0, errors.New("--case requires --role")
	}

	repo, err := repoRoot()
	if err != nil {
		return 0, err
	}

	dirs, err := discoverCases(filepath.Join(repo, "evals"), *roleFlag, *caseFlag)
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
			fmt.Fprintln(stderr, "eval-roles: временный каталог сборки не убран:", err)
		}
	}()

	runAgentBin, err := resolveRunAgentBin(repo, binDir)
	if err != nil {
		return 0, err
	}

	outcomes := make([]CaseOutcome, 0, len(cases))
	for _, c := range cases {
		outcomes = append(outcomes, evaluateCase(runAgentBin, repo, c, stderr, *keepFailedFlag, *cloneFlag))
	}

	printSummary(stdout, outcomes)
	return exitCode(outcomes), nil
}

// repoRoot — корень репозитория: оттуда берётся корпус кейсов и оттуда же
// собирается run-agent. Сторож на evals/ обязателен: без него запуск не из
// корня находит пустое множество и зеленеет «0 cases found», как будто
// кейсов действительно нет. Роли здесь не проверяются — их каталог с этого
// этапа лежит в office/, и открывает офис уже дочерний run-agent.
func repoRoot() (string, error) {
	root := os.Getenv(runner.ConfigRootEnv)
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		root = wd
	}
	if info, err := os.Stat(filepath.Join(root, "evals")); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%q не похож на корень репозитория: нет evals/", root)
	}
	return root, nil
}
