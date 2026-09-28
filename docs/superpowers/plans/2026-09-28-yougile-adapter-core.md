---
change: yougile-adapter-core
design-doc: docs/superpowers/specs/2026-09-28-yougile-adapter-core-design.md
base-ref: a73fc3304d71ade69e4f6e497275663850ff2839
---

# yougile-adapter-core Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A new package `internal/tracker/yougile` that implements the core surface of `tracker.Tracker` (`Whoami`, `Get`, `List`, `ListReady`, `ListExpired`, `Claim`, `Renew`, `Release`, `Transition`, `Comment`, `SetHumanFlag`, `SetAttempts`, `CreateTask`, `FindByMarker`) against YouGile's real REST API (`https://yougile.com/api-v2`).

**Architecture:** Plain `net/http` + `encoding/json` client, the same way `internal/tracker/jira/jira.go` talks to JIRA: one shared `call` helper, no SDK, no retries. A task's status is its board column (`columnId`), mapped through `Config.ColumnIDs`. Lease, attempts, human-wait flag and labels live in the task's free-JSON `apiData` field, which every write path read-modify-writes as a whole object while keeping keys it does not own. `Claim` does write-then-reread-and-verify, like `jira`.

**Tech Stack:** Go 1.26.6 (`go.mod`), standard library only (`net/http`, `net/http/httptest`, `encoding/json`, `crypto/sha256`, `html`). No new dependencies.

**Spec:** `docs/openspec/changes/yougile-adapter-core/specs/tracker-yougile/spec.md`. Design doc: `docs/superpowers/specs/2026-09-28-yougile-adapter-core-design.md`. Decisions and non-goals: `docs/openspec/changes/yougile-adapter-core/{proposal.md,design.md}`. Task boundaries: `docs/openspec/changes/yougile-adapter-core/tasks.md`. Every task below names the `tasks.md` item it implements. After a task's commit, tick that item's box in `tasks.md` in the same commit.

**Execution order differs from `tasks.md` numbering in one place.** Item 3.1 (the `apiData` codec) runs before 2.4/2.2/2.3, because `Get` and every listing call decode `apiData` to fill lease fields. Order: 1.1, 1.2, 2.1, 3.1, 2.4, 2.2, 2.3, 3.2, 3.3, 3.4, 4.1, 4.2, 4.3, 5.1, 5.2, 6.1, 6.2.

## Where this plan departs from the design doc (read before Task 1)

The design doc is the source for the architecture. The points below are cases where it contradicts the real `tracker.Tracker` contract (`internal/tracker/tracker.go`), its only caller, or the real API. The plan follows the contract and the API in each case:

1. **`Claim` honors `ClaimRequest.ExpectStatus` and `WorkingStatus`.** Design §5 checks only the lease. The contract (`tracker.go`, `ClaimRequest`) and `jira.Claim` also refuse when the task has moved away from `ExpectStatus`, and they move it to `WorkingStatus` when that is set and differs from the current status. The status/column move is done here in the **same** `PUT` as the lease write, which JIRA cannot do.
2. **Every mutating method except `Claim` and `CreateTask` goes through `tracker.CheckOwner`.** The design doc does not mention it. The contract requires it ("Методы, меняющие задачу, принимают Actor и обязаны проверять право через CheckOwner").
3. **`FindByMarker` matches task labels, not comment text.** Design §7, the spec scenario "A marked comment is found by its marker" and the batch file's acceptance scenario all describe a comment scan. The only real caller, `internal/pipeline/splits.go:ensureChildren`, passes the marker as `TaskInput.Labels` to `CreateTask` and then looks it up with `FindByMarker`. `jira` (`labels = %q`) and `mock` (`slices.Contains(task.Labels, marker)`) both match labels. YouGile has no labels, so they are stored in `apiData["labels"]` at creation. A comment scan would also cost one chat request per task in the project, against a limit of 50 requests per minute per company. The change owner confirmed label matching on 2026-09-28, and the delta spec scenario was amended in the same commit as this plan.
4. **Listing uses `GET /api-v2/task-list`.** The OpenAPI spec marks `GET /api-v2/tasks` (used in design §4) `deprecated: true` ("Используйте /task-list вместо этого"). Both endpoints take the same `columnId`/`limit`/`offset` filters.
5. **`apiData` keeps keys it does not own, and always writes its own keys explicitly.** The design's `apiDataPayload` struct with `omitempty` would drop any key another integration wrote and would never send `"lease": null`. If YouGile's `PUT` shallow-merges `apiData` rather than replacing it, a missing key would never clear the lease. The codec here keeps unknown keys (`extra map[string]json.RawMessage`) and always emits `lease` (null when free), `attempts`, `human_wait` and `labels`.
6. **The `idempotencyKey` hash also covers the final description (with `DescriptionAppend`) and the sorted labels.** Design §6 hashes only `(project, Summary, Description)`. Two split children with the same title and prose but different markers would then collide, and YouGile would silently hand back the first child for the second. The labels carry the split marker, so hashing them keeps the key unique per child.
7. **"Exactly one claim succeeds" is guaranteed only for the half of the race that jira also covers.** Reread-and-verify catches the case where our write was overwritten. Two claimants who both read the task as free before either writes can still both succeed (the mirror race documented on `jira.Claim`). YouGile has no CAS, so nothing closes it. The tests pin down the covered half (overwritten after write means `ErrClaimLost`, and a live lease is never overwritten). The spec scenario overclaims, just as it would for jira.
8. **`Config.BaseURL` is the host root (`https://yougile.com`).** The code adds the `/api-v2` prefix, the same way `jira.go` adds `apiPath`. The comment in design §2 suggested putting `/api-v2` inside `BaseURL`.
9. **Creation column: `Config.CreateStatus`** (a graph status name that must be a key of `ColumnIDs`). Design §6/§10 left the field name to Build. An empty value is allowed at `Open` and makes `CreateTask` fail loudly.
10. **The `project` argument must equal `Config.ProjectID`.** Any other value returns `tracker.ErrNoProject`. One `Tracker` serves one YouGile project (design §2). `Task.Project` is set to `Config.ProjectID`. How the runner's `projects.local.yaml` key maps to a YouGile project id is left to `yougile-wiring-and-docs`.
11. **Status-as-sticker vs status-as-column.** The batch file (`.comet/batches/yougile-tracker-adapter.json`, change 1 goals and acceptance scenarios) still says "status as a string-sticker … stickerId/stickerStateId filtering". `proposal.md`, `design.md`, the design doc and `spec.md` all record the later reversal to column-as-status. This plan follows the reversal. The batch file is stale and needs updating, not the plan.
12. **Go comments and error strings are in Russian.** This follows the repo's `CLAUDE.md` and every existing `.go` file, and matches the precedent in `docs/superpowers/plans/2026-09-23-doctor.md`. Design §2 suggested English errors because this change's artifacts are in English. That applies to the artifacts, not to shipped source.
13. **File layout adds `task.go` and splits the tests by concern** (`yougile_test.go` holds the fake server and fixture, then `status_test.go`, `lease_test.go`, `task_test.go`, `comment_test.go`, `contract_test.go`). Design §1 put `Get`/`toTask` nowhere specific and all tests in one file.

## Global Constraints

- Module `github.com/kao73/virtual-office`, Go `1.26.6`. **No new dependencies.** `go.mod` and `go.sum` stay untouched.
- **No `var _ tracker.Tracker = (*Tracker)(nil)` anywhere in this change.** Without `LinkDependsOn`, `AddAttachment` and `GetAttachment` it would not compile. `yougile-dependencies-attachments` adds that line. Signature drift is caught instead by the `coreTracker` subset interface in `contract_test.go` (Task 16).
- **No edits** to `internal/tracker/tracker.go`, `internal/tracker/config.go`, `cmd/runner/*`, `office/*`. The package is not reachable from the runner in this change.
- HTTP: `Authorization: Bearer <APIKey>`, `Accept: application/json`, `Content-Type: application/json` when there is a body. `&http.Client{Timeout: 30 * time.Second}`. **One attempt per request and no retry or backoff code** (design.md Decisions).
- The API key never appears in an error, log line or test failure message.
- Rate limit: 50 requests per minute per company. Do not add avoidable requests. `owned()` reads the task without its chat. The column list is fetched once in `Open` and cached. User emails are cached per `Tracker`.
- Contract errors are wrapped with `%w`: `tracker.ErrClaimLost`, `tracker.ErrNotOwner`, `tracker.ErrNotFound`, `tracker.ErrNoProject`.
- The adapter **never creates** columns, boards or projects. `Open` only reads and validates.
- `go test ./...` and `go vet ./...` (what CI runs, `.github/workflows/ci.yml`) must never touch the network. Live tests sit behind `//go:build yougile_live`.
- Live tests run **only** against the `office-polygon` sandbox project. Never touch the `Clens` board, which is a live client board.

## Review Focus

1. **A card dragged by a human into a column outside `ColumnIDs`** (for example an "Ideas" column on the same board). `Get` must fail with `ErrUnmappedColumn` and name the column, not coerce the card into some status. `List`/`ListReady`/`ListExpired` scan only configured columns, so they never see it. `FindByMarker` scans every project column and must fail loudly on a match in an unmapped column rather than report "not found", which would lead to a duplicate child. Pinned in Task 5 (`TestGetTaskInUnmappedColumnFails`) and Task 15 (`TestFindByMarkerFailsLoudOnUnmappedColumn`).
2. **`apiData` holding keys written by someone else** (another integration, or the later `depends_on`/attachment keys). Every write path must keep them byte-for-byte. Pinned in Task 4 (`TestAPIDataKeepsForeignKeys`), Task 8 (`TestClaimKeepsForeignAPIDataAndCounters`) and Task 13 (`TestSetAttemptsKeepsLeaseAndForeignKeys`).
3. **Clearing a lease under either `PUT` semantics.** If YouGile merged `apiData` instead of replacing it, a `Release` that just omitted `lease` would leave the task owned forever. `Release` must send `"lease": null` explicitly. Pinned in Task 10 (`TestReleaseSendsExplicitNullLease`) and checked live in Task 17.
4. **Archived or deleted cards still sitting in a configured column.** They must never come back from `ListReady`/`List`/`ListExpired`/`FindByMarker`, or a Done-and-archived card could be picked up again. Pinned in Task 6 (`TestListReadySkipsArchivedAndDeleted`).
5. **More cards in a column than the server returns in one page**, with a server-side cap below the `limit` we ask for. Pagination must follow `paging.next`, not "the page came back short", and must stop on a server that never says it is done. Pinned in Task 1 (`TestListAllFollowsNextPastServerCap`, `TestListAllGivesUpOnEndlessPaging`) and Task 6 (`TestListReadyPaginates`).

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/tracker/yougile/yougile.go` | Package doc, `Config`, `Tracker`, `Open`, `checkProject`, `call`, `statusError`, `snippet`, generic `listAll` pagination, `userDTO`, `Whoami` |
| `internal/tracker/yougile/status.go` | Board/column DTOs, `loadColumns` (validates `ColumnIDs` in `Open`), `columnFor`, `tasksInColumn`, `collect`, `List`, `ListReady`, `ListExpired`, `Transition` |
| `internal/tracker/yougile/lease.go` | `apiData` codec (`decodeAPIData`, `encode`), `leaseData`, `Claim`, `Renew`, `Release`, `SetHumanFlag`, `SetAttempts` |
| `internal/tracker/yougile/task.go` | `taskDTO`, `ErrUnmappedColumn`, `getRaw`, `toTask`, `Get`, `owned`, `mutateAPIData`, `putTask`, `CreateTask`, `idempotencyKey`, `joinDescription` |
| `internal/tracker/yougile/comment.go` | `messageDTO`, `comments`, `userEmail`, `Comment`, `messageHTML`, `FindByMarker` |
| `internal/tracker/yougile/yougile_test.go` | `fakeYouGile` (an `httptest` handler with in-memory state), `fixture`, `serve`, `testConfig`, tests for Tasks 1–3 |
| `internal/tracker/yougile/lease_test.go` | codec, Claim, Renew, Release, SetHumanFlag/SetAttempts tests |
| `internal/tracker/yougile/task_test.go` | Get, CreateTask tests |
| `internal/tracker/yougile/status_test.go` | List/ListReady/ListExpired/Transition tests |
| `internal/tracker/yougile/comment_test.go` | Comment, FindByMarker tests |
| `internal/tracker/yougile/contract_test.go` | `coreTracker` subset interface assertion plus the ownership table test |
| `internal/tracker/yougile/live_test.go` | `//go:build yougile_live`, a single lifecycle smoke test against `office-polygon` |

Run every command from the worktree root `/Users/aleksejkolesnikov/IdeaProjects/virtual-office/.worktrees/yougile-adapter-core`.

---

### Task 1: Package scaffolding, HTTP helper, fake server (tasks.md 1.1)

**Files:**
- Create: `internal/tracker/yougile/yougile.go`
- Create: `internal/tracker/yougile/yougile_test.go`

**Interfaces:**
- Consumes: `tracker.ErrNotFound`, `tracker.ErrNoProject` from `internal/tracker/tracker.go`.
- Produces:
  - `type Config struct { BaseURL, APIKey, ProjectID string; ColumnIDs map[string]string; CreateStatus string }`
  - `type Tracker struct { cfg Config; client *http.Client; columnStatus map[string]string; columns []columnInfo; mu sync.Mutex; users map[string]string; Now func() time.Time }`. `columnInfo` is declared in Task 3. Until then, declare `columns []columnInfo` together with `type columnInfo struct{ ID, Title, BoardID string }` in `yougile.go`. Task 3 moves the type to `status.go`.
  - `func Open(cfg Config) (*Tracker, error)`
  - `func (t *Tracker) checkProject(project string) error`
  - `func (t *Tracker) call(method, path string, query url.Values, in, out any) error`. `path` is relative to `/api-v2`, for example `"/users/me"`.
  - `func listAll[T any](t *Tracker, path string, query url.Values) ([]T, error)`
  - consts `apiPrefix = "/api-v2"`, `pageLimit = 1000`, `maxPages = 100`
  - test helpers: `fakeYouGile`, `fakeTask`, `fakeMessage`, `newFake(t)`, `serve(t, fake) string`, `testConfig(baseURL) Config`, `fixture(t) (*Tracker, *fakeYouGile)`, consts `testProject`, `testKey`, `colReady`, `colWork`, `colReview`, `colOutside`, `officeUserID`, `humanUserID`, var `now`.

- [x] **Step 1: Write the fake server, fixture and failing tests**

Create `internal/tracker/yougile/yougile_test.go`:

