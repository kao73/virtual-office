package main

import (
	"path/filepath"

	"github.com/kao73/virtual-office/internal/runner"
)

// Case — golden case, разобранный из expect.yaml.
type Case struct {
	Role   string      `yaml:"role"`
	Checks []CheckSpec `yaml:"checks"`
	dir    string      // ставит загрузчик, из YAML не читается
}

// id — имя собственного каталога кейса, например "capability-basic-plan".
func (c Case) id() string { return filepath.Base(c.dir) }

// CheckSpec — одна объявленная проверка из списка checks в expect.yaml.
type CheckSpec struct {
	Kind string `yaml:"kind"`

	// kind: outcome
	Expect            string `yaml:"expect,omitempty"`
	QuestionsNotEmpty bool   `yaml:"questions_not_empty,omitempty"`
	NextOwner         string `yaml:"next_owner,omitempty"`
	// ChildrenCountMin — только вместе с expect: split, необязательно:
	// минимум элементов в split.children[]. Ноль (умолчание) значит «не
	// проверять число». Не точное число: Result.Validate уже гарантирует
	// непустой список, а ровно сколько частей предложит роль — вопрос
	// её собственного суждения о постановке, и не то же самое, что «предложила
	// разбивку хоть на что-то» (единственное, что здесь стоит проверять
	// детерминированно).
	ChildrenCountMin int `yaml:"children_count_min,omitempty"`

	// kind: diff_scope
	Allow []string `yaml:"allow,omitempty"`

	// kind: fixture_tests
	Command string `yaml:"command,omitempty"`

	// kind: llm_judge — зарезервирован, разбирается, обработчика пока нет
	Criteria  string `yaml:"criteria,omitempty"`
	JudgeRole string `yaml:"judge_role,omitempty"`
}

// Checker судит один объявленный check по прогону роли.
type Checker interface {
	Run(ctx CheckContext) CheckResult
}

// CheckContext — всё, что нужно Checker'у, чтобы вынести вердикт по одной проверке.
type CheckContext struct {
	FixtureDir    string // материализованный временный git-репозиторий
	InitialCommit string // SHA первого коммита фикстуры — вход для diff_scope
	Result        runner.Result
	Spec          CheckSpec
}

// CheckResult — вердикт одной проверки.
type CheckResult struct {
	Pass   bool
	Detail string
	Err    error // сама проверка не выполнилась (инфраструктура) — не то же самое, что Pass=false (поведение роли)
}

// CaseOutcome — итоговый вердикт по одному кейсу.
type CaseOutcome struct {
	Case   string // "<role>/<case-id>"
	Status string // "passed" | "failed" | "errored"
	Checks []CheckResult
	Err    error // задан, когда Status == "errored"
}
