---
comet_change: yougile-adapter-core
role: technical-design
canonical_spec: openspec
---

# yougile-adapter-core — deep technical design

First of four changes building a second real `Tracker` adapter
(`internal/tracker/yougile`) against YouGile's REST API
(`.comet/batches/yougile-tracker-adapter.json`). Scope, goals, non-goals,
and the high-level decisions (and why each was made, including one
reversed mid-brainstorm) are in `proposal.md` and `design.md` under
`docs/openspec/changes/yougile-adapter-core/` and are not restated here in
full — this document refines them to concrete Go types, call sequences,
and edge cases for Build.

The single architectural reversal from this brainstorm, in one sentence:
status is modeled as the task's board `columnId` directly, not a separate
status-sticker, because live research this session confirmed YouGile has
no JIRA-style capability for one task to appear on multiple boards with
different column groupings — the premise `docs/DESIGN.md`'s "status is
not a column" principle depends on. Full reasoning, including the two
named trade-offs accepted (no N:1 status grouping into one column;
drag-and-drop is an unvalidated direct state mutation), is in
`design.md`'s Decisions section.

## 1. Package layout

```
internal/tracker/yougile/
  yougile.go       — Config, Tracker, Open, low-level HTTP helper
  status.go        — status<->column resolution, List/ListReady/ListExpired
  lease.go          — apiData schema, Claim/Renew/Release, SetHumanFlag/SetAttempts
  comment.go        — Comment, FindByMarker
  yougile_test.go   — httptest.NewServer-based unit tests
  live_test.go      — gated live-API smoke tests (build tag, see §8)
```

One package, several files by concern — mirrors `internal/tracker/jira`
having `jira.go` (core) and `wiki.go` (a separable concern, markup
conversion) as siblings, without forcing everything into one file the way
`jira.go` itself does (that file is 1727 lines partly because JIRA's REST
shape is more verbose; no reason to reproduce that here).

## 2. Config and Tracker

```go
type Config struct {
    BaseURL   string            // https://yougile.com/api-v2 (constant in practice, still a field: no different than jira.go's cfg.BaseURL, keeps tests free to point at httptest.NewServer)
    APIKey    string            // never logged; comes from env at the call site (yougile-wiring-and-docs), not read from disk by this package
    ProjectID string            // YouGile project id this Tracker instance serves — one Tracker per project, matching how jira.Open(cfg) is scoped per-project via cfg
    ColumnIDs map[string]string // graph status name -> YouGile columnId, e.g. {"Ready": "...", "InProgress": "..."}
}

type Tracker struct {
    cfg     Config
    client  *http.Client
    baseURL *url.URL

    // columnStatus is the reverse of cfg.ColumnIDs, built once at Open()
    // and validated for ambiguity there — same shape as jira.Tracker.graph.
    columnStatus map[string]string // columnId -> graph status name

    // columns caches the project's column list (id, name) fetched once at
    // Open() and reused by List/ListReady/ListExpired instead of
    // re-fetching per call (design.md Risks: no project-level task listing
    // in the real API, only per-column).
    columns []columnInfo

    Now func() time.Time // testability hook, matching jira.Tracker.Now
}

// No `var _ tracker.Tracker = (*Tracker)(nil)` assertion in this change —
// it would not compile until yougile-dependencies-attachments adds
// LinkDependsOn/AddAttachment/GetAttachment (proposal.md Non-Goals).
// That line is added there, not here.

func Open(cfg Config) (*Tracker, error) { ... }
```

`Open(cfg)` sequence, mirroring `jira.OpenAs`'s validation-before-construction
shape:

1. Validate `cfg.BaseURL`, `cfg.APIKey`, `cfg.ProjectID` are non-empty —
   same "fail loud, fail at open" philosophy as `jira.go`'s `base_url`/
   `auth.mode` checks.