```go
package yougile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

var now = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

const (
	testProject  = "proj-1"
	testKey      = "task-1"
	colReady     = "col-ready"
	colWork      = "col-work"
	colReview    = "col-review"
	colOutside   = "col-ideas" // есть на доске проекта, но ни одному статусу не сопоставлена
	officeUserID = "user-office"
	humanUserID  = "user-human"
)

// fakeTask — задача в памяти фейкового YouGile.
type fakeTask struct {
	ID, Title, Description, ColumnID string
	Timestamp                        int64 // мс, как timestamp в TaskDto
	Archived, Deleted                bool
	APIData                          map[string]any
}

func (t *fakeTask) dto() map[string]any {
	m := map[string]any{
		"id": t.ID, "title": t.Title, "description": t.Description, "columnId": t.ColumnID,
		"timestamp": t.Timestamp, "archived": t.Archived, "deleted": t.Deleted,
	}
	if t.APIData != nil {
		m["apiData"] = t.APIData
	}
	return m
}

// fakeMessage — сообщение чата задачи. ID — время создания в мс, как в ChatMessageDto.
type fakeMessage struct {
	ID      int64
	From    string
	Text    string
	HTML    string
	Deleted bool
}

// fakeYouGile — минимальный YouGile: отвечает на те запросы, которые шлёт
// адаптер, и запоминает их, чтобы тест проверял и исход, и форму запроса.
type fakeYouGile struct {
	t  *testing.T
	mu sync.Mutex

	projects map[string]bool
	boards   []map[string]any // {"id","title","projectId"}
	columns  []map[string]any // {"id","title","boardId"}
	tasks    map[string]*fakeTask
	order    []string // порядок создания задач
	messages map[string][]fakeMessage
	users    map[string]string // id → email
	me       string
	idem     map[string]string // idempotencyKey → id задачи
	nextID   int

	pageCap       int  // >0 — сервер режет страницу до этого размера, что бы ни просили
	endlessPaging bool // сервер всегда говорит next=true и отдаёт первый элемент

	requests  []string // "METHOD /api-v2/path?query"
	lastAuth  string
	puts      []map[string]any // тела PUT /tasks/{id}
	posts     []map[string]any // тела POST /tasks
	chatPosts []map[string]any // тела POST /chats/{id}/messages

	// afterPut вызывается после применения PUT /tasks/{id}, до ответа клиенту,
	// без удержания f.mu: так выглядит конкурент, записавший своё следом за нами.
	afterPut func(id string)
	// fail — "METHOD /api-v2/path" → код, которым ответить вместо обработки.
	fail map[string]int
}

func newFake(t *testing.T) *fakeYouGile {
	f := &fakeYouGile{
		t:        t,
		projects: map[string]bool{testProject: true},
		boards: []map[string]any{
			{"id": "board-1", "title": "Office", "projectId": testProject},
			{"id": "board-other", "title": "Other", "projectId": "proj-other"},
		},
		columns: []map[string]any{
			{"id": colReady, "title": "Ready", "boardId": "board-1"},
			{"id": colWork, "title": "In Progress", "boardId": "board-1"},
			{"id": colReview, "title": "Review", "boardId": "board-1"},
			{"id": colOutside, "title": "Ideas", "boardId": "board-1"},
			{"id": "col-foreign", "title": "Foreign", "boardId": "board-other"},
		},
		tasks:    map[string]*fakeTask{},
		messages: map[string][]fakeMessage{},
		users:    map[string]string{officeUserID: "office@example.com", humanUserID: "human@example.com"},
		me:       officeUserID,
		idem:     map[string]string{},
		fail:     map[string]int{},
	}
	f.addTask(&fakeTask{ID: testKey, Title: "First task", Description: "Do it", ColumnID: colReady,
		Timestamp: now.Add(-time.Hour).UnixMilli()})
	return f
}

func (f *fakeYouGile) addTask(task *fakeTask) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tasks[task.ID] = task
	f.order = append(f.order, task.ID)
}

// task — копия задачи для проверки в тесте.
func (f *fakeYouGile) task(id string) fakeTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	return *f.tasks[id]
}

// setLease — записать аренду в apiData мимо адаптера, как это сделал бы другой прогон.
func (f *fakeYouGile) setLease(id, runID string, until time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	task := f.tasks[id]
	if task.APIData == nil {
		task.APIData = map[string]any{}
	}
	task.APIData["lease"] = map[string]any{"owner": "someone", "run_id": runID, "lease_until": until.Format(time.RFC3339Nano)}
}

// count — сколько дошло запросов с данным префиксом "METHOD /api-v2/path".
func (f *fakeYouGile) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func number(raw string, fallback int) int {
	if n, err := strconv.Atoi(raw); err == nil {
		return n
	}
	return fallback
}

func (f *fakeYouGile) page(items []any, q url.Values) map[string]any {
	limit, offset := number(q.Get("limit"), 1000), number(q.Get("offset"), 0)
	if f.pageCap > 0 && limit > f.pageCap {
		limit = f.pageCap
	}
	if f.endlessPaging {
		return map[string]any{
			"paging":  map[string]any{"count": 1, "limit": limit, "offset": offset, "next": true},
			"content": items[:1],
		}
	}
	offset = min(offset, len(items))
	end := min(offset+limit, len(items))
	return map[string]any{
		"paging":  map[string]any{"count": end - offset, "limit": limit, "offset": offset, "next": end < len(items)},
		"content": items[offset:end],
	}
}

func (f *fakeYouGile) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	f.lastAuth = r.Header.Get("Authorization")
	if code, ok := f.fail[r.Method+" "+r.URL.Path]; ok {
		f.mu.Unlock()
		http.Error(w, `{"error":"forced"}`, code)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, apiPrefix)
	status, body := f.route(r.Method, path, r)
	var hook func()
	if r.Method == http.MethodPut && strings.HasPrefix(path, "/tasks/") && status == http.StatusOK && f.afterPut != nil {
		id, after := strings.TrimPrefix(path, "/tasks/"), f.afterPut
		hook = func() { after(id) }
	}
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// route вызывается под f.mu.
func (f *fakeYouGile) route(method, path string, r *http.Request) (int, any) {
	q := r.URL.Query()
	notFound := map[string]any{"error": "not found"}
	decode := func() map[string]any {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Errorf("%s %s: тело не разобрано: %v", method, path, err)
		}
		return body
	}

	switch {
	case method == http.MethodGet && path == "/users/me":
		return http.StatusOK, map[string]any{"id": f.me, "email": f.users[f.me]}

	case method == http.MethodGet && strings.HasPrefix(path, "/users/"):
		id := strings.TrimPrefix(path, "/users/")
		email, ok := f.users[id]
		if !ok {
			return http.StatusNotFound, notFound
		}
		return http.StatusOK, map[string]any{"id": id, "email": email}

	case method == http.MethodGet && strings.HasPrefix(path, "/projects/"):
		id := strings.TrimPrefix(path, "/projects/")
		if !f.projects[id] {
			return http.StatusNotFound, notFound
		}
		return http.StatusOK, map[string]any{"id": id, "title": "polygon"}

	case method == http.MethodGet && path == "/boards":
		var items []any
		for _, b := range f.boards {
			if b["projectId"] == q.Get("projectId") {
				items = append(items, b)
			}
		}
		return http.StatusOK, f.page(items, q)

	case method == http.MethodGet && path == "/columns":
		var items []any
		for _, c := range f.columns {
			if c["boardId"] == q.Get("boardId") {
				items = append(items, c)
			}
		}
		return http.StatusOK, f.page(items, q)

	case method == http.MethodGet && path == "/task-list":
		var items []any
		for _, id := range f.order {
			task := f.tasks[id]
			if task.Deleted {
				continue
			}
			if col := q.Get("columnId"); col != "" && task.ColumnID != col {
				continue
			}
			items = append(items, task.dto())
		}
		return http.StatusOK, f.page(items, q)

	case method == http.MethodGet && strings.HasPrefix(path, "/tasks/"):
		task, ok := f.tasks[strings.TrimPrefix(path, "/tasks/")]
		if !ok || task.Deleted {
			return http.StatusNotFound, notFound
		}
		return http.StatusOK, task.dto()

	case method == http.MethodPost && path == "/tasks":
		body := decode()
		f.posts = append(f.posts, body)
		key, _ := body["idempotencyKey"].(string)
		if id, ok := f.idem[key]; ok && key != "" {
			return http.StatusCreated, map[string]any{"id": id}
		}
		f.nextID++
		id := fmt.Sprintf("task-new-%d", f.nextID)
		task := &fakeTask{ID: id, Timestamp: now.UnixMilli() + int64(f.nextID)}
		task.Title, _ = body["title"].(string)
		task.Description, _ = body["description"].(string)
		task.ColumnID, _ = body["columnId"].(string)
		task.APIData, _ = body["apiData"].(map[string]any)
		f.tasks[id] = task
		f.order = append(f.order, id)
		if key != "" {
			f.idem[key] = id
		}
		return http.StatusCreated, map[string]any{"id": id}

	case method == http.MethodPut && strings.HasPrefix(path, "/tasks/"):
		id := strings.TrimPrefix(path, "/tasks/")
		task, ok := f.tasks[id]
		if !ok {
			return http.StatusNotFound, notFound
		}
		body := decode()
		f.puts = append(f.puts, body)
		if v, ok := body["columnId"].(string); ok {
			task.ColumnID = v
		}
		if v, ok := body["apiData"].(map[string]any); ok {
			task.APIData = v // замена целиком: адаптер обязан писать объект полностью
		}
		if v, ok := body["deleted"].(bool); ok {
			task.Deleted = v
		}
		return http.StatusOK, map[string]any{"id": id}

	case strings.HasPrefix(path, "/chats/") && strings.HasSuffix(path, "/messages"):
		chat := strings.TrimSuffix(strings.TrimPrefix(path, "/chats/"), "/messages")
		if _, ok := f.tasks[chat]; !ok {
			return http.StatusNotFound, notFound
		}
		if method == http.MethodPost {
			body := decode()
			f.chatPosts = append(f.chatPosts, body)
			msg := fakeMessage{ID: now.UnixMilli() + int64(len(f.messages[chat])) + 1, From: f.me}
			msg.Text, _ = body["text"].(string)
			msg.HTML, _ = body["textHtml"].(string)
			f.messages[chat] = append(f.messages[chat], msg)
			return http.StatusCreated, map[string]any{"id": msg.ID}
		}
		// Новые первыми: адаптер обязан сортировать сам, а не полагаться на порядок сервера.
		var items []any
		msgs := f.messages[chat]
		for i := len(msgs) - 1; i >= 0; i-- {
			m := msgs[i]
			items = append(items, map[string]any{
				"id": m.ID, "fromUserId": m.From, "text": m.Text, "textHtml": m.HTML,
				"label": "", "editTimestamp": 0, "reactions": map[string]any{}, "deleted": m.Deleted,
			})
		}
		return http.StatusOK, f.page(items, q)
	}
	return http.StatusNotFound, map[string]any{"error": "no route " + method + " " + path}
}

func serve(t *testing.T, fake *fakeYouGile) string {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return server.URL
}

func testConfig(baseURL string) Config {
	return Config{
		BaseURL: baseURL, APIKey: "test-key", ProjectID: testProject,
		ColumnIDs:    map[string]string{"Ready": colReady, "InProgress": colWork, "Review": colReview},
		CreateStatus: "Ready",
	}
}

func fixture(t *testing.T) (*Tracker, *fakeYouGile) {
	t.Helper()
	fake := newFake(t)
	tr, err := Open(testConfig(serve(t, fake)))
	if err != nil {
		t.Fatalf("трекер не открыт: %v", err)
	}
	tr.Now = func() time.Time { return now }
	return tr, fake
}

func TestOpenRejectsIncompleteConfig(t *testing.T) {
	good := testConfig("https://yougile.example")
	cases := map[string]func(*Config){
		"нет base_url":            func(c *Config) { c.BaseURL = "" },
		"base_url без схемы":      func(c *Config) { c.BaseURL = "yougile.com" },
		"нет ключа API":           func(c *Config) { c.APIKey = "" },
		"нет проекта":             func(c *Config) { c.ProjectID = "" },
		"пустая карта статусов":   func(c *Config) { c.ColumnIDs = nil },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := good
			spoil(&cfg)
			if _, err := Open(cfg); err == nil {
				t.Fatal("Open принял неполную конфигурацию")
			}
		})
	}
}

func TestCallSendsBearerKey(t *testing.T) {
	tr, fake := fixture(t)
	var out struct {
		ID string `json:"id"`
	}
	if err := tr.call(http.MethodGet, "/projects/"+testProject, nil, nil, &out); err != nil {
		t.Fatalf("call: %v", err)
	}
	if out.ID != testProject {
		t.Errorf("ответ не разобран: %+v", out)
	}
	if fake.lastAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q", fake.lastAuth)
	}
}

func TestCallMapsNotFound(t *testing.T) {
	tr, _ := fixture(t)
	err := tr.call(http.MethodGet, "/tasks/missing", nil, nil, nil)
	if !errors.Is(err, tracker.ErrNotFound) {
		t.Errorf("404 дал %v, ожидался ErrNotFound", err)
	}
}

func TestCallErrorNeverLeaksAPIKey(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			tr, fake := fixture(t)
			fake.fail["GET /api-v2/users/me"] = code
			err := tr.call(http.MethodGet, "/users/me", nil, nil, nil)
			if err == nil {
				t.Fatal("ошибочный ответ прошёл как успех")
			}
			if strings.Contains(err.Error(), "test-key") {
				t.Errorf("ключ API в тексте ошибки: %v", err)
			}
			if !strings.Contains(err.Error(), strconv.Itoa(code)) {
				t.Errorf("в ошибке нет кода %d: %v", code, err)
			}
		})
	}
}

func TestListAllFollowsNextPastServerCap(t *testing.T) {
	tr, fake := fixture(t)
	fake.pageCap = 2
	for i := range 5 {
		fake.addTask(&fakeTask{ID: fmt.Sprintf("extra-%d", i), ColumnID: colReady, Timestamp: now.UnixMilli() + int64(i)})
	}
	got, err := listAll[struct {
		ID string `json:"id"`
	}](tr, "/task-list", url.Values{"columnId": {colReady}})
	if err != nil {
		t.Fatalf("listAll: %v", err)
	}
	if len(got) != 6 {
		t.Errorf("получено %d задач, ожидалось 6 (страницы по 2)", len(got))
	}
}

func TestListAllGivesUpOnEndlessPaging(t *testing.T) {
	tr, fake := fixture(t)
	fake.endlessPaging = true
	_, err := listAll[struct {
		ID string `json:"id"`
	}](tr, "/task-list", url.Values{"columnId": {colReady}})
	if err == nil || !strings.Contains(err.Error(), strconv.Itoa(maxPages)) {
		t.Errorf("бесконечная пагинация дала %v, ожидался отказ с упоминанием %d страниц", err, maxPages)
	}
}

func TestCheckProjectRejectsForeignProject(t *testing.T) {
	tr, _ := fixture(t)
	if err := tr.checkProject("OTHER"); !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("чужой проект дал %v, ожидался ErrNoProject", err)
	}
	if err := tr.checkProject(testProject); err != nil {
		t.Errorf("свой проект отвергнут: %v", err)
	}
}
```

- [x] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/`
Expected: FAIL at build time with `undefined: Open`, `undefined: Config`, `undefined: apiPrefix`, `undefined: listAll`, `undefined: maxPages`.

- [x] **Step 3: Write the minimal implementation**

Create `internal/tracker/yougile/yougile.go`:

```go
// Package yougile — трекер поверх YouGile REST API v2 (https://yougile.com/api-v2,
// спека — https://yougile.com/api-json).
//
// Ходим в настоящий REST, а не через yougile-mcp: обёртка не пробрасывает ни
// apiData, ни фильтры листинга. Клиентской библиотеки нет по той же причине,
// что у jira: нужен контроль над каждым запросом.
//
// Статус задачи — её колонка на доске (Config.ColumnIDs): в YouGile задача
// живёт ровно на одной доске и в одной колонке, и принцип «статус — не колонка»
// из docs/DESIGN.md, написанный для JIRA с её многими досками, здесь опоры не
// имеет (docs/openspec/changes/yougile-adapter-core/design.md, Decisions).
//
// Аренда, счётчик попыток, флаг «ждёт человека» и метки живут в apiData
// задачи — свободном JSON-поле (lease.go).
//
// Пакет пока не реализует tracker.Tracker целиком: LinkDependsOn и вложения
// добавит yougile-dependencies-attachments, и только тогда здесь появится
// проверка `var _ tracker.Tracker = (*Tracker)(nil)`.
package yougile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

// apiPrefix — префикс REST API; BaseURL — корень хоста.
const apiPrefix = "/api-v2"

// pageLimit — сколько элементов просить за страницу: максимум, который
// принимает API.
const pageLimit = 1000

// maxPages — потолок страниц на один листинг: сервер, который никогда не
// говорит «дальше пусто», не должен крутить цикл вечно.
const maxPages = 100

// Config — подключение и раскладка статусов по колонкам. Загрузчика из файла
// пока нет — его добавит yougile-wiring-and-docs; ключ API приходит из
// окружения на стороне вызывающего и сюда попадает уже значением.
type Config struct {
	// BaseURL — корень хоста, https://yougile.com. Поле, а не константа, —
	// чтобы тесты направляли трекер на httptest.
	BaseURL string
	// APIKey — ключ API. Не пишется ни в ошибки, ни в лог.
	APIKey string
	// ProjectID — id проекта YouGile. Один Tracker — один проект.
	ProjectID string
	// ColumnIDs — статус графа → id колонки. Колонки заводит человек; адаптер
	// их не создаёт, а на Open сверяет, что они есть.
	ColumnIDs map[string]string
	// CreateStatus — статус графа, в колонку которого CreateTask кладёт новую
	// задачу. Пусто — CreateTask откажет при вызове.
	CreateStatus string
}

// columnInfo — колонка проекта, кэшированная на Open.
type columnInfo struct {
	ID, Title, BoardID string
}

