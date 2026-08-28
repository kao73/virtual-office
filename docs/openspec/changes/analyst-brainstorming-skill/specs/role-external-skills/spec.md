## Purpose

Lets a headless office role draw on an external, versioned skill's expertise while still terminating safely and deterministically when that skill's own instructions assume a live human participant it does not have.

## ADDED Requirements

### Requirement: Human-approval fallback
When a role's mounted external skill reaches a point in its own instructions that requires live human approval or an interactive answer, and no interactive question tool is available to the run, the role SHALL end the run through the office's own `needs_human` outcome with a typed, answerable question, rather than stalling, erroring uncontrolled, or fabricating consent.

#### Scenario: Skill hits its own approval gate on a genuinely ambiguous task
- **WHEN** `analyst` runs a task whose correct handling depends on a decision the repository cannot resolve on its own, with the external skill invoked
- **THEN** the run ends with `outcome: needs_human` and a `questions[]` entry shaped as `{id, text, options[]}`, answerable by the existing tracker comment protocol

### Requirement: No redundant re-invocation on resume
When a role resumes a task after a human has answered a question the mounted skill's process raised, and the committed plan already reflects that answer, the role SHALL NOT invoke the skill's process a second time for the same decision.

#### Scenario: Resuming with an already-satisfied plan
- **WHEN** `analyst` runs again on a task whose plan is already committed to the change directory and matches the human's answer
- **THEN** the run completes with `outcome: done` and does not call the skill a second time

### Requirement: Skill output reconciles with the role's fixed artifact contract
Regardless of what location or hand-off convention a mounted skill's own instructions describe, the role SHALL always record the outcome of using that skill in its own existing artifact contract (the change directory's `brief.md`/`design.md`/`tasks.md`), and SHALL NOT follow the skill's own conventions for where to save output or which other skill to hand off to next.

#### Scenario: Skill's own instructions suggest a different save location or hand-off
- **WHEN** the mounted skill's process would otherwise save output elsewhere or recommend invoking a further skill
- **THEN** the role's actual output still lands in the change directory's existing three files, and no other skill is invoked as a hand-off

### Requirement: Task plans specify concrete, checkable detail
A task plan produced with the mounted skill's help SHALL name the exact files each task touches and what each task's change consumes or produces, and SHALL NOT contain placeholder text standing in for real content.

#### Scenario: A completed task plan is reviewed for placeholders
- **WHEN** `tasks.md` is read after `analyst` finishes a task
- **THEN** every task item names concrete file paths and has no "TBD", "similar to task N", or equivalent placeholder text