2. Build `columnStatus` from `cfg.ColumnIDs`, rejecting on any column id
   mapped from two different status names (mirrors `jira.OpenAs`'s
   `graph` construction exactly, same error shape: `"status %q сопоставлен
   и с %q, и с %q"` becomes the English equivalent here since this
   change's artifacts are English).
3. Fetch the project's board(s)/columns once (`GET /api-v2/columns?boardId=...`
   per board in the project — see §4 on board enumeration) and confirm
   every id in `cfg.ColumnIDs` actually exists. Fail loudly on a
   configured column id that isn't real. **Never creates a column** —
   this is a read/validate step only, matching the "no side-effecting
   constructor" decision in `design.md`.
4. Construct `Tracker{cfg, client: &http.Client{Timeout: 30 * time.Second}, ...}`
   — same timeout as `jira.go`, no retry logic (design.md Decisions).

## 3. HTTP layer

```go
func (t *Tracker) call(method, path string, body, out any) error {
    // Marshal body (if non-nil) to JSON, build request against
    // t.cfg.BaseURL+path, set:
    //   Authorization: Bearer <t.cfg.APIKey>
    //   Content-Type: application/json
    // Do it, decode non-2xx into a *APIError carrying status+body,
    // decode 2xx body into out (if non-nil). One attempt, no retry.
}
```

Mirrors `jira.go`'s single shared low-level request helper (used by every
method instead of each hand-rolling `http.NewRequest`). Auth header
confirmed live this session: bearer key, no separate company-id exchange
needed for this account type.

## 4. Status as column: List / ListReady / ListExpired / Transition

**Board/column enumeration.** The real API's task-listing endpoints filter
by `columnId` or `assignedTo`, not by project directly (confirmed this
session against both the OpenAPI spec and the account used in
`office-polygon`). `Open()` therefore fetches the project's board(s) and
each board's columns once, caching `t.columns` as `[]columnInfo{ID, Name,
BoardID}`. `List`/`ListReady`/`ListExpired` all resolve to "for the
relevant column ids, list tasks in that column" rather than a single
project-scoped call.

```go
func (t *Tracker) ListReady(project, status string) ([]tracker.TaskRef, error) {
    columnID, ok := t.cfg.ColumnIDs[status]
    if !ok {
        return nil, fmt.Errorf("no column configured for status %q", status)
    }
    tasks, err := t.tasksInColumn(columnID) // GET /api-v2/tasks?columnId=...
    if err != nil {
        return nil, err
    }
    ready := make([]tracker.TaskRef, 0, len(tasks))
    for _, task := range tasks {
        if !task.LeaseAlive(t.Now()) {
            ready = append(ready, task.Ref())
        }
    }
    sortByCreatedAt(ready) // FIFO only — design.md: no native priority field, JIRA's priority tier is dropped, not replicated
    return ready, nil
}

func (t *Tracker) List(project string, statuses []string) ([]tracker.TaskRef, error) {
    // Same shape as ListReady but does not drop leased tasks (jira.go's
    // List doc comment explains why: this list is for humans, and "who's
    // working on it right now" is the first thing they look for) —
    // resolves each status to its column, concatenates tasksInColumn results.
}

func (t *Tracker) ListExpired(project string, now time.Time) ([]tracker.TaskRef, error) {
    // Must scan every configured column, not just one status's — an
    // expired lease can sit in any status. Iterates t.cfg.ColumnIDs'
    // values (or t.columns directly), filters task.RunID != "" && !task.LeaseAlive(now).
}

