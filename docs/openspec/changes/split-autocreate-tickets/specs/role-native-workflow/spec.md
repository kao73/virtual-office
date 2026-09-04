## MODIFIED Requirements

### Requirement: A human's reply to a split proposal is not re-investigated as Shape work
When `analyst` resumes a task whose context shows a human reply to a previously posted `split`
question, `analyst` SHALL NOT invoke `comet native new` or perform a Shape investigation of the
original постановка before branching on that reply: a confirming reply re-affirms `split` without
new investigation, a declining reply proceeds with ordinary Shape on the whole постановка, and any
other reply is treated as new information requiring reassessment before either path is taken. The
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
