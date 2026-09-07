---
change: split-dependency-gate
design-doc: docs/superpowers/specs/2026-09-07-split-dependency-gate-design.md
base-ref: 7ef17c6268e3091ff22f7a2d503966d8576f34c0
---

# split-dependency-gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `claim()` refuses a candidate whose `depends_on` names a task that
is not yet in a terminal status — the same check for every role of the
graph, on both trackers (`mock`, `jira`) — and `runner ls` names what a
blocked candidate is waiting on. The fix that makes this possible on JIRA:
`jira.toTask` currently never parses `issuelinks` back into `Task.DependsOn`,
and `search()` (`ListReady`/`List`) doesn't even request that field, so the
gate would see every JIRA candidate's `DependsOn` as empty even after the
parsing is fixed.

**Architecture:** Read path first (`TaskRef.DependsOn` field +
`Task.Ref()`, `jira.toTask` parsing `issuelinks`, `jira.searchFields()`
requesting the field) — verified live on the JIRA polygon against the
already-known `Blocks`-type direction before anything depends on it. Then a
single pure helper, `pipeline.UnmetDependencies(ref, byKey, terminal)`,
shared by two callers: `Office.claim()` (new `Office.projectByKey` builds
`byKey` from one `List(project, statuses)` call per project per tick, lazily
— only when `ListReady` actually returned candidates) and
`cmd/runner/board.go`'s `printBoard` (same `List` call it already makes,
reused). No new label, no second source of truth — `depends_on` is read
back from the same `issuelinks` that `LinkDependsOn` (Change 1) already
writes.

**Tech Stack:** Go 1.26 (`go.mod`), standard library
(`net/http`, `encoding/json`), `gopkg.in/yaml.v3`. Tests — `go test`;
`internal/tracker/jira` uses the existing `httptest.Server`-backed `fakeJira`
harness; `internal/pipeline` and `cmd/runner` use the existing
`mock.Tracker`-backed fixtures (`newOffice(t)` / `mock.New`).

**Spec:**
- Design Doc (authoritative for concrete signatures, decisions, and the
  live-verification methodology):
  `docs/superpowers/specs/2026-09-07-split-dependency-gate-design.md`
- Comet Design (same decisions, OpenSpec format):
  `docs/openspec/changes/split-dependency-gate/design.md`
- Proposal: `docs/openspec/changes/split-dependency-gate/proposal.md`
- Task boundaries (8 groups, 17 checkboxes):
  `docs/openspec/changes/split-dependency-gate/tasks.md`
