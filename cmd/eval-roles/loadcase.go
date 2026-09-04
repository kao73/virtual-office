package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kao73/virtual-office/internal/runner"
	"gopkg.in/yaml.v3"
)

// LoadCase разбирает <dir>/expect.yaml в Case и сверяет её с собственным
// каталогом кейса: role обязан совпадать с именем родительского каталога,
// а checks не должен быть пуст.
func LoadCase(dir string) (Case, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "expect.yaml"))
	if err != nil {
		return Case{}, fmt.Errorf("expect.yaml не прочитан: %w", err)
	}

	var c Case
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Case{}, fmt.Errorf("expect.yaml не разобран: %w", err)
	}
	c.dir = dir

	wantRole := filepath.Base(filepath.Dir(dir))
	if c.Role != wantRole {
		return Case{}, fmt.Errorf("expect.yaml: role=%q не совпадает с каталогом роли %q", c.Role, wantRole)
	}
	if len(c.Checks) == 0 {
		return Case{}, errors.New("expect.yaml: checks пуст")
	}
	for _, chk := range c.Checks {
		// Опечатка в данных не должна стоить платного прогона агента: и
		// неизвестный kind, и пустой обязательный параметр всплывали бы
		// только в dispatchCheck/checkers ПОСЛЕ настоящего прогона роли,
		// и выглядели бы в сводке как FAIL — то есть как регресс роли,
		// а не как сломанный expect.yaml.
		if !knownCheckKind(chk.Kind) {
			return Case{}, fmt.Errorf("expect.yaml: неизвестный kind %q", chk.Kind)
		}
		switch chk.Kind {
		case "outcome":
			// expect — фиксированный список из пяти исходов, в отличие
			// от next_owner: там значением легитимно стоит любое имя роли,
			// и docs/contracts/agent-io.md сознательно не проверяет её
			// существование «на этапе 1» — здесь это же решение соблюдается.
			if strings.TrimSpace(chk.Expect) == "" {
				return Case{}, fmt.Errorf("expect.yaml: outcome-проверка без expect")
			}
			if !knownOutcome(chk.Expect) {
				return Case{}, fmt.Errorf("expect.yaml: outcome-проверка с неизвестным expect %q", chk.Expect)
			}
			// children_count_min при любом другом expect outcomeChecker молча
			// не смотрит вовсе (Run проверяет его только при want ==
			// OutcomeSplit) — то же немое исчезновение опечатки, ради которого
			// заведён весь этот switch, только не всплывающее уже никогда,
			// а не просто после платного прогона.
			if chk.ChildrenCountMin > 0 && chk.Expect != string(runner.OutcomeSplit) {
				return Case{}, fmt.Errorf("expect.yaml: children_count_min задан при expect=%q, а не split", chk.Expect)
			}
		case "fixture_tests":
			// Пустой command не провалился бы сам — `sh -c ""` выходит с
			// кодом 0, и проверка молча зазеленела бы, ничего не проверив.
			// У diff_scope такой ловушки нет: пустой Allow — законное
			// значение (запрет любых изменений).
			if strings.TrimSpace(chk.Command) == "" {
				return Case{}, fmt.Errorf("expect.yaml: fixture_tests-проверка без command")
			}
		}
	}
	return c, nil
}

// knownCheckKind сообщает, известен ли kind: тем же четырём значениям, что
// разбирает CheckSpec (см. types.go), включая ещё не реализованный
// llm_judge — dispatchCheck сам явно проваливает его как «not implemented»,
// и это осознанно необработанный вид, а не опечатка.
func knownCheckKind(kind string) bool {
	switch kind {
	case "outcome", "diff_scope", "fixture_tests", "llm_judge":
		return true
	default:
		return false
	}
}

// knownOutcome сообщает, является ли expect одним из пяти исходов,
// которые вообще способен вернуть агент (internal/runner.Outcome).
func knownOutcome(expect string) bool {
	switch runner.Outcome(expect) {
	case runner.OutcomeDone, runner.OutcomeNeedsHuman, runner.OutcomeBlocked, runner.OutcomeFailed, runner.OutcomeSplit:
		return true
	default:
		return false
	}
}