// Tracker — трекер поверх YouGile.
type Tracker struct {
	cfg    Config
	client *http.Client

	// columnStatus — обратная карта ColumnIDs: id колонки → статус графа.
	columnStatus map[string]string
	// columns — все колонки всех досок проекта, прочитанные один раз на Open.
	columns []columnInfo

	mu    sync.Mutex
	users map[string]string // id пользователя → email, кэш авторов комментариев

	// Now — часы раннера: аренду сверяем ими, а не серверными.
	Now func() time.Time
}

// Open готовит трекер: проверяет конфигурацию и ничего на сервере не создаёт.
func Open(cfg Config) (*Tracker, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("yougile: base_url не задан")
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("yougile: base_url не разобран: %w", err)
	}
	if base.Scheme == "" || base.Host == "" {
		return nil, fmt.Errorf("yougile: base_url=%q: нет схемы или хоста (пример: https://yougile.com)", cfg.BaseURL)
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.APIKey == "" {
		return nil, errors.New("yougile: ключ API не задан")
	}
	if cfg.ProjectID == "" {
		return nil, errors.New("yougile: id проекта не задан")
	}
	if len(cfg.ColumnIDs) == 0 {
		return nil, errors.New("yougile: карта статус → колонка пуста")
	}

	return &Tracker{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
		users:  map[string]string{},
		Now:    time.Now,
	}, nil
}

// checkProject — Tracker обслуживает ровно один проект YouGile.
func (t *Tracker) checkProject(project string) error {
	if project != t.cfg.ProjectID {
		return fmt.Errorf("%w: %q (этот трекер обслуживает проект YouGile %q)",
			tracker.ErrNoProject, project, t.cfg.ProjectID)
	}
	return nil
}

// call выполняет один запрос к API — без повторов: повтор делает следующий
// тик раннера (design.md, Decisions). Тело ответа при ошибке попадает в текст
// ошибки: YouGile объясняет отказ в нём.
func (t *Tracker) call(method, path string, query url.Values, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("запрос не сериализован: %w", err)
		}
		body = bytes.NewReader(raw)
	}

	target := t.cfg.BaseURL + apiPrefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return fmt.Errorf("запрос не собран: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+t.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return statusError(method, path, resp.StatusCode, raw)
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s %s: ответ не разобран: %w\n%s", method, path, err, snippet(raw))
	}
	return nil
}

// statusError различает беды, которые лечатся по-разному. Ключ API сюда
// не попадает никогда: в текст идут только метод, путь, код и тело ответа.
func statusError(method, path string, code int, body []byte) error {
	switch code {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s %s (404)", tracker.ErrNotFound, method, path)
	case http.StatusUnauthorized:
		return fmt.Errorf("%s %s: ключ API не принят (401): %s", method, path, snippet(body))
	case http.StatusForbidden:
		return fmt.Errorf("%s %s: доступ запрещён (403), ключу не хватает прав: %s", method, path, snippet(body))
	case http.StatusTooManyRequests:
		return fmt.Errorf("%s %s: превышен rate limit YouGile (429, не более 50 запросов в минуту на компанию): %s",
			method, path, snippet(body))
	default:
		return fmt.Errorf("%s %s: %d: %s", method, path, code, snippet(body))
	}
}

// snippet обрезает тело ответа до смысла.
func snippet(body []byte) string {
	const limit = 400
	text := strings.TrimSpace(string(body))
	if len(text) > limit {
		return text[:limit] + "…"
	}
	return text
}

// page — страница любого листинга API v2: paging + content.
type page[T any] struct {
	Paging struct {
		Next bool `json:"next"`
	} `json:"paging"`
	Content []T `json:"content"`
}

// listAll проходит листинг до конца по paging.next. Короткая страница конца
// не означает: у сервера бывает свой потолок ниже запрошенного limit.
func listAll[T any](t *Tracker, path string, query url.Values) ([]T, error) {
	q := url.Values{}
	maps.Copy(q, query)
	var all []T
	offset := 0
	for range maxPages {
		q.Set("limit", strconv.Itoa(pageLimit))
		q.Set("offset", strconv.Itoa(offset))
		var p page[T]
		if err := t.call(http.MethodGet, path, q, nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Content...)
		if !p.Paging.Next || len(p.Content) == 0 {
			return all, nil
		}
		offset += len(p.Content)
	}
	return nil, fmt.Errorf("GET %s: больше %d страниц, сервер не подтверждает конец списка", path, maxPages)
}
```

`endlessPaging` in the fake returns one item with `next=true` on every page. The loop therefore runs `maxPages` times and returns the error, whose text contains `100`.

- [x] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -run 'TestOpen|TestCall|TestListAll|TestCheckProject' -v`
Expected: PASS for all.

- [x] **Step 5: Commit**

```bash
gofmt -l internal/tracker/yougile/   # expect no output
git add internal/tracker/yougile/yougile.go internal/tracker/yougile/yougile_test.go docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): package scaffold, bearer HTTP helper, paginated listing"
```

---

### Task 2: Whoami (tasks.md 1.2)

**Files:**
- Modify: `internal/tracker/yougile/yougile.go` (append)
- Test: `internal/tracker/yougile/yougile_test.go` (append)

**Interfaces:**
- Consumes: `(*Tracker).call` from Task 1.
- Produces: `type userDTO struct { ID string \`json:"id"\`; Email string \`json:"email"\` }`; `func (t *Tracker) Whoami() (string, error)`. It returns the email of the key's user. Task 5 uses `userDTO` to resolve comment authors, and those emails are what gets compared against `Whoami`.

- [ ] **Step 1: Write the failing tests**

Append to `yougile_test.go`:

```go
func TestWhoamiReturnsEmail(t *testing.T) {
	tr, _ := fixture(t)
	who, err := tr.Whoami()
	if err != nil || who != "office@example.com" {
		t.Errorf("Whoami = %q, %v", who, err)
	}
}

// Пустой email — не «никто», а сломанный ответ: сравнивать авторов было бы
// не с чем, и каждый комментарий офиса сошёл бы за слова человека.
func TestWhoamiRejectsEmptyEmail(t *testing.T) {
	tr, fake := fixture(t)
	fake.users[officeUserID] = ""
	if _, err := tr.Whoami(); err == nil {
		t.Error("пустой email принят")
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run TestWhoami`
Expected: FAIL to build with `tr.Whoami undefined`.

- [ ] **Step 3: Write the minimal implementation**

Append to `yougile.go`:

```go
// userDTO — пользователь YouGile; нужны id и email.
type userDTO struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

// Whoami — email пользователя, чей ключ API у трекера. Авторов комментариев
// Get отдаёт тоже email'ами (comment.go), так что сравнение идёт по учётке,
// а не по тексту.
func (t *Tracker) Whoami() (string, error) {
	var me userDTO
	if err := t.call(http.MethodGet, "/users/me", nil, nil, &me); err != nil {
		return "", err
	}
	if me.Email == "" {
		return "", errors.New("yougile: /users/me не назвал email — сравнивать авторов комментариев не с чем")
	}
	return me.Email, nil
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -run TestWhoami -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/yougile.go internal/tracker/yougile/yougile_test.go docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): Whoami via /users/me"
```

---

### Task 3: Validate the status→column map at Open (tasks.md 2.1)

**Files:**
- Create: `internal/tracker/yougile/status.go`
- Modify: `internal/tracker/yougile/yougile.go` (move `columnInfo` to `status.go` and extend `Open`)
- Test: `internal/tracker/yougile/yougile_test.go` (append)

**Interfaces:**
- Consumes: `call`, `listAll`, `Config`, `Tracker` from Task 1.
- Produces: `type boardDTO`, `type columnDTO`, `type columnInfo` (moved), `func (t *Tracker) loadColumns() error`. After `Open`, `t.columnStatus` (column id → status) and `t.columns` (every live column of every board of the project) are filled.

- [ ] **Step 1: Write the failing tests**

Append to `yougile_test.go`:

```go
func openWith(t *testing.T, fake *fakeYouGile, spoil func(*Config)) (*Tracker, error) {
	t.Helper()
	cfg := testConfig(serve(t, fake))
	spoil(&cfg)
	return Open(cfg)
}

func TestOpenRejectsAmbiguousColumnMapping(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) {
		c.ColumnIDs = map[string]string{"Ready": colReady, "Backlog": colReady}
	})
	if err == nil {
		t.Fatal("одна колонка на два статуса принята")
	}
	for _, want := range []string{colReady, "Ready", "Backlog"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("в ошибке нет %q: %v", want, err)
		}
	}
}

func TestOpenRejectsEmptyColumnID(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) { c.ColumnIDs["Ready"] = "" })
	if err == nil || !strings.Contains(err.Error(), "Ready") {
		t.Errorf("пустой id колонки дал %v", err)
	}
}

func TestOpenRejectsMissingColumn(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) { c.ColumnIDs["Ready"] = "col-missing" })
	if err == nil || !strings.Contains(err.Error(), "col-missing") {
		t.Errorf("несуществующая колонка дала %v", err)
	}
}

// Колонка есть в компании, но на доске чужого проекта: для этого трекера её нет.
func TestOpenRejectsColumnOfAnotherProject(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) { c.ColumnIDs["Ready"] = "col-foreign" })
	if err == nil || !strings.Contains(err.Error(), "col-foreign") {
		t.Errorf("колонка чужого проекта дала %v", err)
	}
}

func TestOpenRejectsUnknownProject(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) { c.ProjectID = "nope" })
	if !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("незнакомый проект дал %v, ожидался ErrNoProject", err)
	}
}

func TestOpenRejectsCreateStatusOutsideMap(t *testing.T) {
	_, err := openWith(t, newFake(t), func(c *Config) { c.CreateStatus = "Backlog" })
	if err == nil || !strings.Contains(err.Error(), "Backlog") {
		t.Errorf("create_status вне карты дал %v", err)
	}
}

func TestOpenAllowsEmptyCreateStatus(t *testing.T) {
	if _, err := openWith(t, newFake(t), func(c *Config) { c.CreateStatus = "" }); err != nil {
		t.Errorf("пустой create_status отвергнут: %v", err)
	}
}

func TestOpenCachesProjectColumnsAndCreatesNothing(t *testing.T) {
	tr, fake := fixture(t)
	if len(tr.columns) != 4 {
		t.Errorf("кэш колонок: %+v, ожидались 4 колонки board-1", tr.columns)
	}
	for _, c := range tr.columns {
		if c.ID == "col-foreign" {
			t.Errorf("в кэш попала колонка чужого проекта")
		}
	}
	if tr.columnStatus[colWork] != "InProgress" {
		t.Errorf("обратная карта: %+v", tr.columnStatus)
	}
	if n := fake.count("POST") + fake.count("PUT"); n != 0 {
		t.Errorf("Open что-то записал: %d запросов записи", n)
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run TestOpen`
Expected: FAIL. `TestOpenRejectsAmbiguousColumnMapping`, `TestOpenRejectsMissingColumn`, `TestOpenRejectsColumnOfAnotherProject`, `TestOpenRejectsUnknownProject`, `TestOpenRejectsCreateStatusOutsideMap` and `TestOpenRejectsEmptyColumnID` report "принята"/nil errors. `TestOpenCachesProjectColumnsAndCreatesNothing` reports an empty cache.

- [ ] **Step 3: Write the minimal implementation**

In `yougile.go`, delete the `columnInfo` type (it moves to `status.go`). In `Open`, replace the final `return &Tracker{...}, nil` with:

```go
	// Обратная карта: строим один раз и падаем на неоднозначности сразу, а не
	// посреди первого цикла. Обход в порядке ключей — ошибка одна и та же при
	// каждом запуске.
	columnStatus := make(map[string]string, len(cfg.ColumnIDs))
	for _, status := range slices.Sorted(maps.Keys(cfg.ColumnIDs)) {
		id := cfg.ColumnIDs[status]
		if id == "" {
			return nil, fmt.Errorf("yougile: у статуса %q пустой id колонки", status)
		}
		if before, found := columnStatus[id]; found {
			return nil, fmt.Errorf("yougile: колонка %q сопоставлена и с %q, и с %q", id, before, status)
		}
		columnStatus[id] = status
	}
	if cfg.CreateStatus != "" {
		if _, ok := cfg.ColumnIDs[cfg.CreateStatus]; !ok {
			return nil, fmt.Errorf("yougile: create_status %q не входит в карту статус → колонка", cfg.CreateStatus)
		}
	}

	t := &Tracker{
		cfg:          cfg,
		client:       &http.Client{Timeout: 30 * time.Second},
		columnStatus: columnStatus,
		users:        map[string]string{},
		Now:          time.Now,
	}
	if err := t.loadColumns(); err != nil {
		return nil, err
	}
	return t, nil
```

Add `"slices"` to the `yougile.go` imports.

Create `internal/tracker/yougile/status.go`:

```go
package yougile

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/kao73/virtual-office/internal/tracker"
)

type boardDTO struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Deleted   bool   `json:"deleted"`
}

type columnDTO struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	BoardID string `json:"boardId"`
	Deleted bool   `json:"deleted"`
}

// columnInfo — колонка проекта, кэшированная на Open.
type columnInfo struct {
	ID, Title, BoardID string
}

// loadColumns читает доски и колонки проекта один раз и сверяет с ними
// ColumnIDs. Ничего не создаёт: колонки заводит человек, а опечатка в id
// должна ронять Open, а не тихо давать пустую очередь.
func (t *Tracker) loadColumns() error {
	if err := t.call(http.MethodGet, "/projects/"+url.PathEscape(t.cfg.ProjectID), nil, nil, nil); err != nil {
		if errors.Is(err, tracker.ErrNotFound) {
			return fmt.Errorf("%w: проект YouGile %q", tracker.ErrNoProject, t.cfg.ProjectID)
		}
		return err
	}

	boards, err := listAll[boardDTO](t, "/boards", url.Values{"projectId": {t.cfg.ProjectID}})
	if err != nil {
		return err
	}
	var columns []columnInfo
	for _, board := range boards {
		if board.Deleted || board.ProjectID != t.cfg.ProjectID {
			continue
		}
		list, err := listAll[columnDTO](t, "/columns", url.Values{"boardId": {board.ID}})
		if err != nil {
			return err
		}
		for _, c := range list {
			if c.Deleted || c.BoardID != board.ID {
				continue
			}
			columns = append(columns, columnInfo{ID: c.ID, Title: c.Title, BoardID: c.BoardID})
		}
	}

	known := make(map[string]bool, len(columns))
	for _, c := range columns {
		known[c.ID] = true
	}
	var missing []string
	for _, status := range slices.Sorted(maps.Keys(t.cfg.ColumnIDs)) {
		if id := t.cfg.ColumnIDs[status]; !known[id] {
			missing = append(missing, status+"="+id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("yougile: в проекте %q нет колонок %s — заведи их на доске, адаптер колонок не создаёт",
			t.cfg.ProjectID, strings.Join(missing, ", "))
	}
	t.columns = columns
	return nil
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS for every test so far, including Task 1's (the fixture now performs the column fetch against the fake).

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): validate status→column map against live board columns at Open"
```

---

### Task 4: apiData codec (tasks.md 3.1)