func (t *Tracker) Transition(key string, by tracker.Actor, toStatus string) error {
    columnID, ok := t.cfg.ColumnIDs[toStatus]
    if !ok {
        return fmt.Errorf("no column configured for status %q", toStatus)
    }
    // PUT /api-v2/tasks/{key} with {"columnId": columnID}. Single write,
    // no second field to keep in sync (the column-as-status reversal's
    // whole point) — contrast with the sticker design this replaced,
    // which would have needed a second write here.
}
```

`Task.Status` (used by `Get`/`toTask`-equivalent) is derived as
`t.columnStatus[task.ColumnID]`; a task whose column isn't in
`columnStatus` (e.g., someone dragged it to a column outside the
configured graph) is a real, expected failure mode — see §9.

## 5. `apiData` schema and the lease/attempts/human_wait split

```go
type apiDataPayload struct {
    Lease     *leaseData `json:"lease,omitempty"`
    Attempts  int        `json:"attempts,omitempty"`
    HumanWait bool       `json:"human_wait,omitempty"`
    // yougile-dependencies-attachments adds sibling keys here:
    // DependsOn   []string          `json:"depends_on,omitempty"`
    // Attachments map[string]string `json:"attachments,omitempty"`
}

type leaseData struct {
    Owner      string    `json:"owner"`
    RunID      string    `json:"run_id"`
    LeaseUntil time.Time `json:"lease_until"`
}
```

**Read-modify-write invariant** (design.md): every mutating call —
`Claim`, `Renew`, `Release`, `SetHumanFlag`, `SetAttempts` — follows the
same shape:

```go
func (t *Tracker) withApiData(key string, mutate func(*apiDataPayload) error) error {
    task, err := t.getRaw(key)
    if err != nil {
        return err
    }
    payload := decodeApiData(task.ApiData) // zero value if absent/nil
    if err := mutate(&payload); err != nil {
        return err
    }
    return t.updateApiData(key, payload) // PUT with the whole encoded object
}
```

`Claim`:

```go
func (t *Tracker) Claim(req tracker.ClaimRequest) error {
    err := t.withApiData(req.Key, func(p *apiDataPayload) error {
        if p.Lease != nil && p.Lease.RunID != "" && p.Lease.LeaseUntil.After(t.Now()) {
            return tracker.ErrClaimLost // already live-owned by someone else; write nothing
        }
        p.Lease = &leaseData{Owner: req.Owner, RunID: req.RunID, LeaseUntil: req.LeaseUntil}
        return nil
    })
    if err != nil {
        return err
    }
    // Re-read and verify, matching jira.go's Claim: after writing, read
    // the task back and confirm p.Lease.RunID == req.RunID. If not,
    // ErrClaimLost — someone else won the race between our write and our
    // reread. Nothing to roll back: we never overwrote a live lease (the
    // check above already refused to), so the loser's write simply never
    // happened to be the one that stuck.
}
```

`Renew` only succeeds if the *current* lease's `RunID` matches the caller
and is still live; `Release` clears `p.Lease = nil` while leaving
`Attempts`/`HumanWait` untouched (spec requirement: "Releasing a lease
does not change the task's status" — and, by construction here, doesn't
touch the sibling `apiData` fields either).

## 6. `CreateTask` and idempotency

```go
func (t *Tracker) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error) {
    key := idempotencyKey(project, input.Summary, input.Description) // sha256, hex-encoded
    // POST /api-v2/tasks with {title, description, columnId: <project's default/backlog column>, idempotencyKey: key}
    // ...
}

