## Context

See `proposal.md` - Why. This change depends on `yougile-wiring-and-docs`
(itself depending on `yougile-dependencies-attachments`, which depends on
`yougile-adapter-core`) — the full chain in
`.comet/batches/yougile-tracker-adapter.json`.

`office-polygon` (the YouGile project used throughout this session's
recon) already carries leftover test artifacts: a `office_status_test`
string-sticker with `Ready`/`InProgress` states, left there deliberately
for reuse. It has no real role/workflow graph configured on it yet.

## Goals / Non-Goals

**Goals:**
- Direct, live evidence that the wired-in `yougile` tracker drives a real
  role through a real status lifecycle against the real YouGile API.
- Direct, live evidence that the human/office account distinction holds
  under the real API, not just in fixtures.

**Non-Goals:**
- Broad multi-role, multi-task load testing — one real end-to-end pass is
  the bar this change sets, matching how `jira`/`mock` were first trusted.
- Validating against the live client board (`Clens`) — that board is
  reserved for client acceptance, never for office testing/debugging
  (`clens-not-a-test-bed`).

## Decisions

**Reuse `office-polygon` rather than provisioning a brand-new project.**
It already exists, is already understood, and already carries a
reusable status sticker from this session's recon. Alternative considered:
a fresh dedicated project — rejected as unnecessary churn; `office-polygon`
was always intended as a reusable sandbox, and mixing recon leftovers with
a real validation run is acceptable there in a way it would not be on
`Clens`.

**Validate with a minimal task graph, not the full `analyst ->
implementer -> reviewer` chain, if time-boxing forces a choice.** A single
role exercising claim → work → comment → transition already tests every
adapter method this batch built; the full chain adds realism but not new
adapter coverage. If the full chain is easy to run, prefer it (it's a
better fidelity match to `CLAUDE.md`'s "живой... проект" precedent for
`jira`/`mock`); if not, the minimal version still satisfies this change's
goal.

## Risks / Trade-offs

- [The dedicated non-human YouGile account from `yougile-wiring-and-docs`
  might not exist yet when this change is picked up] → This change cannot
  start without it; treat it as a hard precondition, not a task to redo
  here.
- [Live validation could surface a real defect rather than just confirming
  correctness] → Expected and fine; per proposal.md, fixes get scoped back
  into the change that owns the affected behavior, not patched in place
  here.
