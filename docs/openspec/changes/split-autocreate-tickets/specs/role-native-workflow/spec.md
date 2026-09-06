## REMOVED Requirements

### Requirement: A human's reply to a split proposal is not re-investigated as Shape work

**Reason**: Replaced (not just edited) — the live spec's version carries a third
scenario, "Confirmed child creation ends the split resume loop", written for
wave 1's manual ticket-creation flow. That scenario has no counterpart in
this change's world: `split-autocreate-tickets` makes ticket creation
deterministic runner behavior (`CompleteSplits`), so the second-confirmation
resume this scenario describes never happens — the manual "have the children
been created?" question it answered no longer exists. OpenSpec's `MODIFIED`
merge requires every scenario in the live requirement to survive into the
replacement block; dropping one is only representable as `REMOVED` + `ADDED`
under the same name. See `docs/notes/analyst-task-splitting.md` and
`design.md`, decision #6, for the full reasoning.

**Migration**: None — no code implements the old third scenario's outcome any
more (`roles/analyst/role.md`'s corresponding resume branch was deleted in
this change's Task 15/16); nothing regresses by this scenario's removal.

## ADDED Requirements

### Requirement: A human's reply to a split proposal is not re-investigated as Shape work
When `analyst` resumes a task whose context shows a human reply to a previously posted `split`
question, `analyst` SHALL NOT invoke `comet native new` or perform a Shape investigation of the
original постановка before branching on that reply: a confirming reply re-affirms `split` without
new investigation, a declining reply proceeds with ordinary Shape on the whole постановка, and any
other reply is treated as new information: `analyst` SHALL respond with `needs_human` and
a clarifying question rather than re-affirming `split` or choosing either path outright — a second
`split` marker is what the runner treats as an unconditional confirmation, and an ambiguous reply
does not warrant one. The
re-affirmed `split` carries the same confirmation question as the original proposal: creation of
the confirmed children is deterministic runner behavior outside any agent run, not something a
human confirms in a further reply, so `analyst` SHALL NOT invent a follow-up question asking
whether the children have been created.

#### Scenario: Confirmed split ends the resumed run without invoking Comet Native
- **WHEN** `analyst` resumes a task and its context shows the human confirmed a previously
  proposed split
- **THEN** the run ends with `outcome: split` again, `comet native new` is never invoked, and the
  summary states that the runner will create and link the proposed child tickets automatically —
  the run does not ask a new question about ticket creation, and does not defer that step to the
  human

#### Scenario: Declined split proceeds with ordinary Shape
- **WHEN** `analyst` resumes a task and its context shows the human declined the proposed split,
  choosing to carry the постановка as one task
- **THEN** `analyst` invokes `comet native new` on the original постановка and continues the
  ordinary Shape investigation
