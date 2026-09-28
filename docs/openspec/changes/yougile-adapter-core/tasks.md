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
- [ ] 2.2 Implement `List`/`ListReady` using server-side `columnId`
      filtering (not client-side filtering); cache the project's column
      list per `Tracker` instance rather than re-fetching it per call.
- [ ] 2.3 Implement `ListExpired` (tasks whose lease, read from `apiData`,
      has passed) by enumerating the project's columns.
- [ ] 2.4 Implement `Get` returning full task state including comments.

## 3. Lease and ownership

- [x] 3.1 Define the `apiData` lease sub-schema (namespaced, see design.md
      Decisions) and helpers to read/write it.
- [ ] 3.2 Implement `Claim`: write lease fields, reread, verify ownership,
      return `ErrClaimLost`-equivalent on loss.
- [ ] 3.3 Implement `Renew`: succeed only while the calling run's lease is
      still live.
- [ ] 3.4 Implement `Release`: clear lease fields without changing status,
      usable by both a run and the system actor.

## 4. Task lifecycle

- [ ] 4.1 Implement `Transition` (moves `columnId` to the column configured
      for the target status).
- [ ] 4.2 Implement `CreateTask` using a content-derived `idempotencyKey`
      (hash of project+summary+description, not a fresh random value per
      call — see design.md Decisions) for dedup.
- [ ] 4.3 Implement `SetHumanFlag` and `SetAttempts` (both likely additional
      `apiData` fields, following the same namespaced schema).

## 5. Comments and marker protocol

- [ ] 5.1 Implement `Comment`, writing through to YouGile's task-chat/message
      endpoint.
- [ ] 5.2 Implement `FindByMarker` (scoped project search, since real API
      listing does not support full-text search — confirm exact query
      approach against the OpenAPI spec at build time).

## 6. Tests

- [ ] 6.1 Unit tests for each implemented method against a fixture/fake
      HTTP transport (mirroring `internal/tracker/jira/jira_test.go`'s
      approach), not against the live API.
- [ ] 6.2 A small number of live-API smoke tests against `office-polygon`,
      gated so they do not run by default in CI (mirroring how JIRA-live
      tests, if any, are gated).
