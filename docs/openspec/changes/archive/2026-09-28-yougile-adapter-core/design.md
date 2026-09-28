## Context

See `proposal.md` - Why. This change builds `internal/tracker/yougile`'s
core CRUD/lease surface only, against YouGile's real REST API
(`https://yougile.com/api-v2`), not the local `yougile-mcp` MCP server used
during recon (confirmed to expose a materially narrower surface than the
real API — see memory `project_yougile_second_adapter_viable.md`). The
existing `internal/tracker/jira` adapter is the closest precedent: it talks
`net/http` directly to JIRA's REST API and already solves the "no atomic
CAS" problem this adapter also faces.

Confirmed live against the real API in `office-polygon` this session:
- `apiData` on a task is a free JSON field with exact round-trip (unlike
  `extensionData`, which is strictly validated and rejects arbitrary
  payloads).
- `idempotencyKey` on task creation deduplicates correctly.
- A task belongs to exactly one board, in exactly one column
  (`columnId` is a single field, not a list; confirmed both by the
  `TaskDto`/`CreateTaskDto`/`UpdateTaskDto` OpenAPI schema and by
  YouGile's own documentation of its company→project→board→column→task
  hierarchy as a strict tree, not a JIRA-style graph where one issue can
  appear on many boards).

(Listing tasks filtered by `stickerId`+`stickerStateId` was also confirmed
server-side and genuinely working, but is no longer used by this change —
see Decisions below.)

## Goals / Non-Goals

**Goals:**
- A `tracker-yougile` capability whose behavior (per `specs/tracker-yougile/spec.md`)
  matches the lease/claim/comment guarantees the `jira` adapter already
  provides, for the subset of `tracker.Tracker` this change covers.

**Non-Goals:**
- `LinkDependsOn`, `AddAttachment`/`GetAttachment` — `yougile-dependencies-attachments`.
- Wiring into `internal/tracker/config.go` / `cmd/runner/office.go` — `yougile-wiring-and-docs`.
- A compiling `var _ tracker.Tracker = (*Tracker)(nil)` assertion — that only
  becomes true once `yougile-dependencies-attachments` lands, since Go
  interface satisfaction is all-or-nothing. This change's tests call the
  implemented methods directly.

## Decisions

