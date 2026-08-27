package main

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/kao73/virtual-office/internal/runner"
)

// checkers dispatches a CheckSpec.Kind to its Checker. "llm_judge" is
// deliberately absent: a dispatch miss is how the spec's "Unimplemented
// check kind" requirement is satisfied (see dispatchCheck in run.go, Task 11).
var checkers = map[string]Checker{
	"outcome":    outcomeChecker{},
	"diff_scope": diffScopeChecker{},
}

// outcomeChecker asserts Result.Outcome against spec.Expect, and — when
// expect is needs_human and questions_not_empty is set — that Questions
// is non-empty.
type outcomeChecker struct{}

func (outcomeChecker) Run(ctx CheckContext) CheckResult {
	want := runner.Outcome(ctx.Spec.Expect)
	got := ctx.Result.Outcome
	if got != want {
		return CheckResult{Pass: false, Detail: fmt.Sprintf("outcome=%q, expected %q", got, want)}
	}
	if want == runner.OutcomeNeedsHuman && ctx.Spec.QuestionsNotEmpty && len(ctx.Result.Questions) == 0 {
		return CheckResult{Pass: false, Detail: "outcome=needs_human but questions is empty"}
	}
	return CheckResult{Pass: true, Detail: fmt.Sprintf("outcome=%q as expected", got)}
}

// diffScopeChecker fails if any path changed since InitialCommit falls
// outside every glob in spec.Allow. An empty diff always passes, even
// against an empty Allow list.
type diffScopeChecker struct{}

func (diffScopeChecker) Run(ctx CheckContext) CheckResult {
	paths, err := changedPaths(ctx.FixtureDir, ctx.InitialCommit)
	if err != nil {
		return CheckResult{Err: err}
	}

	var outside []string
	for _, path := range paths {
		if !matchesAny(path, ctx.Spec.Allow) {
			outside = append(outside, path)
		}
	}
	if len(outside) > 0 {
		return CheckResult{Pass: false, Detail: "changed outside allow: " + strings.Join(outside, ", ")}
	}
	return CheckResult{Pass: true, Detail: "all changed paths within allow"}
}

// changedPaths returns every path that differs from initialCommit: tracked
// files changed or added (committed or not, via `git diff`) plus untracked
// files the role left behind without staging them (via `git ls-files
// --others`). `git diff` alone misses the second group — a stray untracked
// file is exactly the kind of out-of-scope write this check exists to catch.
func changedPaths(fixtureDir, initialCommit string) ([]string, error) {
	tracked, err := exec.Command("git", "-C", fixtureDir, "diff", "--name-only", initialCommit).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git diff не выполнен: %w: %s", err, tracked)
	}
	untracked, err := exec.Command("git", "-C", fixtureDir, "ls-files", "--others", "--exclude-standard").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git ls-files не выполнен: %w: %s", err, untracked)
	}

	seen := map[string]bool{}
	var paths []string
	for _, raw := range [][]byte{tracked, untracked} {
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if line == "" || seen[line] {
				continue
			}
			seen[line] = true
			paths = append(paths, line)
		}
	}
	return paths, nil
}

func matchesAny(path string, allow []string) bool {
	for _, pat := range allow {
		if globMatch(pat, path) {
			return true
		}
	}
	return false
}
