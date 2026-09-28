## Why

`internal/tracker.Tracker` today has exactly two implementations — `jira` and
`mock` — and the JIRA test polygon this project develops against periodically
goes unreachable (the machine it runs on is outside our control). A second
real tracker adapter, validated against YouGile's actual REST API rather than
against the thin local `yougile-mcp` tool wrapper, is both a genuine second
capability for the office and the first real test of whether the `Tracker`
abstraction holds against a tracker with a materially different data model
(no native status separate from board column — and, unlike JIRA, no way for
one task to appear on more than one board, confirmed by live research this
session, so the board/column model is embraced directly rather than worked
around; no native custom fields beyond a strictly-validated `extensionData`,
but a reliable free-JSON `apiData` field and native `idempotencyKey`
support).

This change is the first of four (`yougile-adapter-core` →
`yougile-dependencies-attachments` → `yougile-wiring-and-docs` →
`yougile-live-validation`, tracked in
`.comet/batches/yougile-tracker-adapter.json`) and delivers the core
CRUD/lease surface only.

## What Changes

- Add a new package `internal/tracker/yougile` implementing the core,
  non-relational, non-attachment surface of `tracker.Tracker` against
  YouGile's real REST API (`https://yougile.com/api-v2`, documented at
  `https://yougile.com/api-json`): `Whoami`, `Get`, `List`, `ListReady`,
  `ListExpired`, `Claim`, `Renew`, `Release`, `Transition`, `Comment`,
  `SetHumanFlag`, `SetAttempts`, `CreateTask`, `FindByMarker`.
- Model task status directly as the task's board column: `Transition`
  moves `columnId`, and `ListReady`/`List` filter by `columnId` server-side.
  `docs/DESIGN.md`'s "status is not a column" principle was written for
  JIRA, where the same issue can appear on many boards with different
  column groupings of the same statuses; live research this session
  confirmed YouGile has no equivalent (a task lives on exactly one board,
  in exactly one column — see `design.md` Decisions for the full
  reasoning), so the principle's premise does not hold here and a
  dedicated status-sticker would add a second, separately-written state
  field for no corresponding benefit.
- Store the lease fields (`agent_owner`, `run_id`, `lease_until`) in the
  task's `apiData` field, confirmed live to round-trip arbitrary JSON
  exactly (unlike `extensionData`, which is strictly validated and rejects
  arbitrary payloads with `400`).
- Use YouGile's `idempotencyKey` on task creation as the create-time
  idempotency mechanism, confirmed live to deduplicate repeated creates.
- Implement `Claim` with the same write-then-reread-and-verify-owner
  pattern the `jira` adapter already uses, since YouGile has no atomic
  compare-and-swap either (matching JIRA in this respect, not worse).

This change does **not** implement `LinkDependsOn` or
`AddAttachment`/`GetAttachment` (`yougile-dependencies-attachments`), does
not wire the adapter into `internal/tracker/config.go` or
`cmd/runner/office.go` (`yougile-wiring-and-docs`), and does not run any
live end-to-end role pipeline (`yougile-live-validation`). Because Go
interface satisfaction is all-or-nothing, `internal/tracker/yougile` will
not compile as a `tracker.Tracker` until `yougile-dependencies-attachments`
lands; this change's own tests exercise the implemented methods directly,
not through the interface assertion.

## Capabilities

### New Capabilities
- `tracker-yougile`: a second real `Tracker` adapter (alongside `jira` and
  `mock`) backed by YouGile's REST API — this change covers its
  status/lease/comment/create/claim core; dependency links and attachments
  follow in a later change under the same capability.

### Modified Capabilities
(none — this is a new, additive capability; no existing spec's requirements
change)

## Impact

- New code: `internal/tracker/yougile/*.go` (new package).
- No changes to `internal/tracker/tracker.go` (the interface itself), to
  `internal/tracker/config.go`, or to `cmd/runner/office.go` in this change.
- No changes to any deployed project's `projects.local.yaml` or
  `tracker.yaml` yet — the adapter is not reachable from the runner until
  `yougile-wiring-and-docs`.
- Depends on network access to the real YouGile REST API and a YouGile API
  key with access to a sandbox project (`office-polygon` was used during
  recon; a dedicated non-human office account is a concern for
  `yougile-wiring-and-docs`, not this change).