**Status is the task's board column directly, not a separate sticker.**
`Transition` moves `columnId`; `ListReady`/`List` filter by `columnId`
server-side; the config's `status_map` maps graph status names to column
ids (captured once during setup, the same shape as JIRA's
`fields.customfield_*` ids). Alternative considered and initially chosen,
then reversed after discussion: a dedicated string-sticker holding status
independent of column, matching `docs/DESIGN.md`'s "status is not a
column" principle. That principle exists because in JIRA the same issue
can appear on many different boards, each grouping the underlying
statuses into columns differently — the runner must not need to know
about any particular board's layout. Live research this session
(OpenAPI schema + YouGile's own documentation) confirmed YouGile has no
equivalent: a task belongs to exactly one board, in exactly one column,
full stop. The premise the principle depends on does not hold here, so
paying for a second, separately-written state field bought nothing YouGile
could actually use:
- It would have removed one genuine capability (grouping several graph
  statuses into one column purely for human visual convenience — with a
  sticker, N statuses can map to one column; with the column itself as
  status, it's necessarily 1:1). This is real, but was judged not worth
  the added complexity of two state fields that must be kept in sync,
  reasoned through explicitly with the change's owner.
- Column-as-status also removes an entire class of risk the sticker
  design would have introduced: since `Transition` would have had to
  write both the sticker (source of truth) and the column (kept in sync
  purely for human visibility), a mid-sequence failure between the two
  writes could leave them briefly inconsistent — self-healing on the next
  transition, not a correctness bug, but still a real edge case. With
  column-as-status there is only one field, one write, nothing to
  desynchronize.
- It also means every drag-and-drop of a card — the single most natural,
  least deliberate interaction the tool offers — is now a direct,
  unvalidated mutation of the runner's own state, with no workflow-engine
  check on which transitions are legal (YouGile has none; JIRA's board
  interactions are typically constrained by its workflow). This is an
  accepted trade-off, not a blind spot: it was raised and weighed
  explicitly, and simplicity won.

**Lease fields live in `apiData`, not `description`.** `apiData` round-trips
exactly and is isolated from the task's human-facing text. Alternative
considered: encode lease JSON inside `description` (the approach initially
suspected necessary for Linear, which has no equivalent field) — rejected
because it would require read-modify-write of human-authored text on every
claim/renew/release, risking clobbering concurrent human edits.

**Claim uses write-then-reread-and-verify, matching the `jira` adapter.**
YouGile has no native optimistic-lock/CAS primitive, same as JIRA. Reusing
the already-proven pattern (`internal/tracker/jira/jira.go`) avoids
inventing a new concurrency strategy for one adapter.

**`apiData`'s internal JSON schema reserves room for later changes.**
`apiData` now carries lease data only (`Claim`/`Renew`/`Release`) plus
`attempts`/`human_wait`: a nested `lease{owner, run_id, lease_until}`
sub-object (written and cleared together as a unit) alongside top-level
`attempts`/`human_wait` fields (independent lifecycle — both survive
`Release`, mirroring how JIRA's own `customfield_10104` attempts field and
its human-flag label are not part of the lease fields it clears on
release either). `yougile-dependencies-attachments` will add further
sibling top-level keys (`depends_on`, an attachment id→url manifest) to
the same object. Invariant that must hold for every write path (`Claim`,
`Renew`, `Release`, `SetHumanFlag`, `SetAttempts`, and later
`LinkDependsOn`/`AddAttachment`): read-modify-write the whole `apiData`
object, never write a partial one — a partial write would silently drop
sibling fields it doesn't know about.

**`CreateTask` uses a content-derived `idempotencyKey`.** Unlike JIRA,
where `CreateTask` has no idempotency of its own and the caller is
expected to check `FindByMarker` before calling it (`tracker.go`'s own
doc comment on `FindByMarker`: "источник идемпотентности пакетного
создания"), YouGile offers a native `idempotencyKey`. It is used here as
defense-in-depth against a caller retrying `CreateTask` after an
ambiguous failure (this change deliberately has no HTTP-level retries —
see below — so an ambiguous failure reaches the caller, who may retry).
The key is derived deterministically as a hash of
`(project, input.Summary, input.Description)`, not a fresh random value
per call, so a retried call with identical input reuses the same key and
YouGile's own dedup applies. This is additive: the `FindByMarker`
check-before-create pattern remains the primary idempotency mechanism for
batch creation, unchanged from how `jira` already works.

**`ListReady` sorts by creation time only, dropping JIRA's priority tier.**
JIRA's `ListReady` sorts `ORDER BY priority DESC, created ASC`; YouGile has
no native priority field (and this change does not introduce a
sticker-based one), so `ListReady` sorts by creation timestamp ascending
only (FIFO). `tracker.go`'s own doc comment already leaves ordering to the
implementation ("отсортированы так, как решает реализация"); a
priority tier can be added later without an interface change if it turns
out to matter.

**No HTTP-level retry logic, matching `jira.go` exactly (grep-confirmed:
zero retry/backoff code in the existing adapter).** A single request, a
single result; an error propagates to the caller as-is. Resilience to
transient network trouble already exists one layer up: per
`runner-multi-tracker`, an error in one office does not stop the others
within a tick, and `loop` logs a failing office's error and continues —
the next tick is the retry. Adding retry logic inside the adapter would be
a new pattern with no precedent anywhere in this codebase, solving a
problem the runner's own loop already solves at a different layer.

## Risks / Trade-offs

- [YouGile's REST API rate limit (50 req/min per company)] → Poll cadence
  and `ListReady`/`List` batching are sized to stay well under this; not a
  hard blocker but worth measuring once implemented.
- [No native CAS] → Same exposure as the `jira` adapter already has in
  production; not a new class of risk.
- [Real API occasionally showed network-path instability during this
  session's recon, traced to a local VPN route, not the API itself] →
  Not a design concern for the adapter itself; the "no retry" decision
  above still stands regardless — the instability was a local development-
  machine issue (resolved via VPN split-tunneling), not evidence the API
  itself needs defensive retries.
- [Column-as-status accepts two real, named costs in exchange for
  simplicity] → (1) No way to group several graph statuses into one
  column for human visual convenience — every status is necessarily its
  own column, 1:1. (2) Every drag-and-drop of a card is an unvalidated
  direct mutation of runner state, with no workflow-engine check on which
  transitions are legal. Both were raised and weighed explicitly with the
  change's owner, not overlooked; simplicity (one state field, one write,
  nothing to desynchronize, no multi-board scenario to design around
  since YouGile doesn't have one) won.
- [`List`/`ListReady`/`ListExpired` must enumerate a project's columns
  to gather its tasks, since the real API's listing endpoints filter by
  `columnId`/`assignedTo`, not by project directly] → Implementation
  detail for Build, not a behavior change: cache the column list per
  `Tracker` instance rather than re-fetching it on every call.
