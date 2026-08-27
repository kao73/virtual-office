package main

import (
	"path/filepath"

	"github.com/kao73/virtual-office/internal/runner"
)

// Case — golden case parsed from expect.yaml.
type Case struct {
	Role   string      `yaml:"role"`
	Checks []CheckSpec `yaml:"checks"`
	dir    string      // set by the loader, not read from YAML
}

// id — the case's own directory name, e.g. "capability-basic-plan".
func (c Case) id() string { return filepath.Base(c.dir) }

// CheckSpec — one declared check inside expect.yaml's checks list.
type CheckSpec struct {
	Kind string `yaml:"kind"`

	// kind: outcome
	Expect            string `yaml:"expect,omitempty"`
	QuestionsNotEmpty bool   `yaml:"questions_not_empty,omitempty"`

	// kind: diff_scope
	Allow []string `yaml:"allow,omitempty"`

	// kind: fixture_tests
	Command string `yaml:"command,omitempty"`

	// kind: llm_judge — reserved, parsed, no handler yet
	Criteria  string `yaml:"criteria,omitempty"`
	JudgeRole string `yaml:"judge_role,omitempty"`
}

// Checker evaluates one declared check against a role's run.
type Checker interface {
	Run(ctx CheckContext) CheckResult
}

// CheckContext — everything one Checker needs to judge one check.
type CheckContext struct {
	FixtureDir    string // materialized temp git repo
	InitialCommit string // fixture's first commit SHA — input to diff_scope
	Result        runner.Result
	Spec          CheckSpec
}

// CheckResult — one check's verdict.
type CheckResult struct {
	Pass   bool
	Detail string
	Err    error // the check itself couldn't run (infra) — distinct from Pass=false (role behavior)
}

// CaseOutcome — one case's aggregate verdict.
type CaseOutcome struct {
	Case   string // "<role>/<case-id>"
	Status string // "passed" | "failed" | "errored"
	Checks []CheckResult
	Err    error // set when Status == "errored"
}
