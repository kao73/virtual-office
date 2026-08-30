## Purpose

Lets a headless office role draw on external, versioned skills' expertise while still terminating safely and deterministically when those skills' own instructions assume a live human participant, or a hand-off to a capability, the role does not have.

## ADDED Requirements

### Requirement: Human-approval fallback
When a role's mounted external skill reaches a point in its own instructions that requires live human approval or an interactive answer, and no interactive question tool is available to the run, the role SHALL end the run through the office's own `needs_human` outcome with a typed, answerable question, rather than stalling, erroring uncontrolled, or fabricating consent.

#### Scenario: Skill hits its own approval gate on a genuinely ambiguous task
- **WHEN** `analyst` runs a task whose correct handling depends on a decision the repository cannot resolve on its own, with the external skill invoked
- **THEN** the run ends with `outcome: needs_human` and a `questions[]` entry shaped as `{id, text, options[]}`, answerable by the existing tracker comment protocol

### Requirement: No redundant re-invocation on resume
When a role resumes a task after a human has answered a question a mounted skill's process raised, and prior work already reflects that answer, the role SHALL NOT invoke the skill's process a second time for the same decision.

#### Scenario: Resuming with already-satisfied prior work
- **WHEN** `analyst` runs again on a task whose spec/plan is already committed — wherever the skill's own conventions placed it — and matches the human's answer
- **THEN** the run completes with `outcome: done` and does not call the skill a second time

### Requirement: Every produced artifact is declared, regardless of location
A role using a mounted skill's own file-location conventions SHALL name every file it actually creates or modifies in `result.json`'s `artifacts` field, so that wherever a skill chooses to save its output, that output does not become untraceable to the task that produced it.

#### Scenario: Skill saves output to its own default location
- **WHEN** a mounted skill saves a spec or plan to a location the office's own fixed contract does not name (e.g. `docs/superpowers/specs/...`, `docs/superpowers/plans/...`)
- **THEN** `result.json`'s `artifacts` field lists that exact path

### Requirement: Hand-off steps the role cannot honor are neutralized by instruction, not by editing the skill
When a mounted skill's own process ends with a step that assumes a capability or a live participant the role does not have (an interactive execution choice, a required sub-skill the role isn't granted), the role SHALL follow an explicit dispatcher instruction that ends its run without acting on that step, and SHALL NOT modify the skill's own files or the content it produces to work around the step.

#### Scenario: A skill's terminal step names a capability the role doesn't have
- **WHEN** `writing-plans` reaches its "Execution Handoff" step, naming `subagent-driven-development` or `executing-plans`
- **THEN** `analyst` does not answer the question, does not invoke either named skill, and finishes its run with the plan already saved — unedited, including any header text the skill wrote