**Files:**
- Create: `internal/tracker/yougile/lease.go`
- Create: `internal/tracker/yougile/lease_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks. The codec is pure.
- Produces:
  - `type leaseData struct { Owner string \`json:"owner"\`; RunID string \`json:"run_id"\`; LeaseUntil time.Time \`json:"lease_until"\` }`
  - `type apiData struct { Lease *leaseData; Attempts int; HumanWait bool; Labels []string; extra map[string]json.RawMessage }`
  - consts `keyLease = "lease"`, `keyAttempts = "attempts"`, `keyHumanWait = "human_wait"`, `keyLabels = "labels"`
  - `func decodeAPIData(raw json.RawMessage) (apiData, error)`
  - `func (d apiData) encode() map[string]any`. It always contains all four own keys (`lease` is `nil` when the task is free) plus every foreign key unchanged.

- [ ] **Step 1: Write the failing tests**

Create `internal/tracker/yougile/lease_test.go`:

```go
package yougile

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

// roundTrip — encode и обратно через настоящий JSON, как это пройдёт по проводу.
func roundTrip(t *testing.T, d apiData) map[string]any {
	t.Helper()
	raw, err := json.Marshal(d.encode())
	if err != nil {
		t.Fatalf("encode не сериализуется: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("encode дал не объект: %v", err)
	}
	return out
}

func TestDecodeAPIDataEmptyIsZero(t *testing.T) {
	for _, raw := range []string{"", "null", "  ", "{}"} {
		d, err := decodeAPIData(json.RawMessage(raw))
		if err != nil {
			t.Errorf("%q: %v", raw, err)
		}
		if d.Lease != nil || d.Attempts != 0 || d.HumanWait || d.Labels != nil || d.extra != nil {
			t.Errorf("%q дал не нулевое значение: %+v", raw, d)
		}
	}
}

func TestDecodeAPIDataReadsOwnKeys(t *testing.T) {
	until := time.Date(2026, 9, 28, 12, 30, 0, 123456789, time.UTC)
	raw := `{"lease":{"owner":"implementer","run_id":"run-1","lease_until":"` + until.Format(time.RFC3339Nano) +
		`"},"attempts":2,"human_wait":true,"labels":["split:VO-1:a"]}`
	d, err := decodeAPIData(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if d.Lease == nil || d.Lease.Owner != "implementer" || d.Lease.RunID != "run-1" || !d.Lease.LeaseUntil.Equal(until) {
		t.Errorf("аренда: %+v", d.Lease)
	}
	if d.Attempts != 2 || !d.HumanWait || !reflect.DeepEqual(d.Labels, []string{"split:VO-1:a"}) {
		t.Errorf("поля: %+v", d)
	}
}

// Review Focus #2: ключи, записанные не нами, переживают любую нашу запись.
func TestAPIDataKeepsForeignKeys(t *testing.T) {
	raw := `{"crm":{"deal":42,"tags":["a","b"]},"depends_on":["task-9"],"attempts":1}`
	d, err := decodeAPIData(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	d.Attempts = 5
	out := roundTrip(t, d)
	if !reflect.DeepEqual(out["crm"], map[string]any{"deal": float64(42), "tags": []any{"a", "b"}}) {
		t.Errorf("чужой ключ crm искажён: %#v", out["crm"])
	}
	if !reflect.DeepEqual(out["depends_on"], []any{"task-9"}) {
		t.Errorf("чужой ключ depends_on искажён: %#v", out["depends_on"])
	}
	if out["attempts"] != float64(5) {
		t.Errorf("attempts = %#v", out["attempts"])
	}
}

// Review Focus #3: свои ключи пишутся всегда, аренда — явным null, иначе
// при слиянии apiData на сервере снятая аренда не снялась бы.
func TestEncodeAlwaysWritesOwnKeys(t *testing.T) {
	out := roundTrip(t, apiData{})
	for _, key := range []string{keyLease, keyAttempts, keyHumanWait, keyLabels} {
		if _, ok := out[key]; !ok {
			t.Errorf("ключ %q не записан: %#v", key, out)
		}
	}
	if out[keyLease] != nil {
		t.Errorf("свободная аренда записана как %#v, ожидался null", out[keyLease])
	}
	if !reflect.DeepEqual(out[keyLabels], []any{}) {
		t.Errorf("пустые метки записаны как %#v, ожидался []", out[keyLabels])
	}
}

func TestDecodeAPIDataRejectsGarbage(t *testing.T) {
	for _, raw := range []string{`"string"`, `[1,2]`, `{"lease":"not an object"}`, `{"attempts":"three"}`} {
		if _, err := decodeAPIData(json.RawMessage(raw)); err == nil {
			t.Errorf("%s принят", raw)
		}
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run 'APIData|Encode'`
Expected: FAIL to build with `undefined: apiData`, `undefined: decodeAPIData`, `undefined: keyLease`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/tracker/yougile/lease.go`:

```go
package yougile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Ключи apiData, которыми владеет адаптер. yougile-dependencies-attachments
// добавит сюда depends_on и манифест вложений — теми же соседями верхнего уровня.
const (
	keyLease     = "lease"
	keyAttempts  = "attempts"
	keyHumanWait = "human_wait"
	keyLabels    = "labels"
)

// leaseData — аренда: пишется и снимается целиком.
type leaseData struct {
	Owner      string    `json:"owner"`
	RunID      string    `json:"run_id"`
	LeaseUntil time.Time `json:"lease_until"`
}

// apiData — наш взгляд на apiData задачи. attempts и human_wait живут вне
// lease: Release их не трогает, как jira не трогает attempts и метку человека.
//
// Инвариант каждой записи: apiData читается целиком, меняется и пишется
// целиком. extra держит ключи, которых адаптер не знает, — без него запись
// молча стирала бы чужие данные.
type apiData struct {
	Lease     *leaseData
	Attempts  int
	HumanWait bool
	Labels    []string
	extra     map[string]json.RawMessage
}

// decodeAPIData разбирает apiData. Пусто и null — нулевое значение: задача,
// которую офис ни разу не трогал, свободна.
func decodeAPIData(raw json.RawMessage) (apiData, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return apiData{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return apiData{}, fmt.Errorf("apiData не JSON-объект: %w", err)
	}

	var d apiData
	targets := map[string]any{keyLease: &d.Lease, keyAttempts: &d.Attempts, keyHumanWait: &d.HumanWait, keyLabels: &d.Labels}
	for key, target := range targets {
		value, ok := fields[key]
		if !ok {
			continue
		}
		delete(fields, key)
		if err := json.Unmarshal(value, target); err != nil {
			return apiData{}, fmt.Errorf("apiData.%s: %w", key, err)
		}
	}
	if len(fields) > 0 {
		d.extra = fields
	}
	return d, nil
}

// encode — объект целиком для PUT: чужие ключи как были, свои — все и явно.
// lease пишется null'ом, а не пропускается: если сервер сливает apiData,
// а не заменяет, пропущенный ключ оставил бы аренду висеть.
func (d apiData) encode() map[string]any {
	out := make(map[string]any, len(d.extra)+4)
	for key, value := range d.extra {
		out[key] = value
	}
	out[keyLease] = d.Lease // nil *leaseData сериализуется в null
	out[keyAttempts] = d.Attempts
	out[keyHumanWait] = d.HumanWait
	labels := d.Labels
	if labels == nil {
		labels = []string{}
	}
	out[keyLabels] = labels
	return out
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -run 'APIData|Encode' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/lease.go internal/tracker/yougile/lease_test.go docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): apiData codec that keeps foreign keys and writes own keys explicitly"
```

---

### Task 5: Get with comments and author emails (tasks.md 2.4)

**Files:**
- Create: `internal/tracker/yougile/task.go`
- Create: `internal/tracker/yougile/comment.go`
- Create: `internal/tracker/yougile/task_test.go`

**Interfaces:**
- Consumes: `call`, `listAll`, `userDTO` (Tasks 1–2), `decodeAPIData`, `apiData` (Task 4), `t.columnStatus` (Task 3).
- Produces:
  - `type taskDTO struct { ID, Title, Description, ColumnID string; Timestamp float64; Archived, Deleted bool; APIData json.RawMessage }` with JSON tags `id,title,description,columnId,timestamp,archived,deleted,apiData`
  - `var ErrUnmappedColumn = errors.New(...)`
  - `func (t *Tracker) getRaw(key string) (taskDTO, error)`
  - `func (t *Tracker) toTask(raw taskDTO) (tracker.Task, apiData, error)`
  - `func (t *Tracker) Get(key string) (tracker.Task, error)`
  - `type messageDTO struct { ID float64; FromUserID, Text string; Deleted bool }`
  - `func (t *Tracker) comments(key string) ([]tracker.Comment, error)`
  - `func (t *Tracker) userEmail(id string) (string, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/tracker/yougile/task_test.go`:

```go
package yougile

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

func TestGetMapsColumnAndAPIData(t *testing.T) {
	tr, fake := fixture(t)
	until := now.Add(30 * time.Minute)
	fake.tasks[testKey].APIData = map[string]any{
		"lease":    map[string]any{"owner": "implementer", "run_id": "run-1", "lease_until": until.Format(time.RFC3339Nano)},
		"attempts": 2, "human_wait": true, "labels": []any{"m-1"},
	}

	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	want := tracker.Task{
		Key: testKey, Project: testProject, Summary: "First task", Description: "Do it", Status: "Ready",
		Labels: []string{"m-1"}, Owner: "implementer", RunID: "run-1", LeaseUntil: until,
		Attempts: 2, HumanFlag: true,
		Comments: []tracker.Comment{}, // Get отдаёт пустую, не nil, переписку
	}
	task.LeaseUntil = task.LeaseUntil.UTC()
	if !reflect.DeepEqual(task, want) {
		t.Errorf("Get =\n%+v\nожидалось\n%+v", task, want)
	}
}

func TestGetReadsCommentsOldestFirstWithAuthorEmails(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{
		{ID: 1000, From: humanUserID, Text: "вопрос человека"},
		{ID: 2000, From: officeUserID, Text: "[office run:r1 role:analyst]\nответ"},
	}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	want := []tracker.Comment{
		{ID: "1000", Author: "human@example.com", Created: time.UnixMilli(1000).UTC(), Body: "вопрос человека"},
		{ID: "2000", Author: "office@example.com", Created: time.UnixMilli(2000).UTC(), Body: "[office run:r1 role:analyst]\nответ"},
	}
	if !reflect.DeepEqual(task.Comments, want) {
		t.Errorf("комментарии:\n%+v\nожидалось\n%+v", task.Comments, want)
	}
}

func TestGetSkipsDeletedMessages(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{{ID: 1000, From: humanUserID, Text: "стёрто", Deleted: true}}
	task, err := tr.Get(testKey)
	if err != nil || len(task.Comments) != 0 {
		t.Errorf("удалённое сообщение попало в переписку: %+v, %v", task.Comments, err)
	}
}

func TestGetCachesAuthorLookups(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{
		{ID: 1, From: humanUserID, Text: "a"}, {ID: 2, From: humanUserID, Text: "b"}, {ID: 3, From: humanUserID, Text: "c"},
	}
	for range 2 {
		if _, err := tr.Get(testKey); err != nil {
			t.Fatal(err)
		}
	}
	if n := fake.count("GET /api-v2/users/" + humanUserID); n != 1 {
		t.Errorf("email автора спрошен %d раз, ожидался 1", n)
	}
}

// Уволенного из компании автора сервер не отдаёт. Его id — не учётка офиса,
// значит слова человека; ронять из-за этого Get нельзя.
func TestGetKeepsCommentOfVanishedAuthor(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{{ID: 1, From: "user-gone", Text: "старое"}}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Comments) != 1 || task.Comments[0].Author != "user-gone" {
		t.Errorf("комментарий пропавшего автора: %+v", task.Comments)
	}
}

func TestGetUnknownTaskIsNotFound(t *testing.T) {
	tr, _ := fixture(t)
	if _, err := tr.Get("missing"); !errors.Is(err, tracker.ErrNotFound) {
		t.Errorf("Get(missing) дал %v", err)
	}
}

// Review Focus #1: карточку утащили в колонку вне графа — громкая ошибка,
// а не выдуманный статус.
func TestGetTaskInUnmappedColumnFails(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].ColumnID = colOutside
	_, err := tr.Get(testKey)
	if !errors.Is(err, ErrUnmappedColumn) || !strings.Contains(err.Error(), colOutside) {
		t.Errorf("задача вне графа дала %v", err)
	}
}

func TestGetMalformedAPIDataNamesTask(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].APIData = map[string]any{"lease": "garbage"}
	_, err := tr.Get(testKey)
	if err == nil || !strings.Contains(err.Error(), testKey) {
		t.Errorf("битый apiData дал %v, ожидалась ошибка с ключом задачи", err)
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run TestGet`
Expected: FAIL to build with `tr.Get undefined`, `undefined: ErrUnmappedColumn`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/tracker/yougile/task.go`:

```go
package yougile

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"

	"github.com/kao73/virtual-office/internal/tracker"
)

// ErrUnmappedColumn — задача стоит в колонке, которой не сопоставлен ни один
// статус графа: человек завёл колонку вне графа или утащил туда карточку.
// Статус не выдумывается — ошибка громкая.
var ErrUnmappedColumn = errors.New("yougile: задача в колонке вне карты статусов")

// taskDTO — задача в ответе API: то, что читает адаптер.
type taskDTO struct {
	ID          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	ColumnID    string          `json:"columnId"`
	Timestamp   float64         `json:"timestamp"` // мс создания
	Archived    bool            `json:"archived"`
	Deleted     bool            `json:"deleted"`
	APIData     json.RawMessage `json:"apiData"`
}

// getRaw — задача без переписки: одного запроса хватает и проверке владения,
// и захвату; чат нужен только Get.
func (t *Tracker) getRaw(key string) (taskDTO, error) {
	var raw taskDTO
	if err := t.call(http.MethodGet, "/tasks/"+url.PathEscape(key), nil, nil, &raw); err != nil {
		return taskDTO{}, err
	}
	if raw.Deleted {
		return taskDTO{}, fmt.Errorf("%w: задача YouGile %s удалена", tracker.ErrNotFound, key)
	}
	return raw, nil
}

// toTask переводит задачу API в модель раннера. apiData возвращается рядом —
// его перепишут и отправят обратно целиком те, кто мутирует задачу.
func (t *Tracker) toTask(raw taskDTO) (tracker.Task, apiData, error) {
	data, err := decodeAPIData(raw.APIData)
	if err != nil {
		return tracker.Task{}, apiData{}, fmt.Errorf("задача YouGile %s: %w", raw.ID, err)
	}
	status, ok := t.columnStatus[raw.ColumnID]
	if !ok {
		return tracker.Task{}, apiData{}, fmt.Errorf("%w: задача %s стоит в колонке %q, которой не сопоставлен ни один статус",
			ErrUnmappedColumn, raw.ID, raw.ColumnID)
	}
	task := tracker.Task{
		Key: raw.ID, Project: t.cfg.ProjectID, Summary: raw.Title, Description: raw.Description,
		Status: status, Labels: slices.Clone(data.Labels), Attempts: data.Attempts, HumanFlag: data.HumanWait,
	}
	if data.Lease != nil {
		task.Owner, task.RunID, task.LeaseUntil = data.Lease.Owner, data.Lease.RunID, data.Lease.LeaseUntil
	}
	return task, data, nil
}

// Get — задача целиком, включая всю переписку чата задачи.
func (t *Tracker) Get(key string) (tracker.Task, error) {
	raw, err := t.getRaw(key)
	if err != nil {
		return tracker.Task{}, err
	}
	task, _, err := t.toTask(raw)
	if err != nil {
		return tracker.Task{}, err
	}
	comments, err := t.comments(key)
	if err != nil {
		return tracker.Task{}, err
	}
	task.Comments = comments
	return task, nil
}
```

Create `internal/tracker/yougile/comment.go`:

```go
package yougile

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

// messageDTO — сообщение чата задачи. id — одновременно время создания в мс.
type messageDTO struct {
	ID         float64 `json:"id"`
	FromUserID string  `json:"fromUserId"`
	Text       string  `json:"text"`
	Deleted    bool    `json:"deleted"`
}

// comments — переписка задачи от старых к новым. Чат задачи в YouGile
// адресуется id самой задачи. Порядок сервера не обещан — сортируем сами.
func (t *Tracker) comments(key string) ([]tracker.Comment, error) {
	msgs, err := listAll[messageDTO](t, "/chats/"+url.PathEscape(key)+"/messages", nil)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(msgs, func(a, b messageDTO) int { return cmp.Compare(a.ID, b.ID) })

	comments := make([]tracker.Comment, 0, len(msgs))
	for _, m := range msgs {
		if m.Deleted {
			continue
		}
		author, err := t.userEmail(m.FromUserID)
		if err != nil {
			return nil, err
		}
		ms := int64(m.ID)
		comments = append(comments, tracker.Comment{
			ID: strconv.FormatInt(ms, 10), Author: author, Created: time.UnixMilli(ms).UTC(), Body: m.Text,
		})
	}
	return comments, nil
}

// userEmail — email автора по id, с кэшем на весь Tracker: авторов в переписке
// немного, а rate limit — 50 запросов в минуту на компанию.
//
// Пользователя, которого сервер больше не знает (удалён из компании), автором
// называет его id: учёткой офиса он не совпадёт ни с чем, то есть это слова
// человека, — и ронять из-за него чтение задачи незачем.
func (t *Tracker) userEmail(id string) (string, error) {
	t.mu.Lock()
	email, ok := t.users[id]
	t.mu.Unlock()
	if ok {
		return email, nil
	}

	var user userDTO
	err := t.call(http.MethodGet, "/users/"+url.PathEscape(id), nil, nil, &user)
	switch {
	case errors.Is(err, tracker.ErrNotFound):
		email = id
	case err != nil:
		return "", fmt.Errorf("автор комментария %s не определён: %w", id, err)
	default:
		email = user.Email
	}

	t.mu.Lock()
	t.users[id] = email
	t.mu.Unlock()
	return email, nil
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): Get with column-derived status, apiData lease and chat comments"
```

---

### Task 6: List and ListReady (tasks.md 2.2)

**Files:**
- Modify: `internal/tracker/yougile/status.go` (append)
- Create: `internal/tracker/yougile/status_test.go`

**Interfaces:**
- Consumes: `listAll`, `checkProject`, `taskDTO`, `toTask` (Tasks 1, 5).
- Produces:
  - `func (t *Tracker) columnFor(status string) (string, error)`
  - `func (t *Tracker) tasksInColumn(columnID string) ([]taskDTO, error)`. Drops deleted and archived tasks, and tasks whose `columnId` differs from the filter.
  - `func (t *Tracker) collect(columnIDs []string, keep func(tracker.Task) bool) ([]tracker.TaskRef, error)`. Sorts by `Timestamp` ascending, then by `ID`.
  - `func (t *Tracker) ListReady(project, status string) ([]tracker.TaskRef, error)`
  - `func (t *Tracker) List(project string, statuses []string) ([]tracker.TaskRef, error)`

- [ ] **Step 1: Write the failing tests**

Create `internal/tracker/yougile/status_test.go`:

```go
package yougile

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

func keys(refs []tracker.TaskRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Key)
	}
	return out
}

func TestListReadyFiltersByColumnServerSide(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "in-review", ColumnID: colReview, Timestamp: now.UnixMilli()})
	refs, err := tr.ListReady(testProject, "Ready")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(keys(refs), []string{testKey}) {
		t.Errorf("ListReady = %v", keys(refs))
	}
	if refs[0].Status != "Ready" || refs[0].Project != testProject {
		t.Errorf("ref: %+v", refs[0])
	}
	if fake.count("GET /api-v2/task-list?columnId="+colReady) != 1 {
		t.Errorf("фильтр по колонке не ушёл на сервер: %v", fake.requests)
	}
}

func TestListReadyDropsLiveLeaseKeepsExpired(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "expired", ColumnID: colReady, Timestamp: now.UnixMilli()})
	fake.setLease(testKey, "run-live", now.Add(time.Minute))
	fake.setLease("expired", "run-old", now.Add(-time.Minute))
	refs, err := tr.ListReady(testProject, "Ready")
	if err != nil || !slices.Equal(keys(refs), []string{"expired"}) {
		t.Errorf("ListReady = %v, %v", keys(refs), err)
	}
}

