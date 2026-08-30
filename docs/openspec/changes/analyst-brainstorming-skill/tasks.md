## 1. Vendor the skills

- [x] 1.1 Copy Superpowers `brainstorming` v6.3.0 (github.com/obra/superpowers, commit `b36e0829c6d0140e93cfef2ca599b1b07d4a7797`) into `skills/brainstorming/`, matching the copy already validated on `experiment/skill-question-spike`
- [x] 1.2 Copy Superpowers `writing-plans`, same repo/tag/commit, into `skills/writing-plans/`, matching the copy already validated on `experiment/writing-plans-handoff-spike`
- [x] 1.3 Add a plain source-record file per skill (e.g. `skills/brainstorming/SOURCE.md`, `skills/writing-plans/SOURCE.md`) naming the repo, tag, and commit — informational only, no automated check

## 2. Analyst role changes

- [x] 2.1 `roles/analyst/role.yaml`: set `skills: [brainstorming, writing-plans]`
- [x] 2.2 `roles/analyst/role.md`: add the dispatcher — always invoke `Skill: office-role-analyst:brainstorming` at the start of a new task
- [x] 2.3 `roles/analyst/role.md`: add the resume-skip clause — do not re-invoke the skill when prior work (a committed spec/plan, wherever it was saved, or the role's own past `artifacts` visible in the tracker history) already reflects any human answer received
- [x] 2.4 `roles/analyst/role.md`: remove the "Три файла в каталоге изменения" instruction as the mandatory output location — let `brainstorming`/`writing-plans` save wherever their own conventions call for
- [x] 2.5 `roles/analyst/role.md`: add the artifacts rule — every file actually created or modified, wherever it lands, must be named in `result.json`'s `artifacts` field
- [x] 2.6 `roles/analyst/role.md`: add the Execution Handoff rule — when `writing-plans` asks which execution approach to use, never answer and never invoke `subagent-driven-development` or `executing-plans`; the role's work ends once the plan is saved, unedited

## 3. Eval coverage

- [x] 3.1 Add `evals/analyst/<case>-ambiguous-decision/`: a task with a genuine, repository-unresolvable ambiguity; expect `needs_human` with a correctly shaped `questions[]`
- [ ] 3.2 Add `evals/analyst/<case>-resume-no-reinvoke/`: a fixture with prior work already present (spec/plan committed wherever `brainstorming`/`writing-plans` would have put it) and a human answer already recorded; expect `done` without a second `Skill` invocation
- [ ] 3.3 Confirm `result.json`'s `artifacts` field names the real files in both new cases — this is now the only link between a freely-placed file and its task

## 4. Verification

- [ ] 4.1 Run the two new `analyst` golden cases from section 3; confirm they pass
- [ ] 4.2 Run `go test ./...`
