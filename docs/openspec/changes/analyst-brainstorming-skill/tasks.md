## 1. Vendor the skill

- [ ] 1.1 Copy Superpowers `brainstorming` v6.3.0 (github.com/obra/superpowers, commit `b36e0829c6d0140e93cfef2ca599b1b07d4a7797`) into `skills/brainstorming/`, matching the copy already validated on `experiment/skill-question-spike`
- [ ] 1.2 Add a plain source-record file (e.g. `skills/brainstorming/SOURCE.md`) naming the repo, tag, and commit — informational only, no automated check

## 2. Analyst role changes

- [ ] 2.1 `roles/analyst/role.yaml`: set `skills: [brainstorming]`
- [ ] 2.2 `roles/analyst/role.md`: add the dispatcher — always invoke `Skill: office-role-analyst:brainstorming` at the start of a new task
- [ ] 2.3 `roles/analyst/role.md`: add the resume-skip clause — do not re-invoke the skill when a committed plan already reflects any human answer received
- [ ] 2.4 `roles/analyst/role.md`: add the scope cap — use only the skill's classification/questions/approach-comparison; always write the outcome to the existing `brief.md`/`design.md`/`tasks.md`; never write to `docs/superpowers/` or invoke `writing-plans`

## 3. Plan template enrichment

- [ ] 3.1 `roles/analyst/templates/tasks.md`: add per-task structure — exact files touched, what the task consumes/produces, embedded test code where useful
- [ ] 3.2 `roles/analyst/templates/tasks.md` / `role.md`: add the no-placeholder rule (no "TBD", "similar to task N", or equivalent)

## 4. Implementer plan discovery

- [ ] 4.1 `internal/runner/input.go`: remove the computed "План: `<path>`" line from `context.md`
- [ ] 4.2 `roles/implementer/role.md`: replace the "context names `tasks.md`" wording with an instruction to check the (already-named) change directory itself for a committed plan

## 5. Eval coverage

- [ ] 5.1 Add `evals/analyst/<case>-ambiguous-decision/`: a task with a genuine, repository-unresolvable ambiguity; expect `needs_human` with a correctly shaped `questions[]`
- [ ] 5.2 Add `evals/analyst/<case>-resume-no-reinvoke/`: a fixture with an already-committed plan and a human answer already recorded; expect `done` without a second `Skill` invocation

## 6. Verification

- [ ] 6.1 Run existing `implementer` golden cases in `eval-roles` unmodified; confirm no regression from task 4.2's wording change
- [ ] 6.2 Run the two new `analyst` golden cases from section 5; confirm they pass
- [ ] 6.3 Run `go test ./...`