func TestListReadySortsByCreation(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "oldest", ColumnID: colReady, Timestamp: now.Add(-3 * time.Hour).UnixMilli()})
	fake.addTask(&fakeTask{ID: "newest", ColumnID: colReady, Timestamp: now.UnixMilli()})
	refs, _ := tr.ListReady(testProject, "Ready")
	if !slices.Equal(keys(refs), []string{"oldest", testKey, "newest"}) {
		t.Errorf("порядок: %v", keys(refs))
	}
}

// Review Focus #4.
func TestListReadySkipsArchivedAndDeleted(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "archived", ColumnID: colReady, Timestamp: now.UnixMilli(), Archived: true})
	fake.addTask(&fakeTask{ID: "deleted", ColumnID: colReady, Timestamp: now.UnixMilli(), Deleted: true})
	refs, _ := tr.ListReady(testProject, "Ready")
	if !slices.Equal(keys(refs), []string{testKey}) {
		t.Errorf("ListReady = %v", keys(refs))
	}
	all, _ := tr.List(testProject, []string{"Ready"})
	if !slices.Equal(keys(all), []string{testKey}) {
		t.Errorf("List = %v", keys(all))
	}
}

// Review Focus #5.
func TestListReadyPaginates(t *testing.T) {
	tr, fake := fixture(t)
	fake.pageCap = 2
	for i := range 5 {
		fake.addTask(&fakeTask{ID: fmt.Sprintf("extra-%d", i), ColumnID: colReady, Timestamp: now.UnixMilli() + int64(i)})
	}
	refs, err := tr.ListReady(testProject, "Ready")
	if err != nil || len(refs) != 6 {
		t.Errorf("ListReady за страницами: %d задач, %v", len(refs), err)
	}
}

func TestListReadyUnknownStatusFails(t *testing.T) {
	tr, _ := fixture(t)
	_, err := tr.ListReady(testProject, "Backlog")
	if err == nil || !strings.Contains(err.Error(), "Backlog") {
		t.Errorf("незнакомый статус дал %v", err)
	}
}

func TestListReadyUnknownProject(t *testing.T) {
	tr, _ := fixture(t)
	if _, err := tr.ListReady("OTHER", "Ready"); !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("чужой проект дал %v", err)
	}
}

func TestListKeepsLeasedTasksAcrossStatuses(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "in-review", ColumnID: colReview, Timestamp: now.UnixMilli()})
	fake.setLease(testKey, "run-live", now.Add(time.Minute))
	refs, err := tr.List(testProject, []string{"Ready", "Review"})
	if err != nil || !slices.Equal(keys(refs), []string{testKey, "in-review"}) {
		t.Errorf("List = %v, %v", keys(refs), err)
	}
	if refs[0].RunID != "run-live" {
		t.Errorf("аренда не видна в List: %+v", refs[0])
	}
}

func TestListWithoutStatusesAsksNothing(t *testing.T) {
	tr, fake := fixture(t)
	before := len(fake.requests)
	refs, err := tr.List(testProject, nil)
	if refs != nil || err != nil || len(fake.requests) != before {
		t.Errorf("List(nil) = %v, %v; запросов %d", refs, err, len(fake.requests)-before)
	}
}