func idempotencyKey(parts ...string) string {
    h := sha256.New()
    for _, p := range parts {
        h.Write([]byte(p))
        h.Write([]byte{0}) // separator so ("ab","c") != ("a","bc")
    }
    return hex.EncodeToString(h.Sum(nil))
}
```

Deterministic, not `uuid.New()` — design.md: a fresh random key per call
would defeat the purpose (a caller retry after an ambiguous failure would
get a *different* key and dedup wouldn't fire). This is additive
defense-in-depth; the primary idempotency mechanism for batch creation
remains the caller checking `FindByMarker` before calling `CreateTask` at
all (unchanged from how `jira` already works — JIRA's own `CreateTask` has
no idempotency of its own).

Which column does a freshly created task land in? `TaskInput` carries no
status — `CreateTask`'s doc comment in `tracker.go` doesn't specify one
either. Resolved the same way `jira.go` does implicitly (new issues land
wherever the project's default status/column is): this change adds a
`Config` field for the creation column (likely the column configured for
the graph's entry status, e.g. `Ready`/`Backlog` — confirm the exact
initial status name against `office/tracker.example.yaml`'s `status_map`
when wiring up `yougile-wiring-and-docs`, since it isn't this change's
concern to invent a status graph).

## 7. Comments, `FindByMarker`, human/office distinction

`Comment(key, by, body)` posts `body` (marker line + prose, already
formatted by the shared, tracker-agnostic `internal/tracker/marker.go`
before this method ever sees it) to YouGile's task-chat/message endpoint.
`Get(key)` returns comments read back from the same endpoint, each tagged
with its author's user id — resolved against `GET /api-v2/users` (id→email)
so the caller can compare against `Whoami()`'s result, per the spec
requirement "Human-authored replies are distinguishable from
office-authored comments."

`FindByMarker(project, marker)`: the real API's task listing does not
support full-text search (confirmed this session). Implementation
enumerates the project's columns (the same cached `t.columns` used by
§4) and scans each task's comments for the marker — bounded by the
project's actual task count, not a full-text index. Acceptable for this
project's scale (small number of concurrently active tasks per project);
flagged as a build-time detail to confirm against real task volumes, not
a blocking design decision, since it doesn't change the interface
contract.

## 8. Testing

Unit tests: `httptest.NewServer` with a fake handler function per test
case, mirroring `jira_test.go` exactly (not a mocked `http.RoundTripper`)
— point `Config.BaseURL` at the test server's URL. Covers every method
implemented in this change against the spec's scenarios (§ specs/tracker-yougile/spec.md):
concurrent-claim race (one wins), renew-on-expired-lease failure,
release-preserves-status, repeated-idempotencyKey dedup, marker round-trip,
column-mismatch failure mode from §4/§9.

Live smoke tests: a small `//go:build yougile_live` (or equivalent) gated
file, run manually against `office-polygon`, not part of default `go test
./...` — mirrors however JIRA's own live-only tests (if any exist) are
gated; confirm the exact convention used elsewhere in the repo when
writing this file.

## 9. Edge cases and failure modes

- **A task's column isn't in the configured `status_map`** (human created
  a column outside the graph, or dragged a task there). `Get`/`List`-family
  calls encountering this: treat as an explicit error surfaced to the
  caller (a task with unknown status is not silently coerced into some
  default status) — mirrors the "fail loud" philosophy throughout this
  design rather than guessing.
- **Two runs racing `Claim` on the same task**: covered in §5 — the
  reread-and-verify step is what decides the winner; both writes may
  reach the server, but only the reread confirms which one the caller
  should trust.
- **`apiData` absent entirely** (task never touched by the office):
  `decodeApiData` on empty/nil input returns a zero-value `apiDataPayload`
  (`Lease: nil`), which `LeaseAlive`-equivalent logic already treats as
  unowned — no special-casing needed beyond what the zero value already
  gives for free.
- **`SetHumanFlag`/`SetAttempts` called on a task with no lease** (e.g.,
  between `Release` and the next `Claim`): both fields live outside
  `Lease` in `apiDataPayload` precisely so this is legal — no lease
  required to set them, matching how JIRA's `human_flag_label` and
  `customfield_10104` (attempts) aren't gated on an active lease either.

## 10. Open items deferred to Build (non-blocking)

- Exact creation-column config field name/default (§6) — small, confirm
  when `yougile-wiring-and-docs` designs the full `tracker-yougile.yaml`
  schema, since this change's `Config` is a plain struct without a loader
  yet (design.md Decisions: `LoadConfig` deliberately deferred).
- `FindByMarker`'s column-enumeration-and-scan approach (§7) — fine at
  expected scale; worth a note if task volumes per project turn out
  larger than assumed.
