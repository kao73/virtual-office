## 1. Ledger and budget plumbing

- [x] 1.1 Add an `--eval` bool flag to `cmd/run-agent`; thread it through to the accounting step. Verify: invoking `run-agent --eval ...` writes a ledger entry with `eval: true` in `ledger.jsonl`.
- [x] 1.2 Add `Eval bool \`json:"eval,omitempty"\`` to `ledger.Entry` (`internal/ledger/ledger.go`). Verify: `go test ./internal/ledger/...` passes; an entry with `Eval: true` round-trips through JSON marshal/unmarshal correctly.
- [x] 1.3 Extend `ledger.Filter` with an eval-exclusion option and wire it into `internal/pipeline/budget.go`'s `per_role_daily` daily-spend query. Verify: a unit test with both eval and non-eval ledger entries for a role on the same day shows `per_role_daily` spend counting only the non-eval entries.

## 2. Harness core (`cmd/eval-roles`)

- [x] 2.1 Scaffold `cmd/eval-roles/main.go`: discover case directories under `evals/<role>/<case-id>/`, with an optional flag to filter by role or case name. Verify: running against an empty `evals/` tree reports "0 cases found" without erroring.
- [x] 2.2 Implement fixture materialization: copy a case's `fixture/` into a temp dir, `git init && git add -A && git commit`. Verify: a unit test confirms the temp dir is a valid git repo with the fixture's contents committed.
- [x] 2.3 Implement invocation of `cmd/run-agent --role <role> --workdir <tempdir> --task <case>/task.md --eval` and parse the resulting `result.json` into `internal/runner.Result`. Verify: running one real case end-to-end produces a parsed `Result` matching the actual run outcome.
- [x] 2.4 Implement a pass/fail summary report across all discovered cases (case name, pass/fail, failure detail per failed check). Verify: running the harness against a mix of passing and deliberately-failing cases prints a summary that correctly counts both.

## 3. Check dispatcher and check kinds

- [x] 3.1 Implement `expect.yaml` parsing (`role`, `checks` list) and the `Checker` interface (`Run(caseDir, result) (pass bool, detail string, err error)`). Verify: unit test parses a case using all three MVP check kinds plus a case declaring `llm_judge` without a parse error.
- [x] 3.2 Implement the `outcome` checker (asserts `Outcome` value; when `expect: needs_human`, also asserts non-empty `Questions`). Verify: unit tests cover `done`/`needs_human`/`blocked`/`failed` against synthetic `Result` values.
- [x] 3.3 Implement the `diff_scope` checker (diffs the fixture's working tree against its initial commit, checks changed paths against `allow` globs). Verify: unit test with an in-scope change passing and an out-of-scope change failing.
- [x] 3.4 Implement the `fixture_tests` checker (runs the declared command inside the fixture, checks exit code). Verify: unit test with a fixture whose command passes and one where it deliberately fails.
- [x] 3.5 Implement the dispatcher's explicit failure for `kind: llm_judge` ("check kind not implemented"), not a silent no-op. Verify: a case declaring `llm_judge` causes the harness to report that case as failed with an explicit "not implemented" message.

## 4. Golden case library (MVP: 6 cases)

- [x] 4.1 Author `evals/analyst/capability-basic-plan/` — well-specified task; expect `outcome: done` plus required plan sections present. Verify: passes against the current `analyst` role.
- [x] 4.2 Author `evals/analyst/escalation-ambiguous-task/` — deliberately ambiguous task; expect `outcome: needs_human`, `questions_not_empty: true`. Verify: passes against the current role.
- [x] 4.3 Author `evals/implementer/capability-basic-bugfix/` — fixture with a known bug and a task describing the fix; expect `outcome: done` plus `fixture_tests: go test ./...` green. Verify: passes against the current role.
- [x] 4.4 Author `evals/implementer/escalation-ambiguous-task/` — deliberately ambiguous task; expect `outcome: needs_human`. Verify: passes against the current role.
- [ ] 4.5 Author `evals/reviewer/capability-spot-defect/` — fixture with a deliberately planted defect; expect the review to identify it. Verify: passes against the current role.
- [ ] 4.6 Author `evals/reviewer/escalation-ambiguous-task/` — deliberately ambiguous task; expect `outcome: needs_human`. Verify: passes against the current role.

## 5. End-to-end verification

- [ ] 5.1 Run the full 6-case MVP sweep via `cmd/eval-roles` against the roles' current `role.md`/`role.yaml`. Verify: harness summary reports 6/6 passed.
- [ ] 5.2 Demonstrate the regression signal: temporarily remove the escalation guidance from `implementer`'s `role.md`, re-run its escalation case, confirm it fails, then restore the file. Verify: the case fails while the guidance is removed; `git diff` shows the role file byte-for-byte restored afterward.
- [ ] 5.3 Confirm eval-sweep runs do not change `per_role_daily` budget consumption used by the production pipeline. Verify: compare a role's `per_role_daily` spend calculation before and after running the eval sweep for that role — unchanged.