func TestListingDoesNotRefetchColumns(t *testing.T) {
	tr, fake := fixture(t)
	for range 3 {
		if _, err := tr.List(testProject, []string{"Ready", "Review"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := fake.count("GET /api-v2/columns"); n != 1 {
		t.Errorf("колонки спрошены %d раз, ожидался 1 (на Open)", n)
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run 'TestList'`
Expected: FAIL to build with `tr.ListReady undefined`, `tr.List undefined`.

- [ ] **Step 3: Write the minimal implementation**

Append to `status.go` (and add `"cmp"` to its imports):

```go
// columnFor — колонка статуса графа; незнакомый статус — ошибка конфигурации.
func (t *Tracker) columnFor(status string) (string, error) {
	id, ok := t.cfg.ColumnIDs[status]
	if !ok {
		return "", fmt.Errorf("yougile: статусу %q не сопоставлена колонка", status)
	}
	return id, nil
}

// tasksInColumn — живые задачи колонки, фильтр по columnId — на сервере.
// Архивные и удалённые отбрасываются: архивная карточка в колонке Done не
// должна снова стать кандидатом. Сверка columnId — страховка на случай,
// если сервер фильтр проигнорирует.
func (t *Tracker) tasksInColumn(columnID string) ([]taskDTO, error) {
	all, err := listAll[taskDTO](t, "/task-list", url.Values{"columnId": {columnID}})
	if err != nil {
		return nil, err
	}
	live := all[:0]
	for _, task := range all {
		if task.Deleted || task.Archived || task.ColumnID != columnID {
			continue
		}
		live = append(live, task)
	}
	return live, nil
}

// collect — задачи названных колонок, от старых к новым (FIFO: приоритета
// в YouGile нет, design.md Decisions), отфильтрованные keep.
func (t *Tracker) collect(columnIDs []string, keep func(tracker.Task) bool) ([]tracker.TaskRef, error) {
	var raws []taskDTO
	for _, id := range columnIDs {
		tasks, err := t.tasksInColumn(id)
		if err != nil {
			return nil, err
		}
		raws = append(raws, tasks...)
	}
	slices.SortStableFunc(raws, func(a, b taskDTO) int {
		return cmp.Or(cmp.Compare(a.Timestamp, b.Timestamp), cmp.Compare(a.ID, b.ID))
	})

	refs := make([]tracker.TaskRef, 0, len(raws))
	for _, raw := range raws {
		task, _, err := t.toTask(raw)
		if err != nil {
			return nil, err
		}
		if keep(task) {
			refs = append(refs, task.Ref())
		}
	}
	return refs, nil
}

// ListReady — кандидаты статуса: без живой аренды, от старых к новым.
func (t *Tracker) ListReady(project, status string) ([]tracker.TaskRef, error) {
	if err := t.checkProject(project); err != nil {
		return nil, err
	}
	id, err := t.columnFor(status)
	if err != nil {
		return nil, err
	}
	return t.collect([]string{id}, func(task tracker.Task) bool { return !task.LeaseAlive(t.Now()) })
}

// List — задачи названных статусов как есть, с живой арендой тоже: этот
// список смотрит человек, и «кто работает сейчас» — первое, что он ищет.
func (t *Tracker) List(project string, statuses []string) ([]tracker.TaskRef, error) {
	if err := t.checkProject(project); err != nil {
		return nil, err
	}
	if len(statuses) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(statuses))
	for _, status := range statuses {
		id, err := t.columnFor(status)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return t.collect(ids, func(tracker.Task) bool { return true })
}
```

`TaskRef.Updated` stays zero because YouGile's `TaskDto` has no last-modified timestamp. `runner ls` then shows no age for YouGile tasks. `yougile-wiring-and-docs` should record this.

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): List/ListReady by server-side columnId filter, FIFO order"
```

---

### Task 7: ListExpired (tasks.md 2.3)

**Files:**
- Modify: `internal/tracker/yougile/status.go` (append)
- Test: `internal/tracker/yougile/status_test.go` (append)

**Interfaces:**
- Consumes: `collect`, `checkProject` (Task 6).
- Produces: `func (t *Tracker) configuredColumns() []string` (the values of `ColumnIDs`, sorted); `func (t *Tracker) ListExpired(project string, now time.Time) ([]tracker.TaskRef, error)`.

- [ ] **Step 1: Write the failing tests**

Append to `status_test.go`:

```go
func TestListExpiredScansEveryConfiguredColumn(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "stuck-review", ColumnID: colReview, Timestamp: now.UnixMilli()})
	fake.addTask(&fakeTask{ID: "live-work", ColumnID: colWork, Timestamp: now.UnixMilli()})
	fake.setLease("stuck-review", "run-dead", now.Add(-time.Minute))
	fake.setLease("live-work", "run-live", now.Add(time.Minute))
	// testKey без аренды вовсе — не истёкшая, а свободная.

	refs, err := tr.ListExpired(testProject, now)
	if err != nil || !slices.Equal(keys(refs), []string{"stuck-review"}) {
		t.Errorf("ListExpired = %v, %v", keys(refs), err)
	}
	if fake.count("GET /api-v2/task-list?columnId="+colOutside) != 0 {
		t.Error("ListExpired заглянул в колонку вне графа")
	}
}

func TestListExpiredUsesGivenNow(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	refs, _ := tr.ListExpired(testProject, now.Add(2*time.Minute))
	if !slices.Equal(keys(refs), []string{testKey}) {
		t.Errorf("ListExpired(now+2m) = %v", keys(refs))
	}
}

func TestListExpiredUnknownProject(t *testing.T) {
	tr, _ := fixture(t)
	if _, err := tr.ListExpired("OTHER", now); !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("чужой проект дал %v", err)
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run TestListExpired`
Expected: FAIL to build with `tr.ListExpired undefined`.

- [ ] **Step 3: Write the minimal implementation**

Append to `status.go` (and add `"time"` to its imports):

```go
// configuredColumns — колонки всех статусов графа, в стабильном порядке.
func (t *Tracker) configuredColumns() []string {
	return slices.Sorted(maps.Values(t.cfg.ColumnIDs))
}

// ListExpired — задачи с истёкшей арендой в любом статусе графа: сырьё для
// reaper. Колонки вне графа не смотрит — их задачи Get всё равно не прочтёт.
func (t *Tracker) ListExpired(project string, now time.Time) ([]tracker.TaskRef, error) {
	if err := t.checkProject(project); err != nil {
		return nil, err
	}
	return t.collect(t.configuredColumns(), func(task tracker.Task) bool {
		return task.RunID != "" && !task.LeaseAlive(now)
	})
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): ListExpired across all configured columns"
```

---

### Task 8: Claim (tasks.md 3.2)

**Files:**
- Modify: `internal/tracker/yougile/lease.go` (append)
- Modify: `internal/tracker/yougile/task.go` (append `putTask`)
- Test: `internal/tracker/yougile/lease_test.go` (append)

**Interfaces:**
- Consumes: `getRaw`, `toTask` (Task 5), `apiData.encode` (Task 4), `columnFor` (Task 6).
- Produces: `func (t *Tracker) putTask(key string, body map[string]any) error`; `func (t *Tracker) Claim(req tracker.ClaimRequest) error`.

- [ ] **Step 1: Write the failing tests**

Append to `lease_test.go` (add `"errors"` and `"github.com/kao73/virtual-office/internal/tracker"` to its imports):

```go
func claimReq(runID string) tracker.ClaimRequest {
	return tracker.ClaimRequest{
		Key: testKey, RunID: runID, Owner: "implementer", LeaseUntil: now.Add(30 * time.Minute),
		ExpectStatus: "Ready", WorkingStatus: "InProgress",
	}
}

func putLease(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	data, _ := body["apiData"].(map[string]any)
	lease, _ := data["lease"].(map[string]any)
	return lease
}

// Аренда и перевод в рабочую колонку — одной записью: YouGile это умеет,
// и состояния «аренда есть, статус старый» между ними не бывает.
func TestClaimWritesLeaseAndColumnInOnePut(t *testing.T) {
	tr, fake := fixture(t)
	if err := tr.Claim(claimReq("run-1")); err != nil {
		t.Fatalf("захват не удался: %v", err)
	}
	if len(fake.puts) != 1 {
		t.Fatalf("PUT'ов %d, ожидался один", len(fake.puts))
	}
	lease := putLease(t, fake.puts[0])
	if lease["run_id"] != "run-1" || lease["owner"] != "implementer" {
		t.Errorf("аренда: %#v", lease)
	}
	if fake.puts[0]["columnId"] != colWork {
		t.Errorf("columnId = %#v, ожидалась рабочая колонка", fake.puts[0]["columnId"])
	}
	task, _ := tr.Get(testKey)
	if task.Status != "InProgress" || task.RunID != "run-1" || !task.LeaseUntil.Equal(now.Add(30*time.Minute)) {
		t.Errorf("после захвата: %+v", task)
	}
}

func TestClaimWithoutWorkingStatusKeepsColumn(t *testing.T) {
	for name, working := range map[string]string{"рабочего статуса нет": "", "рабочий = текущий": "Ready"} {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			req := claimReq("run-1")
			req.WorkingStatus = working
			if err := tr.Claim(req); err != nil {
				t.Fatal(err)
			}
			if _, moved := fake.puts[0]["columnId"]; moved {
				t.Errorf("колонка тронута: %#v", fake.puts[0])
			}
			if fake.task(testKey).ColumnID != colReady {
				t.Error("задача уехала из Ready")
			}
		})
	}
}

// Spec: «Claiming an already-owned live lease fails» — и ничего не пишет.
func TestClaimRefusesLiveLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-other", now.Add(time.Minute))
	err := tr.Claim(claimReq("run-1"))
	if !errors.Is(err, tracker.ErrClaimLost) {
		t.Errorf("захват живой аренды дал %v", err)
	}
	if len(fake.puts) != 0 {
		t.Errorf("живая аренда перезаписана: %#v", fake.puts)
	}
	if task, _ := tr.Get(testKey); task.RunID != "run-other" {
		t.Errorf("владелец сменился: %q", task.RunID)
	}
}

func TestClaimTakesExpiredLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-dead", now.Add(-time.Minute))
	if err := tr.Claim(claimReq("run-1")); err != nil {
		t.Errorf("истёкшая аренда не отдана: %v", err)
	}
}

func TestClaimChecksExpectedStatus(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].ColumnID = colReview
	if err := tr.Claim(claimReq("run-1")); !errors.Is(err, tracker.ErrClaimLost) {
		t.Errorf("захват из чужого статуса дал %v", err)
	}
	if len(fake.puts) != 0 {
		t.Error("записано, хотя статус не тот")
	}
}

// Spec: «A losing claimant is told it lost» — нашу запись перезаписали
// следом, перечитывание это видит.
func TestClaimLostWhenOverwrittenAfterWrite(t *testing.T) {
	tr, fake := fixture(t)
	fake.afterPut = func(id string) { fake.setLease(id, "run-winner", now.Add(time.Hour)) }
	if err := tr.Claim(claimReq("run-1")); !errors.Is(err, tracker.ErrClaimLost) {
		t.Errorf("проигранная гонка дала %v", err)
	}
}

func TestSecondClaimantLoses(t *testing.T) {
	tr, _ := fixture(t)
	if err := tr.Claim(claimReq("run-1")); err != nil {
		t.Fatal(err)
	}
	second := claimReq("run-2")
	second.ExpectStatus = "InProgress"
	if err := tr.Claim(second); !errors.Is(err, tracker.ErrClaimLost) {
		t.Errorf("второй захват дал %v", err)
	}
	if task, _ := tr.Get(testKey); task.RunID != "run-1" {
		t.Errorf("владелец: %q", task.RunID)
	}
}

// Review Focus #2.
func TestClaimKeepsForeignAPIDataAndCounters(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].APIData = map[string]any{"crm": map[string]any{"deal": 7}, "attempts": 2, "human_wait": true}
	if err := tr.Claim(claimReq("run-1")); err != nil {
		t.Fatal(err)
	}
	data := fake.task(testKey).APIData
	if data["crm"] == nil || data["attempts"] != float64(2) || data["human_wait"] != true {
		t.Errorf("apiData после захвата: %#v", data)
	}
}

func TestClaimUnknownWorkingStatusWritesNothing(t *testing.T) {
	tr, fake := fixture(t)
	req := claimReq("run-1")
	req.WorkingStatus = "Nowhere"
	if err := tr.Claim(req); err == nil {
		t.Error("незнакомый рабочий статус принят")
	}
	if len(fake.puts) != 0 {
		t.Error("записано при незнакомом рабочем статусе")
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run 'Claim'`
Expected: FAIL to build with `tr.Claim undefined`.

- [ ] **Step 3: Write the minimal implementation**

Append to `task.go`:

```go
// putTask — PUT /tasks/{key}: только переданные поля задачи.
func (t *Tracker) putTask(key string, body map[string]any) error {
	return t.call(http.MethodPut, "/tasks/"+url.PathEscape(key), nil, body, nil)
}
```

Append to `lease.go` (add `"errors"`, `"github.com/kao73/virtual-office/internal/tracker"` to its imports):

```go
// Claim — захват: записать аренду (и рабочую колонку) одним PUT, перечитать,
// сверить владельца.
//
// CAS в YouGile нет, как и в JIRA. Сверка закрывает половину гонки — ту, где
// нашу запись затёрли после нас: мы честно проигрываем. Зеркальную — оба
// прочли задачу свободной до чьей-либо записи — не закрывает ничто; см.
// доккомент jira.Claim. Живую чужую аренду захват не перезаписывает никогда.
func (t *Tracker) Claim(req tracker.ClaimRequest) error {
	if req.RunID == "" {
		return errors.New("yougile: захват без run_id — сверять владельца будет не с чем")
	}
	var working string
	if req.WorkingStatus != "" {
		id, err := t.columnFor(req.WorkingStatus)
		if err != nil {
			return err
		}
		working = id
	}

	raw, err := t.getRaw(req.Key)
	if err != nil {
		return err
	}
	task, data, err := t.toTask(raw)
	if err != nil {
		return err
	}
	switch {
	case task.Status != req.ExpectStatus:
		return fmt.Errorf("%w: %s в статусе %q, а захват шёл из %q",
			tracker.ErrClaimLost, req.Key, task.Status, req.ExpectStatus)
	case task.LeaseAlive(t.Now()):
		return fmt.Errorf("%w: %s арендована прогоном %s до %s",
			tracker.ErrClaimLost, req.Key, task.RunID, task.LeaseUntil.Format(time.RFC3339))
	}

	data.Lease = &leaseData{Owner: req.Owner, RunID: req.RunID, LeaseUntil: req.LeaseUntil}
	body := map[string]any{"apiData": data.encode()}
	if working != "" && req.WorkingStatus != task.Status {
		body["columnId"] = working
	}
	if err := t.putTask(req.Key, body); err != nil {
		return err
	}

	freshRaw, err := t.getRaw(req.Key)
	if err != nil {
		return err
	}
	fresh, _, err := t.toTask(freshRaw)
	if err != nil {
		return err
	}
	if fresh.RunID != req.RunID {
		return fmt.Errorf("%w: после захвата %s владеет %s", tracker.ErrClaimLost, req.Key, fresh.RunID)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): Claim writes lease+column in one PUT, rereads and verifies owner"
```

---

### Task 9: Renew, with the shared ownership helpers (tasks.md 3.3)

**Files:**
- Modify: `internal/tracker/yougile/task.go` (append `owned`, `mutateAPIData`)
- Modify: `internal/tracker/yougile/lease.go` (append `Renew`)
- Test: `internal/tracker/yougile/lease_test.go` (append)

**Interfaces:**
- Consumes: `getRaw`, `toTask`, `putTask`, `tracker.CheckOwner`, `tracker.ByRun`.
- Produces:
  - `func (t *Tracker) owned(key string, by tracker.Actor) (tracker.Task, apiData, error)`. One `GET /tasks/{key}`, no chat request.
  - `func (t *Tracker) mutateAPIData(key string, by tracker.Actor, change func(*apiData)) error`
  - `func (t *Tracker) Renew(key, runID string, leaseUntil time.Time) error`

- [ ] **Step 1: Write the failing tests**

Append to `lease_test.go`:

```go
func TestRenewExtendsOwnLiveLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	later := now.Add(time.Hour)
	if err := tr.Renew(testKey, "run-1", later); err != nil {
		t.Fatal(err)
	}
	task, _ := tr.Get(testKey)
	if !task.LeaseUntil.Equal(later) || task.RunID != "run-1" || task.Owner != "someone" {
		t.Errorf("после продления: %+v", task)
	}
	if fake.count("GET /api-v2/chats/") != 1 { // только Get из самого теста
		t.Error("проверка владения тянула переписку — лишний запрос под rate limit")
	}
}

// Spec: «Renewing an expired lease fails» — и аренда остаётся как была.
func TestRenewRefusesExpiredLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(-time.Minute))
	if err := tr.Renew(testKey, "run-1", now.Add(time.Hour)); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("продление истёкшей дало %v", err)
	}
	if len(fake.puts) != 0 {
		t.Error("истёкшая аренда переписана")
	}
}

func TestRenewRefusesForeignLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-other", now.Add(time.Minute))
	if err := tr.Renew(testKey, "run-1", now.Add(time.Hour)); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("продление чужой дало %v", err)
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run TestRenew`
Expected: FAIL to build with `tr.Renew undefined`.

- [ ] **Step 3: Write the minimal implementation**

Append to `task.go`:

```go
// owned читает задачу (без переписки) и проверяет право актора её менять
// общим tracker.CheckOwner: разъехавшись с jira и mock, правило дало бы гонку.
func (t *Tracker) owned(key string, by tracker.Actor) (tracker.Task, apiData, error) {
	raw, err := t.getRaw(key)
	if err != nil {
		return tracker.Task{}, apiData{}, err
	}
	task, data, err := t.toTask(raw)
	if err != nil {
		return tracker.Task{}, apiData{}, err
	}
	if err := tracker.CheckOwner(task, by, t.Now()); err != nil {
		return tracker.Task{}, apiData{}, err
	}
	return task, data, nil
}

// mutateAPIData — прочитать apiData целиком, поменять, записать целиком.
// Колонку не трогает.
func (t *Tracker) mutateAPIData(key string, by tracker.Actor, change func(*apiData)) error {
	_, data, err := t.owned(key, by)
	if err != nil {
		return err
	}
	change(&data)
	return t.putTask(key, map[string]any{"apiData": data.encode()})
}
```

Append to `lease.go`:

```go
// Renew продлевает свою живую аренду. Истёкшую продлевать поздно: её уже мог
// забрать другой — CheckOwner откажет ErrNotOwner.
func (t *Tracker) Renew(key, runID string, leaseUntil time.Time) error {
	return t.mutateAPIData(key, tracker.ByRun(runID), func(d *apiData) {
		if d.Lease != nil { // CheckOwner уже гарантировал живую аренду этого прогона
			d.Lease.LeaseUntil = leaseUntil
		}
	})
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): Renew own live lease through CheckOwner"
```

---

### Task 10: Release (tasks.md 3.4)

**Files:**
- Modify: `internal/tracker/yougile/lease.go` (append)
- Test: `internal/tracker/yougile/lease_test.go` (append)

**Interfaces:**
- Consumes: `mutateAPIData` (Task 9).
- Produces: `func (t *Tracker) Release(key string, by tracker.Actor) error`

- [ ] **Step 1: Write the failing tests**

Append to `lease_test.go`:

```go
// Spec: «A released task keeps its status»; attempts/human_wait/чужое — тоже на месте.
func TestReleaseClearsLeaseKeepsStatusAndCounters(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].ColumnID = colWork
	fake.tasks[testKey].APIData = map[string]any{"attempts": 2, "human_wait": true, "crm": "keep"}
	fake.setLease(testKey, "run-1", now.Add(time.Minute))

	if err := tr.Release(testKey, tracker.ByRun("run-1")); err != nil {
		t.Fatal(err)
	}
	task, _ := tr.Get(testKey)
	if task.RunID != "" || task.Owner != "" || !task.LeaseUntil.IsZero() {
		t.Errorf("аренда не снята: %+v", task)
	}
	if task.Status != "InProgress" || task.Attempts != 2 || !task.HumanFlag {
		t.Errorf("снятие аренды тронуло лишнее: %+v", task)
	}
	if fake.task(testKey).APIData["crm"] != "keep" {
		t.Error("чужой ключ apiData потерян")
	}
}

// Review Focus #3.
func TestReleaseSendsExplicitNullLease(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	if err := tr.Release(testKey, tracker.ByRun("run-1")); err != nil {
		t.Fatal(err)
	}
	body := fake.puts[len(fake.puts)-1]
	data := body["apiData"].(map[string]any)
	if lease, present := data["lease"]; !present || lease != nil {
		t.Errorf("lease в теле PUT: %#v (присутствует: %v), ожидался явный null", lease, present)
	}
	if _, moved := body["columnId"]; moved {
		t.Error("Release тронул колонку")
	}
}

func TestReleaseBySystemOnlyWhenExpired(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	if err := tr.Release(testKey, tracker.BySystem()); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("системное снятие живой аренды дало %v", err)
	}
	fake.setLease(testKey, "run-1", now.Add(-time.Minute))
	if err := tr.Release(testKey, tracker.BySystem()); err != nil {
		t.Errorf("reaper не снял истёкшую: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run TestRelease`
Expected: FAIL to build with `tr.Release undefined`.

- [ ] **Step 3: Write the minimal implementation**

Append to `lease.go`:

```go
// Release снимает аренду, не трогая колонку, attempts, human_wait и чужие
// ключи apiData. Снимает и сам прогон, и reaper — системной операцией
// над чужой истёкшей арендой.
func (t *Tracker) Release(key string, by tracker.Actor) error {
	return t.mutateAPIData(key, by, func(d *apiData) { d.Lease = nil })
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): Release clears lease with explicit null, keeps status and counters"
```

---

### Task 11: Transition (tasks.md 4.1)

**Files:**
- Modify: `internal/tracker/yougile/status.go` (append)
- Test: `internal/tracker/yougile/status_test.go` (append)

**Interfaces:**
- Consumes: `columnFor` (Task 6), `owned`, `putTask` (Tasks 8–9).
- Produces: `func (t *Tracker) Transition(key string, by tracker.Actor, toStatus string) error`

- [ ] **Step 1: Write the failing tests**

Append to `status_test.go`:

```go
// Spec: «Transitioning a task moves it to the corresponding column».
func TestTransitionMovesColumnOnly(t *testing.T) {
	tr, fake := fixture(t)
	if err := tr.Transition(testKey, tracker.BySystem(), "Review"); err != nil {
		t.Fatal(err)
	}
	if fake.task(testKey).ColumnID != colReview {
		t.Errorf("колонка = %q", fake.task(testKey).ColumnID)
	}
	body := fake.puts[len(fake.puts)-1]
	if len(body) != 1 || body["columnId"] != colReview {
		t.Errorf("тело PUT: %#v, ожидался только columnId", body)
	}
}

func TestTransitionFollowsOwnership(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	if err := tr.Transition(testKey, tracker.ByRun("run-2"), "Review"); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("чужой прогон дал %v", err)
	}
	if err := tr.Transition(testKey, tracker.ByRun("run-1"), "Review"); err != nil {
		t.Errorf("владелец не смог перевести: %v", err)
	}
}

func TestTransitionUnknownStatusAsksNothing(t *testing.T) {
	tr, fake := fixture(t)
	before := len(fake.requests)
	if err := tr.Transition(testKey, tracker.BySystem(), "Nowhere"); err == nil {
		t.Error("незнакомый статус принят")
	}
	if len(fake.requests) != before {
		t.Error("запрос ушёл до проверки статуса")
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run TestTransition`
Expected: FAIL to build with `tr.Transition undefined`.

- [ ] **Step 3: Write the minimal implementation**

Append to `status.go`:

```go
// Transition переводит задачу в колонку статуса. Одно поле, одна запись:
// второго состояния, которое надо держать в согласии, нет. apiData не трогает.
func (t *Tracker) Transition(key string, by tracker.Actor, toStatus string) error {
	column, err := t.columnFor(toStatus)
	if err != nil {
		return err
	}
	if _, _, err := t.owned(key, by); err != nil {
		return err
	}
	return t.putTask(key, map[string]any{"columnId": column})
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): Transition moves columnId under CheckOwner"
```

---

### Task 12: CreateTask with a content-derived idempotencyKey (tasks.md 4.2)

**Files:**
- Modify: `internal/tracker/yougile/task.go` (append)
- Test: `internal/tracker/yougile/task_test.go` (append)

**Interfaces:**
- Consumes: `checkProject`, `columnFor`, `apiData.encode`, `getRaw`, `toTask`.
- Produces:
  - `func (t *Tracker) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error)`
  - `func idempotencyKey(parts ...string) string`. Lowercase hex SHA-256 (64 chars), with a NUL byte after each part.
  - `func joinDescription(description, appendix string) string`. The same join rule as `jira`/`mock`.
  - Task 15 (`FindByMarker`) reads the labels from `apiData["labels"]`.

- [ ] **Step 1: Write the failing tests**

Append to `task_test.go` (add `"encoding/hex"` to its imports):

```go
func TestCreateTaskPostsIntoCreateColumn(t *testing.T) {
	tr, fake := fixture(t)
	ref, err := tr.CreateTask(testProject, tracker.TaskInput{
		Summary: "Child", Description: "prose", DescriptionAppend: "parent verbatim", Labels: []string{"split:VO-1:a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Key != "task-new-1" || ref.Status != "Ready" || ref.Summary != "Child" || ref.Project != testProject {
		t.Errorf("ref: %+v", ref)
	}
	body := fake.posts[0]
	if body["title"] != "Child" || body["description"] != "prose\n\nparent verbatim" || body["columnId"] != colReady {
		t.Errorf("тело POST: %#v", body)
	}
	labels := body["apiData"].(map[string]any)["labels"]
	if !reflect.DeepEqual(labels, []any{"split:VO-1:a"}) {
		t.Errorf("метки в apiData: %#v", labels)
	}
	key, _ := body["idempotencyKey"].(string)
	if _, err := hex.DecodeString(key); err != nil || len(key) != 64 {
		t.Errorf("idempotencyKey = %q, ожидался hex SHA-256", key)
	}
}

// Spec: «A repeated create returns the original task».
func TestCreateTaskRepeatedReturnsSameTask(t *testing.T) {
	tr, fake := fixture(t)
	input := tracker.TaskInput{Summary: "Child", Description: "prose", Labels: []string{"m-1"}}
	first, err := tr.CreateTask(testProject, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tr.CreateTask(testProject, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Key != second.Key {
		t.Errorf("повтор создал новую задачу: %s и %s", first.Key, second.Key)
	}
	if len(fake.tasks) != 2 { // testKey + одна созданная
		t.Errorf("задач в трекере %d, ожидалось 2", len(fake.tasks))
	}
}

// Два ребёнка split с одинаковым текстом, но разными метками — разные задачи.
func TestCreateTaskDifferentLabelsAreDifferentTasks(t *testing.T) {
	tr, _ := fixture(t)
	a, _ := tr.CreateTask(testProject, tracker.TaskInput{Summary: "Same", Description: "same", Labels: []string{"split:P:a"}})
	b, _ := tr.CreateTask(testProject, tracker.TaskInput{Summary: "Same", Description: "same", Labels: []string{"split:P:b"}})
	if a.Key == b.Key {
		t.Errorf("разные метки слились в одну задачу %s", a.Key)
	}
}

func TestIdempotencyKeySeparatesParts(t *testing.T) {
	if idempotencyKey("ab", "c") == idempotencyKey("a", "bc") {
		t.Error("части склеились без разделителя")
	}
	if idempotencyKey("a", "b") != idempotencyKey("a", "b") {
		t.Error("ключ не детерминирован")
	}
}

func TestJoinDescription(t *testing.T) {
	cases := map[[2]string]string{
		{"prose", ""}:       "prose",
		{"", "appendix"}:    "appendix",
		{"prose", "append"}: "prose\n\nappend",
	}
	for in, want := range cases {
		if got := joinDescription(in[0], in[1]); got != want {
			t.Errorf("joinDescription(%q, %q) = %q", in[0], in[1], got)
		}
	}
}

func TestCreateTaskWithoutCreateStatusPostsNothing(t *testing.T) {
	fake := newFake(t)
	tr, err := openWith(t, fake, func(c *Config) { c.CreateStatus = "" })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.CreateTask(testProject, tracker.TaskInput{Summary: "x"}); err == nil {
		t.Error("создание без create_status принято")
	}
	if len(fake.posts) != 0 {
		t.Error("POST ушёл без create_status")
	}
}

func TestCreateTaskUnknownProject(t *testing.T) {
	tr, _ := fixture(t)
	if _, err := tr.CreateTask("OTHER", tracker.TaskInput{Summary: "x"}); !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("чужой проект дал %v", err)
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run 'CreateTask|IdempotencyKey|JoinDescription'`
Expected: FAIL to build with `tr.CreateTask undefined`, `undefined: idempotencyKey`, `undefined: joinDescription`.

- [ ] **Step 3: Write the minimal implementation**

Append to `task.go` (add `"crypto/sha256"`, `"encoding/hex"` to its imports):

```go
// CreateTask заводит задачу в колонке CreateStatus. Метки (у YouGile своих нет)
// ложатся в apiData.labels — по ним ищет FindByMarker.
//
// idempotencyKey выводится из содержимого, а не случаен: повтор после
// неоднозначного отказа отдаёт тот же ключ, и YouGile возвращает уже
// созданную задачу. Это страховка поверх основного механизма — проверки
// FindByMarker перед созданием (pipeline.ensureChildren). Метки входят в хэш,
// чтобы два ребёнка split с одинаковым текстом не слились в одну задачу.
func (t *Tracker) CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error) {
	if err := t.checkProject(project); err != nil {
		return tracker.TaskRef{}, err
	}
	if t.cfg.CreateStatus == "" {
		return tracker.TaskRef{}, errors.New("yougile: create_status не задан — новой задаче некуда лечь")
	}
	column, err := t.columnFor(t.cfg.CreateStatus)
	if err != nil {
		return tracker.TaskRef{}, err
	}

	description := joinDescription(input.Description, input.DescriptionAppend)
	labels := slices.Sorted(slices.Values(input.Labels))
	body := map[string]any{
		"title":          input.Summary,
		"description":    description,
		"columnId":       column,
		"apiData":        apiData{Labels: slices.Clone(input.Labels)}.encode(),
		"idempotencyKey": idempotencyKey(append([]string{project, input.Summary, description}, labels...)...),
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := t.call(http.MethodPost, "/tasks", nil, body, &created); err != nil {
		return tracker.TaskRef{}, err
	}

	raw, err := t.getRaw(created.ID)
	if err != nil {
		return tracker.TaskRef{}, err
	}
	task, _, err := t.toTask(raw)
	if err != nil {
		return tracker.TaskRef{}, err
	}
	return task.Ref(), nil
}

// idempotencyKey — SHA-256 частей с NUL после каждой: ("ab","c") ≠ ("a","bc").
func idempotencyKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// joinDescription — то же правило, что у jira и mock: разделитель только
// когда есть что разделять.
func joinDescription(description, appendix string) string {
	switch {
	case appendix == "":
		return description
	case description == "":
		return appendix
	default:
		return description + "\n\n" + appendix
	}
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): CreateTask with content-derived idempotencyKey and labels in apiData"
```

---

### Task 13: SetHumanFlag and SetAttempts (tasks.md 4.3)

**Files:**
- Modify: `internal/tracker/yougile/lease.go` (append)
- Test: `internal/tracker/yougile/lease_test.go` (append)

**Interfaces:**
- Consumes: `mutateAPIData` (Task 9).
- Produces: `func (t *Tracker) SetHumanFlag(key string, by tracker.Actor, on bool) error`; `func (t *Tracker) SetAttempts(key string, by tracker.Actor, n int) error`

- [ ] **Step 1: Write the failing tests**

Append to `lease_test.go`:

```go
// Review Focus #2.
func TestSetAttemptsKeepsLeaseAndForeignKeys(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].APIData = map[string]any{"crm": "keep"}
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	if err := tr.SetAttempts(testKey, tracker.ByRun("run-1"), 3); err != nil {
		t.Fatal(err)
	}
	task, _ := tr.Get(testKey)
	if task.Attempts != 3 || task.RunID != "run-1" {
		t.Errorf("после SetAttempts: %+v", task)
	}
	if fake.task(testKey).APIData["crm"] != "keep" {
		t.Error("чужой ключ потерян")
	}
}

// Без аренды — законно для системной операции (между Release и Claim).
func TestSetHumanFlagBySystemOnFreeTask(t *testing.T) {
	tr, _ := fixture(t)
	for _, on := range []bool{true, false} {
		if err := tr.SetHumanFlag(testKey, tracker.BySystem(), on); err != nil {
			t.Fatal(err)
		}
		if task, _ := tr.Get(testKey); task.HumanFlag != on {
			t.Errorf("HumanFlag = %v, ожидалось %v", task.HumanFlag, on)
		}
	}
}

func TestCountersFollowOwnership(t *testing.T) {
	tr, fake := fixture(t)
	if err := tr.SetAttempts(testKey, tracker.ByRun("run-1"), 1); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("прогон без аренды дал %v", err)
	}
	if err := tr.SetHumanFlag(testKey, tracker.ByRun("run-1"), true); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("прогон без аренды дал %v", err)
	}
	if len(fake.puts) != 0 {
		t.Error("записано без права")
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run 'SetAttempts|SetHumanFlag|Counters'`
Expected: FAIL to build with `tr.SetAttempts undefined`, `tr.SetHumanFlag undefined`.

- [ ] **Step 3: Write the minimal implementation**

Append to `lease.go`:

```go
// SetHumanFlag — атрибут «ждёт человека» в apiData.human_wait. Аренда для
// него не нужна: вне lease он и лежит ради этого.
func (t *Tracker) SetHumanFlag(key string, by tracker.Actor, on bool) error {
	return t.mutateAPIData(key, by, func(d *apiData) { d.HumanWait = on })
}

// SetAttempts — счётчик попыток в apiData.attempts.
func (t *Tracker) SetAttempts(key string, by tracker.Actor, n int) error {
	return t.mutateAPIData(key, by, func(d *apiData) { d.Attempts = n })
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): SetHumanFlag/SetAttempts in apiData under CheckOwner"
```

---

### Task 14: Comment (tasks.md 5.1)

**Files:**
- Modify: `internal/tracker/yougile/comment.go` (append)
- Create: `internal/tracker/yougile/comment_test.go`

**Interfaces:**
- Consumes: `owned` (Task 9), `call`, `comments` (Task 5).
- Produces: `func (t *Tracker) Comment(key string, by tracker.Actor, body string) error`; `func messageHTML(body string) string`

- [ ] **Step 1: Write the failing tests**

Create `internal/tracker/yougile/comment_test.go`:

```go
package yougile

import (
	"errors"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

const markedBody = "[office run:r1 role:analyst outcome:question]\nЧто делать с <b>тегами</b> & амперсандом?\n\n## Вопросы\n1. да/нет"

func TestCommentPostsVerbatimTextAndEscapedHTML(t *testing.T) {
	tr, fake := fixture(t)
	if err := tr.Comment(testKey, tracker.BySystem(), markedBody); err != nil {
		t.Fatal(err)
	}
	post := fake.chatPosts[0]
	if post["text"] != markedBody {
		t.Errorf("text искажён: %q", post["text"])
	}
	wantHTML := "[office run:r1 role:analyst outcome:question]<br>Что делать с &lt;b&gt;тегами&lt;/b&gt; &amp; амперсандом?<br><br>## Вопросы<br>1. да/нет"
	if post["textHtml"] != wantHTML {
		t.Errorf("textHtml = %q", post["textHtml"])
	}
	if post["label"] != "" {
		t.Errorf("label = %#v, ожидалась пустая строка (поле обязательно)", post["label"])
	}
}

// Spec: комментарий читается обратно дословно; автор — та же учётка, что Whoami.
func TestCommentRoundTripsAndIsAttributedToOffice(t *testing.T) {
	tr, _ := fixture(t)
	if err := tr.Comment(testKey, tracker.BySystem(), markedBody); err != nil {
		t.Fatal(err)
	}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	who, _ := tr.Whoami()
	last := task.Comments[len(task.Comments)-1]
	if last.Body != markedBody || last.Author != who {
		t.Errorf("прочитано обратно: %+v (Whoami=%q)", last, who)
	}
}

func TestCommentFollowsOwnership(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	if err := tr.Comment(testKey, tracker.BySystem(), "x"); !errors.Is(err, tracker.ErrNotOwner) {
		t.Errorf("система поверх живой аренды дала %v", err)
	}
	if len(fake.chatPosts) != 0 {
		t.Error("комментарий ушёл без права")
	}
	if err := tr.Comment(testKey, tracker.ByRun("run-1"), "x"); err != nil {
		t.Errorf("владелец не смог написать: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run TestComment`
Expected: FAIL to build with `tr.Comment undefined`.

- [ ] **Step 3: Write the minimal implementation**

Append to `comment.go` (add `"html"`, `"strings"` to its imports):

```go
// Comment пишет в чат задачи. Раннер читает text, поэтому он уходит дословно
// (строка-маркер и раздел «Вопросы» обязаны доехать буква в букву). textHtml
// API требует обязательно — это тот же текст, экранированный, с <br> вместо
// переводов строк, чтобы человек в интерфейсе видел то же самое.
func (t *Tracker) Comment(key string, by tracker.Actor, body string) error {
	if _, _, err := t.owned(key, by); err != nil {
		return err
	}
	return t.call(http.MethodPost, "/chats/"+url.PathEscape(key)+"/messages", nil,
		map[string]any{"text": body, "textHtml": messageHTML(body), "label": ""}, nil)
}

// messageHTML — текст комментария как безопасный HTML.
func messageHTML(body string) string {
	return strings.Join(strings.Split(html.EscapeString(body), "\n"), "<br>")
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): Comment posts verbatim text to the task chat under CheckOwner"
```

---

### Task 15: FindByMarker (tasks.md 5.2)

**Files:**
- Modify: `internal/tracker/yougile/comment.go` (append)
- Test: `internal/tracker/yougile/comment_test.go` (append)

**Interfaces:**
- Consumes: `checkProject`, `t.columns` (Task 3), `tasksInColumn` (Task 6), `decodeAPIData` (Task 4), `toTask` (Task 5), labels written by `CreateTask` (Task 12).
- Produces: `func (t *Tracker) FindByMarker(project, marker string) ([]tracker.TaskRef, error)`

This task implements the label semantics from departure #3 at the top of the plan. The marker is compared against `apiData.labels`, never against comment text. The spec scenario's wording ("a marked comment") is flagged for amendment in Verify.

- [ ] **Step 1: Write the failing tests**

Append to `comment_test.go` (add `"slices"` and `"strings"` to its imports):

```go
func TestFindByMarkerFindsLabeledTask(t *testing.T) {
	tr, _ := fixture(t)
	want, err := tr.CreateTask(testProject, tracker.TaskInput{Summary: "Child", Labels: []string{"split:P:a"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.CreateTask(testProject, tracker.TaskInput{Summary: "Other", Labels: []string{"split:P:b"}}); err != nil {
		t.Fatal(err)
	}
	refs, err := tr.FindByMarker(testProject, "split:P:a")
	if err != nil || len(refs) != 1 || refs[0].Key != want.Key {
		t.Errorf("FindByMarker = %+v, %v", refs, err)
	}
}

// Ребёнка уже унесли дальше по графу — метка всё равно находит его.
func TestFindByMarkerSearchesEveryStatusColumn(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "moved", ColumnID: colReview, Timestamp: now.UnixMilli(),
		APIData: map[string]any{"labels": []any{"split:P:a"}}})
	refs, err := tr.FindByMarker(testProject, "split:P:a")
	if err != nil || !slices.Equal(keys(refs), []string{"moved"}) {
		t.Errorf("FindByMarker = %v, %v", keys(refs), err)
	}
}

// Review Focus #1: найденная метка в колонке вне графа — громкая ошибка,
// а не «не найдено», которое кончилось бы дублем ребёнка.
func TestFindByMarkerFailsLoudOnUnmappedColumn(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "parked", ColumnID: colOutside, Timestamp: now.UnixMilli(),
		APIData: map[string]any{"labels": []any{"split:P:a"}}})
	_, err := tr.FindByMarker(testProject, "split:P:a")
	if !errors.Is(err, ErrUnmappedColumn) || !strings.Contains(err.Error(), "parked") {
		t.Errorf("метка вне графа дала %v", err)
	}
}

func TestFindByMarkerEmptyWhenNothingMatches(t *testing.T) {
	tr, _ := fixture(t)
	refs, err := tr.FindByMarker(testProject, "split:none")
	if err != nil || len(refs) != 0 {
		t.Errorf("FindByMarker = %+v, %v", refs, err)
	}
}

// Метку в тексте комментария FindByMarker не ищет: контракт — метки задачи
// (pipeline.ensureChildren кладёт её в TaskInput.Labels).
func TestFindByMarkerIgnoresCommentText(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{{ID: 1, From: officeUserID, Text: "split:P:a"}}
	refs, err := tr.FindByMarker(testProject, "split:P:a")
	if err != nil || len(refs) != 0 {
		t.Errorf("FindByMarker по тексту комментария: %+v, %v", refs, err)
	}
}

func TestFindByMarkerUnknownProject(t *testing.T) {
	tr, _ := fixture(t)
	if _, err := tr.FindByMarker("OTHER", "m"); !errors.Is(err, tracker.ErrNoProject) {
		t.Errorf("чужой проект дал %v", err)
	}
}
```

- [ ] **Step 2: Run the tests and check they fail**

Run: `go test ./internal/tracker/yougile/ -run TestFindByMarker`
Expected: FAIL to build with `tr.FindByMarker undefined`.

- [ ] **Step 3: Write the minimal implementation**

Append to `comment.go`:

```go
// FindByMarker — задачи проекта с меткой marker в apiData.labels. Источник
// идемпотентности пакетного создания (pipeline.ensureChildren).
//
// Полнотекстового поиска у API нет, так что обходим все колонки проекта —
// не только графа: ребёнок, которого человек утащил за пределы графа, иначе
// выглядел бы «не найденным» и был бы создан заново. Такая находка — громкая
// ошибка ErrUnmappedColumn: статус для неё не выдумываем.
func (t *Tracker) FindByMarker(project, marker string) ([]tracker.TaskRef, error) {
	if err := t.checkProject(project); err != nil {
		return nil, err
	}
	var found []taskDTO
	for _, column := range t.columns {
		tasks, err := t.tasksInColumn(column.ID)
		if err != nil {
			return nil, err
		}
		for _, raw := range tasks {
			data, err := decodeAPIData(raw.APIData)
			if err != nil {
				return nil, fmt.Errorf("задача YouGile %s: %w", raw.ID, err)
			}
			if slices.Contains(data.Labels, marker) {
				found = append(found, raw)
			}
		}
	}
	slices.SortStableFunc(found, func(a, b taskDTO) int {
		return cmp.Or(cmp.Compare(a.Timestamp, b.Timestamp), cmp.Compare(a.ID, b.ID))
	})

	refs := make([]tracker.TaskRef, 0, len(found))
	for _, raw := range found {
		task, _, err := t.toTask(raw)
		if err != nil {
			return nil, err
		}
		refs = append(refs, task.Ref())
	}
	return refs, nil
}
```

- [ ] **Step 4: Run the tests and check they pass**

Run: `go test ./internal/tracker/yougile/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "feat(yougile): FindByMarker over apiData labels across all project columns"
```

---

### Task 16: Contract subset check, ownership sweep, full verification (tasks.md 6.1)

Every method got its unit tests inside its own task. This task adds the two cross-cutting checks that no single method owns and runs the full verification.

**Files:**
- Create: `internal/tracker/yougile/contract_test.go`

**Interfaces:**
- Consumes: every exported method from Tasks 2–15.
- Produces: `type coreTracker interface` (test-only). It lists exactly the 14 methods this change implements, with signatures copied verbatim from `internal/tracker/tracker.go`.

- [ ] **Step 1: Write the tests**

Create `internal/tracker/yougile/contract_test.go`:

```go
package yougile

import (
	"errors"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

// coreTracker — подмножество tracker.Tracker, которое реализует этот change:
// сигнатуры скопированы из internal/tracker/tracker.go дословно. Полная
// проверка `var _ tracker.Tracker = (*Tracker)(nil)` не скомпилируется, пока
// yougile-dependencies-attachments не добавит LinkDependsOn/AddAttachment/
// GetAttachment, — а расхождение сигнатур ловить надо уже сейчас.
type coreTracker interface {
	Whoami() (string, error)
	ListReady(project, status string) ([]tracker.TaskRef, error)
	ListExpired(project string, now time.Time) ([]tracker.TaskRef, error)
	List(project string, statuses []string) ([]tracker.TaskRef, error)
	Get(key string) (tracker.Task, error)
	Claim(req tracker.ClaimRequest) error
	Renew(key, runID string, leaseUntil time.Time) error
	Release(key string, by tracker.Actor) error
	Transition(key string, by tracker.Actor, toStatus string) error
	Comment(key string, by tracker.Actor, body string) error
	SetHumanFlag(key string, by tracker.Actor, on bool) error
	SetAttempts(key string, by tracker.Actor, n int) error
	CreateTask(project string, input tracker.TaskInput) (tracker.TaskRef, error)
	FindByMarker(project, marker string) ([]tracker.TaskRef, error)
}

var _ coreTracker = (*Tracker)(nil)

// Правило владения общее для всех трекеров: ни одна мутация не проходит
// ни у системной операции поверх живой аренды, ни у прогона без аренды,
// и отказ случается до записи.
func TestEveryMutationFollowsOwnership(t *testing.T) {
	mutations := map[string]func(*Tracker, tracker.Actor) error{
		"Release":      func(tr *Tracker, a tracker.Actor) error { return tr.Release(testKey, a) },
		"Transition":   func(tr *Tracker, a tracker.Actor) error { return tr.Transition(testKey, a, "Review") },
		"Comment":      func(tr *Tracker, a tracker.Actor) error { return tr.Comment(testKey, a, "x") },
		"SetHumanFlag": func(tr *Tracker, a tracker.Actor) error { return tr.SetHumanFlag(testKey, a, true) },
		"SetAttempts":  func(tr *Tracker, a tracker.Actor) error { return tr.SetAttempts(testKey, a, 1) },
	}
	for name, mutate := range mutations {
		t.Run(name+"/система поверх живой аренды", func(t *testing.T) {
			tr, fake := fixture(t)
			fake.setLease(testKey, "run-1", now.Add(time.Minute))
			if err := mutate(tr, tracker.BySystem()); !errors.Is(err, tracker.ErrNotOwner) {
				t.Errorf("дало %v", err)
			}
			if len(fake.puts)+len(fake.chatPosts) != 0 {
				t.Error("записано без права")
			}
		})
		t.Run(name+"/прогон без аренды", func(t *testing.T) {
			tr, fake := fixture(t)
			if err := mutate(tr, tracker.ByRun("run-1")); !errors.Is(err, tracker.ErrNotOwner) {
				t.Errorf("дало %v", err)
			}
			if len(fake.puts)+len(fake.chatPosts) != 0 {
				t.Error("записано без права")
			}
		})
		t.Run(name+"/актор не задан", func(t *testing.T) {
			tr, _ := fixture(t)
			if err := mutate(tr, tracker.Actor{}); !errors.Is(err, tracker.ErrNotOwner) {
				t.Errorf("нулевой актор дал %v", err)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `go test ./internal/tracker/yougile/ -run 'TestEveryMutationFollowsOwnership' -v`
Expected: PASS right away, because every mutator already goes through `owned`. If a subtest fails, that mutator skips `owned`. Fix the mutator, not the test.

- [ ] **Step 3: Run the full verification**

```bash
gofmt -l internal/tracker/yougile/                   # expect: no output
go vet ./...                                         # expect: no output, exit 0
go test -race -count=1 ./internal/tracker/...        # expect: ok for tracker, jira, mock, yougile
go test ./...                                        # expect: all ok (what CI runs)
grep -rn "tracker.Tracker = " internal/tracker/yougile/ || echo "no full assertion — correct"
git diff --stat a73fc3304d71ade69e4f6e497275663850ff2839 -- internal/tracker/tracker.go internal/tracker/config.go cmd/ office/ go.mod go.sum
```
Expected: the `grep` prints `no full assertion — correct`, and the final `git diff --stat` prints nothing (no out-of-scope files touched).

- [ ] **Step 4: Commit**

```bash
git add internal/tracker/yougile/contract_test.go docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "test(yougile): contract subset assertion and ownership sweep across mutators"
```

---

### Task 17: Gated live smoke test against office-polygon (tasks.md 6.2)

**Files:**
- Create: `internal/tracker/yougile/live_test.go`

**Interfaces:**
- Consumes: the whole public surface plus `putTask`, `getRaw`, `decodeAPIData` (same package).
- Produces: `TestLiveLifecycle`, built only with `-tags yougile_live`. There is no earlier precedent for live-gated tests in this repo (the only `//go:build` tags are `release`/`unix`), so this sets the convention: a build tag, not `testing.Short`, which means a plain `go test ./...` never compiles the file.

- [ ] **Step 1: Write the live test**

Create `internal/tracker/yougile/live_test.go`:

```go
//go:build yougile_live

// Живая проверка против песочницы office-polygon. Никогда — против Clens:
// это живая клиентская доска. Запуск:
//
//	YOUGILE_API_KEY=… YOUGILE_PROJECT_ID=<office-polygon> \
//	YOUGILE_COLUMNS='Ready=<id>,InProgress=<id>' \
//	go test -tags yougile_live -run TestLive -count=1 -v ./internal/tracker/yougile/
//
// Запросов ~25 — под rate limit 50/мин на компанию; два запуска подряд
// могут в него упереться.
package yougile

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

func liveTracker(t *testing.T) *Tracker {
	t.Helper()
	key, project, columns := os.Getenv("YOUGILE_API_KEY"), os.Getenv("YOUGILE_PROJECT_ID"), os.Getenv("YOUGILE_COLUMNS")
	if key == "" || project == "" || columns == "" {
		t.Fatal("нужны YOUGILE_API_KEY, YOUGILE_PROJECT_ID, YOUGILE_COLUMNS (Status=columnId,…) — только песочница office-polygon")
	}
	base := os.Getenv("YOUGILE_BASE_URL")
	if base == "" {
		base = "https://yougile.com"
	}
	ids := map[string]string{}
	for _, pair := range strings.Split(columns, ",") {
		status, id, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			t.Fatalf("YOUGILE_COLUMNS: %q не вида Status=columnId", pair)
		}
		ids[status] = id
	}
	for _, need := range []string{"Ready", "InProgress"} {
		if ids[need] == "" {
			t.Fatalf("YOUGILE_COLUMNS: нет колонки для %s", need)
		}
	}
	tr, err := Open(Config{BaseURL: base, APIKey: key, ProjectID: project, ColumnIDs: ids, CreateStatus: "Ready"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return tr
}

func TestLiveLifecycle(t *testing.T) {
	tr := liveTracker(t)
	project := tr.cfg.ProjectID

	who, err := tr.Whoami()
	if err != nil || who == "" {
		t.Fatalf("Whoami = %q, %v", who, err)
	}

	stamp := time.Now().UTC().Format("20060102T150405.000")
	marker := "office-live:" + stamp
	input := tracker.TaskInput{Summary: "office live smoke " + stamp, Description: "created by live_test.go", Labels: []string{marker}}

	// Spec: повторное создание возвращает ту же задачу.
	first, err := tr.CreateTask(project, input)
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	t.Cleanup(func() { _ = tr.putTask(first.Key, map[string]any{"deleted": true}) })
	second, err := tr.CreateTask(project, input)
	if err != nil || second.Key != first.Key {
		t.Fatalf("повторное создание: %s vs %s, %v", first.Key, second.Key, err)
	}
	if first.Status != "Ready" {
		t.Errorf("новая задача в статусе %q", first.Status)
	}

	// Чужой ключ apiData — должен пережить все наши записи.
	raw, err := tr.getRaw(first.Key)
	if err != nil {
		t.Fatal(err)
	}
	data, err := decodeAPIData(raw.APIData)
	if err != nil {
		t.Fatal(err)
	}
	data.extra = map[string]json.RawMessage{"live_probe": json.RawMessage(`"keep-me"`)}
	if err := tr.putTask(first.Key, map[string]any{"apiData": data.encode()}); err != nil {
		t.Fatal(err)
	}

	// Claim: аренда + рабочая колонка одним PUT, перечитывание.
	runID := "live-run-" + stamp
	if err := tr.Claim(tracker.ClaimRequest{
		Key: first.Key, RunID: runID, Owner: "live", LeaseUntil: time.Now().Add(10 * time.Minute),
		ExpectStatus: "Ready", WorkingStatus: "InProgress",
	}); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	later := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Millisecond)
	if err := tr.Renew(first.Key, runID, later); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if err := tr.SetAttempts(first.Key, tracker.ByRun(runID), 2); err != nil {
		t.Fatalf("SetAttempts: %v", err)
	}
	if err := tr.SetHumanFlag(first.Key, tracker.ByRun(runID), true); err != nil {
		t.Fatalf("SetHumanFlag: %v", err)
	}

	body := "[office run:" + runID + " role:live]\nпроза с <угловыми> & амперсандом\n\n## Вопросы\n1. да?"
	if err := tr.Comment(first.Key, tracker.ByRun(runID), body); err != nil {
		t.Fatalf("Comment: %v", err)
	}

	task, err := tr.Get(first.Key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if task.Status != "InProgress" || task.RunID != runID || !task.LeaseUntil.Equal(later) ||
		task.Attempts != 2 || !task.HumanFlag {
		t.Errorf("после захвата/продления: %+v", task)
	}
	if len(task.Comments) == 0 {
		t.Fatal("комментарий не прочитан обратно")
	}
	last := task.Comments[len(task.Comments)-1]
	if last.Body != body {
		t.Errorf("text исказился на сервере:\n%q\nожидалось\n%q", last.Body, body)
	}
	if last.Author != who {
		t.Errorf("автор %q, а Whoami %q", last.Author, who)
	}

	// Release: аренда снята явным null, статус и счётчики на месте, чужой ключ цел.
	if err := tr.Release(first.Key, tracker.ByRun(runID)); err != nil {
		t.Fatalf("Release: %v", err)
	}
	raw, err = tr.getRaw(first.Key)
	if err != nil {
		t.Fatal(err)
	}
	released, data, err := tr.toTask(raw)
	if err != nil {
		t.Fatal(err)
	}
	if released.RunID != "" || released.Status != "InProgress" || released.Attempts != 2 || !released.HumanFlag {
		t.Errorf("после Release: %+v", released)
	}
	if string(data.extra["live_probe"]) != `"keep-me"` {
		t.Errorf("чужой ключ apiData потерян: %s", raw.APIData)
	}

	found, err := tr.FindByMarker(project, marker)
	if err != nil || len(found) != 1 || found[0].Key != first.Key {
		t.Errorf("FindByMarker = %+v, %v", found, err)
	}
	ready, err := tr.ListReady(project, "InProgress")
	if err != nil || !slices.ContainsFunc(ready, func(r tracker.TaskRef) bool { return r.Key == first.Key }) {
		t.Errorf("ListReady(InProgress) не видит освобождённую задачу: %v", err)
	}
}
```

- [ ] **Step 2: Check the default build excludes it, and vet it under its tag**

```bash
go test ./internal/tracker/yougile/ -run TestLive -v    # expect: "testing: warning: no tests to run", ok
go vet -tags yougile_live ./internal/tracker/yougile/  # expect: no output
```

- [ ] **Step 3: Run it live (manual gate, office-polygon only)**

1. Export the key without printing it. Per the owner's machine notes it is in the `yougile-mcp` server's `env` block in `~/.claude.json`, not in the shell profile. The actual key name is `YOUGILE_API_KEY`:
   `export YOUGILE_API_KEY="$(jq -r '.. | objects | select(has("YOUGILE_API_KEY")) | .YOUGILE_API_KEY' ~/.claude.json | head -1)"`
2. Find the office-polygon project id and two column ids (Ready and InProgress). If they don't exist, create the columns by hand in the UI. The adapter never creates columns.
   `curl -s -H "Authorization: Bearer $YOUGILE_API_KEY" 'https://yougile.com/api-v2/projects?limit=100' | jq '.content[] | {id,title}'`
   then `…/api-v2/boards?projectId=<id>` and `…/api-v2/columns?boardId=<id>`.
3. Run: `YOUGILE_PROJECT_ID=<id> YOUGILE_COLUMNS='Ready=<id>,InProgress=<id>' go test -tags yougile_live -run TestLive -count=1 -v ./internal/tracker/yougile/`
   Expected: PASS. If it fails on `text исказился на сервере`, YouGile derives `text` from `textHtml`. Record the exact server output and stop: this changes the Comment design and needs a decision from the owner. If it fails on `после Release` with a non-empty `RunID`, the `null` lease did not clear. Record the raw `apiData` and stop. If it fails on network timeouts, see the owner's note on AmneziaVPN split tunnelling before suspecting the adapter.
4. Record the run (date, project id, pass/fail, any server-side surprises) in the change's verification evidence, not in code.

- [ ] **Step 4: Commit**

```bash
git add internal/tracker/yougile/live_test.go docs/openspec/changes/yougile-adapter-core/tasks.md
git commit -m "test(yougile): gated live lifecycle smoke test against office-polygon"
```

---

## Self-review notes (kept for the reviewer)

- **Spec coverage:** column-as-status (Tasks 6, 11), claim with reread (Task 8), renew live only (Task 9), release keeps status (Task 10), idempotent create (Task 12), comment verbatim plus find by marker (Tasks 14 and 15, with label semantics per departure #3), human vs office by account (Tasks 2, 5, 14). Every `tasks.md` item 1.1–6.2 maps to exactly one task.
- **Known gaps left to later changes, not this plan:** `TaskRef.Updated` is always zero (the API has no modified timestamp). `Task.Attachments` and `Task.DependsOn` are always empty (`yougile-dependencies-attachments`). Description is sent as plain text into a field YouGile renders as HTML, and markdown→HTML conversion is not attempted (`yougile-wiring-and-docs`/`yougile-live-validation` should decide). There is no `LoadConfig` (`yougile-wiring-and-docs`).