- Delta specs (behavioral contract this plan's tasks satisfy):
  `docs/openspec/changes/split-dependency-gate/specs/pipeline-dependency-gate/spec.md`,
  `docs/openspec/changes/split-dependency-gate/specs/role-native-workflow/spec.md`

## Global Constraints

- Language convention (`CLAUDE.md`): doc comments, error messages, log
  lines, and ticket-facing text — Russian; identifiers, YAML/JSON keys,
  function and type names — English.
- No LLM anywhere in `internal/pipeline`/`internal/tracker`: every routing
  decision comes from `workflow.yaml` and `result.json`, never the prompt
  (`docs/DESIGN.md` §2.1).
- `Tracker` methods that mutate a task take an `Actor` and must check it via
  `tracker.CheckOwner`; methods that only read take none (existing contract
  rule, `internal/tracker/tracker.go`).
- `mock` is a debugging tool, not a concurrent-tracker simulation
  (`internal/tracker/mock` package doc) — it needs **no changes** for this
  change beyond what the shared `Task.Ref()` fix in Task 1 already gives it
  (design doc: "mock: без изменений").
- JIRA REST is v2 only; target server is JIRA Server 8.13
  (`internal/tracker/jira` package doc — v3/ADF is Cloud-only).
- Visible, not silent, failure is the house style: a dependency the tracker
  can't find is treated as unresolved, never as satisfied
  (`docs/DESIGN.md`; design.md decision #6).
- `terminal` status is answered by the existing `Workflow.IsTerminal`
  abstraction (`internal/tracker/config.go`), never a hardcoded `"Done"`
  string (design.md decision #5).
- The gate is one code path for every role — no `if roleName == "reviewer"`
  special case (design.md decision #4).
- Each `internal/pipeline` file keeps its tests in the adjacent
  `X_test.go` (existing convention: `prpass.go`/`prpass_test.go`,
  `archive.go`/`archive_test.go`) — `deps.go`/`deps_test.go` follows suit.
- `go build ./...`, `go vet ./...`, `gofmt -l .` (empty), `go test ./...`
  must be green after each task's commit, not just at the end.

---

## File Structure

- `internal/tracker/tracker.go` — **modify**: `TaskRef.DependsOn []string`
  field, `Task.Ref()` copies it, `Task.DependsOn` doc comment updated to
  drop the now-stale "jira never reads this back" claim.
- `internal/tracker/tracker_test.go` — **modify**: tests for `Task.Ref()`
  copying `DependsOn`.
- `internal/tracker/jira/jira.go` — **modify**: `toTask` parses
  `issuelinks`; `searchFields()` requests `"issuelinks"`.
- `internal/tracker/jira/jira_test.go` — **modify**: `fakeJira` gains
  `remoteIssuelinks`/`lastSearchFields`; new tests for parsing and for the
  search request/response round trip.
- `internal/pipeline/deps.go` — **create**: `UnmetDependencies`,
  `describeUnmet` — the one shared implementation the gate and `ls` both
  call, per design.md decision #3.
- `internal/pipeline/deps_test.go` — **create**: unit tests for both.
- `internal/pipeline/pipeline.go` — **modify**: `claim()` wired to the
  gate; new `Office.projectByKey`.
- `internal/pipeline/pipeline_test.go` — **modify**: `claim()` gate tests
  (implementer- and analyst-flow candidates, terminal unblock, missing
  dependency).
- `internal/pipeline/splits.go` — **modify** (optional, Task 10):
  `linkChildren` becomes lazy, checking `Get(key).DependsOn` before
  `LinkDependsOn` — no longer relying solely on server-side idempotency.
- `internal/pipeline/splits_test.go` — **modify** (optional, Task 10): test
  proving a retried batch doesn't re-POST an already-recorded link.
- `cmd/runner/board.go` — **modify**: `printBoard` gains a `terminal
  func(string) bool` parameter and a `dependsColumn`; `boardCommand` passes
  `o.Workflow.IsTerminal`.
- `cmd/runner/board_test.go` — **modify**: existing `printBoard` call sites
  updated for the new parameter; new test for the dependency column.
- `roles/analyst/role.md` — **modify**: `depends_on` criterion next to the
  `split` outcome description.
- `docs/notes/analyst-task-splitting.md` — **modify** (Tasks 4/5/12, live
  verification write-ups): not part of the Comet artifacts this task is
  read-only on — the project's existing convention for recording live
  findings (same file Change 1's live verification used).

---

## Task 1: `TaskRef.DependsOn` — the shared read path for both trackers

Closes **tasks.md 1.1**. `Task.Ref()` is the single place both `mock`'s
`list()` and `jira`'s `search()` go through to build the `TaskRef` a
candidate is filtered on — today it silently drops `DependsOn`, so even
`mock`'s `ListReady`/`List` results carry no dependency information despite
`mock.Get()` reading it correctly. Fixing `Ref()` fixes both
implementations' `List`/`ListReady` paths in one place.

**Files:**
- Modify: `internal/tracker/tracker.go`
- Modify: `internal/tracker/tracker_test.go`

**Interfaces:**
- Produces: `TaskRef.DependsOn []string`; `Task.Ref()` now copies it.

- [x] **Step 1: Write the failing test**

  In `internal/tracker/tracker_test.go`, add `"slices"` to the imports,
  then:

  ```go
  // TestTaskRefCopiesDependsOn доказывает, что Ref() отдаёт зависимости
  // так же, как Task: гейт очерёдности (internal/pipeline.claim()) и
  // видимость в runner ls читают DependsOn из TaskRef, полученного через
  // ListReady/List, а не через Get() — без этого поля в Ref() оба пути
  // видели бы кандидата без единой зависимости, даже когда Task.DependsOn
  // на нём заполнен.
  func TestTaskRefCopiesDependsOn(t *testing.T) {
      task := Task{Key: "OFF-2", DependsOn: []string{"OFF-1", "OFF-0"}}
      ref := task.Ref()
      if !slices.Equal(ref.DependsOn, []string{"OFF-1", "OFF-0"}) {
          t.Errorf("TaskRef.DependsOn = %v, ожидалось [OFF-1 OFF-0]", ref.DependsOn)
      }
  }

  func TestTaskRefDependsOnEmptyWhenTaskHasNone(t *testing.T) {
      ref := Task{Key: "OFF-1"}.Ref()
      if len(ref.DependsOn) != 0 {
          t.Errorf("TaskRef.DependsOn = %v, ожидался пустой список", ref.DependsOn)
      }
  }
  ```

- [x] **Step 2: Run test to verify it fails**

  Run: `go test ./internal/tracker/... -run TestTaskRefCopiesDependsOn -v`
  Expected: FAIL to compile — `ref.DependsOn undefined (type TaskRef has no
  field or method DependsOn)`.

- [x] **Step 3: Add the field and copy it**

  In `internal/tracker/tracker.go`, in `TaskRef` (after `Status string`):

  ```go
  type TaskRef struct {
      Key     string
      Project string
      Summary string
      Status  string

      // DependsOn — ключи задач, от которых зависит эта. То же поле, что
      // Task.DependsOn (см. его доккомент) — Ref() копирует его наравне
      // с остальными: гейт очерёдности (internal/pipeline) и runner ls
      // читают именно TaskRef, полученный через ListReady/List, не Task
      // через Get().
      DependsOn []string
  ```

  In `Task.Ref()`:

  ```go
  func (t Task) Ref() TaskRef {
      return TaskRef{
          Key: t.Key, Project: t.Project, Summary: t.Summary, Status: t.Status,
          Owner: t.Owner, RunID: t.RunID, LeaseUntil: t.LeaseUntil,
          Attempts: t.Attempts, HumanFlag: t.HumanFlag, DependsOn: t.DependsOn,
      }
  }
  ```

- [x] **Step 4: Run test to verify it passes**

  Run: `go test ./internal/tracker/... -run TestTaskRefCopiesDependsOn -v`
  Expected: PASS.

- [x] **Step 5: Full package run — nothing else broke**

  Run: `go build ./... && go vet ./... && go test ./internal/tracker/...`

- [x] **Step 6: Commit**

  ```bash
  git add internal/tracker/tracker.go internal/tracker/tracker_test.go
  git commit -m "feat(tracker): copy DependsOn in Task.Ref()"
  ```

---

## Task 2: `jira.toTask` parses `issuelinks` into `Task.DependsOn`

Closes **tasks.md 1.2** and the `toTask`-facing half of **1.4**.

**Files:**
- Modify: `internal/tracker/jira/jira.go`
- Modify: `internal/tracker/jira/jira_test.go`

**Interfaces:**
- Consumes: `t.cfg.DependsOnLink` (existing `Config` field), `text(v any)
  string` (existing helper).
- Produces: `toTask` populates `tracker.Task.DependsOn`.

- [ ] **Step 1: Extend the fake server with an `issuelinks` fixture field**

  In `internal/tracker/jira/jira_test.go`, add a field to `fakeJira` (near
  `issueAttachments`):

  ```go
      issueAttachments []map[string]any
      // remoteIssuelinks — то, что "issuelinks" отдаёт GET/поиск задачи в
      // этом тесте: подделывает то, что реально хранит сервер, в отличие
      // от issueLinks (без круглой буквы l после "issue") выше, которое
      // ловит исходящие POST /issueLink этого же трекера.
      remoteIssuelinks []any
  ```

  In `(f *fakeJira) issue()`, alongside the existing `attachment` handling:

  ```go
      if f.remoteIssuelinks != nil {
          fields["issuelinks"] = f.remoteIssuelinks
      }
  ```

- [ ] **Step 2: Write the failing test**

  In `internal/tracker/jira/jira_test.go`:

  ```go
  // TestGetParsesDependsOnFromIssuelinks покрывает четыре формы записи
  // issuelinks разом (tasks.md 1.4): связь с совпадающим типом, связь с
  // чужим type.name (игнорируется), несколько связей сразу, запись только
  // с inwardIssue (обратная сторона — не читается: DependsOn — это "от
  // кого зависит эта задача", не "кто зависит от неё").
  func TestGetParsesDependsOnFromIssuelinks(t *testing.T) {
      tr, fake := fixture(t) // fixture() уже задаёт DependsOnLink: "Depends"
      fake.remoteIssuelinks = []any{
          map[string]any{
              "type":         map[string]any{"name": "Depends"},
              "outwardIssue": map[string]any{"key": "VO-5"},
          },
          map[string]any{
              // чужой тип связи — не должен попасть в DependsOn
              "type":         map[string]any{"name": "Blocks"},
              "outwardIssue": map[string]any{"key": "VO-9"},
          },
          map[string]any{
              // обратная сторона — не читается
              "type":        map[string]any{"name": "Depends"},
              "inwardIssue": map[string]any{"key": "VO-3"},
          },
          map[string]any{
              "type":         map[string]any{"name": "Depends"},
              "outwardIssue": map[string]any{"key": "VO-6"},
          },
      }

      task, err := tr.Get("VO-1")
      if err != nil {
          t.Fatalf("задача не прочитана: %v", err)
      }
      want := []string{"VO-5", "VO-6"}
      if !slices.Equal(task.DependsOn, want) {
          t.Errorf("DependsOn = %v, ожидалось %v", task.DependsOn, want)
      }
  }

  // TestGetDependsOnEmptyWithoutIssuelinksField — задача без issuelinks
  // вовсе (обычный случай для большинства тикетов) не должна давать сбой
  // разбора и не должна давать ложных зависимостей.
  func TestGetDependsOnEmptyWithoutIssuelinksField(t *testing.T) {
      tr, _ := fixture(t)
      task, err := tr.Get("VO-1")
      if err != nil {
          t.Fatalf("задача не прочитана: %v", err)
      }
      if len(task.DependsOn) != 0 {
          t.Errorf("DependsOn = %v, ожидался пустой список", task.DependsOn)
      }
  }
  ```

- [ ] **Step 3: Run tests to verify they fail**

  Run: `go test ./internal/tracker/jira/... -run TestGetParsesDependsOnFromIssuelinks -v`
  Expected: FAIL — `task.DependsOn` stays empty, `want` isn't matched.

- [ ] **Step 4: Implement the parsing**

  In `internal/tracker/jira/jira.go`, `toTask`, right before `return task`
  (after the `attachment` block):

  ```go
      if links, ok := fields["issuelinks"].([]any); ok {
          for _, raw := range links {
              link, ok := raw.(map[string]any)
              if !ok {
                  continue
              }
              typ, _ := link["type"].(map[string]any)
              if text(typ["name"]) != t.cfg.DependsOnLink {
                  continue
              }
              // outwardIssue заполнен только у той стороны связи, что была
              // записана как outwardIssue при POST /issueLink — а
              // LinkDependsOn пишет туда dependsOnKey (см. его доккомент
              // про развёрнутое направление). inwardIssue здесь —
              // обратная связь ("кто зависит от меня"), её не читаем:
              // DependsOn — это "от кого зависит эта задача", не "кто
              // зависит от неё".
              if out, ok := link["outwardIssue"].(map[string]any); ok {
                  task.DependsOn = append(task.DependsOn, text(out["key"]))
              }
          }
      }
  ```

  Пустой `t.cfg.DependsOnLink` не требует отдельной обработки: `text(typ["name"])
  != ""` для любой настоящей связи, значит ничего не совпадёт и
  `DependsOn` останется пустым — тот же режим отказа, что уже есть у
  `LinkDependsOn` (`internal/tracker/jira/jira.go`, вокруг строки 744), но
  без явного `error`: здесь это не операция записи, падать нечему.

- [ ] **Step 5: Run tests to verify they pass**

  Run: `go test ./internal/tracker/jira/... -run 'TestGetParsesDependsOnFromIssuelinks|TestGetDependsOnEmptyWithoutIssuelinksField' -v`
  Expected: PASS.

- [ ] **Step 6: Full package run**

  Run: `go build ./... && go vet ./... && go test ./internal/tracker/jira/...`

- [ ] **Step 7: Commit**

  ```bash
  git add internal/tracker/jira/jira.go internal/tracker/jira/jira_test.go
  git commit -m "feat(jira): parse issuelinks into Task.DependsOn"
  ```

---

## Task 3: `jira.searchFields()` requests `issuelinks`; doc comment fixed

Closes **tasks.md 1.3**, the remainder of **1.4** (a `TaskRef` from
`search()` carrying the same `DependsOn` a `Get()` would), and **1.5**.

`Get(key)` already returns the full `issuelinks` field regardless of what's
requested — JIRA's `GET /issue/{key}` doesn't take a `fields` filter the way
`/search` does. `ListReady`/`List` go through `search()` →
`t.searchFields()`, and without `"issuelinks"` in that list, a real JIRA
server would omit the field from search results even though Task 2's
parsing is correct — the gate would see every candidate's `DependsOn` as
empty. This is the mistake the design doc calls out by name (§1, "search()
tashes ... **но НЕ** search()"): fix and prove it with a test on the
*request*, not just on `toTask` in isolation.

**Files:**
- Modify: `internal/tracker/jira/jira.go`
- Modify: `internal/tracker/jira/jira_test.go`
- Modify: `internal/tracker/tracker.go` (doc comment only)

**Interfaces:**
- Consumes: Task 1's `TaskRef.DependsOn`, Task 2's `toTask` parsing.
- Produces: `searchFields()` includes `"issuelinks"`; `ListReady`/`List`
  candidates carry `DependsOn`.

- [ ] **Step 1: Capture the requested field list in the fake server**

  In `internal/tracker/jira/jira_test.go`, add a field to `fakeJira`:

  ```go
      lastJQL      string
      // lastSearchFields — "fields" последнего тела POST /search: то, что
      // на самом деле запрашивает searchFields(), не то, что фейковый
      // сервер решает вернуть (он всегда отдаёт issue() целиком — см.
      // TestSearchRequestsIssuelinksField ниже, которая проверяет именно
      // запрос).
      lastSearchFields []any
  ```

  In `ServeHTTP`, `case r.URL.Path == "/rest/api/2/search":`, right after
  `f.lastJQL, _ = body["jql"].(string)`:

  ```go
      f.lastSearchFields, _ = body["fields"].([]any)
  ```

- [ ] **Step 2: Write the failing tests**

  ```go
  // TestSearchRequestsIssuelinksField доказывает, что searchFields()
  // просит issuelinks у сервера — без этого поля ListReady/List на живом
  // JIRA отдавали бы пустой DependsOn у каждого кандидата даже при верном
  // toTask (design doc §1).
  func TestSearchRequestsIssuelinksField(t *testing.T) {
      tr, fake := fixture(t)
      if _, err := tr.ListReady("VO", "Ready"); err != nil {
          t.Fatalf("список не прочитан: %v", err)
      }

      found := false
      for _, raw := range fake.lastSearchFields {
          if s, _ := raw.(string); s == "issuelinks" {
              found = true
          }
      }
      if !found {
          t.Errorf("запрошенные поля поиска не включают issuelinks: %v", fake.lastSearchFields)
      }
  }

  // TestListCandidateCarriesDependsOnLikeGet доказывает, что кандидат из
  // List() несёт ту же зависимость, что и Get() той же задачи — не только
  // форма запроса верна, но и итоговое значение совпадает (tasks.md 1.4,
  // последний пункт).
  func TestListCandidateCarriesDependsOnLikeGet(t *testing.T) {
      tr, fake := fixture(t)
      fake.remoteIssuelinks = []any{
          map[string]any{"type": map[string]any{"name": "Depends"}, "outwardIssue": map[string]any{"key": "VO-5"}},
      }

      refs, err := tr.List("VO", []string{"Ready"})
      if err != nil {
          t.Fatalf("список не прочитан: %v", err)
      }
      if len(refs) != 1 {
          t.Fatalf("кандидатов %d, ожидался 1", len(refs))
      }

      task, err := tr.Get("VO-1")
      if err != nil {
          t.Fatalf("задача не прочитана: %v", err)
      }
      if !slices.Equal(refs[0].DependsOn, task.DependsOn) {
          t.Errorf("List().DependsOn = %v, Get().DependsOn = %v — разошлись", refs[0].DependsOn, task.DependsOn)
      }
      if !slices.Equal(refs[0].DependsOn, []string{"VO-5"}) {
          t.Errorf("DependsOn = %v, ожидалось [VO-5]", refs[0].DependsOn)
      }
  }
  ```

- [ ] **Step 3: Run tests to verify they fail**

  Run: `go test ./internal/tracker/jira/... -run 'TestSearchRequestsIssuelinksField|TestListCandidateCarriesDependsOnLikeGet' -v`
  Expected: `TestSearchRequestsIssuelinksField` FAILs (field list doesn't
  include `"issuelinks"`); `TestListCandidateCarriesDependsOnLikeGet` PASSes
  already if Task 2 is done (the fake server ignores the requested field
  list and always returns the fixture in full) — that's expected and fine,
  it's still worth keeping as a regression guard once the request itself is
  fixed.

- [ ] **Step 4: Add `"issuelinks"` to `searchFields()`**

  In `internal/tracker/jira/jira.go`:

  ```go
  func (t *Tracker) searchFields() []string {
      return []string{
          "summary", "description", "status", "project", "labels", "updated", "issuelinks",
          t.cfg.Fields.Owner, t.cfg.Fields.RunID, t.cfg.Fields.LeaseUntil, t.cfg.Fields.Attempts,
      }
  }
  ```

- [ ] **Step 5: Update the now-stale doc comment on `Task.DependsOn`**

  In `internal/tracker/tracker.go`, replace:

  ```go
      // DependsOn — ключи задач, от которых зависит эта (LinkDependsOn).
      // Пишется этой волной, не читается никаким кодом Change 1 — гейт
      // очерёдности по этому полю добавит Change 2.
      //
      // Get() гарантированно отражает связь, записанную LinkDependsOn, не на
      // всех реализациях: mock — да (хранит и читает то же поле), jira — нет
      // (LinkDependsOn там только шлёт POST /issueLink, toTask не разбирает
      // issuelinks обратно). Сейчас безвредно — поле никто не читает, но
      // Change 2 обязан спроектировать гейт с учётом этой асимметрии, а не
      // понадеяться на неё молча.
      DependsOn []string
  ```

  with:

  ```go
      // DependsOn — ключи задач, от которых зависит эта. Пишется
      // LinkDependsOn, читается обратно через Get/List/ListReady на обеих
      // реализациях: mock хранит и читает то же поле, jira разбирает
      // issuelinks в toTask (см. его доккомент про направление
      // outward/inward) и запрашивает это поле явно в searchFields() —
      // без него List/ListReady отдавали бы пустой DependsOn даже при
      // верном Get(). Гейт очерёдности по этому полю —
      // internal/pipeline/deps.go, Office.claim().
      DependsOn []string
  ```

- [ ] **Step 6: Run tests to verify they pass**

  Run: `go test ./internal/tracker/jira/... -run 'TestSearchRequestsIssuelinksField|TestListCandidateCarriesDependsOnLikeGet' -v`
  Expected: PASS.

- [ ] **Step 7: Full package run**

  Run: `go build ./... && go vet ./... && go test ./internal/tracker/...`

- [ ] **Step 8: Commit**

  ```bash
  git add internal/tracker/jira/jira.go internal/tracker/jira/jira_test.go internal/tracker/tracker.go
  git commit -m "feat(jira): request issuelinks in search(), update stale DependsOn doc comment"
  ```

---

## Task 4: Live verification — reference `Blocks` type read direction (manual)

Closes **tasks.md 2.1**. **Not a subagent-executable TDD task** — this
produces a fact about the live server, verified independently of the code
under test, per the design doc's explicit lesson (§1 "Живая верификация —
не повторять прошлую ошибку"): a prior round verified only the *form* of a
request/response, not its *meaning* on the real server, and shipped a
reversed direction as a result (see `internal/tracker/jira/jira.go`'s
`LinkDependsOn` doc comment for the write-side incident this mirrors).

**Prerequisite:** access to the JIRA Server 8.13 polygon (`10.73.10.235`)
and its credentials — see the local memory notes on the polygon and tracker
credentials (not part of this repo). A throwaway project/issue type usable
for scratch tickets on that instance (the same one prior live verifications
in this line used, per `docs/notes/analyst-task-splitting.md`).

**Procedure:**

- [ ] **Step 1: Set up request variables**

  ```bash
  BASE="<base_url from the polygon's tracker.yaml>"
  AUTH="<jira user>:<jira password>"     # matches ${OFFICE_HOME}/tracker.yaml accounts.default
  PROJECT="<throwaway project key on the polygon>"
  ```

- [ ] **Step 2: Create two throwaway issues, A and B**

  ```bash
  curl -s -u "$AUTH" -H "Content-Type: application/json" -H "X-Atlassian-Token: no-check" \
    -X POST "$BASE/rest/api/2/issue" \
    -d "{\"fields\":{\"project\":{\"key\":\"$PROJECT\"},\"summary\":\"throwaway A (gate direction check)\",\"issuetype\":{\"name\":\"Task\"}}}"
  curl -s -u "$AUTH" -H "Content-Type: application/json" -H "X-Atlassian-Token: no-check" \
    -X POST "$BASE/rest/api/2/issue" \
    -d "{\"fields\":{\"project\":{\"key\":\"$PROJECT\"},\"summary\":\"throwaway B (gate direction check)\",\"issuetype\":{\"name\":\"Task\"}}}"
  ```

  Record the returned keys as `KEY_A`, `KEY_B`.

- [ ] **Step 3: Link them by hand with the reference `Blocks` type — not
      through `LinkDependsOn`/`jira.go`**

  Choose roles explicitly and deliberately, independent of any assumption
  the code under test makes: "A blocks B" (A is the blocker).

  ```bash
  curl -s -u "$AUTH" -H "Content-Type: application/json" -H "X-Atlassian-Token: no-check" \
    -X POST "$BASE/rest/api/2/issueLink" \
    -d "{\"type\":{\"name\":\"Blocks\"},\"outwardIssue\":{\"key\":\"$KEY_A\"},\"inwardIssue\":{\"key\":\"$KEY_B\"}}"
  ```

- [ ] **Step 4: Read the UI's own account of the relationship**

  Open both `$KEY_A` and `$KEY_B` in the JIRA web UI. Note, in your own
  words, which issue the UI says "blocks" the other, and which says "is
  blocked by".

- [ ] **Step 5: Inspect the raw `issuelinks` JSON on both sides**

  ```bash
  curl -s -u "$AUTH" "$BASE/rest/api/2/issue/$KEY_A?fields=issuelinks" | jq .
  curl -s -u "$AUTH" "$BASE/rest/api/2/issue/$KEY_B?fields=issuelinks" | jq .
  ```

  Record which key's own `issuelinks` entry for this link carries
  `outwardIssue` and which carries `inwardIssue`.

- [ ] **Step 6: Compare Step 4 and Step 5, write down the finding**

  Confirm the JSON's `outwardIssue`-carrying side is the one the UI calls
  the blocker (the outward-facing text of type `Blocks` is literally
  "blocks"). This is the ground truth Task 2's parsing (`outwardIssue` →
  `DependsOn`) must match for whatever type name Task 5 tests next. Write
  the concrete finding down (a line in your working notes is enough — Task 5
  folds the combined finding into `docs/notes/analyst-task-splitting.md`).

- [ ] **Step 7: Delete both throwaway issues**

  ```bash
  curl -s -u "$AUTH" -X DELETE "$BASE/rest/api/2/issue/$KEY_A"
  curl -s -u "$AUTH" -X DELETE "$BASE/rest/api/2/issue/$KEY_B"
  ```

No commit for this task — it's a verification finding, not a code change.
Task 5 is blocked on this one completing with a clear, written-down
direction finding.

---

## Task 5: Live verification — real `DependsOnLink`/`LinkDependsOn` direction (manual)

Closes **tasks.md 2.2**. Depends on Task 4's finding. Same manual,
non-TDD nature — this is what the design doc calls "тот же приём, каким уже
один раз проверялось направление записи", now applied to the actual
`depends_on_link` type this codebase uses, not the reference `Blocks` type.

**Procedure:**

- [ ] **Step 1: Create two more throwaway issues, C and D**

  Same as Task 4 Step 2, with new summaries (e.g. "throwaway C/D (real
  DependsOnLink check)").

- [ ] **Step 2: Link them through the actual code path under review**

  Read the real `depends_on_link` value from the polygon's
  `${OFFICE_HOME}/tracker.yaml` (whatever string is configured there — do
  not assume it's `"Blocks"`; that was Task 4's reference type only). Call
  `LinkDependsOn` the same way `internal/pipeline/splits.go`'s
  `linkChildren` does — either via a short throwaway Go snippet exercising
  `jira.Tracker.LinkDependsOn("C", "D", tracker.BySystem())` against the
  polygon config, or by hand-issuing the *identical* request the current
  implementation sends (`internal/tracker/jira/jira.go`, `LinkDependsOn`):

  ```bash
  curl -s -u "$AUTH" -H "Content-Type: application/json" -H "X-Atlassian-Token: no-check" \
    -X POST "$BASE/rest/api/2/issueLink" \
    -d "{\"type\":{\"name\":\"<depends_on_link from tracker.yaml>\"},\"outwardIssue\":{\"key\":\"$KEY_D\"},\"inwardIssue\":{\"key\":\"$KEY_C\"}}"
  ```

  (this mirrors `key=C` depends on `dependsOnKey=D`, i.e. "C depends on D").

- [ ] **Step 2: Read both sides**

  ```bash
  curl -s -u "$AUTH" "$BASE/rest/api/2/issue/$KEY_C?fields=issuelinks" | jq .
  curl -s -u "$AUTH" "$BASE/rest/api/2/issue/$KEY_D?fields=issuelinks" | jq .
  ```

- [ ] **Step 3: Confirm the direction Task 2's parsing assumes**

  Confirm `$KEY_C`'s own `issuelinks` entry for this link carries
  `outwardIssue: {key: $KEY_D}` — this is exactly what Task 2's code reads
  as `task.DependsOn = [D]` when parsing issue C. Cross-check against Task
  4's finding: the meaning of `outwardIssue`/`inwardIssue` should be the
  same regardless of link type name — if it isn't, that's the finding to
  write down and act on, not paper over.

- [ ] **Step 4: Delete both throwaway issues**

  ```bash
  curl -s -u "$AUTH" -X DELETE "$BASE/rest/api/2/issue/$KEY_C"
  curl -s -u "$AUTH" -X DELETE "$BASE/rest/api/2/issue/$KEY_D"
  ```

- [ ] **Step 5: Record the outcome, gate proceeding on it**

  If Step 3 confirms the direction Task 2's code already assumes, add a
  short entry to `docs/notes/analyst-task-splitting.md` (matching the
  section/format prior live-verification entries in that file use) stating
  the read direction is confirmed on the polygon for the configured
  `depends_on_link` type. **If it does not match — stop.** Do not proceed
  to Task 6 or beyond until Task 2's parsing (or the understanding of which
  field means what) is corrected and this task is re-run. The gate (Task 7)
  must never be built on top of an unconfirmed read direction — this is the
  entire reason Tasks 4 and 5 exist as separate, explicit plan items before
  Task 6.

No separate code commit for this task unless Step 3 finds a mismatch (in
which case: fix Task 2's code, add a regression test reproducing the real
direction, and commit that fix before continuing).

---

## Task 6: `internal/pipeline/deps.go` — the shared dependency-status helper

Closes **tasks.md 3.1** and **3.2**.

**Files:**
- Create: `internal/pipeline/deps.go`
- Create: `internal/pipeline/deps_test.go`

**Interfaces:**
- Consumes: `tracker.TaskRef` (with `DependsOn` from Task 1).
- Produces: `pipeline.UnmetDependencies(ref tracker.TaskRef, byKey
  map[string]tracker.TaskRef, terminal func(string) bool)
  []tracker.TaskRef`; `describeUnmet(unmet []tracker.TaskRef) string`
  (unexported, used by Task 7's `claim()` log line).

- [ ] **Step 1: Write the failing tests**

  Create `internal/pipeline/deps_test.go`:

  ```go
  package pipeline

  import (
      "strings"
      "testing"

      "github.com/kao73/virtual-office/internal/tracker"
  )

  func terminalIsDone(status string) bool { return status == "Done" }

  func TestUnmetDependenciesNoneWhenAllTerminal(t *testing.T) {
      ref := tracker.TaskRef{Key: "OFF-2", DependsOn: []string{"OFF-1"}}
      byKey := map[string]tracker.TaskRef{"OFF-1": {Key: "OFF-1", Status: "Done"}}

      if unmet := UnmetDependencies(ref, byKey, terminalIsDone); len(unmet) != 0 {
          t.Errorf("зависимость терминальна, но гейт видит незакрытой: %+v", unmet)
      }
  }

  func TestUnmetDependenciesReturnsNonTerminal(t *testing.T) {
      ref := tracker.TaskRef{Key: "OFF-2", DependsOn: []string{"OFF-1"}}
      byKey := map[string]tracker.TaskRef{"OFF-1": {Key: "OFF-1", Status: "Review"}}

      unmet := UnmetDependencies(ref, byKey, terminalIsDone)
      if len(unmet) != 1 || unmet[0].Key != "OFF-1" || unmet[0].Status != "Review" {
          t.Errorf("незакрытая зависимость не найдена: %+v", unmet)
      }
  }

  // TestUnmetDependenciesTreatsMissingKeyAsUnresolved — зависимость,
  // которой нет в byKey (удалена, никогда не существовала), не должна
  // считаться свободной. Возвращается нулевой TaskRef{} (Key == "") —
  // явный сигнал вызывающему "нечем подтвердить", а не тихий пропуск
  // (design doc §2, decision #6 в design.md).
  func TestUnmetDependenciesTreatsMissingKeyAsUnresolved(t *testing.T) {
      ref := tracker.TaskRef{Key: "OFF-2", DependsOn: []string{"OFF-404"}}
      byKey := map[string]tracker.TaskRef{}

      unmet := UnmetDependencies(ref, byKey, terminalIsDone)
      if len(unmet) != 1 {
          t.Fatalf("отсутствующая зависимость не считается незакрытой: %+v", unmet)
      }
      if unmet[0].Key != "" {
          t.Errorf("ожидался нулевой TaskRef для отсутствующей зависимости, получено %+v", unmet[0])
      }
  }

  func TestUnmetDependenciesEmptyWhenNoDependsOn(t *testing.T) {
      ref := tracker.TaskRef{Key: "OFF-1"}
      if unmet := UnmetDependencies(ref, nil, terminalIsDone); unmet != nil {
          t.Errorf("задача без depends_on считается заблокированной: %+v", unmet)
      }
  }

  func TestDescribeUnmetNamesKeyAndStatus(t *testing.T) {
      got := describeUnmet([]tracker.TaskRef{{Key: "OFF-2", Status: "Review"}, {Key: "OFF-3", Status: "InProgress"}})
      want := "OFF-2 (Review), OFF-3 (InProgress)"
      if got != want {
          t.Errorf("describeUnmet = %q, ожидалось %q", got, want)
      }
  }

  func TestDescribeUnmetHandlesMissingDependency(t *testing.T) {
      got := describeUnmet([]tracker.TaskRef{{}})
      if !strings.Contains(got, "неизвестная") {
          t.Errorf("describeUnmet не сообщает о пропавшей зависимости: %q", got)
      }
  }
  ```

- [ ] **Step 2: Run tests to verify they fail**

  Run: `go test ./internal/pipeline/... -run 'TestUnmetDependencies|TestDescribeUnmet' -v`
  Expected: FAIL to compile — `UnmetDependencies`/`describeUnmet` undefined.

- [ ] **Step 3: Implement**

  Create `internal/pipeline/deps.go`:

  ```go
  // Package pipeline — deps.go: общий помощник, отвечающий на вопрос «чего
  // ждёт эта задача», для гейта Office.claim() и для видимости в runner ls
  // (cmd/runner/board.go). Одна реализация, не две — design.md decision #3,
  // docs/superpowers/specs/2026-09-07-split-dependency-gate-design.md §2.
  package pipeline

  import (
      "fmt"
      "strings"

      "github.com/kao73/virtual-office/internal/tracker"
  )

  // UnmetDependencies — зависимости ref, чей статус ещё не терминален, по
  // данным уже загруженного среза задач проекта (byKey, обычно из
  // List(project, statuses): Office.projectByKey строит его для claim(),
  // cmd/runner/board.go — для printBoard, оба одним и тем же вызовом,
  // который они и так уже делают).
  //
  // Зависимость, которой нет в byKey (задача удалена или никогда не
  // существовала), тоже считается незакрытой — её нулевое значение,
  // TaskRef{} (Key == ""), возвращается как есть, а не подменяется:
  // явный сигнал вызывающему «эту зависимость нечем подтвердить», а не
  // тихий пропуск. Падать громко, не считать свободной зависимость,
  // которую нечем подтвердить (docs/DESIGN.md, принцип видимых отказов;
  // design.md decision #6).
  func UnmetDependencies(ref tracker.TaskRef, byKey map[string]tracker.TaskRef, terminal func(string) bool) []tracker.TaskRef {
      var unmet []tracker.TaskRef
      for _, key := range ref.DependsOn {
          dep, found := byKey[key]
          if !found || !terminal(dep.Status) {
              unmet = append(unmet, dep)
          }
      }
      return unmet
  }

  // describeUnmet — «OFF-2 (Review), OFF-3 (InProgress)» для лога claim():
  // имя и текущий статус каждой незакрытой зависимости. Пустой Key
  // (UnmetDependencies отдаёт его для зависимости, которой нет в byKey)
  // показывается отдельной пометкой — молчать о том, что зависимость
  // вообще пропала, было бы хуже, чем показать её без статуса.
  func describeUnmet(unmet []tracker.TaskRef) string {
      parts := make([]string, len(unmet))
      for i, dep := range unmet {
          key := dep.Key
          if key == "" {
              key = "неизвестная задача"
          }
          parts[i] = fmt.Sprintf("%s (%s)", key, dep.Status)
      }
      return strings.Join(parts, ", ")
  }
  ```

- [ ] **Step 4: Run tests to verify they pass**

  Run: `go test ./internal/pipeline/... -run 'TestUnmetDependencies|TestDescribeUnmet' -v`
  Expected: PASS.

- [ ] **Step 5: Full package run**

  Run: `go build ./... && go vet ./... && go test ./internal/pipeline/...`

- [ ] **Step 6: Commit**

  ```bash
  git add internal/pipeline/deps.go internal/pipeline/deps_test.go
  git commit -m "feat(pipeline): add UnmetDependencies helper shared by claim() and ls"
  ```

---

## Task 7: Gate in `claim()`

Closes **tasks.md 4.1** and **4.2**.

**Files:**
- Modify: `internal/pipeline/pipeline.go`
- Modify: `internal/pipeline/pipeline_test.go`

**Interfaces:**
- Consumes: `pipeline.UnmetDependencies` and `describeUnmet` (Task 6).
- Produces: `Office.projectByKey(project string) (map[string]tracker.TaskRef,
  error)`; `claim()` skips candidates with unmet dependencies.

- [ ] **Step 1: Write the failing tests**

  In `internal/pipeline/pipeline_test.go`:

  ```go
  // TestClaimSkipsCandidateWithUnresolvedDependency доказывает, что
  // кандидат с незакрытым depends_on не берётся в работу: implementer не
  // должен опереться на код, которого зависимость ещё не смержила.
  func TestClaimSkipsCandidateWithUnresolvedDependency(t *testing.T) {
      var log strings.Builder
      o := newOffice(t)
      o.Office.Log = &log
      // OFF-1 (заведена newOffice по умолчанию в Ready) не участвует в этом
      // тесте как кандидат — иначе она будет взята первой (ключи в mock
      // возвращаются по возрастанию) и гейт для OFF-3 не проверится вовсе.
      if err := o.tasks.Move("OFF-1", "Blocked"); err != nil {
          t.Fatalf("подготовка не удалась: %v", err)
      }

      if err := o.tasks.Add(tracker.Task{
          Key: "OFF-2", Project: "OFF", Status: "Review", Summary: "Блокирующая часть",
      }); err != nil {
          t.Fatalf("блокер не заведён: %v", err)
      }
      if err := o.tasks.Add(tracker.Task{
          Key: "OFF-3", Project: "OFF", Status: "Ready", Summary: "Зависимая часть",
          DependsOn: []string{"OFF-2"},
      }); err != nil {
          t.Fatalf("зависимая задача не заведена: %v", err)
      }

      if o.tick(t) {
          t.Fatal("незакрытая зависимость не должна была позволить взять задачу")
      }

      task := o.get(t, "OFF-3")
      if task.RunID != "" || task.Status != "Ready" {
          t.Errorf("заблокированная задача сдвинулась: %+v", task)
      }
      if !strings.Contains(log.String(), "OFF-3") || !strings.Contains(log.String(), "OFF-2") {
          t.Errorf("лог не называет, чего ждёт задача:\n%s", log.String())
      }
  }

  // TestClaimTakesCandidateOnceDependencyIsTerminal — тот же кандидат, что
  // выше, но зависимость уже в терминальном статусе: гейт больше не мешает.
  func TestClaimTakesCandidateOnceDependencyIsTerminal(t *testing.T) {
      o := newOffice(t)
      if err := o.tasks.Move("OFF-1", "Blocked"); err != nil {
          t.Fatalf("подготовка не удалась: %v", err)
      }
      if err := o.tasks.Add(tracker.Task{
          Key: "OFF-2", Project: "OFF", Status: "Done", Summary: "Блокирующая часть",
      }); err != nil {
          t.Fatalf("блокер не заведён: %v", err)
      }
      if err := o.tasks.Add(tracker.Task{
          Key: "OFF-3", Project: "OFF", Status: "Ready", Summary: "Зависимая часть",
          DependsOn: []string{"OFF-2"},
      }); err != nil {
          t.Fatalf("зависимая задача не заведена: %v", err)
      }

      if !o.tick(t) {
          t.Fatal("задача с разрешённой зависимостью должна была уйти в работу")
      }
      if o.agent.seen.Passport.TaskKey != "OFF-3" {
          t.Errorf("в работу ушла %q, ожидалась OFF-3", o.agent.seen.Passport.TaskKey)
      }
  }

  // TestClaimTreatsMissingDependencyAsUnresolved — depends_on называет
  // задачу, которой в трекере нет вовсе: гейт обязан считать её незакрытой,
  // а не свободной (pipeline-dependency-gate/spec.md, "A missing
  // dependency task blocks the candidate").
  func TestClaimTreatsMissingDependencyAsUnresolved(t *testing.T) {
      o := newOffice(t)
      if err := o.tasks.Move("OFF-1", "Blocked"); err != nil {
          t.Fatalf("подготовка не удалась: %v", err)
      }
      if err := o.tasks.Add(tracker.Task{
          Key: "OFF-3", Project: "OFF", Status: "Ready", Summary: "Зависимая часть",
          DependsOn: []string{"OFF-404"},
      }); err != nil {
          t.Fatalf("зависимая задача не заведена: %v", err)
      }

      if o.tick(t) {
          t.Fatal("зависимость на несуществующую задачу не должна считаться разрешённой")
      }
      task := o.get(t, "OFF-3")
      if task.RunID != "" {
          t.Errorf("задача с зависимостью на несуществующий тикет всё равно захвачена: %+v", task)
      }
  }

  // TestClaimGatesAnalystCandidateTheSameWay — тот же гейт для analyst'а,
  // не только для implementer'а: единый код без исключений по роли
  // (pipeline-dependency-gate/spec.md, "The gate applies uniformly across
  // workflow roles").
  func TestClaimGatesAnalystCandidateTheSameWay(t *testing.T) {
      o := newOffice(t)
      if err := o.tasks.Move("OFF-1", "Blocked"); err != nil {
          t.Fatalf("подготовка не удалась: %v", err)
      }
      if err := o.tasks.Add(tracker.Task{
          Key: "OFF-2", Project: "OFF", Status: "Ready", Summary: "Блокирующая часть",
      }); err != nil {
          t.Fatalf("блокер не заведён: %v", err)
      }
      if err := o.tasks.Add(tracker.Task{
          Key: "OFF-5", Project: "OFF", Status: "Analysis", Summary: "Дочерняя постановка",
          DependsOn: []string{"OFF-2"},
      }); err != nil {
          t.Fatalf("зависимая задача не заведена: %v", err)
      }

      worked, err := o.Tick(context.Background(), "analyst")
      if err != nil {
          t.Fatalf("тик не прошёл: %v", err)
      }
      if worked {
          t.Fatal("аналитик не должен был взять задачу с незакрытой зависимостью")
      }
      if task := o.get(t, "OFF-5"); task.RunID != "" {
          t.Errorf("заблокированная постановка всё равно захвачена: %+v", task)
      }
  }
  ```

- [ ] **Step 2: Run tests to verify they fail**

  Run: `go test ./internal/pipeline/... -run 'TestClaimSkipsCandidateWithUnresolvedDependency|TestClaimTakesCandidateOnceDependencyIsTerminal|TestClaimTreatsMissingDependencyAsUnresolved|TestClaimGatesAnalystCandidateTheSameWay' -v`
  Expected: FAIL — `TestClaimSkipsCandidateWithUnresolvedDependency`,
  `TestClaimTreatsMissingDependencyAsUnresolved`, and
  `TestClaimGatesAnalystCandidateTheSameWay` claim the candidate anyway
  (`o.tick(t)`/`worked` is `true`); `TestClaimTakesCandidateOnceDependencyIsTerminal`
  already passes (nothing blocks it yet) — that's expected, it's a
  regression guard for after the gate lands.

- [ ] **Step 3: Wire the gate into `claim()`, add `projectByKey`**

  In `internal/pipeline/pipeline.go`, replace the existing `claim()`:

  ```go
  // claim выбирает первого годного кандидата и берёт его. Пустой ключ означает,
  // что работы нет.
  func (o *Office) claim(roleName string, flow tracker.RoleFlow, role runner.Role) (claimed, error) {
      lease := o.now().Add(time.Duration(role.Limits.TimeoutSec)*time.Second + o.Workflow.LeaseMargin())

      for _, project := range o.projects() {
          refs, err := o.Tracker.ListReady(project, flow.ReadsFrom)
          if o.skipProject(project, err) {
              continue
          }
          if err != nil {
              return claimed{}, err
          }
          for _, ref := range refs {
              if ref.Attempts >= o.Workflow.Limits.MaxAttempts {
                  o.logf("%s: попытки исчерпаны (%d), пропускаю", ref.Key, ref.Attempts)
                  continue
              }
              task, taken, err := o.take(ref, roleName, flow, lease)
              if err != nil {
                  return claimed{}, err
              }
              if taken {
                  return task, nil
              }
          }
      }
      return claimed{}, nil
  }
  ```

  with:

  ```go
  // claim выбирает первого годного кандидата и берёт его. Пустой ключ означает,
  // что работы нет.
  func (o *Office) claim(roleName string, flow tracker.RoleFlow, role runner.Role) (claimed, error) {
      lease := o.now().Add(time.Duration(role.Limits.TimeoutSec)*time.Second + o.Workflow.LeaseMargin())

      for _, project := range o.projects() {
          refs, err := o.Tracker.ListReady(project, flow.ReadsFrom)
          if o.skipProject(project, err) {
              continue
          }
          if err != nil {
              return claimed{}, err
          }
          if len(refs) == 0 {
              continue
          }

          // Статусы зависимостей — один List на проект, не Get() на каждую
          // зависимость каждого кандидата: тот же принцип, что уже
          // применяет printBoard (cmd/runner/board.go). Ленивый: только
          // когда в refs вообще есть кандидаты (design.md decision #3).
          byKey, err := o.projectByKey(project)
          if err != nil {
              return claimed{}, err
          }

          for _, ref := range refs {
              if ref.Attempts >= o.Workflow.Limits.MaxAttempts {
                  o.logf("%s: попытки исчерпаны (%d), пропускаю", ref.Key, ref.Attempts)
                  continue
              }
              // Гейт очерёдности: один и тот же код для всех ролей графа,
              // без исключения reviewer — его зависимость уже разрешена
              // к этому моменту по построению (design.md decision #4).
              if unmet := UnmetDependencies(ref, byKey, o.Workflow.IsTerminal); len(unmet) > 0 {
                  o.logf("%s: ждёт %s, пропускаю", ref.Key, describeUnmet(unmet))
                  continue
              }
              task, taken, err := o.take(ref, roleName, flow, lease)
              if err != nil {
                  return claimed{}, err
              }
              if taken {
                  return task, nil
              }
          }
      }
      return claimed{}, nil
  }

  // projectByKey — задачи проекта во всех статусах графа, по ключу. Общее
  // сырьё для гейта зависимостей (claim(), через UnmetDependencies) и для
  // видимости в runner ls (cmd/runner/board.go, printBoard) — оба
  // спрашивают трекер о том же самом List(project, statuses).
  func (o *Office) projectByKey(project string) (map[string]tracker.TaskRef, error) {
      refs, err := o.Tracker.List(project, o.Workflow.Statuses)
      if err != nil {
          return nil, err
      }
      byKey := make(map[string]tracker.TaskRef, len(refs))
      for _, ref := range refs {
          byKey[ref.Key] = ref
      }
      return byKey, nil
  }
  ```

- [ ] **Step 4: Run tests to verify they pass**

  Run: `go test ./internal/pipeline/... -run 'TestClaimSkipsCandidateWithUnresolvedDependency|TestClaimTakesCandidateOnceDependencyIsTerminal|TestClaimTreatsMissingDependencyAsUnresolved|TestClaimGatesAnalystCandidateTheSameWay' -v`
  Expected: PASS.

- [ ] **Step 5: Full package run — the rest of the suite still passes**

  Run: `go build ./... && go vet ./... && go test ./internal/pipeline/...`
  This is the step most likely to expose a fixture that implicitly relied
  on `claim()` never calling `List` — read any failure carefully rather
  than papering over it (e.g. a fake tracker embedding `mock.Tracker` but
  missing a working `List` override for a test-specific status).

- [ ] **Step 6: Commit**

  ```bash
  git add internal/pipeline/pipeline.go internal/pipeline/pipeline_test.go
  git commit -m "feat(pipeline): gate claim() on unresolved depends_on, uniformly across roles"
  ```

---

## Task 8: Visibility in `runner ls`

Closes **tasks.md 5.1** and **5.2**.

**Files:**
- Modify: `cmd/runner/board.go`
- Modify: `cmd/runner/board_test.go`

**Interfaces:**
- Consumes: `pipeline.UnmetDependencies` (Task 6).
- Produces: `printBoard(tasks tracker.Tracker, projects, statuses []string,
  terminal func(string) bool, now time.Time, out io.Writer) error` (new
  `terminal` parameter); `dependsColumn(unmet []tracker.TaskRef) string`.

- [ ] **Step 1: Write the failing test, update existing call sites**

  In `cmd/runner/board_test.go`, update the three existing `printBoard`
  calls to pass a `terminal` function (matching the real `workflow.yaml`,
  `terminal: [Done]`):

  ```go
  // In TestBoardShowsWhoWorksAndForHowLong:
  if err := printBoard(tr, []string{"OFF"}, statuses, func(s string) bool { return s == "Done" }, boardNow, &out); err != nil {

  // In TestBoardSaysWhenEmpty:
  if err := printBoard(tr, []string{"OFF"}, []string{"Ready"}, func(s string) bool { return s == "Done" }, boardNow, &out); err != nil {

  // In TestBoardSkipsProjectUnknownToTracker:
  if err := printBoard(tasks, []string{"AAA", "OFF"}, []string{"Ready"}, func(s string) bool { return s == "Done" }, boardNow, &out); err != nil {
  ```

  Then add the new test:

  ```go
  // TestBoardShowsBlockedDependency доказывает, что ls называет, чего
  // ждёт заблокированная задача, без отдельной команды
  // (pipeline-dependency-gate/spec.md, "A blocked candidate's wait is
  // visible without extra tooling").
  func TestBoardShowsBlockedDependency(t *testing.T) {
      tr := mock.New(t.TempDir())
      tr.Now = func() time.Time { return boardNow }

      add := func(task tracker.Task) {
          if err := tr.Add(task); err != nil {
              t.Fatalf("задача не создана: %v", err)
          }
      }
      add(tracker.Task{Key: "OFF-1", Project: "OFF", Status: "Ready", Summary: "Первая часть"})
      add(tracker.Task{Key: "OFF-2", Project: "OFF", Status: "Ready", Summary: "Вторая часть", DependsOn: []string{"OFF-1"}})

      var out bytes.Buffer
      terminal := func(s string) bool { return s == "Done" }
      if err := printBoard(tr, []string{"OFF"}, []string{"Ready"}, terminal, boardNow, &out); err != nil {
          t.Fatalf("доска не напечатана: %v", err)
      }

      var off1Line, off2Line string
      for _, line := range strings.Split(out.String(), "\n") {
          switch {
          case strings.HasPrefix(line, "OFF-1 "):
              off1Line = line
          case strings.HasPrefix(line, "OFF-2 "):
              off2Line = line
          }
      }
      if !strings.Contains(off2Line, "ждёт: OFF-1 (Ready)") {
          t.Errorf("строка OFF-2 не называет зависимость:\n%s", off2Line)
      }
      if strings.Contains(off1Line, "ждёт:") {
          t.Errorf("у задачи без зависимостей появилась колонка ожидания:\n%s", off1Line)
      }
  }
  ```

- [ ] **Step 2: Run tests to verify they fail**

  Run: `go test ./cmd/runner/... -run TestBoard -v`
  Expected: compile failure everywhere (`printBoard` signature mismatch)
  until Step 3 lands, then `TestBoardShowsBlockedDependency` specifically
  fails on the missing column.

- [ ] **Step 3: Implement**

  In `cmd/runner/board.go`, add the import:

  ```go
  import (
      "fmt"
      "io"
      "strings"
      "time"

      "github.com/kao73/virtual-office/internal/pipeline"
      "github.com/kao73/virtual-office/internal/tracker"
  )
  ```

  Replace `printBoard`:

  ```go
  // printBoard печатает доску проектов: по запросу на проект, без переписки.
  //
  // terminal решает, что считать «зависимость уже разрешена» — то же
  // понятие графа, что использует гейт claim() (internal/pipeline.
  // UnmetDependencies), а не отдельное здесь понятие: расхождение между
  // тем, что видит ls, и тем, что реально блокирует claim(), было бы хуже,
  // чем лишний параметр.
  func printBoard(tasks tracker.Tracker, projects, statuses []string, terminal func(string) bool, now time.Time, out io.Writer) error {
      shown := 0
      for _, project := range projects {
          refs, err := tasks.List(project, statuses)
          if notice, skip := tracker.SkipUnknownProject(project, err); skip {
              fmt.Fprintln(out, notice)
              continue
          }
          if err != nil {
              return err
          }

          byKey := make(map[string]tracker.TaskRef, len(refs))
          for _, ref := range refs {
              byKey[ref.Key] = ref
          }

          for _, ref := range refs {
              unmet := pipeline.UnmetDependencies(ref, byKey, terminal)
              fmt.Fprintf(out, "%-10s %-12s %-24s попыток:%d %-16s %s%-10s %s\n",
                  ref.Key, ref.Status, lease(ref, now), ref.Attempts, waiting(ref), dependsColumn(unmet), age(ref.Updated, now), ref.Summary)
              shown++
          }
      }
      if shown == 0 {
          fmt.Fprintf(out, "задач нет (проекты: %s)\n", strings.Join(projects, ", "))
      }
      return nil
  }

  // dependsColumn — «ждёт: OFF-1 (Ready) » рядом с задачей, у которой есть
  // незакрытые зависимости; пусто — зависимостей нет или все терминальны.
  // Несёт собственную заполняющую пробельность: формат строки выше не
  // держит отдельного места для этой колонки между waiting и age, только
  // сама колонка и её хвостовой пробел, когда она непуста.
  func dependsColumn(unmet []tracker.TaskRef) string {
      if len(unmet) == 0 {
          return ""
      }
      parts := make([]string, len(unmet))
      for i, dep := range unmet {
          key := dep.Key
          if key == "" {
              key = "неизвестная задача"
          }
          parts[i] = fmt.Sprintf("%s (%s)", key, dep.Status)
      }
      return fmt.Sprintf("ждёт: %s ", strings.Join(parts, ", "))
  }
  ```

  In `boardCommand`, update the call site:

  ```go
  return printBoard(o.Tracker, projects, o.Workflow.Statuses, o.Workflow.IsTerminal, time.Now(), out)
  ```

- [ ] **Step 4: Run tests to verify they pass**

  Run: `go test ./cmd/runner/... -run TestBoard -v`
  Expected: PASS.

- [ ] **Step 5: Full package run**

  Run: `go build ./... && go vet ./... && go test ./cmd/runner/...`

- [ ] **Step 6: Commit**

  ```bash
  git add cmd/runner/board.go cmd/runner/board_test.go
  git commit -m "feat(runner): show what a blocked task is waiting on in ls"
  ```

---

## Task 9: `roles/analyst/role.md` — the `depends_on` criterion

Closes **tasks.md 6.1**.

**Files:**
- Modify: `roles/analyst/role.md`

**Interfaces:** none (prose only).

- [ ] **Step 1: Insert the criterion next to the `split` outcome description**

  In `roles/analyst/role.md`, in the `## Исходы` section, replace:

  ```markdown
  `done` означает ровно `next_owner: implementer`. «Готово, посмотрите» — это
  `needs_human` с вопросом, а не `done` с передачей человеку. По той же
  причине резюме после подтверждённого разбиения — снова `split`, не `done`:
  у `done` для этой роли `next_owner: none` (единственное, чем ты могла бы
  назвать «работы больше нет») едет по умолчанию в `Ready`, к разработчику,
  который получил бы неразрезанную задачу.

  ## Когда задачу вернул разработчик
  ```

  with:

  ```markdown
  `done` означает ровно `next_owner: implementer`. «Готово, посмотрите» — это
  `needs_human` с вопросом, а не `done` с передачей человеку. По той же
  причине резюме после подтверждённого разбиения — снова `split`, не `done`:
  у `done` для этой роли `next_owner: none` (единственное, чем ты могла бы
  назвать «работы больше нет») едет по умолчанию в `Ready`, к разработчику,
  который получил бы неразрезанную задачу.

  `depends_on` ставь только когда одна подзадача не может **начаться**, пока
  не смержен код другой — она опирается на схему, миграцию, модель данных или
  экспортируемый интерфейс, которого без предшественника ещё не существует.
  «Так логичнее по порядку» или «легче ревьюить последовательно» — не
  основание: раннер трактует `depends_on` буквально и не даст ни аналитику,
  ни разработчику начать работу над зависимой задачей, пока предшественник не
  смержен. Лишняя связь не упорядочивает работу, а откладывает её — режет тот
  самый параллелизм, ради которого разбиение затевалось.

  ## Когда задачу вернул разработчик
  ```

- [ ] **Step 2: Verify — no automated test targets role.md prose directly**

  `internal/runner/role_test.go` and `cmd/eval-roles/run_test.go` load
  role files structurally (front matter, network domains) and don't assert
  exact body text — running the full suite (Task 11) is the check that
  nothing structural broke. There is no separate TDD cycle for this task;
  the "test" is a careful re-read of the inserted paragraph against
  `docs/openspec/changes/split-dependency-gate/specs/role-native-workflow/spec.md`'s
  two scenarios ("A genuine interface dependency is recorded" / "A
  convenience ordering is not recorded as a dependency") to confirm the
  wording covers both.

- [ ] **Step 3: Commit**

  ```bash
  git add roles/analyst/role.md
  git commit -m "docs(analyst): add depends_on criterion for split children"
  ```

---

## Task 10 (optional, not blocking): `linkChildren` laziness cleanup

Closes **tasks.md 7.1**. Explicitly optional per `tasks.md` — server-side
idempotency of `POST /issueLink` already makes the current behavior safe;
this only trims a redundant write on every retried `Loop` cycle for a
stuck split batch. Skip this task without blocking the rest of the plan if
time is short.

**Files:**
- Modify: `internal/pipeline/splits.go`
- Modify: `internal/pipeline/splits_test.go`

**Interfaces:**
- Consumes: `tracker.Tracker.Get(key).DependsOn` (now trustworthy on JIRA
  too, per Tasks 2/3/5).
- Produces: `linkChildren` unchanged in signature, changed in behavior
  (skips a pair already present in `Get(key).DependsOn`).

- [ ] **Step 1: Write the failing test**

  In `internal/pipeline/splits_test.go`:

  ```go
  // countingLinksFlakyClose комбинирует подсчёт вызовов LinkDependsOn (для
  // проверки того, что повтор не шлёт уже записанную связь заново) с
  // персистентным сбоем закрытия родителя (Transition) — тем же приёмом,
  // что flakyClose выше, — чтобы CompleteSplits вызывался дважды на одном
  // и том же застрявшем тикете, не полагаясь на реальный успех закрытия.
  type countingLinksFlakyClose struct {
      *mock.Tracker
      failCloseOn string
      linkCalls   int
  }

  func (c *countingLinksFlakyClose) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error {
      c.linkCalls++
      return c.Tracker.LinkDependsOn(key, dependsOnKey, by)
  }

  func (c *countingLinksFlakyClose) Transition(key string, by tracker.Actor, toStatus string) error {
      if key == c.failCloseOn {
          return errors.New("сеть недоступна")
      }
      return c.Tracker.Transition(key, by, toStatus)
  }

  // TestLinkChildrenSkipsAlreadyLinkedPairOnRetry доказывает, что застрявший
  // на закрытии тикет не шлёт POST /issueLink заново на каждый цикл Loop
  // для пары, уже связанной прошлым проходом.
  func TestLinkChildrenSkipsAlreadyLinkedPairOnRetry(t *testing.T) {
      o := newOffice(t)
      confirmSplit(t, o)
      wrap := &countingLinksFlakyClose{Tracker: o.tasks, failCloseOn: "OFF-1"}
      o.useTracker(wrap)

      if err := o.CompleteSplits(context.Background()); err != nil {
          t.Fatalf("первый проход не должен падать целиком: %v", err)
      }
      if wrap.linkCalls != 1 {
          t.Fatalf("после первого прохода ожидался 1 вызов LinkDependsOn, получено %d", wrap.linkCalls)
      }

      if err := o.CompleteSplits(context.Background()); err != nil {
          t.Fatalf("второй проход не должен падать целиком: %v", err)
      }
      if wrap.linkCalls != 1 {
          t.Errorf("повторный проход снова отправил уже записанную связь: всего вызовов %d, ожидался 1", wrap.linkCalls)
      }

      transaction, err := o.tasks.FindByMarker("OFF", splitChildMarker("OFF-1", "transaction-crud"))
      if err != nil || len(transaction) != 1 {
          t.Fatalf("операции не найдены: %+v, %v", transaction, err)
      }
      category, err := o.tasks.FindByMarker("OFF", splitChildMarker("OFF-1", "category-crud"))
      if err != nil || len(category) != 1 {
          t.Fatalf("категория не найдена: %+v, %v", category, err)
      }
      linked, err := o.tasks.Get(transaction[0].Key)
      if err != nil {
          t.Fatalf("операции не прочитаны: %v", err)
      }
      if !slices.Contains(linked.DependsOn, category[0].Key) {
          t.Errorf("связь потерялась после повторного прохода: %v", linked.DependsOn)
      }
  }
  ```

- [ ] **Step 2: Run test to verify it fails**

  Run: `go test ./internal/pipeline/... -run TestLinkChildrenSkipsAlreadyLinkedPairOnRetry -v`
  Expected: FAIL — `wrap.linkCalls` is `2` after the second pass (current
  `linkChildren` re-sends every pair every retry).

- [ ] **Step 3: Implement**

  In `internal/pipeline/splits.go`, replace the doc comment and body of
  `linkChildren`:

  ```go
  // linkChildren связывает уже существующих детей по depends_on. Отдельным
  // подпроходом после того, как **все** дети существуют: ребёнок может
  // зависеть от того, кто в split.children[] идёт позже него, и связывать
  // раньше, чем существуют оба конца, нечем.
  //
  // Ленивый: перед LinkDependsOn проверяет Get(key).DependsOn — теперь,
  // когда чтение надёжно и на JIRA тоже (split-dependency-gate,
  // internal/tracker/jira/jira.go toTask/searchFields), застрявший тикет
  // больше не шлёт все POST /issueLink заново на каждый цикл Loop, полагаясь
  // только на серверную дедупликацию.
  func (o *Office) linkChildren(children []runner.SplitChild, keys map[string]string) error {
      by := tracker.BySystem()
      for _, child := range children {
          if len(child.DependsOn) == 0 {
              continue
          }
          existing, err := o.Tracker.Get(keys[child.ID])
          if err != nil {
              return err
          }
          for _, dep := range child.DependsOn {
              depKey := keys[dep]
              if slices.Contains(existing.DependsOn, depKey) {
                  continue
              }
              if err := o.Tracker.LinkDependsOn(keys[child.ID], depKey, by); err != nil {
                  return err
              }
          }
      }
      return nil
  }
  ```

- [ ] **Step 4: Run test to verify it passes**

  Run: `go test ./internal/pipeline/... -run TestLinkChildrenSkipsAlreadyLinkedPairOnRetry -v`
  Expected: PASS.

- [ ] **Step 5: Full package run — existing split tests still pass**

  Run: `go build ./... && go vet ./... && go test ./internal/pipeline/...`
  Pay particular attention to `TestCompleteSplitsResumesInterruptedBatch`
  and `TestCompleteSplitsCreatesAndLinksChildren`, which exercise the same
  code path from a different angle.

- [ ] **Step 6: Commit**

  ```bash
  git add internal/pipeline/splits.go internal/pipeline/splits_test.go
  git commit -m "refactor(pipeline): make linkChildren lazy, now that Get().DependsOn is trustworthy on JIRA"
  ```

---

## Task 11: Final verification — full build/vet/test pass

Closes **tasks.md 8.1**.

- [ ] **Step 1: Run the full verification suite**

  ```bash
  gofmt -l .
  go build ./...
  go vet ./...
  go test ./...
  ```

  Expected: `gofmt -l .` prints nothing; `go build`/`go vet` exit 0; `go
  test ./...` all green.

- [ ] **Step 2: Skim the diff against the spec's scenarios**

  For each scenario in
  `docs/openspec/changes/split-dependency-gate/specs/pipeline-dependency-gate/spec.md`
  and
  `docs/openspec/changes/split-dependency-gate/specs/role-native-workflow/spec.md`,
  point to the test (Task 6, 7, 8, or 9) that covers it. Any gap found here
  gets its own small test added to the relevant task before moving on —
  don't defer it to Task 12's live check, which is not automated and not a
  substitute for unit coverage.

- [ ] **Step 3: Commit (only if Step 2 found and fixed a gap)**

  If Step 2 didn't require any code changes, there's nothing to commit for
  this task — it's a verification checkpoint, not a code-producing one.

---

## Task 12: Live end-to-end check on the polygon (manual)

Closes **tasks.md 8.2**. Requires Tasks 1–9 (and, if done, 10) merged and
deployable to the polygon's JIRA-backed office configuration. Not
TDD-executable — this is the full pipeline running against a real trekker,
not a fake.

**Procedure:**

- [ ] **Step 1: Set up a real split with a genuine dependency**

  On the polygon, run (or resume) an `analyst` progon against a task shaped
  so that `split.children[]` naturally has one child depending on another
  (per the new `role.md` criterion — a real shared schema/interface, not a
  convenience ordering), through to confirmation (two `split` outcomes in a
  row), letting `CompleteSplits` create and link the children as it already
  does per Change 1.

- [ ] **Step 2: Confirm the dependent child is withheld**

  Run `runner ls` (or `runner tick`/`runner loop` for the relevant role)
  and confirm:
  - the dependent child does **not** get claimed by `implementer` while its
    blocker sits in a non-terminal status;
  - `runner ls`'s output for the dependent child names the blocker's key
    and current status (the `dependsColumn` from Task 8);
  - the log line from `claim()` (Task 7) names the same thing when the tick
    runs with the dependent child as the only ready candidate.

- [ ] **Step 3: Confirm the dependent child unblocks**

  Move the blocker to `Done` (merge its PR through the normal PR pass, or
  move it by hand if this is a scratch scenario) and confirm a subsequent
  tick claims the dependent child — no manual step beyond the normal
  workflow.

- [ ] **Step 4: Record the outcome**

  Add a short entry to `docs/notes/analyst-task-splitting.md` (same
  convention as prior live-verification write-ups in that file) noting
  which real ticket(s) this was confirmed on, mirroring how Change 1's live
  confirmation on EXP-2/EXP-3/EXP-12/EXP-15 was recorded.

No code commit expected from this task unless Step 2 or 3 exposes a defect
— in that case, treat it as a bug found by live verification: write a
regression test in the relevant earlier task's test file first, then fix,
then re-run this task's procedure from Step 1.
