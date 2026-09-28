## 1. Package scaffolding and HTTP client

- [x] 1.1 Create `internal/tracker/yougile` package with a config type (API
      key, base URL, project/board identifiers) and a minimal authenticated
      HTTP client (mirroring `internal/tracker/jira/jira.go`'s `net/http`
      usage, not a generated SDK).
- [x] 1.2 Implement `Whoami` against the real API's current-user endpoint.

## 2. Status modeling

- [x] 2.1 Resolve/validate the configured status→column mapping at `Open()`
      time (columns must already exist — the adapter never creates them),
      failing loudly and clearly on a missing or ambiguous mapping,
      mirroring `jira.go`'s reverse status-map validation.
- [x] 2.2 Implement `List`/`ListReady` using server-side `columnId`
      filtering (not client-side filtering); cache the project's column
      list per `Tracker` instance rather than re-fetching it per call.
- [x] 2.3 Implement `ListExpired` (tasks whose lease, read from `apiData`,
      has passed) by enumerating the project's columns.
- [x] 2.4 Implement `Get` returning full task state including comments.

## 3. Lease and ownership

- [x] 3.1 Define the `apiData` lease sub-schema (namespaced, see design.md
      Decisions) and helpers to read/write it.
- [x] 3.2 Implement `Claim`: write lease fields, reread, verify ownership,
      return `ErrClaimLost`-equivalent on loss.
- [x] 3.3 Implement `Renew`: succeed only while the calling run's lease is
      still live.
- [x] 3.4 Implement `Release`: clear lease fields without changing status,
      usable by both a run and the system actor.

## 4. Task lifecycle

- [x] 4.1 Implement `Transition` (moves `columnId` to the column configured
      for the target status).
- [x] 4.2 Implement `CreateTask` using a content-derived `idempotencyKey`
      (hash of project+summary+description, not a fresh random value per
      call — see design.md Decisions) for dedup.
- [x] 4.3 Implement `SetHumanFlag` and `SetAttempts` (both likely additional
      `apiData` fields, following the same namespaced schema).

## 5. Comments and marker protocol

- [x] 5.1 Implement `Comment`, writing through to YouGile's task-chat/message
      endpoint.
- [x] 5.2 Implement `FindByMarker` (scoped project search, since real API
      listing does not support full-text search — confirm exact query
      approach against the OpenAPI spec at build time).

## 6. Tests

- [x] 6.1 Unit tests for each implemented method against a fixture/fake
      HTTP transport (mirroring `internal/tracker/jira/jira_test.go`'s
      approach), not against the live API.
- [x] 6.2 A small number of live-API smoke tests against `office-polygon`,
      gated so they do not run by default in CI (mirroring how JIRA-live
      tests, if any, are gated).

## Build review notes (accepted, not blocking)

Final whole-branch review (review_mode standard, range a73fc33..85d8ccc):
no Critical or Important findings. Accepted Minor findings. Each one is
carried to a later change or to the owner.

- Office keys in `apiData` sit at the top level (`lease`, `attempts`,
  `human_wait`, `labels`) with no namespace. A card with a foreign
  `apiData` shape that `decodeAPIData` rejects fails `List`/`ListReady`/
  `ListExpired` for its whole column and fails `FindByMarker` project-wide.
  Decide on namespacing before `yougile-wiring-and-docs` puts real data
  on boards.
- `FindByMarker` skips archived cards, unlike `jira`. An archived split
  child looks not-found and is covered only by `idempotencyKey`, whose
  server-side lifetime is unverified.
- `FindByMarker` reads every project column per call. `ensureChildren`
  multiplies that cost by the number of children, against the 50 req/min
  limit.
- Other accepted items: the column set is cached at `Open`; an
  idempotent replay can return a deleted task; `http://` base URLs are
  accepted; an `io.ReadAll` error is ignored; `loadColumns` issues N+1
  requests; `userEmail` can send a duplicate `/users` request under
  concurrency; outgoing requests carry no `context.Context`.
- `TaskRef.Updated` is always zero for YouGile, which has no last-modified
  field. `yougile-wiring-and-docs` must document this for `runner ls`.
- The claim-race spec scenario was reworded during Build by coordinator
  ruling. It now names the overwritten-claimant case and documents the
  mirror race, the same limitation as `jira.Claim`. Flag it to the owner
  at verify.
