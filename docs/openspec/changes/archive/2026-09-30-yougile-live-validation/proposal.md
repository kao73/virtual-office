## Why

Every tracker this project has trusted so far (`jira`, `mock`) was
validated by an actual live run, not just by unit tests against fixtures —
per `CLAUDE.md`: "Механика проверена дважды на живом (не полигонном)
проекте." `yougile-adapter-core`, `yougile-dependencies-attachments`, and
`yougile-wiring-and-docs` (`.comet/batches/yougile-tracker-adapter.json`)
together produce a wired-in adapter, but wired-in and fixture-tested is not
the same as proven to work against the real API end to end, driven by real
agent roles. This change (4 of 4, depending on `yougile-wiring-and-docs`)
closes that gap.

## What Changes

- Provision a sandbox YouGile project (reusing `office-polygon` or a fresh
  one — see design.md) with a small demo task graph.
- Run the runner (`tick`/`loop`) with at least one real role against it,
  ideally the full `analyst -> implementer -> reviewer` graph, and observe
  a task move through its full status lifecycle driven entirely by the
  office.
- Exercise the human-reply path at least once (a comment from a
  distinguishable human account, not the office account), confirming the
  account-based human/office distinction works against the real API, not
  just in unit tests.
- Record findings (what worked, what didn't, any follow-up fixes needed)
  the same way `docs/notes/stage-N-retro.md` and prior live-run retros do
  for this project.

This change intentionally produces no new adapter code as a goal. If
validation surfaces a structural problem in the adapter, wiring, or docs,
the fix is scoped back into `yougile-adapter-core`,
`yougile-dependencies-attachments`, or `yougile-wiring-and-docs` rather
than absorbed silently here.

## Capabilities

(none — this change is a live validation exercise, not a system behavior
change; `.openspec.yaml` sets `skip_specs: true`)

## Impact

- No production code changes expected.
- A real, dedicated non-human YouGile account (provisioned in
  `yougile-wiring-and-docs`) must exist and be usable before this change
  can start.
- Produces a retro/findings note, likely under `docs/notes/`, documenting
  the outcome.
