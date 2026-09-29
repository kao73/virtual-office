---
change: yougile-dependencies-attachments
design-doc: docs/superpowers/specs/2026-09-29-yougile-dependencies-attachments-design.md
base-ref: 279a7bd6b4bb3157bfc9be4eb657845c3b43c2df
---

# yougile-dependencies-attachments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `internal/tracker/yougile.Tracker` a complete `tracker.Tracker` by adding `LinkDependsOn`, `AddAttachment` and `GetAttachment`, after moving all office data in `apiData` under one `virtual_office` namespace.

**Architecture:** The `apiData` codec in `lease.go` keeps every foreign top-level key as it was and reads and writes only `apiData.virtual_office` (`v: 1`). A card whose office data is unreadable (`ErrOfficeData`) is skipped by the listings and reported through a new `Tracker.Logf` hook. Every other path fails loudly on it. Dependencies are stored as `virtual_office.depends_on`, and `LinkDependsOn` posts a chat note (`Зависит от: …`) *before* writing the id. Attachments get no manifest. They live where the YouGile UI puts them: file messages in the task chat (`/root/#file:/user-data/<uuid>/<name>`) and `/user-data/…` anchors in the description. `AddAttachment` uploads the file and posts a file message. `GetAttachment` finds the link, rebuilds the URL on `BaseURL` and downloads it through a separate `http.Client` that sends no `Authorization` header and follows only a narrow set of redirects.

**Tech Stack:** Go 1.26.6 (`go.mod`), standard library only (`net/http`, `net/http/httptest`, `mime/multipart`, `encoding/json`, `regexp`, `html`, `log`). No new dependencies.

**Spec:** Design Doc `docs/superpowers/specs/2026-09-29-yougile-dependencies-attachments-design.md` (the source of HOW, followed exactly, including §7). Delta spec: `docs/openspec/changes/yougile-dependencies-attachments/specs/tracker-yougile/spec.md`. Task boundaries: `docs/openspec/changes/yougile-dependencies-attachments/tasks.md`. Executors read the Design Doc and this plan together.

Run every command from the worktree root `/Users/aleksejkolesnikov/IdeaProjects/virtual-office/.worktrees/yougile-dependencies-attachments`.

## How this plan maps to `tasks.md` (and where it reframes it)

The Design Doc's §7 departs from the Open artifacts. This plan follows the Design Doc. Three `tasks.md` items therefore change their meaning. Task 1 records this in `tasks.md` itself, so the reframed boxes can still be ticked honestly:

- **2.1** says "`depends_on` sub-object … namespaced alongside the lease sub-object". It is implemented as a `depends_on` **array of task ids** inside the single `apiData.virtual_office` object, next to `lease`, `attempts`, `human_wait` and `labels` (Design §2, §3).
- **3.1** says "attachment manifest sub-object in `apiData` (id→url)". It is **dropped as written**, because there is no manifest (Design §7). Its place is taken by attachment *discovery* from chat file messages and description links (Design §4.1). That work is Tasks 7–8, and 3.1 is ticked in Task 8 with the reframing note.
- **3.4** says "live-check URL stability, immediately and after a delay". Design §1 already records the fact (bytes identical immediately and after 20 s, confirmed live on 2026-09-28/29). Design §7 turns the remaining check into the live byte round-trip, which is Task 13. 3.4 is ticked there.
- **3.2 / 3.3** keep their titles, but the mechanics change: upload plus a chat file message, and resolve through chat or description links instead of a manifest.

| `tasks.md` item | Plan task(s) | Ticked in |
|---|---|---|
| 1.1 Rebase onto master, lease sub-schema unchanged | Task 1 (already satisfied: branch is on master `69c5b89`) | Task 1 |
| 1.2 Re-check ADDED vs MODIFIED | Task 1 (already decided in Design: stays `ADDED`, Design §7) | Task 1 |
| 2.1 `depends_on` in `apiData` | Tasks 2 (namespace), 4 (storage) | Task 4 |
| 2.2 `LinkDependsOn` | Task 5 | Task 5 |
| 2.3 claim gate consults `UnmetDependencies` | Task 6 | Task 6 |
| 3.1 manifest → *reframed: discovery* | Tasks 7, 8 | Task 8 |
| 3.2 `AddAttachment` | Task 9 | Task 9 |
| 3.3 `GetAttachment` | Tasks 10, 11 | Task 11 |
| 3.4 URL stability → *reframed: live round-trip* | Task 13 | Task 13 |
| 4.1 `var _ tracker.Tracker` compiles | Task 12 | Task 12 |
| 4.2 unit tests against a fake | Tasks 2–11 | Task 11 |
| 4.3 live smoke: dependency and attachment | Task 13 | Task 13 |

Each task's commit ticks its `tasks.md` boxes (`- [ ]` → `- [x]`) **in the same commit**.

## Global Constraints

- Module `github.com/kao73/virtual-office`, Go `1.26.6`. **No new dependencies**, and `go.mod`/`go.sum` stay untouched.
- **Language:** Go code comments and error strings are in **Russian**, and identifiers are English (repo `CLAUDE.md`). This plan and commit messages are in English.
- **Commits:** English conventional commits (`feat(yougile): …`, `refactor(yougile): …`, `test(yougile): …`, `docs(…): …`). Every commit message ends with the trailer:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  ```
- **TDD:** write the failing test, run it and **see it red for the expected reason**, implement, and see it green. A test that pins existing behavior and passes at once is called out explicitly as such. For those, the mutation probe is the red.
- **Per-task check set** (run before every commit):
  ```bash
  go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
  ```
  At the end (Task 14), also run the full `go test ./...`.
- **Mutation probe for every new test.** Commit first. Then break the guarded line named in the task, run the named test, see it red, and **restore by re-editing the exact same lines back**. Do not use `git checkout`, `git restore` or `git stash`. Confirm with `git diff --quiet && echo clean`. A probe that stays green means the test does not guard what it claims to, so fix the test and amend the commit.
- **Unit tests never touch the network.** Everything runs against `httptest` servers on `127.0.0.1`. No unit test may name a real host that would be dialed, so `ru.yougile.com` may appear only as a string that is never fetched. The live test sits behind `//go:build yougile_live`.
- **Live tests run only against the `office-polygon` sandbox.** Never touch `Clens`, which is a live client board.
- The API key never appears in an error, log line or test failure message. **Downloads never carry `Authorization`** (Design §1: Go forwards it to subdomains on redirect).
- Rate limit is 50 requests per minute per company. Add no avoidable requests. `GetAttachment` costs exactly three requests (task, chat, file) plus redirect hops. No cache (Design §4.3).
- Contract errors are wrapped with `%w`: `tracker.ErrNotFound`, `tracker.ErrNotOwner`, `tracker.ErrClaimLost`, `tracker.ErrNoProject`, plus the new `ErrOfficeData`.
- **No edits** to `internal/pipeline/*`, `cmd/runner/*`, `office/*`. `internal/tracker/tracker.go` changes only in doc comments (Task 14).
- Exact strings from the Design Doc, verbatim:
  - namespace key `virtual_office`, schema `v: 1`;
  - skip log `"yougile: задача %s пропущена: %v"`;
  - dependency note `Зависит от: <idTaskProject> «<title>»` (with the task id when `idTaskProject` is empty);
  - file message text `/root/#file:<url>` (with `text` == `textHtml`);
  - comment body for a file message `[вложение: <name>]`;
  - uuid pattern `[0-9a-fA-F-]{36}`.

## Review Focus

The five inputs most likely to bite a real user that the Design Doc's test list does not name. Each has a test in the owning task.

1. **Description HTML that is not the one sample we saw.** Examples are anchor text wrapped in tags (`<a …><span>ТЗ</span>.pdf</a>`), `&amp;` in the `href` query, and plain-text descriptions with no HTML at all. The name must come out as `ТЗ.pdf`, the link must still be found, and plain text must simply yield no attachments. Pinned in Task 7 (`TestFileLinksFromDescriptionHTML`).
2. **A file name with a literal `%` or other characters that break a second decode** (e.g. a human's `100%.txt`, which appears as `100%25.txt` once-encoded). Decoding must stop at the first failure or no-op and not produce garbage or an error. Pinned in Task 7 (`TestDecodeNameStopsOnFailure`).
3. **A human deletes the chat message carrying a file.** The file must disappear from `Task.Attachments` and from `Comments`, as in the UI. Pinned in Task 7 (`TestFileLinksSkipDeletedMessages`) and Task 8 (existing `TestGetSkipsDeletedMessages` still holds).
4. **A card still holding change-1 top-level keys (`lease`, `labels`) on `office-polygon`.** It must read as free and unlabeled. Its legacy keys must be preserved on write, and `FindByMarker` must not match a legacy top-level label. Pinned in Task 2 (`TestLegacyTopLevelKeysAreForeign`).
5. **A path segment that would move the rebuilt download URL** (`.`/`..`, or `%2E%2E` that decodes to `..`). Such a link is not an attachment. It is never fetched and is not listed. Pinned in Task 7 (`TestUserDataLinkRejectsDotSegments`).

---

## File Structure

| File | Responsibility | Change |
|---|---|---|
| `internal/tracker/yougile/lease.go` | `ErrOfficeData`, `keyNamespace`, `schemaVersion`, `officeData` (wire), `apiData` (in-memory) codec, Claim/Renew/Release/SetHumanFlag/SetAttempts | Modify (Tasks 2, 4, 12) |
| `internal/tracker/yougile/yougile.go` | package doc, `Config`, `Tracker` (+`Logf`, `files`), `Open`, `call` → `send`, `upload`, `newFileClient`, `fileHostAllowed` | Modify (Tasks 3, 9, 10, 12) |
| `internal/tracker/yougile/status.go` | `collect` skips `ErrOfficeData` cards through `Logf` | Modify (Task 3) |
| `internal/tracker/yougile/task.go` | `taskDTO.IDTaskProject`, `toTask` fills `DependsOn`, `Get` fills `Attachments` | Modify (Tasks 4, 5, 8) |
| `internal/tracker/yougile/comment.go` | `chat` (raw messages), `comments(msgs)` with file rendering, `postChat`, `Comment` | Modify (Tasks 5, 8) |
| `internal/tracker/yougile/depends.go` | `LinkDependsOn`, `dependencyNote` | Create (Task 5) |
| `internal/tracker/yougile/attachment.go` | `fileLink`, `fileLinks`, `chatFileLink`, `descriptionLink`, `userDataLink`, `decodeName`, `attachmentRefs`, `AddAttachment`, `uploadedLink`, `GetAttachment`, `download` | Create (Tasks 7, 9, 11) |
| `internal/tracker/yougile/yougile_test.go` | fake: namespace helpers, `IDTaskProject`, upload route, `/user-data` redirect, storage server, `addFile` | Modify (Tasks 2, 5, 9, 11) |
| `internal/tracker/yougile/lease_test.go`, `task_test.go`, `comment_test.go`, `status_test.go` | fixture migration and new tests | Modify |
| `internal/tracker/yougile/depends_test.go` | `LinkDependsOn` and claim-gate tests | Create (Tasks 5, 6) |
| `internal/tracker/yougile/attachment_test.go` | parser, AddAttachment, file client, GetAttachment tests | Create (Tasks 7, 9–11) |
| `internal/tracker/yougile/contract_test.go` | ownership table (+LinkDependsOn, +AddAttachment). `coreTracker` removed | Modify (Tasks 5, 9, 12) |
| `internal/tracker/yougile/live_test.go` | `TestLiveDependenciesAndAttachments` | Modify (Task 13) |
| `internal/tracker/tracker.go` | doc comments on `Task.DependsOn` and `LinkDependsOn` name yougile | Modify, comments only (Task 14) |
| `docs/openspec/changes/yougile-dependencies-attachments/tasks.md` | checkboxes and reframing notes | Modify (every task) |

---

### Task 1: Check off the rebase items and record the reframing in tasks.md (tasks.md 1.1, 1.2)

- [x] Task 1 complete: Check off the rebase items and record the reframing in tasks.md (tasks.md 1.1, 1.2)

**Files:**
- Modify: `docs/openspec/changes/yougile-dependencies-attachments/tasks.md`

**Interfaces:** none.

- [x] **Step 1: Confirm 1.1 is really satisfied**

Run: `git merge-base --is-ancestor 69c5b89 HEAD && echo on-master && ls docs/openspec/changes/archive | grep yougile-adapter-core`
Expected: `on-master` and `2026-09-28-yougile-adapter-core`. If either is missing, stop and report. Do not rebase yourself.

- [x] **Step 2: Edit tasks.md**

Tick 1.1 and 1.2, and add a one-line reason under each. Under 2.1, 3.1 and 3.4, add an indented reframing note without ticking them. The exact text to insert:

Under `- [x] 1.1 …`:
```markdown
      _Done: branch sits on master `69c5b89` (after `yougile-adapter-core` archived); the lease schema is the one this change namespaces (Design Doc §2)._
```
Under `- [x] 1.2 …`:
```markdown
      _Done: stays `ADDED` — the new requirements add behavior and change no existing text (Design Doc §7)._
```
Under `- [ ] 2.1 …`:
```markdown
      _Reframed (Design Doc §2–3): `depends_on` is an array of task ids inside the single `apiData.virtual_office` object._
```
Under `- [ ] 3.1 …`:
```markdown
      _Reframed (Design Doc §7): no manifest. Attachments are discovered from chat file messages and description links (§4.1)._
```
Under `- [ ] 3.4 …`:
```markdown
      _Reframed (Design Doc §1, §7): stability already confirmed live (identical bytes immediately and after 20 s); the remaining check is the live byte round-trip._
```

- [x] **Step 3: Commit**

```bash
git add docs/openspec/changes/yougile-dependencies-attachments/tasks.md
git commit -m "docs(yougile-dependencies-attachments): check off rebase items, record design reframing

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Move office data in apiData under `virtual_office` (tasks.md 2.1, part 1)

- [x] Task 2 complete: Move office data in apiData under `virtual_office` (tasks.md 2.1, part 1)

**Files:**
- Modify: `internal/tracker/yougile/lease.go:1-90` (codec block)
- Modify: `internal/tracker/yougile/yougile_test.go` (`setLeaseOf`, new helpers)
- Modify: `internal/tracker/yougile/lease_test.go` (codec tests, `putLease`, fixtures)
- Modify: `internal/tracker/yougile/task_test.go:18-20,116,138`
- Modify: `internal/tracker/yougile/comment_test.go:83,95,132,134`

**Interfaces:**
- Consumes: existing `apiData` users (`toTask`, `mutateAPIData`, `Claim`, `CreateTask`, `FindByMarker`, `live_test.go` touching `data.extra`).
- Produces:
  - `var ErrOfficeData error`
  - `const keyNamespace = "virtual_office"`, `const schemaVersion = 1`
  - `type officeData struct{ V int; Lease *leaseData; Attempts int; HumanWait bool; Labels []string }` (wire form, JSON tags `v`, `lease`, `attempts`, `human_wait`, `labels`)
  - `apiData` keeps its fields `Lease`, `Attempts`, `HumanWait`, `Labels`, `extra`. `decodeAPIData(json.RawMessage) (apiData, error)` and `(apiData).encode() map[string]any` keep their signatures.
  - test helpers `officeAPIData(fields map[string]any) map[string]any` and `(*fakeYouGile).officeOf(id string) map[string]any`

- [x] **Step 1: Add the fake helpers and migrate `setLeaseOf`** (`yougile_test.go`)

Replace the body of `setLeaseOf` and add two helpers after it:

```go
// setLeaseOf — то же, с владельцем: прогон той же роли пишет то же имя роли.
func (f *fakeYouGile) setLeaseOf(id, owner, runID string, until time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	task := f.tasks[id]
	if task.APIData == nil {
		task.APIData = map[string]any{}
	}
	ns, _ := task.APIData[keyNamespace].(map[string]any)
	if ns == nil {
		ns = map[string]any{}
		task.APIData[keyNamespace] = ns
	}
	ns["lease"] = map[string]any{"owner": owner, "run_id": runID, "lease_until": until.Format(time.RFC3339Nano)}
}

// officeAPIData — apiData, в котором данные офиса лежат в своём пространстве имён.
func officeAPIData(fields map[string]any) map[string]any {
	return map[string]any{keyNamespace: fields}
}

// officeOf — данные офиса в apiData задачи фейка; nil, если их там нет.
func (f *fakeYouGile) officeOf(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	ns, _ := f.tasks[id].APIData[keyNamespace].(map[string]any)
	return ns
}
```

- [x] **Step 2: Rewrite the codec tests** (`lease_test.go`)

Replace `TestDecodeAPIDataReadsOwnKeys`, `TestAPIDataKeepsForeignKeys`, `TestEncodeAlwaysWritesOwnKeys` and `TestDecodeAPIDataRejectsGarbage` with the following. Keep `roundTrip` and `TestDecodeAPIDataEmptyIsZero` unchanged.

```go
func TestDecodeAPIDataReadsNamespace(t *testing.T) {
	until := time.Date(2026, 9, 28, 12, 30, 0, 123456789, time.UTC)
	raw := `{"virtual_office":{"v":1,"lease":{"owner":"implementer","run_id":"run-1","lease_until":"` +
		until.Format(time.RFC3339Nano) + `"},"attempts":2,"human_wait":true,"labels":["split:VO-1:a"]}}`
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
	if d.extra != nil {
		t.Errorf("своё пространство имён попало в чужие ключи: %#v", d.extra)
	}
}

// Версия не указана — это та же первая схема.
func TestDecodeAPIDataAcceptsMissingVersion(t *testing.T) {
	d, err := decodeAPIData(json.RawMessage(`{"virtual_office":{"attempts":3}}`))
	if err != nil || d.Attempts != 3 {
		t.Errorf("без v: %+v, %v", d, err)
	}
}

// Spec «Another integration's data survives office writes»: всё вне
// virtual_office переписывается как было.
func TestAPIDataKeepsForeignKeys(t *testing.T) {
	raw := `{"crm":{"deal":42,"tags":["a","b"]},"virtual_office":{"v":1,"attempts":1}}`
	d, err := decodeAPIData(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	d.Attempts = 5
	out := roundTrip(t, d)
	if !reflect.DeepEqual(out["crm"], map[string]any{"deal": float64(42), "tags": []any{"a", "b"}}) {
		t.Errorf("чужой ключ crm искажён: %#v", out["crm"])
	}
	ns, _ := out[keyNamespace].(map[string]any)
	if ns["attempts"] != float64(5) {
		t.Errorf("attempts = %#v", ns["attempts"])
	}
	if _, leaked := out["attempts"]; leaked {
		t.Error("attempts записан на верхний уровень, а не в virtual_office")
	}
}

// Review Focus #4: ключи change 1 на верхнем уровне (только office-polygon)
// — чужие: не читаются, не мигрируются, переживают запись.
func TestLegacyTopLevelKeysAreForeign(t *testing.T) {
	raw := `{"lease":{"owner":"x","run_id":"old","lease_until":"2099-01-01T00:00:00Z"},"attempts":7,"labels":["split:P:a"]}`
	d, err := decodeAPIData(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if d.Lease != nil || d.Attempts != 0 || d.Labels != nil {
		t.Errorf("верхний уровень прочитан как свой: %+v", d)
	}
	out := roundTrip(t, d)
	if out["attempts"] != float64(7) || out["lease"] == nil || !reflect.DeepEqual(out["labels"], []any{"split:P:a"}) {
		t.Errorf("наследие change 1 не сохранено: %#v", out)
	}
}

// Свои ключи пишутся всегда и явно: lease — null, списки — [], версия — 1.
// Иначе при слиянии apiData на сервере снятая аренда не снялась бы.
func TestEncodeWritesFullNamespace(t *testing.T) {
	out := roundTrip(t, apiData{})
	ns, ok := out[keyNamespace].(map[string]any)
	if !ok {
		t.Fatalf("virtual_office не записан: %#v", out)
	}
	for _, key := range []string{"v", "lease", "attempts", "human_wait", "labels"} {
		if _, ok := ns[key]; !ok {
			t.Errorf("ключ %q не записан: %#v", key, ns)
		}
	}
	if ns["v"] != float64(schemaVersion) {
		t.Errorf("v = %#v", ns["v"])
	}
	if ns["lease"] != nil {
		t.Errorf("свободная аренда записана как %#v, ожидался null", ns["lease"])
	}
	if !reflect.DeepEqual(ns["labels"], []any{}) {
		t.Errorf("пустые метки записаны как %#v, ожидался []", ns["labels"])
	}
	if len(out) != 1 {
		t.Errorf("на верхнем уровне лишнее: %#v", out)
	}
}

func TestDecodeAPIDataRejectsMalformedOfficeData(t *testing.T) {
	for _, raw := range []string{
		`"string"`, `[1,2]`,
		`{"virtual_office":"x"}`, `{"virtual_office":null}`, `{"virtual_office":[1]}`,
		`{"virtual_office":{"lease":"not an object"}}`, `{"virtual_office":{"attempts":"three"}}`,
		`{"virtual_office":{"v":"1"}}`, `{"virtual_office":{"v":-1}}`,
		`{"virtual_office":{"surprise":1}}`,
	} {
		if _, err := decodeAPIData(json.RawMessage(raw)); !errors.Is(err, ErrOfficeData) {
			t.Errorf("%s дал %v, ожидался ErrOfficeData", raw, err)
		}
	}
}

// Spec «Newer office data is not overwritten»: версия новее — отказ,
// названный версией. Поля при этом годные: отказ даёт именно версия.
func TestDecodeAPIDataRefusesNewerVersion(t *testing.T) {
	_, err := decodeAPIData(json.RawMessage(`{"virtual_office":{"v":2,"attempts":1}}`))
	if !errors.Is(err, ErrOfficeData) || !strings.Contains(err.Error(), "v=2") {
		t.Errorf("v=2 дал %v", err)
	}
}

func TestNewerOfficeDataIsNeverOverwritten(t *testing.T) {
	writes := map[string]func(*Tracker) error{
		"Claim":        func(tr *Tracker) error { return tr.Claim(claimReq("run-1")) },
		"Release":      func(tr *Tracker) error { return tr.Release(testKey, tracker.BySystem()) },
		"SetAttempts":  func(tr *Tracker) error { return tr.SetAttempts(testKey, tracker.BySystem(), 1) },
		"SetHumanFlag": func(tr *Tracker) error { return tr.SetHumanFlag(testKey, tracker.BySystem(), true) },
		"Transition":   func(tr *Tracker) error { return tr.Transition(testKey, tracker.BySystem(), "Review") },
		"Comment":      func(tr *Tracker) error { return tr.Comment(testKey, tracker.BySystem(), "x") },
	}
	for name, write := range writes {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			fake.tasks[testKey].APIData = officeAPIData(map[string]any{"v": 2, "attempts": 1})
			if err := write(tr); !errors.Is(err, ErrOfficeData) {
				t.Errorf("дало %v, ожидался ErrOfficeData", err)
			}
			if len(fake.puts)+len(fake.chatPosts) != 0 {
				t.Error("записано поверх новой схемы")
			}
		})
	}
}
```

Add `"strings"` is already imported in `lease_test.go`. Check that `"errors"` is too (it is).

- [x] **Step 3: Migrate the existing fixtures to the namespace**

These are exact replacements:

`lease_test.go` `putLease`:
```go
func putLease(body map[string]any) map[string]any {
	data, _ := body["apiData"].(map[string]any)
	ns, _ := data[keyNamespace].(map[string]any)
	lease, _ := ns["lease"].(map[string]any)
	return lease
}
```
`lease_test.go` `TestClaimKeepsForeignAPIDataAndCounters` body:
```go
	tr, fake := fixture(t)
	fake.tasks[testKey].APIData = map[string]any{"crm": map[string]any{"deal": 7},
		keyNamespace: map[string]any{"attempts": 2, "human_wait": true}}
	if err := tr.Claim(claimReq("run-1")); err != nil {
		t.Fatal(err)
	}
	ns := fake.officeOf(testKey)
	if fake.task(testKey).APIData["crm"] == nil || ns["attempts"] != float64(2) || ns["human_wait"] != true {
		t.Errorf("apiData после захвата: %#v", fake.task(testKey).APIData)
	}
```
`lease_test.go` `TestReleaseClearsLeaseKeepsStatusAndCounters`, the line that sets `APIData`:
```go
	fake.tasks[testKey].APIData = map[string]any{"crm": "keep", keyNamespace: map[string]any{"attempts": 2, "human_wait": true}}
```
`lease_test.go` `TestReleaseSendsExplicitNullLease`, the line `data := body["apiData"].(map[string]any)`:
```go
	data := body["apiData"].(map[string]any)[keyNamespace].(map[string]any)
```
`lease_test.go` `TestClaimFailsWhenColumnDidNotMove`:
```go
	if lease := fake.officeOf(testKey)["lease"]; lease != nil {
```
`task_test.go:18-21` (`TestGetMapsColumnAndAPIData`):
```go
	fake.tasks[testKey].APIData = officeAPIData(map[string]any{
		"lease":    map[string]any{"owner": "implementer", "run_id": "run-1", "lease_until": until.Format(time.RFC3339Nano)},
		"attempts": 2, "human_wait": true, "labels": []any{"m-1"},
	})
```
`task_test.go:116` (`TestGetMalformedAPIDataNamesTask`):
```go
	fake.tasks[testKey].APIData = officeAPIData(map[string]any{"lease": "garbage"})
	_, err := tr.Get(testKey)
	if !errors.Is(err, ErrOfficeData) || !strings.Contains(err.Error(), testKey) {
```
`task_test.go:138`:
```go
	labels := body["apiData"].(map[string]any)[keyNamespace].(map[string]any)["labels"]
```
`comment_test.go:83,95,132,134`: replace each `APIData: map[string]any{"labels": []any{…}}` with `APIData: officeAPIData(map[string]any{"labels": []any{…}})`, keeping the label values.

Add one test to `comment_test.go`, which pins Review Focus #4 for the marker lookup:
```go
// Метка change 1 на верхнем уровне apiData — чужая: по ней не находим.
func TestFindByMarkerIgnoresLegacyTopLevelLabels(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "legacy", ColumnID: colReady, Timestamp: now.UnixMilli(),
		APIData: map[string]any{"labels": []any{"split:P:a"}}})
	refs, err := tr.FindByMarker(testProject, "split:P:a")
	if err != nil || len(refs) != 0 {
		t.Errorf("FindByMarker = %v, %v", keys(refs), err)
	}
}
```

- [x] **Step 4: Run and see red**

Run: `go test ./internal/tracker/yougile/`
Expected: compile error (`undefined: keyNamespace`, `ErrOfficeData`, `schemaVersion`). This is the red.

- [x] **Step 5: Implement the namespaced codec** (`lease.go`)

Replace the block from the `// Ключи apiData, которыми владеет адаптер…` const block through the end of `encode()` with:

```go
// ErrOfficeData — данные офиса в apiData задачи не читаются: apiData не
// объект, virtual_office не объект, не те типы полей, незнакомое поле или
// версия схемы новее этой. Такую задачу адаптер не перезаписывает никогда.
// Листинги её пропускают с записью в Logf (status.go, collect), остальные
// пути падают громко (design doc §2).
var ErrOfficeData = errors.New("yougile: данные офиса в apiData не читаются")

// keyNamespace — единственный ключ верхнего уровня apiData, которым владеет
// офис. Всё остальное в apiData — чужое, в том числе ключи верхнего уровня,
// оставшиеся от change 1 на office-polygon: они не читаются и не мигрируются.
const keyNamespace = "virtual_office"

// schemaVersion — версия схемы virtual_office, которую понимает адаптер.
// Отсутствующая v читается как 1; большая — ErrOfficeData.
const schemaVersion = 1

// leaseData — аренда: пишется и снимается целиком.
type leaseData struct {
	Owner      string    `json:"owner"`
	RunID      string    `json:"run_id"`
	LeaseUntil time.Time `json:"lease_until"`
}

// officeData — virtual_office на проводе. Без omitempty: encode пишет все
// ключи явно — lease null'ом, списки пустыми, — чтобы слияние apiData на
// сервере, если оно там есть, не оставило старых значений.
type officeData struct {
	V         int        `json:"v"`
	Lease     *leaseData `json:"lease"`
	Attempts  int        `json:"attempts"`
	HumanWait bool       `json:"human_wait"`
	Labels    []string   `json:"labels"`
}

// apiData — наш взгляд на apiData задачи. attempts и human_wait живут вне
// lease: Release их не трогает, как jira не трогает attempts и метку человека.
//
// Инвариант каждой записи: apiData читается целиком, меняется и пишется
// целиком. extra держит все ключи верхнего уровня, кроме virtual_office, —
// без него запись молча стирала бы чужие данные.
type apiData struct {
	Lease     *leaseData
	Attempts  int
	HumanWait bool
	Labels    []string
	extra     map[string]json.RawMessage
}

// decodeAPIData разбирает apiData. Пусто, null или нет virtual_office —
// нулевое значение: задача, которую офис ни разу не трогал, свободна.
func decodeAPIData(raw json.RawMessage) (apiData, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return apiData{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return apiData{}, fmt.Errorf("%w: apiData не JSON-объект: %v", ErrOfficeData, err)
	}

	var d apiData
	if ns, ok := fields[keyNamespace]; ok {
		delete(fields, keyNamespace)
		od, err := decodeOffice(ns)
		if err != nil {
			return apiData{}, err
		}
		d = apiData{Lease: od.Lease, Attempts: od.Attempts, HumanWait: od.HumanWait, Labels: od.Labels}
	}
	if len(fields) > 0 {
		d.extra = fields
	}
	return d, nil
}

// decodeOffice разбирает virtual_office строго: только объект, только
// известные поля известных типов. Версию смотрим первой и отдельно — у
// новой схемы поля могут быть другими, и отказ должен назвать версию,
// а не чужое поле.
func decodeOffice(raw json.RawMessage) (officeData, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return officeData{}, fmt.Errorf("%w: %s — не JSON-объект", ErrOfficeData, keyNamespace)
	}
	var head struct {
		V int `json:"v"`
	}
	if err := json.Unmarshal(trimmed, &head); err != nil {
		return officeData{}, fmt.Errorf("%w: %s.v: %v", ErrOfficeData, keyNamespace, err)
	}
	switch {
	case head.V > schemaVersion:
		return officeData{}, fmt.Errorf("%w: %s.v=%d, адаптер знает только версию %d — не перезаписываем",
			ErrOfficeData, keyNamespace, head.V, schemaVersion)
	case head.V < 0:
		return officeData{}, fmt.Errorf("%w: %s.v=%d", ErrOfficeData, keyNamespace, head.V)
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var od officeData
	if err := dec.Decode(&od); err != nil {
		return officeData{}, fmt.Errorf("%w: %s: %v", ErrOfficeData, keyNamespace, err)
	}
	return od, nil
}

// encode — объект целиком для PUT: чужие ключи как были, virtual_office —
// весь и явно.
func (d apiData) encode() map[string]any {
	out := make(map[string]any, len(d.extra)+1)
	for key, value := range d.extra {
		out[key] = value
	}
	out[keyNamespace] = officeData{
		V: schemaVersion, Lease: d.Lease, Attempts: d.Attempts, HumanWait: d.HumanWait,
		Labels: orEmpty(d.Labels),
	}
	return out
}

// orEmpty — nil-срез как [], а не null: пустой список пишется списком.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
```

`toTask` (`task.go`) already wraps the decode error with `задача YouGile %s: %w`, and `FindByMarker` does the same, so `errors.Is(…, ErrOfficeData)` holds on both paths with the task id in the text.

- [x] **Step 6: Run and see green**

Run: `go test ./internal/tracker/yougile/`
Expected: PASS (all old and new tests).

- [x] **Step 7: Check set, then commit**

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
git add internal/tracker/yougile/
git commit -m "refactor(yougile): keep office data under apiData.virtual_office

Office-owned keys move into one namespaced object with a schema version.
Foreign and change-1 top-level keys are preserved verbatim; a newer or
malformed virtual_office is ErrOfficeData and is never overwritten.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [x] **Step 8: Mutation probes** (restore each by re-editing, then `git diff --quiet && echo clean`)
  - In `decodeOffice`, change `head.V > schemaVersion` to `head.V > schemaVersion+1`. Expect `TestDecodeAPIDataRefusesNewerVersion` and `TestNewerOfficeDataIsNeverOverwritten` red.
  - Delete `dec.DisallowUnknownFields()`. Expect `TestDecodeAPIDataRejectsMalformedOfficeData` red on `surprise`.
  - In `encode`, change `out[keyNamespace] =` to `out["office"] =`. Expect `TestEncodeWritesFullNamespace` and several Claim/Release tests red.
  - In `decodeAPIData`, delete `delete(fields, keyNamespace)`. Expect `TestDecodeAPIDataReadsNamespace` red (extra non-nil).
  - In `decodeOffice`, change `trimmed[0] != '{'` to `false`. Expect the `virtual_office":null` case red.

---

### Task 3: `Logf` hook, and listings skip unreadable cards (tasks.md: none, Design §2)

- [x] Task 3 complete: `Logf` hook, and listings skip unreadable cards (tasks.md: none, Design §2)

**Files:**
- Modify: `internal/tracker/yougile/yougile.go` (`Tracker` struct, `Open`, imports)
- Modify: `internal/tracker/yougile/status.go:113-137` (`collect`)
- Test: `internal/tracker/yougile/status_test.go`, `task_test.go`, `comment_test.go`

**Interfaces:**
- Consumes: `ErrOfficeData` (Task 2).
- Produces: `Tracker.Logf func(format string, args ...any)`, defaulting to `log.Printf`. `yougile-wiring-and-docs` will point it at the runner logger.

- [x] **Step 1: Write the failing tests**

`status_test.go`, add at the end:
```go
// logInto — Logf, который копит строки для проверки.
func logInto(tr *Tracker) *[]string {
	var lines []string
	tr.Logf = func(format string, args ...any) { lines = append(lines, fmt.Sprintf(format, args...)) }
	return &lines
}

// Spec «One malformed task does not stop the queue»: листинги идут дальше,
// пропущенная карточка — в Logf, а не молча.
func TestListingsSkipCardWithUnreadableOfficeData(t *testing.T) {
	for name, bad := range map[string]map[string]any{
		"битые данные": officeAPIData(map[string]any{"attempts": "three"}),
		"новая версия": officeAPIData(map[string]any{"v": 2}),
	} {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			lines := logInto(tr)
			fake.addTask(&fakeTask{ID: "broken", ColumnID: colReady, Timestamp: now.UnixMilli(), APIData: bad})
			fake.setLease(testKey, "run-dead", now.Add(-time.Minute))

			ready, err := tr.ListReady(testProject, "Ready")
			if err != nil || !slices.Equal(keys(ready), []string{testKey}) {
				t.Errorf("ListReady = %v, %v", keys(ready), err)
			}
			all, err := tr.List(testProject, []string{"Ready"})
			if err != nil || !slices.Equal(keys(all), []string{testKey}) {
				t.Errorf("List = %v, %v", keys(all), err)
			}
			expired, err := tr.ListExpired(testProject, now)
			if err != nil || !slices.Equal(keys(expired), []string{testKey}) {
				t.Errorf("ListExpired = %v, %v", keys(expired), err)
			}
			if len(*lines) != 3 {
				t.Fatalf("в Logf %d строк, ожидалось 3 (по одной на листинг): %q", len(*lines), *lines)
			}
			for _, line := range *lines {
				if !strings.HasPrefix(line, "yougile: задача broken пропущена: ") {
					t.Errorf("строка лога: %q", line)
				}
			}
		})
	}
}

// Карточка вне графа — не «данные офиса»: её листинг не глотает (сюда она
// попадает, только если сервер проигнорировал фильтр — tasksInColumn её
// отсеет раньше, так что проверяем, что skip узкий, на toTask напрямую).
func TestSkipIsOnlyForOfficeData(t *testing.T) {
	tr, _ := fixture(t)
	_, _, err := tr.toTask(taskDTO{ID: "x", ColumnID: colOutside})
	if errors.Is(err, ErrOfficeData) {
		t.Errorf("колонка вне графа выдана за ErrOfficeData: %v", err)
	}
}

func TestOpenDefaultsLogf(t *testing.T) {
	tr, _ := fixture(t)
	if tr.Logf == nil {
		t.Error("Logf по умолчанию не задан — пропуск карточки упал бы паникой")
	}
}
```
(`fmt`, `errors`, `strings`, `slices`, `time` are already imported in `status_test.go`.)

`comment_test.go`, add:
```go
// FindByMarker — источник идемпотентности детей split: пропусти он
// карточку, ensureChildren завёл бы дубль. Поэтому здесь — громко.
func TestFindByMarkerFailsLoudOnUnreadableOfficeData(t *testing.T) {
	tr, fake := fixture(t)
	fake.addTask(&fakeTask{ID: "broken", ColumnID: colReview, Timestamp: now.UnixMilli(),
		APIData: officeAPIData(map[string]any{"v": 2, "labels": []any{"split:P:a"}})})
	_, err := tr.FindByMarker(testProject, "split:P:a")
	if !errors.Is(err, ErrOfficeData) || !strings.Contains(err.Error(), "broken") {
		t.Errorf("FindByMarker дал %v", err)
	}
}
```

- [x] **Step 2: Run and see red**

Run: `go test ./internal/tracker/yougile/ -run 'TestListingsSkip|TestOpenDefaultsLogf|TestFindByMarkerFailsLoud|TestSkipIsOnly'`
Expected: compile error `tr.Logf undefined`. After Step 3's struct field alone (without the `collect` change), `TestListingsSkip…` fails with the ErrOfficeData error from ListReady. `TestFindByMarkerFailsLoudOnUnreadableOfficeData` and `TestSkipIsOnlyForOfficeData` pin existing behavior and pass once they compile.

- [x] **Step 3: Implement**

`yougile.go`: add `"log"` to imports. In `Tracker`, after `Now`:
```go
	// Logf — куда адаптер сообщает о том, что стерпел, а не вернул ошибкой:
	// листинги пропускают карточку с нечитаемыми данными офиса (status.go,
	// collect). По умолчанию log.Printf; yougile-wiring-and-docs направит
	// его в лог раннера.
	Logf func(format string, args ...any)
```
In `Open`'s `t := &Tracker{…}` literal add `Logf: log.Printf,`.

`status.go` `collect`, replace the loop body after `for _, raw := range raws {`:
```go
		task, _, err := t.toTask(raw)
		if errors.Is(err, ErrOfficeData) {
			// Одна карточка не должна останавливать очередь колонки и reaper.
			t.Logf("yougile: задача %s пропущена: %v", raw.ID, err)
			continue
		}
		if err != nil {
			return nil, err
		}
		if keep(task) {
			refs = append(refs, task.Ref())
		}
```
Update the `collect` doc comment by adding: `Карточка с нечитаемыми данными офиса (ErrOfficeData) пропускается с записью в Logf.`

- [x] **Step 4: Run and see green**

Run: `go test ./internal/tracker/yougile/`
Expected: PASS.

- [x] **Step 5: Check set, then commit**

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
git add internal/tracker/yougile/
git commit -m "feat(yougile): skip cards with unreadable office data in listings

ListReady/List/ListExpired report the card through the new Tracker.Logf
hook and continue; Get, mutators and FindByMarker still fail loudly.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [x] **Step 6: Mutation probes**
  - In `collect`, replace `errors.Is(err, ErrOfficeData)` with `false`. Expect `TestListingsSkipCardWithUnreadableOfficeData` red.
  - Remove the `t.Logf(…)` line and keep the `continue`. Expect the same test red on the line count.
  - In `Open`, delete `Logf: log.Printf,`. Expect `TestOpenDefaultsLogf` red.
  - In `FindByMarker`, temporarily change `return nil, fmt.Errorf("задача YouGile %s: %w", raw.ID, err)` to `continue`. Expect `TestFindByMarkerFailsLoudOnUnreadableOfficeData` red.

---

### Task 4: Store `depends_on` and surface it as `Task.DependsOn` (tasks.md 2.1)

- [x] Task 4 complete: Store `depends_on` and surface it as `Task.DependsOn` (tasks.md 2.1)

**Files:**
- Modify: `internal/tracker/yougile/lease.go` (`officeData`, `apiData`, `decodeAPIData`, `encode`)
- Modify: `internal/tracker/yougile/task.go:72-75` (`toTask`)
- Test: `internal/tracker/yougile/lease_test.go`, `status_test.go`

**Interfaces:**
- Consumes: Task 2 codec.
- Produces: `apiData.DependsOn []string`, wire key `virtual_office.depends_on` (always written, `[]` when empty). `toTask` sets `tracker.Task.DependsOn`, so it also reaches `TaskRef.DependsOn` through `Ref()`.

- [x] **Step 1: Write the failing tests**

`lease_test.go`: in `TestEncodeWritesFullNamespace`, extend the key list to `{"v", "lease", "attempts", "human_wait", "labels", "depends_on"}` and add:
```go
	if !reflect.DeepEqual(ns["depends_on"], []any{}) {
		t.Errorf("пустые зависимости записаны как %#v, ожидался []", ns["depends_on"])
	}
```
Add:
```go
func TestDecodeAPIDataReadsDependsOn(t *testing.T) {
	d, err := decodeAPIData(json.RawMessage(`{"virtual_office":{"v":1,"depends_on":["task-a","task-b"]}}`))
	if err != nil || !reflect.DeepEqual(d.DependsOn, []string{"task-a", "task-b"}) {
		t.Errorf("depends_on: %+v, %v", d.DependsOn, err)
	}
	if out := roundTrip(t, d); !reflect.DeepEqual(out[keyNamespace].(map[string]any)["depends_on"], []any{"task-a", "task-b"}) {
		t.Errorf("depends_on не пережил запись: %#v", out)
	}
}
```
`status_test.go`, add:
```go
// Гейт очерёдности (pipeline.UnmetDependencies) и runner ls читают TaskRef
// из ListReady/List, а не Task из Get: зависимость обязана доехать до ref.
func TestDependsOnReachesRefsAndGet(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].APIData = officeAPIData(map[string]any{"depends_on": []any{"task-base"}})
	want := []string{"task-base"}

	ready, err := tr.ListReady(testProject, "Ready")
	if err != nil || len(ready) != 1 || !slices.Equal(ready[0].DependsOn, want) {
		t.Errorf("ListReady: %+v, %v", ready, err)
	}
	all, err := tr.List(testProject, []string{"Ready"})
	if err != nil || len(all) != 1 || !slices.Equal(all[0].DependsOn, want) {
		t.Errorf("List: %+v, %v", all, err)
	}
	task, err := tr.Get(testKey)
	if err != nil || !slices.Equal(task.DependsOn, want) {
		t.Errorf("Get: %+v, %v", task.DependsOn, err)
	}
}
```

- [x] **Step 2: Run and see red**

Run: `go test ./internal/tracker/yougile/ -run 'TestEncodeWritesFullNamespace|TestDecodeAPIDataReadsDependsOn|TestDependsOnReachesRefsAndGet'`
Expected: compile error `d.DependsOn undefined`.

- [x] **Step 3: Implement**

`lease.go`:
- `officeData`: add the field `DependsOn []string \`json:"depends_on"\`` after `Labels`.
- `apiData`: add `DependsOn []string` after `Labels`, with the comment `// DependsOn — id задач YouGile, от которых зависит эта (LinkDependsOn).`
- `decodeAPIData`: the assignment becomes
  ```go
		d = apiData{Lease: od.Lease, Attempts: od.Attempts, HumanWait: od.HumanWait, Labels: od.Labels, DependsOn: od.DependsOn}
  ```
- `encode`: add `DependsOn: orEmpty(d.DependsOn),` to the `officeData` literal.

`task.go` `toTask`, in the `tracker.Task{…}` literal add `DependsOn: slices.Clone(data.DependsOn),` after `Labels: …`.

- [x] **Step 4: Run and see green**

Run: `go test ./internal/tracker/yougile/`
Expected: PASS. `TestGetMapsColumnAndAPIData` still passes because `slices.Clone(nil)` is `nil`.

- [x] **Step 5: Tick tasks.md 2.1, run the check set, commit**

In `tasks.md`, change `- [ ] 2.1` to `- [x] 2.1`.
```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
git add internal/tracker/yougile/ docs/openspec/changes/yougile-dependencies-attachments/tasks.md
git commit -m "feat(yougile): store depends_on in virtual_office and expose it on refs

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [x] **Step 6: Mutation probes**
  - In `toTask`, delete `DependsOn: slices.Clone(data.DependsOn),`. Expect `TestDependsOnReachesRefsAndGet` red.
  - In `encode`, change `orEmpty(d.DependsOn)` to `d.DependsOn`. Expect `TestEncodeWritesFullNamespace` red (null instead of []).
  - In `decodeAPIData`, drop `DependsOn: od.DependsOn`. Expect `TestDecodeAPIDataReadsDependsOn` red.

---

### Task 5: `LinkDependsOn` with a visible chat note (tasks.md 2.2)

- [x] Task 5 complete: `LinkDependsOn` with a visible chat note (tasks.md 2.2)

**Files:**
- Create: `internal/tracker/yougile/depends.go`
- Modify: `internal/tracker/yougile/task.go` (`taskDTO.IDTaskProject`)
- Modify: `internal/tracker/yougile/comment.go` (`postChat`, `Comment` uses it)
- Modify: `internal/tracker/yougile/yougile_test.go` (`fakeTask.IDTaskProject` in `dto()`)
- Modify: `internal/tracker/yougile/contract_test.go` (ownership table)
- Test: `internal/tracker/yougile/depends_test.go` (create)

**Interfaces:**
- Consumes: `owned`, `getRaw`, `putTask`, `apiData.DependsOn` (Task 4).
- Produces:
  - `func (t *Tracker) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error`
  - `func (t *Tracker) postChat(key, text, textHTML string) error`. This posts a chat message without an ownership check (the caller checks). It is reused by Task 9.
  - `func dependencyNote(dep taskDTO) string`
  - `taskDTO.IDTaskProject string` (JSON `idTaskProject`)

- [x] **Step 1: Extend the fake** (`yougile_test.go`)

In `fakeTask`, add the field `IDTaskProject string` (on its own line after the `ID, Title…` line). In `dto()`, after building `m`:
```go
	if t.IDTaskProject != "" {
		m["idTaskProject"] = t.IDTaskProject
	}
```

- [x] **Step 2: Write the failing tests** (`depends_test.go`)

```go
package yougile

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/kao73/virtual-office/internal/tracker"
)

const depKey = "task-dep"

func withDependency(fake *fakeYouGile, idTaskProject string) {
	fake.addTask(&fakeTask{ID: depKey, Title: "Base", IDTaskProject: idTaskProject,
		ColumnID: colWork, Timestamp: now.Add(-2 * time.Hour).UnixMilli()})
}

func TestLinkDependsOnRecordsLinkAfterVisibleNote(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7")
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
		t.Fatal(err)
	}
	if got := fake.officeOf(testKey)["depends_on"]; !slices.Equal(anyStrings(got), []string{depKey}) {
		t.Errorf("depends_on = %#v", got)
	}
	// Spec «A declared dependency is visible to a human reading the task».
	if len(fake.chatPosts) != 1 || fake.chatPosts[0]["text"] != "Зависит от: ID-7 «Base»" {
		t.Errorf("заметка в чате: %#v", fake.chatPosts)
	}
	ready, _ := tr.ListReady(testProject, "Ready")
	if len(ready) != 1 || !slices.Equal(ready[0].DependsOn, []string{depKey}) {
		t.Errorf("ListReady не видит связь: %+v", ready)
	}
}

func TestLinkDependsOnNoteFallsBackToTaskID(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "")
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
		t.Fatal(err)
	}
	if fake.chatPosts[0]["text"] != "Зависит от: "+depKey+" «Base»" {
		t.Errorf("заметка: %#v", fake.chatPosts[0]["text"])
	}
}

// Как у mock: повтор для записанной пары — ничего не пишет, ни заметки, ни PUT.
func TestLinkDependsOnIsIdempotent(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7")
	for range 2 {
		if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
			t.Fatal(err)
		}
	}
	if len(fake.chatPosts) != 1 || len(fake.puts) != 1 {
		t.Errorf("повтор записал: заметок %d, PUT %d", len(fake.chatPosts), len(fake.puts))
	}
}

// Spec «A task cannot depend on itself»; пустой ключ — тоже отказ, и оба —
// до единого запроса.
func TestLinkDependsOnRejectsEmptyAndSelfWithoutRequests(t *testing.T) {
	for name, dep := range map[string]string{"пустой": "", "сама на себя": testKey} {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			before := len(fake.requests)
			if err := tr.LinkDependsOn(testKey, dep, tracker.BySystem()); err == nil {
				t.Error("принято")
			}
			if len(fake.requests) != before {
				t.Errorf("ушли запросы: %v", fake.requests[before:])
			}
		})
	}
}

func TestLinkDependsOnMissingDependencyIsNotFound(t *testing.T) {
	for name, prepare := range map[string]func(*fakeYouGile){
		"нет такой": func(*fakeYouGile) {},
		"удалена": func(f *fakeYouGile) {
			withDependency(f, "ID-7")
			f.tasks[depKey].Deleted = true
		},
	} {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			prepare(fake)
			if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); !errors.Is(err, tracker.ErrNotFound) {
				t.Errorf("дало %v", err)
			}
			if len(fake.chatPosts)+len(fake.puts) != 0 {
				t.Error("записано при пропавшей зависимости")
			}
		})
	}
}

// Зависимость может стоять вне колонок графа — это не ошибка (getRaw, не load).
func TestLinkDependsOnAcceptsDependencyOutsideGraph(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7")
	fake.tasks[depKey].ColumnID = colOutside
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
		t.Errorf("зависимость вне графа дала %v", err)
	}
}

// Заметка — до записи: linkChildren пропускает уже записанные id, так что
// сбой между ними после записи потерял бы заметку навсегда. Худший исход
// нашего порядка — видимый дубль заметки.
func TestLinkDependsOnPostsNoteBeforeWrite(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7")
	fake.fail["PUT /api-v2/tasks/"+testKey] = 500
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err == nil {
		t.Fatal("сбой записи не дошёл до вызывающего")
	}
	if len(fake.chatPosts) != 1 {
		t.Errorf("заметок %d, ожидалась одна — до записи", len(fake.chatPosts))
	}
	if fake.officeOf(testKey)["depends_on"] != nil {
		t.Error("depends_on записан, хотя PUT упал")
	}
}

func TestLinkDependsOnKeepsForeignKeys(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7")
	fake.tasks[testKey].APIData = map[string]any{"crm": "keep"}
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
		t.Fatal(err)
	}
	if fake.task(testKey).APIData["crm"] != "keep" {
		t.Error("чужой ключ потерян")
	}
}

// anyStrings — []any из JSON-ответа как []string.
func anyStrings(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}
```
`contract_test.go`: add to the `mutations` map
```go
		"LinkDependsOn": func(tr *Tracker, a tracker.Actor) error { return tr.LinkDependsOn(testKey, "task-dep", a) },
```
(No dependency task exists in that fixture. The ownership refusal must come first, or the test would see `ErrNotFound` instead of `ErrNotOwner`.)

- [x] **Step 3: Run and see red**

Run: `go test ./internal/tracker/yougile/`
Expected: compile error `tr.LinkDependsOn undefined`.

- [x] **Step 4: Implement**

`task.go` `taskDTO`: add after `Title`
```go
	IDTaskProject string `json:"idTaskProject"` // человекочитаемый номер задачи в проекте, «ID-7»
```
`comment.go`: replace the body of `Comment` and add `postChat` below it:
```go
func (t *Tracker) Comment(key string, by tracker.Actor, body string) error {
	if _, _, err := t.owned(key, by); err != nil {
		return err
	}
	return t.postChat(key, body, messageHTML(body))
}

// postChat — сообщение в чат задачи. Право на запись проверяет вызывающий.
func (t *Tracker) postChat(key, text, textHTML string) error {
	return t.call(http.MethodPost, "/chats/"+url.PathEscape(key)+"/messages", nil,
		map[string]any{"text": text, "textHtml": textHTML, "label": ""}, nil)
}
```
`depends.go`:
```go
package yougile

import (
	"errors"
	"fmt"
	"slices"

	"github.com/kao73/virtual-office/internal/tracker"
)

// LinkDependsOn записывает, что key зависит от dependsOnKey: id уходит
// в virtual_office.depends_on, откуда его читают toTask и гейт очерёдности
// (pipeline.UnmetDependencies). Своей связи «блокирует» у YouGile нет, а
// apiData в интерфейсе не видно, поэтому человеку связь показывает заметка
// в чате задачи (design doc §3).
//
// Идемпотентно, как у mock: уже записанная пара — nil без единой записи.
//
// Заметка уходит до записи id. pipeline.linkChildren пропускает id, уже
// видные в Get(key).DependsOn: сбой после записи и до заметки потерял бы
// заметку навсегда, а при нашем порядке худший исход — её видимый дубль.
func (t *Tracker) LinkDependsOn(key, dependsOnKey string, by tracker.Actor) error {
	switch {
	case dependsOnKey == "":
		return errors.New("yougile: зависимость без ключа задачи")
	case dependsOnKey == key:
		return fmt.Errorf("yougile: задача %s не может зависеть от самой себя", key)
	}
	_, data, err := t.owned(key, by)
	if err != nil {
		return err
	}
	if slices.Contains(data.DependsOn, dependsOnKey) {
		return nil
	}
	// getRaw, а не load: зависимость может стоять вне колонок графа, и это
	// не ошибка. Пропавшая или удалённая — ErrNotFound.
	dep, err := t.getRaw(dependsOnKey)
	if err != nil {
		return fmt.Errorf("зависимость %s задачи %s: %w", dependsOnKey, key, err)
	}
	note := dependencyNote(dep)
	if err := t.postChat(key, note, messageHTML(note)); err != nil {
		return err
	}
	data.DependsOn = append(data.DependsOn, dependsOnKey)
	return t.putTask(key, map[string]any{"apiData": data.encode()})
}

// dependencyNote — заметка о зависимости для человека: номер задачи в
// проекте, а без него — её id.
func dependencyNote(dep taskDTO) string {
	ref := dep.IDTaskProject
	if ref == "" {
		ref = dep.ID
	}
	return fmt.Sprintf("Зависит от: %s «%s»", ref, dep.Title)
}
```

- [x] **Step 5: Run and see green**

Run: `go test ./internal/tracker/yougile/`
Expected: PASS, including `TestEveryMutationFollowsOwnership/LinkDependsOn/*` and the existing Comment tests (unchanged behavior).

- [x] **Step 6: Tick tasks.md 2.2, run the check set, commit**

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
git add internal/tracker/yougile/ docs/openspec/changes/yougile-dependencies-attachments/tasks.md
git commit -m "feat(yougile): LinkDependsOn with a chat note posted before the write

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [x] **Step 7: Mutation probes**
  - Swap the note block and the `data.DependsOn = …; return t.putTask(…)` lines (write first, then note). Expect `TestLinkDependsOnPostsNoteBeforeWrite` red.
  - Delete the `slices.Contains` early return. Expect `TestLinkDependsOnIsIdempotent` red.
  - Delete the `case dependsOnKey == key:` arm. Expect `TestLinkDependsOnRejectsEmptyAndSelfWithoutRequests/сама_на_себя` red.
  - Replace `t.getRaw(dependsOnKey)` with `taskDTO{ID: dependsOnKey}, error(nil)` (two-value form). Expect `TestLinkDependsOnMissingDependencyIsNotFound` red.
  - Change `ref := dep.IDTaskProject` to `ref := dep.ID`. Expect `TestLinkDependsOnRecordsLinkAfterVisibleNote` red.
  - Move the `owned` call below `getRaw`. Expect `TestEveryMutationFollowsOwnership/LinkDependsOn/*` red.

---

### Task 6: Verify the claim gate reads the recorded link (tasks.md 2.3)

- [x] Task 6 complete: Verify the claim gate reads the recorded link (tasks.md 2.3)

`tasks.md` 2.3 says "verify, don't reimplement". `pipeline.UnmetDependencies` (`internal/pipeline/deps.go:54`) reads `TaskRef.DependsOn` from `ListReady`, and `projectByKey` reads it from `List`. No `pipeline` change is made. The test drives the real gate function over this adapter's refs, which pins the two spec scenarios "not claimable while open" and "claimable once resolved". `go list -deps ./internal/pipeline` contains no `yougile` package, so the test import creates no cycle.

**Files:**
- Test: `internal/tracker/yougile/depends_test.go`

**Interfaces:**
- Consumes: `LinkDependsOn` (Task 5), `pipeline.UnmetDependencies(ref tracker.TaskRef, byKey map[string]tracker.TaskRef, terminal func(string) bool) []tracker.TaskRef`.

- [x] **Step 1: Write the test** (append to `depends_test.go`, add import `"github.com/kao73/virtual-office/internal/pipeline"`)

```go
// Spec «A dependent task is not claimable while its dependency is open» и
// «…becomes claimable once its dependency resolves» — настоящим гейтом
// pipeline над ref'ами этого адаптера. Review здесь — терминальный статус.
func TestClaimGateBlocksUntilDependencyResolves(t *testing.T) {
	tr, fake := fixture(t)
	withDependency(fake, "ID-7") // стоит в InProgress
	if err := tr.LinkDependsOn(testKey, depKey, tracker.BySystem()); err != nil {
		t.Fatal(err)
	}
	terminal := func(status string) bool { return status == "Review" }
	unmet := func() []tracker.TaskRef {
		t.Helper()
		ready, err := tr.ListReady(testProject, "Ready")
		if err != nil || len(ready) != 1 {
			t.Fatalf("ListReady: %+v, %v", ready, err)
		}
		all, err := tr.List(testProject, []string{"Ready", "InProgress", "Review"})
		if err != nil {
			t.Fatal(err)
		}
		byKey := map[string]tracker.TaskRef{}
		for _, ref := range all {
			byKey[ref.Key] = ref
		}
		return pipeline.UnmetDependencies(ready[0], byKey, terminal)
	}

	if got := unmet(); len(got) != 1 || got[0].Key != depKey {
		t.Errorf("пока зависимость открыта, гейт видит %+v", got)
	}
	fake.tasks[depKey].ColumnID = colReview
	if got := unmet(); len(got) != 0 {
		t.Errorf("зависимость закрыта, а гейт держит: %+v", got)
	}
}
```

- [x] **Step 2: Run it**

Run: `go test ./internal/tracker/yougile/ -run TestClaimGateBlocksUntilDependencyResolves -v`
Expected: PASS at once. This pins behavior already delivered by Tasks 4–5, so the mutation probe is the red. If `go vet` or the build reports an import cycle, stop and report. Do not restructure packages.

- [x] **Step 3: Tick tasks.md 2.3, run the check set, commit**

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
git add internal/tracker/yougile/depends_test.go docs/openspec/changes/yougile-dependencies-attachments/tasks.md
git commit -m "test(yougile): pin the pipeline claim gate against recorded dependencies

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [x] **Step 4: Mutation probe**
  - In `toTask`, delete `DependsOn: slices.Clone(data.DependsOn),` again. Expect `TestClaimGateBlocksUntilDependencyResolves` red on the first assertion. Restore.

---

### Task 7: Attachment link parser (tasks.md 3.1, reframed, part 1)

- [x] Task 7 complete: Attachment link parser (tasks.md 3.1, reframed, part 1)

Pure functions with no HTTP. Design §4.1.

**Files:**
- Create: `internal/tracker/yougile/attachment.go`
- Test: `internal/tracker/yougile/attachment_test.go` (create)

**Interfaces:**
- Consumes: `messageDTO` (`comment.go`), `tracker.ValidAttachmentID`, `tracker.AttachmentRef`.
- Produces:
  - `type fileLink struct{ ID, Segment, Name string }`. `ID` is the uuid. `Segment` is the last path segment **exactly as found** (still percent-encoded, used to rebuild the URL). `Name` is the display name.
  - `const fileMessagePrefix = "/root/#file:"`
  - `func fileLinks(description string, msgs []messageDTO) []fileLink`: description links first, then chat oldest-first (the caller passes sorted messages), deduplicated by uuid with the first occurrence winning, and deleted messages skipped. Returns `nil` when there are none.
  - `func chatFileLink(text string) (fileLink, bool)`
  - `func descriptionLink(href, text string) (fileLink, bool)`
  - `func userDataLink(escapedPath string) (fileLink, bool)`: matches `^/user-data/<uuid>/<segment>$`, requires `ValidAttachmentID`, and rejects a segment that decodes to `""`, `.` or `..`
  - `func decodeName(segment string) string`: percent-decodes at most twice, stopping on failure or no-op
  - `func attachmentRefs(links []fileLink) []tracker.AttachmentRef` (nil when empty)

- [x] **Step 1: Write the failing tests** (`attachment_test.go`)

```go
package yougile

import (
	"reflect"
	"testing"

	"github.com/kao73/virtual-office/internal/tracker"
)

const (
	uuid1 = "11111111-2222-4333-8444-555555555555"
	uuid2 = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

// Файл, прикреплённый человеком в чат: имя закодировано дважды (design doc §1).
func TestChatFileLinkDecodesNameTwice(t *testing.T) {
	l, ok := chatFileLink("  /root/#file:/user-data/" + uuid1 + "/%25D0%25A2%25D0%2597.txt\n")
	want := fileLink{ID: uuid1, Segment: "%25D0%25A2%25D0%2597.txt", Name: "ТЗ.txt"}
	if !ok || l != want {
		t.Errorf("chatFileLink = %+v, %v; ожидалось %+v", l, ok, want)
	}
}

func TestChatFileLinkRejectsNonFileText(t *testing.T) {
	for _, text := range []string{
		"вот файл /root/#file:/user-data/" + uuid1 + "/a.txt",  // не с начала
		"/root/#file:/user-data/" + uuid1 + "/a/b.txt",          // лишний сегмент
		"/root/#file:/user-data/not-a-uuid/a.txt",                // не uuid
		"/root/#file:/user-data/" + uuid1 + "/a.txt?x=1",        // хвост
		"/root/#file:https://evil.example/user-data/" + uuid1 + "/a.txt",
	} {
		if l, ok := chatFileLink(text); ok {
			t.Errorf("%q принят как файл: %+v", text, l)
		}
	}
}

// Review Focus #2: буквальный «%» в имени — второе декодирование падает,
// останавливаемся на первом.
func TestDecodeNameStopsOnFailure(t *testing.T) {
	for segment, want := range map[string]string{
		"100%25.txt":             "100%.txt",
		"plain.txt":              "plain.txt",
		"%25D0%25A2.txt":         "Т.txt",
		"%252525.txt":            "%25.txt", // не больше двух раз
		"%D0%A2%D0%97%20v2.pdf": "ТЗ v2.pdf",
	} {
		if got := decodeName(segment); got != want {
			t.Errorf("decodeName(%q) = %q, ожидалось %q", segment, got, want)
		}
	}
}

// Review Focus #1: ссылка в описании — на ru.yougile.com, с previews[] и
// &amp; в запросе, текст ссылки бывает обёрнут в теги.
func TestFileLinksFromDescriptionHTML(t *testing.T) {
	description := `<p>Постановка:</p><p><a href="https://ru.yougile.com/user-data/` + uuid1 +
		`/%D0%A2%D0%97.pdf?previews[]=a&amp;previews[]=b" target="_blank"><span>ТЗ</span>.pdf</a></p>` +
		`<p><a href="https://ru.yougile.com/user-data/` + uuid2 + `/scheme.png"></a></p>` +
		`<p><a href="https://example.com/doc">не файл</a></p>`
	got := fileLinks(description, nil)
	want := []fileLink{
		{ID: uuid1, Segment: "%D0%A2%D0%97.pdf", Name: "ТЗ.pdf"},
		{ID: uuid2, Segment: "scheme.png", Name: "scheme.png"}, // пустой текст — имя из пути
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fileLinks =\n%+v\nожидалось\n%+v", got, want)
	}
}

func TestFileLinksPlainTextDescriptionHasNone(t *testing.T) {
	if got := fileLinks("просто текст /user-data/"+uuid1+"/a.txt без ссылки", nil); got != nil {
		t.Errorf("из простого текста: %+v", got)
	}
}

// Порядок: описание, потом чат от старых к новым; один uuid — одна ссылка,
// выигрывает первая.
func TestFileLinksOrderAndDedup(t *testing.T) {
	description := `<a href="https://ru.yougile.com/user-data/` + uuid1 + `/spec.pdf">Спека.pdf</a>`
	msgs := []messageDTO{
		{ID: 1, Text: "обычный текст"},
		{ID: 2, Text: "/root/#file:/user-data/" + uuid2 + "/b.txt"},
		{ID: 3, Text: "/root/#file:/user-data/" + uuid1 + "/spec.pdf"},
	}
	got := fileLinks(description, msgs)
	want := []fileLink{
		{ID: uuid1, Segment: "spec.pdf", Name: "Спека.pdf"},
		{ID: uuid2, Segment: "b.txt", Name: "b.txt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("fileLinks =\n%+v\nожидалось\n%+v", got, want)
	}
}

// Review Focus #3: человек удалил сообщение с файлом — файла у задачи нет.
func TestFileLinksSkipDeletedMessages(t *testing.T) {
	msgs := []messageDTO{{ID: 1, Text: "/root/#file:/user-data/" + uuid1 + "/a.txt", Deleted: true}}
	if got := fileLinks("", msgs); got != nil {
		t.Errorf("удалённое сообщение дало %+v", got)
	}
}

// Review Focus #5: сегмент, который сдвинул бы пересобранный URL, — не вложение.
func TestUserDataLinkRejectsDotSegments(t *testing.T) {
	for _, segment := range []string{".", "..", "%2E%2E", "%252E%252E"} {
		if l, ok := userDataLink("/user-data/" + uuid1 + "/" + segment); ok {
			t.Errorf("сегмент %q принят: %+v", segment, l)
		}
	}
}

func TestAttachmentRefs(t *testing.T) {
	if got := attachmentRefs(nil); got != nil {
		t.Errorf("пусто дало %+v", got)
	}
	got := attachmentRefs([]fileLink{{ID: uuid1, Segment: "a%20b.txt", Name: "a b.txt"}})
	if want := []tracker.AttachmentRef{{ID: uuid1, Name: "a b.txt"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("attachmentRefs = %+v", got)
	}
}
```

- [x] **Step 2: Run and see red**

Run: `go test ./internal/tracker/yougile/ -run 'FileLink|DecodeName|UserDataLink|AttachmentRefs'`
Expected: compile error `undefined: chatFileLink` (and the others).

- [x] **Step 3: Implement** (`attachment.go`)

```go
package yougile

import (
	"html"
	"net/url"
	"regexp"
	"strings"

	"github.com/kao73/virtual-office/internal/tracker"
)

// Вложения живут там, куда их кладёт интерфейс YouGile: сообщением-файлом
// в чате задачи и ссылкой в описании. Своего манифеста в apiData нет
// (design doc §4, §7): один механизм и для файлов человека, и для файлов
// офиса. id вложения — uuid из пути /user-data/<uuid>/<имя>.

// fileMessagePrefix — текст сообщения-файла в чате: /root/#file:<путь>.
const fileMessagePrefix = "/root/#file:"

var (
	// userDataPath — путь файла YouGile; сегмент имени берётся как есть,
	// закодированным.
	userDataPath = regexp.MustCompile(`^/user-data/([0-9a-fA-F-]{36})/([^/?#]+)$`)
	// anchorPattern — ссылка в HTML описания: href в двойных кавычках и текст.
	anchorPattern = regexp.MustCompile(`(?is)<a\b[^>]*?\bhref\s*=\s*"([^"]*)"[^>]*>(.*?)</a>`)
	// tagPattern — теги внутри текста ссылки.
	tagPattern = regexp.MustCompile(`(?s)<[^>]*>`)
)

// fileLink — найденная ссылка на файл задачи.
type fileLink struct {
	ID      string // uuid файла — он же id вложения
	Segment string // последний сегмент пути как найден, закодированный: из него пересобирается URL
	Name    string // имя для человека и для файла в рабочей папке агента
}

// fileLinks — ссылки на файлы задачи: сначала из описания, потом из чата
// (msgs — от старых к новым). Один uuid — одна ссылка, выигрывает первая.
func fileLinks(description string, msgs []messageDTO) []fileLink {
	var links []fileLink
	seen := map[string]bool{}
	add := func(l fileLink, ok bool) {
		if !ok || seen[l.ID] {
			return
		}
		seen[l.ID] = true
		links = append(links, l)
	}
	for _, m := range anchorPattern.FindAllStringSubmatch(description, -1) {
		add(descriptionLink(m[1], m[2]))
	}
	for _, m := range msgs {
		if m.Deleted {
			continue
		}
		add(chatFileLink(m.Text))
	}
	return links
}

// chatFileLink — сообщение-файл: весь текст (без пробелов по краям) —
// /root/#file:/user-data/<uuid>/<имя>.
func chatFileLink(text string) (fileLink, bool) {
	path, ok := strings.CutPrefix(strings.TrimSpace(text), fileMessagePrefix)
	if !ok {
		return fileLink{}, false
	}
	return userDataLink(path)
}

// descriptionLink — ссылка из описания на любом хосте; хост и запрос
// отбрасываются (скачивание всё равно идёт с BaseURL). Имя — текст ссылки,
// а пустой текст — имя из пути.
func descriptionLink(href, text string) (fileLink, bool) {
	u, err := url.Parse(html.UnescapeString(strings.TrimSpace(href)))
	if err != nil {
		return fileLink{}, false
	}
	l, ok := userDataLink(u.EscapedPath())
	if !ok {
		return fileLink{}, false
	}
	if name := strings.TrimSpace(html.UnescapeString(tagPattern.ReplaceAllString(text, ""))); name != "" {
		l.Name = name
	}
	return l, true
}

// userDataLink разбирает путь /user-data/<uuid>/<сегмент>. Сегмент, который
// раскодируется в пусто, «.» или «..», сдвинул бы пересобранный URL — это
// не вложение.
func userDataLink(escapedPath string) (fileLink, bool) {
	m := userDataPath.FindStringSubmatch(escapedPath)
	if m == nil || !tracker.ValidAttachmentID(m[1]) {
		return fileLink{}, false
	}
	name := decodeName(m[2])
	if name == "" || name == "." || name == ".." {
		return fileLink{}, false
	}
	return fileLink{ID: m[1], Segment: m[2], Name: name}, true
}

// decodeName раскодирует имя не больше двух раз: в чате YouGile кодирует
// его дважды, в описании — один раз. Остановка — на первой неудаче или
// когда декодирование ничего не меняет: буквальный «%» в имени иначе
// превратился бы в мусор.
func decodeName(segment string) string {
	name := segment
	for range 2 {
		next, err := url.PathUnescape(name)
		if err != nil || next == name {
			break
		}
		name = next
	}
	return name
}

// attachmentRefs — ссылки в модели раннера; nil, если вложений нет.
func attachmentRefs(links []fileLink) []tracker.AttachmentRef {
	var refs []tracker.AttachmentRef
	for _, l := range links {
		refs = append(refs, tracker.AttachmentRef{ID: l.ID, Name: l.Name})
	}
	return refs
}
```

- [x] **Step 4: Run and see green**

Run: `go test ./internal/tracker/yougile/`
Expected: PASS. If `TestDecodeNameStopsOnFailure` disagrees on `%252525.txt`, trace it by hand: one pass gives `%2525.txt` and the second gives `%25.txt`. The expected value is correct, so fix the code, not the test.

- [x] **Step 5: Check set, then commit**

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
git add internal/tracker/yougile/attachment.go internal/tracker/yougile/attachment_test.go
git commit -m "feat(yougile): parse attachment links from chat file messages and description

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [x] **Step 6: Mutation probes**
  - In `decodeName`, change `for range 2` to `for range 1`. Expect `TestChatFileLinkDecodesNameTwice` red.
  - Change `for range 2` to `for range 3`. Expect `TestDecodeNameStopsOnFailure` red (`%252525.txt`).
  - Delete `|| seen[l.ID]`. Expect `TestFileLinksOrderAndDedup` red.
  - Delete the `if m.Deleted { continue }` block. Expect `TestFileLinksSkipDeletedMessages` red.
  - Delete `|| name == ".."`. Expect `TestUserDataLinkRejectsDotSegments` red.
  - Replace `tagPattern.ReplaceAllString(text, "")` with `text`. Expect `TestFileLinksFromDescriptionHTML` red.
  - Replace `html.UnescapeString(strings.TrimSpace(href))` with `strings.TrimSpace(href)`. This must stay green, because `&amp;` sits only in the query. It is an expected survivor: note it in the task report and leave the code as is (the unescape is harmless and protects a path containing `&amp;`).

---

### Task 8: `Get` lists attachments and renders file messages (tasks.md 3.1, reframed)

- [x] Task 8 complete: `Get` lists attachments and renders file messages (tasks.md 3.1, reframed)

**Files:**
- Modify: `internal/tracker/yougile/comment.go` (`comments` → `chat` + `comments(msgs)`)
- Modify: `internal/tracker/yougile/task.go` (`Get`)
- Test: `internal/tracker/yougile/task_test.go`

**Interfaces:**
- Consumes: `fileLinks`, `chatFileLink`, `attachmentRefs` (Task 7).
- Produces:
  - `func (t *Tracker) chat(key string) ([]messageDTO, error)`: one listing of the chat (with `includeSystem=false`), sorted oldest first, deleted messages dropped. No author lookups. Reused by `GetAttachment` (Task 11).
  - `func (t *Tracker) comments(msgs []messageDTO) ([]tracker.Comment, error)`: the signature changes from `comments(key)`. A file message's `Body` becomes `[вложение: <name>]`.
  - `Get` fills `Task.Attachments`.

- [x] **Step 1: Write the failing tests** (`task_test.go`)

```go
// Файл, прикреплённый человеком в чат, и ссылка в описании — вложения
// задачи, по порядку: описание, потом чат.
func TestGetListsAttachmentsFromDescriptionAndChat(t *testing.T) {
	tr, fake := fixture(t)
	fake.tasks[testKey].Description = `<p><a href="https://ru.yougile.com/user-data/` + uuid1 +
		`/%D0%A2%D0%97.pdf?previews[]=x">ТЗ.pdf</a></p>`
	fake.messages[testKey] = []fakeMessage{
		{ID: 1000, From: humanUserID, Text: "/root/#file:/user-data/" + uuid2 + "/%25D1%2581%25D1%2585%25D0%25B5%25D0%25BC%25D0%25B0.png"},
	}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	want := []tracker.AttachmentRef{{ID: uuid1, Name: "ТЗ.pdf"}, {ID: uuid2, Name: "схема.png"}}
	if !reflect.DeepEqual(task.Attachments, want) {
		t.Errorf("Attachments = %+v, ожидалось %+v", task.Attachments, want)
	}
}

// Сообщение-файл остаётся в переписке — ответ файлом тоже ответ, — но
// телом «[вложение: имя]», а не сырой служебной строкой.
func TestGetRendersFileMessageAsAttachmentComment(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{
		{ID: 1000, From: officeUserID, Text: "[office run:r1 role:analyst]\n## Вопросы\n1. Где ТЗ?"},
		{ID: 2000, From: humanUserID, Text: "/root/#file:/user-data/" + uuid1 + "/%25D0%25A2%25D0%2597.txt"},
	}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if len(task.Comments) != 2 || task.Comments[1].Body != "[вложение: ТЗ.txt]" || task.Comments[1].Author != "human@example.com" {
		t.Fatalf("переписка: %+v", task.Comments)
	}
	reply, _, found := tracker.HumanReply(task.Comments, []string{"office@example.com"})
	if !found || reply.ID != "2000" {
		t.Errorf("ответ человека файлом не засчитан: %+v, %v", reply, found)
	}
}
```
(`uuid1`/`uuid2` come from `attachment_test.go`, which is the same package. If `tracker.HumanReply` does not recognize the `[office run:r1 role:analyst]` marker, use the marker line from `TestGetReadsCommentsOldestFirstWithAuthorEmails` or build one with `tracker.Marker{…}.String()`. Do not weaken the assertion.)

- [x] **Step 2: Run and see red**

Run: `go test ./internal/tracker/yougile/ -run 'TestGetListsAttachments|TestGetRendersFileMessage'`
Expected: FAIL. `Attachments = []` for the first test, and the raw `/root/#file:…` body for the second.

- [x] **Step 3: Implement**

`comment.go`: replace `comments(key)` with:
```go
// chat — сообщения чата задачи от старых к новым, без удалённых. Чат задачи
// в YouGile адресуется id самой задачи. Порядок сервера не обещан — сортируем
// сами. Авторов не разрешает: вложениям (GetAttachment) они не нужны.
//
// Системные сообщения (перенос карточки, смена исполнителя) — не реплики:
// попади они сюда, tracker.HumanReply принял бы их за ответ человека. API
// по умолчанию их не отдаёт; includeSystem=false — явно, чтобы не зависеть
// от умолчания.
func (t *Tracker) chat(key string) ([]messageDTO, error) {
	msgs, err := listAll[messageDTO](t, "/chats/"+url.PathEscape(key)+"/messages",
		url.Values{"includeSystem": {"false"}})
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(msgs, func(a, b messageDTO) int { return cmp.Compare(a.ID, b.ID) })
	live := msgs[:0]
	for _, m := range msgs {
		if !m.Deleted {
			live = append(live, m)
		}
	}
	return live, nil
}

// comments — переписка в модели раннера, с email'ами авторов. Сообщение-файл
// остаётся репликой (ответ человека файлом — тоже ответ), но телом
// «[вложение: имя]», а не служебной строкой /root/#file:….
func (t *Tracker) comments(msgs []messageDTO) ([]tracker.Comment, error) {
	comments := make([]tracker.Comment, 0, len(msgs))
	for _, m := range msgs {
		author, err := t.userEmail(m.FromUserID)
		if err != nil {
			return nil, err
		}
		body := m.Text
		if link, ok := chatFileLink(m.Text); ok {
			body = "[вложение: " + link.Name + "]"
		}
		ms := int64(m.ID)
		comments = append(comments, tracker.Comment{
			ID: strconv.FormatInt(ms, 10), Author: author, Created: time.UnixMilli(ms).UTC(), Body: body,
		})
	}
	return comments, nil
}
```
`task.go` `Get`:
```go
// Get — задача целиком: переписка чата и вложения из описания и чата.
func (t *Tracker) Get(key string) (tracker.Task, error) {
	raw, task, _, err := t.load(key)
	if err != nil {
		return tracker.Task{}, err
	}
	msgs, err := t.chat(key)
	if err != nil {
		return tracker.Task{}, err
	}
	comments, err := t.comments(msgs)
	if err != nil {
		return tracker.Task{}, err
	}
	task.Comments = comments
	task.Attachments = attachmentRefs(fileLinks(raw.Description, msgs))
	return task, nil
}
```
Run `grep -n "comments(" internal/tracker/yougile/*.go` to confirm that no other caller of the old `comments(key)` remains.

- [x] **Step 4: Run and see green**

Run: `go test ./internal/tracker/yougile/`
Expected: PASS. `TestGetMapsColumnAndAPIData` still passes, because a task with no links gets `Attachments == nil`.

- [x] **Step 5: Tick tasks.md 3.1 (keep its reframing note), run the check set, commit**

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
git add internal/tracker/yougile/ docs/openspec/changes/yougile-dependencies-attachments/tasks.md
git commit -m "feat(yougile): Get lists attachments and renders file messages as comments

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [x] **Step 6: Mutation probes**
  - In `Get`, delete the `task.Attachments = …` line. Expect `TestGetListsAttachmentsFromDescriptionAndChat` red.
  - Change `fileLinks(raw.Description, msgs)` to `fileLinks("", msgs)`. Expect the same test red.
  - In `comments`, delete the `if link, ok := chatFileLink…` block. Expect `TestGetRendersFileMessageAsAttachmentComment` red.
  - In `chat`, invert the filter (`if m.Deleted`). Expect `TestGetSkipsDeletedMessages` red.

---

### Task 9: `AddAttachment`: upload plus a chat file message (tasks.md 3.2)

- [x] Task 9 complete: `AddAttachment`: upload plus a chat file message (tasks.md 3.2)

**Files:**
- Modify: `internal/tracker/yougile/yougile.go` (`call` → `send`, new `upload`)
- Modify: `internal/tracker/yougile/attachment.go` (`AddAttachment`, `uploadedLink`)
- Modify: `internal/tracker/yougile/yougile_test.go` (upload route, `uploads`, `uploadURL`)
- Modify: `internal/tracker/yougile/contract_test.go` (ownership table)
- Test: `internal/tracker/yougile/attachment_test.go`

**Interfaces:**
- Consumes: `owned`, `postChat` (Task 5), `userDataLink`, `fileMessagePrefix` (Task 7).
- Produces:
  - `func (t *Tracker) send(method, path string, query url.Values, body io.Reader, contentType string, out any) error`. This is the old body of `call`, and `call` becomes a JSON wrapper over it.
  - `func (t *Tracker) upload(name string, data []byte) (string, error)`. It returns the upload `url`.
  - `func (t *Tracker) AddAttachment(key string, by tracker.Actor, name string, data []byte) (string, error)`
  - `func uploadedLink(raw string) (fileLink, bool)`
  - fake: `type fakeFile struct{ Name string; Data []byte }`, `fakeYouGile.uploads map[string]fakeFile`, `fakeYouGile.uploadURL func(id, name string) string`, `(*fakeYouGile).addFile(name string, data []byte) string`

- [x] **Step 1: Extend the fake** (`yougile_test.go`)

Add the imports `"io"`. Add the type and fields:
```go
// fakeFile — файл, загруженный через upload-file или прикреплённый «человеком».
type fakeFile struct {
	Name string
	Data []byte
}
```
In `fakeYouGile`, add:
```go
	uploads   map[string]fakeFile            // uuid → файл
	uploadURL func(id, name string) string   // nil — "/user-data/<uuid>/<имя>"; тест подменяет, чтобы испортить ответ
```
In `newFake`, add `uploads: map[string]fakeFile{},`. Add the helpers:
```go
// newFileID — uuid очередного файла; вызывается под f.mu.
func (f *fakeYouGile) newFileID() string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", len(f.uploads)+1)
}

// addFile — файл, прикреплённый человеком в интерфейсе, мимо адаптера.
func (f *fakeYouGile) addFile(name string, data []byte) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.newFileID()
	f.uploads[id] = fakeFile{Name: name, Data: data}
	return id
}
```
In `route`, before the `/chats/` case, add:
```go
	case method == http.MethodPost && path == "/upload-file":
		file, header, err := r.FormFile("file")
		if err != nil {
			f.t.Errorf("upload-file: нет поля file: %v", err)
			return http.StatusBadRequest, map[string]any{"error": "no file"}
		}
		defer file.Close()
		data, _ := io.ReadAll(file)
		id := f.newFileID()
		f.uploads[id] = fakeFile{Name: header.Filename, Data: data}
		u := "/user-data/" + id + "/" + url.PathEscape(header.Filename)
		if f.uploadURL != nil {
			u = f.uploadURL(id, header.Filename)
		}
		return http.StatusOK, map[string]any{"result": "ok", "url": u, "fullUrl": "https://ru.yougile.com" + u}
```

- [x] **Step 2: Write the failing tests** (`attachment_test.go`, add imports `"errors"`, `"strings"`, `"time"`, `"slices"` as needed)

```go
func TestAddAttachmentUploadsAndPostsFileMessage(t *testing.T) {
	tr, fake := fixture(t)
	fake.setLease(testKey, "run-1", now.Add(time.Minute))
	data := []byte("{\"children\":[]}\n")
	id, err := tr.AddAttachment(testKey, tracker.ByRun("run-1"), "split.json", data)
	if err != nil {
		t.Fatal(err)
	}
	file, ok := fake.uploads[id]
	if !ok || file.Name != "split.json" || string(file.Data) != string(data) {
		t.Errorf("загружено: %q → %+v", id, fake.uploads)
	}
	wantText := "/root/#file:/user-data/" + id + "/split.json"
	if len(fake.chatPosts) != 1 || fake.chatPosts[0]["text"] != wantText || fake.chatPosts[0]["textHtml"] != wantText {
		t.Errorf("сообщение-файл: %#v, ожидался text = textHtml = %q", fake.chatPosts, wantText)
	}
	task, err := tr.Get(testKey)
	if err != nil {
		t.Fatal(err)
	}
	if want := []tracker.AttachmentRef{{ID: id, Name: "split.json"}}; !reflect.DeepEqual(task.Attachments, want) {
		t.Errorf("Attachments = %+v", task.Attachments)
	}
	if last := task.Comments[len(task.Comments)-1]; last.Body != "[вложение: split.json]" || last.Author != "office@example.com" {
		t.Errorf("реплика офиса: %+v", last)
	}
}

// text и textHtml сообщения-файла — одна и та же строка, без HTML-экранирования:
// иначе «&» в имени стал бы «&amp;» и интерфейс не узнал бы файл.
func TestAddAttachmentTextEqualsHTMLForAmpersand(t *testing.T) {
	tr, fake := fixture(t)
	if _, err := tr.AddAttachment(testKey, tracker.BySystem(), "a&b.txt", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if len(fake.chatPosts) != 1 || fake.chatPosts[0]["text"] != fake.chatPosts[0]["textHtml"] {
		t.Errorf("text и textHtml разошлись: %#v", fake.chatPosts)
	}
}

func TestAddAttachmentKeepsNonASCIIName(t *testing.T) {
	tr, fake := fixture(t)
	id, err := tr.AddAttachment(testKey, tracker.BySystem(), "ТЗ v2.pdf", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	task, _ := tr.Get(testKey)
	if len(task.Attachments) != 1 || task.Attachments[0] != (tracker.AttachmentRef{ID: id, Name: "ТЗ v2.pdf"}) {
		t.Errorf("Attachments = %+v", task.Attachments)
	}
	if fake.uploads[id].Name != "ТЗ v2.pdf" {
		t.Errorf("имя в multipart: %q", fake.uploads[id].Name)
	}
}

// Файл загружен, но url не того вида — привязать нечего: ошибка, и в чат
// ничего не уходит (осиротевшая загрузка безвредна, design doc §4.2).
func TestAddAttachmentRejectsMalformedUploadAnswer(t *testing.T) {
	for name, u := range map[string]string{
		"не user-data": "/files/whatever.txt",
		"не uuid":      "/user-data/12345/a.txt",
		"пусто":        "",
	} {
		t.Run(name, func(t *testing.T) {
			tr, fake := fixture(t)
			fake.uploadURL = func(string, string) string { return u }
			if _, err := tr.AddAttachment(testKey, tracker.BySystem(), "a.txt", []byte("x")); err == nil {
				t.Error("кривой ответ upload-file принят")
			}
			if len(fake.chatPosts) != 0 {
				t.Errorf("в чат ушло: %#v", fake.chatPosts)
			}
		})
	}
}

func TestAddAttachmentUploadFailureNeverLeaksKey(t *testing.T) {
	tr, fake := fixture(t)
	fake.fail["POST /api-v2/upload-file"] = http.StatusInternalServerError
	_, err := tr.AddAttachment(testKey, tracker.BySystem(), "a.txt", []byte("x"))
	if err == nil || strings.Contains(err.Error(), "test-key") || !strings.Contains(err.Error(), "500") {
		t.Errorf("отказ загрузки дал %v", err)
	}
	if len(fake.chatPosts) != 0 {
		t.Error("сообщение ушло без файла")
	}
}
```
(Add `"net/http"` to the imports.) In `contract_test.go`, add to `mutations`:
```go
		"AddAttachment": func(tr *Tracker, a tracker.Actor) error {
			_, err := tr.AddAttachment(testKey, a, "x.txt", []byte("x"))
			return err
		},
```
In both `len(fake.puts)+len(fake.chatPosts) != 0` checks in that file, add `+len(fake.uploads)`.

- [x] **Step 3: Run and see red**

Run: `go test ./internal/tracker/yougile/`
Expected: compile error `tr.AddAttachment undefined`.

- [x] **Step 4: Implement**

`yougile.go`: add `"mime/multipart"` to the imports. Replace `call` with:
```go
// call выполняет один JSON-запрос к API — без повторов: повтор делает
// следующий тик раннера (design.md, Decisions).
func (t *Tracker) call(method, path string, query url.Values, in, out any) error {
	var body io.Reader
	contentType := ""
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("запрос не сериализован: %w", err)
		}
		body, contentType = bytes.NewReader(raw), "application/json"
	}
	return t.send(method, path, query, body, contentType, out)
}

// send — один запрос к API с готовым телом. Тело ответа при ошибке попадает
// в текст ошибки: YouGile объясняет отказ в нём.
func (t *Tracker) send(method, path string, query url.Values, body io.Reader, contentType string, out any) error {
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
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// … далее без изменений: от `resp, err := t.client.Do(req)` до `return nil`.
}
```
Move the rest of the old `call` body (from `resp, err := t.client.Do(req)` to the end) into `send` unchanged. Add below it:
```go
// upload — POST /upload-file: один файл multipart-полем file. Отдаёт url
// из ответа — /user-data/<uuid>/<имя>. Ключ и ошибки — как у call.
func (t *Tracker) upload(name string, data []byte) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		return "", fmt.Errorf("вложение %q не упаковано: %w", name, err)
	}
	if _, err := part.Write(data); err != nil {
		return "", fmt.Errorf("вложение %q не упаковано: %w", name, err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("вложение %q не упаковано: %w", name, err)
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := t.send(http.MethodPost, "/upload-file", nil, &buf, w.FormDataContentType(), &out); err != nil {
		return "", err
	}
	return out.URL, nil
}
```
`attachment.go`: add the imports `"fmt"`, and add:
```go
// AddAttachment загружает файл и прикрепляет его к задаче сообщением-файлом
// в чате — так же, как это делает интерфейс: человек видит файл, Get находит
// его среди вложений. Отдаёт uuid файла.
//
// Сбой между загрузкой и сообщением оставляет невидимую осиротевшую
// загрузку и возвращает ошибку; повтор загрузит заново (design doc §4.2).
func (t *Tracker) AddAttachment(key string, by tracker.Actor, name string, data []byte) (string, error) {
	if _, _, err := t.owned(key, by); err != nil {
		return "", err
	}
	uploaded, err := t.upload(name, data)
	if err != nil {
		return "", err
	}
	link, ok := uploadedLink(uploaded)
	if !ok {
		return "", fmt.Errorf("yougile: upload-file вернул url %q не вида /user-data/<uuid>/<имя> — файл загружен, но к задаче %s не привязан",
			uploaded, key)
	}
	text := fileMessagePrefix + "/user-data/" + link.ID + "/" + link.Segment
	if err := t.postChat(key, text, text); err != nil {
		return "", err
	}
	return link.ID, nil
}

// uploadedLink — ссылка из ответа upload-file (обычно путь без хоста).
func uploadedLink(raw string) (fileLink, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return fileLink{}, false
	}
	return userDataLink(u.EscapedPath())
}
```

- [x] **Step 5: Run and see green**

Run: `go test ./internal/tracker/yougile/`
Expected: PASS, including every earlier `call` test (`TestCallSendsBearerKey`, `TestCallErrorNeverLeaksAPIKey`, `TestCallRejectsEmptyBodyWhenAnswerExpected`, `TestCallReportsBrokenBody`) and `TestEveryMutationFollowsOwnership/AddAttachment/*`.

- [x] **Step 6: Tick tasks.md 3.2, run the check set, commit**

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
git add internal/tracker/yougile/ docs/openspec/changes/yougile-dependencies-attachments/tasks.md
git commit -m "feat(yougile): AddAttachment uploads the file and posts a chat file message

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [x] **Step 7: Mutation probes**
  - In `AddAttachment`, change `t.postChat(key, text, text)` to `t.postChat(key, text, messageHTML(text))`. Expect `TestAddAttachmentTextEqualsHTMLForAmpersand` red. (`url.PathEscape` leaves `&` as is, and `messageHTML` would turn it into `&amp;`.)
  - Delete the `if !ok { return … }` block, so `link` is zero. Expect `TestAddAttachmentRejectsMalformedUploadAnswer` red.
  - Move `owned` after `upload`. Expect `TestEveryMutationFollowsOwnership/AddAttachment/*` red (uploads > 0).
  - In `upload`, change the field name `"file"` to `"upload"`. Expect `TestAddAttachmentUploadsAndPostsFileMessage` red (the fake reports no field).

---

### Task 10: Download client with a narrow redirect policy (tasks.md 3.3, part 1)

- [x] Task 10 complete: Download client with a narrow redirect policy (tasks.md 3.3, part 1)

**Files:**
- Modify: `internal/tracker/yougile/yougile.go` (`Tracker.files`, `Open`, `newFileClient`, `fileHostAllowed`, `maxFileRedirects`)
- Test: `internal/tracker/yougile/attachment_test.go`

**Interfaces:**
- Produces:
  - `const maxFileRedirects = 5`
  - `func fileHostAllowed(base, target *url.URL) bool`: same scheme as `base`, and a hostname (without port, case-insensitive) equal to `base`'s or a subdomain of it
  - `func newFileClient(base *url.URL) *http.Client`: no default headers, `Timeout: 60 * time.Second`, and a `CheckRedirect` that refuses after `maxFileRedirects` hops or to a host not allowed by `fileHostAllowed`, deleting `Authorization` from every redirected request as a second guard
  - `Tracker.files *http.Client`, set in `Open`

- [x] **Step 1: Write the failing tests** (`attachment_test.go`, add imports `"net/http/httptest"`, `"net/url"`, `"sync/atomic"`)

```go
func TestFileHostAllowed(t *testing.T) {
	cases := []struct {
		base, target string
		want         bool
	}{
		{"https://yougile.com", "https://yougile.com/user-data/x", true},
		{"https://yougile.com", "https://prod-user-data.yougile.com/x", true},
		{"https://yougile.com", "https://PROD-USER-DATA.YouGile.com/x", true},
		{"https://yougile.com", "http://prod-user-data.yougile.com/x", false}, // схема не та
		{"https://yougile.com", "https://evilyougile.com/x", false},           // не поддомен
		{"https://yougile.com", "https://yougile.com.evil.io/x", false},
		{"https://yougile.com", "https://ru.yougile.co/x", false},
		{"http://127.0.0.1:1234", "http://127.0.0.1:9999/x", true}, // порт не важен — так ходят тесты
		{"http://127.0.0.1:1234", "http://localhost:1234/x", false},
	}
	for _, c := range cases {
		base, _ := url.Parse(c.base)
		target, _ := url.Parse(c.target)
		if got := fileHostAllowed(base, target); got != c.want {
			t.Errorf("fileHostAllowed(%s, %s) = %v", c.base, c.target, got)
		}
	}
}

// Цепочка перенаправлений ограничена: пять переходов — да, шестой — нет.
func TestFileClientCapsRedirects(t *testing.T) {
	var hits atomic.Int32
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	t.Cleanup(loop.Close)
	base, _ := url.Parse(loop.URL)
	_, err := newFileClient(base).Get(loop.URL + "/start")
	if err == nil || !strings.Contains(err.Error(), "перенаправлений") {
		t.Errorf("бесконечные перенаправления дали %v", err)
	}
	if got := hits.Load(); got != maxFileRedirects+1 {
		t.Errorf("запросов %d, ожидалось %d (первый и %d переходов)", got, maxFileRedirects+1, maxFileRedirects)
	}
}

func TestFileClientRefusesForeignRedirect(t *testing.T) {
	var foreignHits atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { foreignHits.Add(1) }))
	t.Cleanup(foreign.Close)
	foreignURL := strings.Replace(foreign.URL, "127.0.0.1", "localhost", 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreignURL+"/steal", http.StatusFound)
	}))
	t.Cleanup(origin.Close)
	base, _ := url.Parse(origin.URL)
	if _, err := newFileClient(base).Get(origin.URL + "/user-data/x"); err == nil {
		t.Error("перенаправление на чужой хост пройдено")
	}
	if foreignHits.Load() != 0 {
		t.Error("чужой хост получил запрос")
	}
}

func TestOpenBuildsFileClient(t *testing.T) {
	tr, _ := fixture(t)
	if tr.files == nil || tr.files == tr.client || tr.files.CheckRedirect == nil {
		t.Errorf("клиент файлов: %+v", tr.files)
	}
}
```

- [x] **Step 2: Run and see red**

Run: `go test ./internal/tracker/yougile/ -run 'FileHost|FileClient|OpenBuildsFileClient'`
Expected: compile error `undefined: fileHostAllowed`.

- [x] **Step 3: Implement** (`yougile.go`)

In `Tracker`, after `client *http.Client`:
```go
	// files — клиент для скачивания вложений: без ключа API и с узкой
	// политикой перенаправлений (newFileClient). Клиент API сюда не годится:
	// net/http переносит Authorization на перенаправление в поддомен, а
	// хранилище prod-user-data.yougile.com — поддомен yougile.com.
	files *http.Client
```
In `Open`, `base` is already parsed. Add `files: newFileClient(base),` to the `Tracker` literal. Add:
```go
// maxFileRedirects — сколько перенаправлений разрешено скачиванию файла:
// /user-data/… на хосте API отвечает одним 302 в хранилище, пять — с запасом.
const maxFileRedirects = 5

// newFileClient — клиент скачивания вложений. Заголовков по умолчанию нет,
// ключа API он не знает. Перенаправление — только на ту же схему и на хост
// BaseURL или его поддомен, не больше maxFileRedirects переходов.
func newFileClient(base *url.URL) *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxFileRedirects {
				return fmt.Errorf("больше %d перенаправлений", maxFileRedirects)
			}
			if !fileHostAllowed(base, req.URL) {
				return fmt.Errorf("перенаправление на %s://%s не разрешено: файлы берём только с %s и его поддоменов",
					req.URL.Scheme, req.URL.Host, base.Hostname())
			}
			req.Header.Del("Authorization") // второй заслон: его и так никто не ставит
			return nil
		},
	}
}

// fileHostAllowed — та же схема, что у BaseURL, и хост BaseURL или его
// поддомен; порт не сравнивается.
func fileHostAllowed(base, target *url.URL) bool {
	if !strings.EqualFold(target.Scheme, base.Scheme) {
		return false
	}
	host, root := strings.ToLower(target.Hostname()), strings.ToLower(base.Hostname())
	return host == root || strings.HasSuffix(host, "."+root)
}
```

- [x] **Step 4: Run and see green**

Run: `go test ./internal/tracker/yougile/`
Expected: PASS.

- [x] **Step 5: Check set, then commit**

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
git add internal/tracker/yougile/
git commit -m "feat(yougile): keyless file client with a same-host redirect policy

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [x] **Step 6: Mutation probes**
  - Change `len(via) > maxFileRedirects` to `>=`. Expect `TestFileClientCapsRedirects` red (5 hits).
  - Replace `strings.HasSuffix(host, "."+root)` with `strings.HasSuffix(host, root)`. Expect `TestFileHostAllowed` red (`evilyougile.com`).
  - Delete the scheme check. Expect `TestFileHostAllowed` red (`http://prod-user-data…`).
  - Replace `if !fileHostAllowed(base, req.URL)` with `if false`. Expect `TestFileClientRefusesForeignRedirect` red.
  - Change `files: newFileClient(base),` to `files: &http.Client{},`. Expect `TestOpenBuildsFileClient` red.

---

### Task 11: `GetAttachment` (tasks.md 3.3, 4.2)

- [x] Task 11 complete: `GetAttachment` (tasks.md 3.3, 4.2)

**Files:**
- Modify: `internal/tracker/yougile/attachment.go` (`GetAttachment`, `download`)
- Modify: `internal/tracker/yougile/yougile_test.go` (`/user-data` redirect on the API fake, storage server, `fileAuth`, `redirect`)
- Test: `internal/tracker/yougile/attachment_test.go`

**Interfaces:**
- Consumes: `getRaw`, `chat` (Task 8), `fileLinks` (Task 7), `t.files` (Task 10), `snippet`.
- Produces:
  - `func (t *Tracker) GetAttachment(key, id string) ([]byte, error)`
  - `func (t *Tracker) download(link fileLink) ([]byte, error)`
  - fake: `fakeYouGile.storage string` (storage root URL), `fakeYouGile.redirect string` (override for the 302 target), `fakeYouGile.fileAuth []string` (the `Authorization` of every `/user-data` and storage request), `(*fakeYouGile).serveStorage(http.ResponseWriter, *http.Request)`, `serveStorage(t, fake) string`

- [x] **Step 1: Extend the fake** (`yougile_test.go`)

Fields in `fakeYouGile`:
```go
	storage  string   // корень фейкового хранилища (prod-user-data.yougile.com в жизни)
	redirect string   // не пусто — куда /user-data/… отправляет вместо хранилища
	fileAuth []string // Authorization каждого запроса к /user-data/… и к хранилищу
```
In `ServeHTTP`, right after the `fail` check block and before `path := strings.TrimPrefix(…)`:
```go
	// Файл: хост API отвечает 302 в отдельное хранилище, как YouGile (design doc §1).
	if strings.HasPrefix(r.URL.Path, "/user-data/") {
		f.fileAuth = append(f.fileAuth, r.Header.Get("Authorization"))
		target := f.redirect
		if target == "" {
			target = f.storage + strings.TrimPrefix(r.URL.EscapedPath(), "/user-data")
		}
		f.mu.Unlock()
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
```
Storage handler and server:
```go
// serveStorage — хранилище файлов: отдаёт байты по uuid из первого сегмента пути.
func (f *fakeYouGile) serveStorage(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.fileAuth = append(f.fileAuth, r.Header.Get("Authorization"))
	id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	file, ok := f.uploads[id]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(file.Data)
}

func serveStorage(t *testing.T, fake *fakeYouGile) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(fake.serveStorage))
	t.Cleanup(server.Close)
	return server.URL
}
```
In `fixture`, after `fake := newFake(t)`, add `fake.storage = serveStorage(t, fake)`.

- [x] **Step 2: Write the failing tests** (`attachment_test.go`)

```go
// Spec «An added attachment round-trips» — байт в байт, включая нули и не-UTF-8.
func TestAttachmentRoundTripsBytes(t *testing.T) {
	tr, _ := fixture(t)
	data := []byte{0, 1, 2, 0xff, '\n', 'x'}
	id, err := tr.AddAttachment(testKey, tracker.BySystem(), "blob.bin", data)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tr.GetAttachment(testKey, id)
	if err != nil || string(got) != string(data) {
		t.Errorf("GetAttachment = %v, %v; ожидалось %v", got, err, data)
	}
}

// Spec «Retrieving an attachment does not expose the tracker credentials»:
// ни хост API на /user-data, ни хранилище не видят Authorization.
func TestGetAttachmentSendsNoCredentials(t *testing.T) {
	tr, fake := fixture(t)
	id := fake.addFile("a.txt", []byte("secret-free"))
	fake.messages[testKey] = []fakeMessage{{ID: 1000, From: humanUserID, Text: "/root/#file:/user-data/" + id + "/a.txt"}}
	if _, err := tr.GetAttachment(testKey, id); err != nil {
		t.Fatal(err)
	}
	if len(fake.fileAuth) != 2 {
		t.Fatalf("запросов за файлом %d, ожидалось 2 (хост API и хранилище)", len(fake.fileAuth))
	}
	for i, auth := range fake.fileAuth {
		if auth != "" {
			t.Errorf("запрос %d за файлом нёс Authorization %q", i, auth)
		}
	}
}

// Spec «A file a person attached is among the task's attachments»: чат,
// имя закодировано дважды.
func TestGetAttachmentOfHumanChatFile(t *testing.T) {
	tr, fake := fixture(t)
	id := fake.addFile("ТЗ.txt", []byte("постановка"))
	fake.messages[testKey] = []fakeMessage{
		{ID: 1000, From: humanUserID, Text: "/root/#file:/user-data/" + id + "/%25D0%25A2%25D0%2597.txt"},
	}
	got, err := tr.GetAttachment(testKey, id)
	if err != nil || string(got) != "постановка" {
		t.Errorf("GetAttachment = %q, %v", got, err)
	}
}

// URL пересобирается на BaseURL: хост из ссылки в описании не используется
// никогда — адаптер нельзя направить на произвольный хост.
func TestGetAttachmentRebuildsURLOnBaseURL(t *testing.T) {
	tr, fake := fixture(t)
	var decoyHits atomic.Int32
	decoy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		decoyHits.Add(1)
		_, _ = w.Write([]byte("decoy"))
	}))
	t.Cleanup(decoy.Close)
	id := fake.addFile("ТЗ.pdf", []byte("real"))
	fake.tasks[testKey].Description = `<p><a href="` + decoy.URL + `/user-data/` + id +
		`/%D0%A2%D0%97.pdf?previews[]=x">ТЗ.pdf</a></p>`
	got, err := tr.GetAttachment(testKey, id)
	if err != nil || string(got) != "real" {
		t.Errorf("GetAttachment = %q, %v", got, err)
	}
	if decoyHits.Load() != 0 {
		t.Error("запрос ушёл на хост из описания")
	}
}

func TestGetAttachmentRefusesForeignRedirect(t *testing.T) {
	tr, fake := fixture(t)
	id := fake.addFile("a.txt", []byte("x"))
	fake.messages[testKey] = []fakeMessage{{ID: 1000, From: humanUserID, Text: "/root/#file:/user-data/" + id + "/a.txt"}}
	fake.redirect = strings.Replace(fake.storage, "127.0.0.1", "localhost", 1) + "/" + id + "/a.txt"
	if _, err := tr.GetAttachment(testKey, id); err == nil || errors.Is(err, tracker.ErrNotFound) {
		t.Errorf("перенаправление на чужой хост дало %v", err)
	}
	if len(fake.fileAuth) != 1 {
		t.Errorf("хранилище на чужом хосте получило запрос: %d запросов", len(fake.fileAuth))
	}
}

// Spec «An unknown attachment id is rejected»: id, не упомянутый у задачи, —
// ErrNotFound, и за файлом никто не ходит.
func TestGetAttachmentUnknownIDIsNotFound(t *testing.T) {
	tr, fake := fixture(t)
	other := fake.addFile("b.txt", []byte("чужое"))
	if _, err := tr.GetAttachment(testKey, other); !errors.Is(err, tracker.ErrNotFound) {
		t.Errorf("неупомянутый id дал %v", err)
	}
	if fake.count("GET /user-data/") != 0 {
		t.Error("за неупомянутым файлом ходили")
	}
}

func TestGetAttachmentInvalidIDMakesNoRequest(t *testing.T) {
	tr, fake := fixture(t)
	before := len(fake.requests)
	if _, err := tr.GetAttachment(testKey, "../../etc/passwd"); !errors.Is(err, tracker.ErrNotFound) {
		t.Errorf("негодный id дал %v", err)
	}
	if len(fake.requests) != before {
		t.Errorf("ушли запросы: %v", fake.requests[before:])
	}
}

// Ссылка есть, файла в хранилище нет — ErrNotFound; прочие коды — ошибка с кодом.
func TestGetAttachmentStorageErrors(t *testing.T) {
	tr, fake := fixture(t)
	fake.messages[testKey] = []fakeMessage{{ID: 1000, From: humanUserID, Text: "/root/#file:/user-data/" + uuid1 + "/gone.txt"}}
	if _, err := tr.GetAttachment(testKey, uuid1); !errors.Is(err, tracker.ErrNotFound) {
		t.Errorf("пропавший файл дал %v", err)
	}
	fake.fail["GET /user-data/"+uuid1+"/gone.txt"] = http.StatusInternalServerError
	_, err := tr.GetAttachment(testKey, uuid1)
	if err == nil || errors.Is(err, tracker.ErrNotFound) || !strings.Contains(err.Error(), "500") {
		t.Errorf("500 хранилища дал %v", err)
	}
}
```

- [x] **Step 3: Run and see red**

Run: `go test ./internal/tracker/yougile/`
Expected: compile error `tr.GetAttachment undefined`.

- [x] **Step 4: Implement** (`attachment.go`, add the imports `"io"`, `"net/http"`)

```go
// GetAttachment читает вложение по uuid. Ссылку ищет там же, где Get (описание
// и чат), URL пересобирает на BaseURL и скачивает клиентом без ключа API
// (design doc §4.3). Три запроса — задача, чат, файл; кэша нет намеренно.
func (t *Tracker) GetAttachment(key, id string) ([]byte, error) {
	if !tracker.ValidAttachmentID(id) {
		return nil, fmt.Errorf("%w: вложение %s/%s", tracker.ErrNotFound, key, id)
	}
	raw, err := t.getRaw(key)
	if err != nil {
		return nil, err
	}
	msgs, err := t.chat(key)
	if err != nil {
		return nil, err
	}
	for _, link := range fileLinks(raw.Description, msgs) {
		if link.ID == id {
			return t.download(link)
		}
	}
	return nil, fmt.Errorf("%w: вложение %s/%s не упомянуто ни в описании, ни в чате задачи",
		tracker.ErrNotFound, key, id)
}

// download — GET файла по пути, пересобранному на BaseURL: хост из ссылки
// в описании не используется никогда. Хост API отвечает 302 в хранилище,
// клиент files идёт туда без Authorization.
func (t *Tracker) download(link fileLink) ([]byte, error) {
	target := t.cfg.BaseURL + "/user-data/" + link.ID + "/" + link.Segment
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("вложение %s: запрос не собран: %w", link.ID, err)
	}
	resp, err := t.files.Do(req)
	if err != nil {
		return nil, fmt.Errorf("вложение %s не скачано: %w", link.ID, err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%w: вложение %s (404)", tracker.ErrNotFound, link.ID)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("вложение %s: %d: %s", link.ID, resp.StatusCode, snippet(body))
	case readErr != nil:
		return nil, fmt.Errorf("вложение %s: ответ не дочитан: %w", link.ID, readErr)
	}
	return body, nil
}
```

- [x] **Step 5: Run and see green**

Run: `go test ./internal/tracker/yougile/`
Expected: PASS. If `TestGetAttachmentStorageErrors`' 500 case gets a redirect instead, the `fail` check in `ServeHTTP` is running after the `/user-data` branch. It must run first, as specified in Step 1.

- [x] **Step 6: Tick tasks.md 3.3 and 4.2, run the check set, commit**

```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
git add internal/tracker/yougile/ docs/openspec/changes/yougile-dependencies-attachments/tasks.md
git commit -m "feat(yougile): GetAttachment rebuilds the URL on BaseURL and downloads without the key

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [x] **Step 7: Mutation probes**
  - In `download`, replace `t.files.Do(req)` with `t.client.Do(req)` and add `req.Header.Set("Authorization", "Bearer "+t.cfg.APIKey)` before it. Expect `TestGetAttachmentSendsNoCredentials` red.
  - In `download`, build the target from the description host. To simulate this, add a `Host string` field to `fileLink`, set it in `descriptionLink` from `u.Scheme + "://" + u.Host`, and in `download` use `link.Host` instead of `t.cfg.BaseURL` when it is non-empty. Expect `TestGetAttachmentRebuildsURLOnBaseURL` red (the decoy was hit). Restore all three edits.
  - Delete the `ValidAttachmentID` guard at the top of `GetAttachment`. Expect `TestGetAttachmentInvalidIDMakesNoRequest` red (requests were made).
  - Change `if link.ID == id` to `if true`. `TestGetAttachmentUnknownIDIsNotFound` has no links on the task, so it cannot go red on its own. **Before probing**, add a chat file message for `uuid1` to that test (`fake.messages[testKey] = []fakeMessage{{ID: 1000, From: humanUserID, Text: "/root/#file:/user-data/" + uuid1 + "/a.txt"}}`), commit it as `test(yougile): give the unknown-id test a decoy link`, then probe. Expect red (a download was attempted).
  - Change `resp.StatusCode == http.StatusNotFound` to `false`. Expect `TestGetAttachmentStorageErrors` red.

---

### Task 12: Interface completion and package doc (tasks.md 4.1)

- [x] Task 12 complete: Interface completion and package doc (tasks.md 4.1)

**Files:**
- Modify: `internal/tracker/yougile/yougile.go` (package doc, `var _ tracker.Tracker`)
- Modify: `internal/tracker/yougile/contract_test.go` (remove `coreTracker`)
- Modify: `internal/tracker/yougile/lease.go` (stale comment, if any remains)

**Interfaces:**
- Produces: `var _ tracker.Tracker = (*Tracker)(nil)`.

- [x] **Step 1: Remove `coreTracker` and add the full assertion**

In `contract_test.go`, delete the `coreTracker` interface, its doc comment and `var _ coreTracker = (*Tracker)(nil)`. Keep `TestEveryMutationFollowsOwnership`. In `yougile.go`, directly after the `Tracker` struct:
```go
// Tracker реализует контракт целиком.
var _ tracker.Tracker = (*Tracker)(nil)
```

- [x] **Step 2: Update the package doc** (`yougile.go:14-19`)

Replace the two paragraphs from `// Аренда, счётчик попыток…` through `// проверка \`var _ tracker.Tracker = (*Tracker)(nil)\`.` with:
```go
// Всё своё офис хранит в apiData задачи — свободном JSON-поле — под одним
// ключом virtual_office (lease.go): аренда, счётчик попыток, флаг «ждёт
// человека», метки и зависимости. Остальные ключи apiData — чужие и
// переписываются как были (docs/superpowers/specs/2026-09-29-yougile-dependencies-attachments-design.md, §2).
//
// Зависимость — id в virtual_office.depends_on плюс заметка в чате задачи:
// своей связи между задачами у YouGile нет (depends.go). Вложения живут
// там, куда их кладёт интерфейс, — сообщением-файлом в чате и ссылкой в
// описании; скачиваются клиентом без ключа API (attachment.go).
```
Run `grep -n "yougile-dependencies-attachments добавит\|манифест" internal/tracker/yougile/*.go`. Any hit is a stale forward reference, so rewrite or delete it.

- [x] **Step 3: Build and test**

Run: `go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/`
Expected: PASS.

- [x] **Step 4: Tick tasks.md 4.1, commit**

```bash
git add internal/tracker/yougile/ docs/openspec/changes/yougile-dependencies-attachments/tasks.md
git commit -m "feat(yougile): assert the full tracker.Tracker contract

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [x] **Step 5: Mutation probe**
  - Rename `func (t *Tracker) GetAttachment` to `getAttachment`. Expect `go build ./...` to fail on the `var _` line. Restore.

---

### Task 13: Live smoke test for dependency and attachment round-trip (tasks.md 4.3, 3.4)

- [x] Task 13 complete: Live smoke test for dependency and attachment round-trip (tasks.md 4.3, 3.4)

**Files:**
- Modify: `internal/tracker/yougile/live_test.go`

**Interfaces:**
- Consumes: `CreateTask`, `LinkDependsOn`, `ListReady`, `List`, `Transition`, `Get`, `AddAttachment`, `GetAttachment`, `putTask`, `pipeline.UnmetDependencies`.

- [x] **Step 1: Add the test** (append. Add the imports `"bytes"` and `"github.com/kao73/virtual-office/internal/pipeline"`. Update the header comment's request budget line.)

Header comment, replace the `// Запросов ~25 …` paragraph with:
```go
// Запросов ~25 на TestLiveLifecycle и ~30 на TestLiveDependenciesAndAttachments
// — под rate limit 50/мин на компанию только поодиночке: гоняй их -run по
// одному или с минутой между ними.
//
// Скачивание вложения уходит 302 на prod-user-data.yougile.com
// (111.88.104.34) — отдельный от API хост; на этой машине ему нужна своя
// запись split tunnel в AmneziaVPN, иначе round-trip упадёт по сети.
```
Test:
```go
func TestLiveDependenciesAndAttachments(t *testing.T) {
	tr := liveTracker(t)
	project := tr.cfg.ProjectID
	stamp := time.Now().UTC().Format("20060102T150405.000")

	create := func(summary string) tracker.TaskRef {
		t.Helper()
		ref, err := tr.CreateTask(project, tracker.TaskInput{Summary: summary + " " + stamp,
			Labels: []string{"office-live:" + summary + ":" + stamp}})
		if err != nil {
			t.Fatalf("CreateTask %s: %v", summary, err)
		}
		t.Cleanup(func() { _ = tr.putTask(ref.Key, map[string]any{"deleted": true}) })
		return ref
	}
	base, child := create("office live dep base"), create("office live dep child")

	// Связь: запись, повтор — no-op, видна в ref'ах ListReady и в чате.
	for range 2 {
		if err := tr.LinkDependsOn(child.Key, base.Key, tracker.BySystem()); err != nil {
			t.Fatalf("LinkDependsOn: %v", err)
		}
	}
	gate := func() []tracker.TaskRef {
		t.Helper()
		ready, err := tr.ListReady(project, "Ready")
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(ready, func(r tracker.TaskRef) bool { return r.Key == child.Key })
		if i < 0 || !slices.Equal(ready[i].DependsOn, []string{base.Key}) {
			t.Fatalf("ListReady не видит связь у %s: %+v", child.Key, ready)
		}
		all, err := tr.List(project, []string{"Ready", "InProgress"})
		if err != nil {
			t.Fatal(err)
		}
		byKey := map[string]tracker.TaskRef{}
		for _, r := range all {
			byKey[r.Key] = r
		}
		return pipeline.UnmetDependencies(ready[i], byKey, func(s string) bool { return s == "InProgress" })
	}
	if unmet := gate(); len(unmet) != 1 {
		t.Errorf("пока база в Ready, гейт видит %+v", unmet)
	}
	if err := tr.Transition(base.Key, tracker.BySystem(), "InProgress"); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	if unmet := gate(); len(unmet) != 0 {
		t.Errorf("база «закрыта», а гейт держит: %+v", unmet)
	}
	task, err := tr.Get(child.Key)
	if err != nil {
		t.Fatal(err)
	}
	notes := 0
	for _, c := range task.Comments {
		if strings.HasPrefix(c.Body, "Зависит от: ") {
			notes++
		}
	}
	if notes != 1 {
		t.Errorf("заметок о зависимости %d, ожидалась одна: %+v", notes, task.Comments)
	}

	// Вложение: загрузка → сообщение-файл → байт в байт обратно.
	data := append([]byte("office live attachment "+stamp+"\n"), 0, 0xff)
	name := "live ТЗ " + stamp + ".txt"
	id, err := tr.AddAttachment(child.Key, tracker.BySystem(), name, data)
	if err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	back, err := tr.GetAttachment(child.Key, id)
	if err != nil {
		t.Fatalf("GetAttachment: %v (сеть до prod-user-data.yougile.com? см. шапку файла)", err)
	}
	if !bytes.Equal(back, data) {
		t.Errorf("байты не совпали: %q против %q", back, data)
	}
	task, err = tr.Get(child.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(task.Attachments, tracker.AttachmentRef{ID: id, Name: name}) {
		t.Errorf("вложение не среди Attachments: %+v", task.Attachments)
	}
}
```

- [x] **Step 2: Compile-check without the network**

Run: `go vet -tags yougile_live ./internal/tracker/yougile/ && go test ./internal/tracker/yougile/`
Expected: vet is clean. The untagged tests still PASS, and the live file is not compiled into them.

- [x] **Step 3: Run live (office-polygon only)**

Credentials and column ids come from the owner's environment (see memory notes on tracker credentials). Never use `Clens`.
```bash
YOUGILE_API_KEY=… YOUGILE_PROJECT_ID=<office-polygon> YOUGILE_COLUMNS='Ready=<id>,InProgress=<id>' \
  go test -tags yougile_live -run TestLiveDependenciesAndAttachments -count=1 -v ./internal/tracker/yougile/
```
Expected: PASS. If it fails only at `GetAttachment` with a dial/timeout error on `prod-user-data.yougile.com`, that is the known split-tunnel gap. Record it in the task report and ask the owner to add the AmneziaVPN entry for `111.88.104.34`. Do **not** change the code to work around it. Then run `TestLiveLifecycle` separately, more than a minute later, to confirm the namespace migration live:
```bash
… go test -tags yougile_live -run TestLiveLifecycle -count=1 -v ./internal/tracker/yougile/
```

- [x] **Step 4: Tick tasks.md 4.3 and 3.4 (keep its reframing note), commit**

Tick only if both live runs passed. If the round-trip was blocked by the network, leave 3.4/4.3 unticked and say so in the report.
```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./internal/tracker/yougile/
git add internal/tracker/yougile/live_test.go docs/openspec/changes/yougile-dependencies-attachments/tasks.md
git commit -m "test(yougile): live smoke for dependency gate and attachment round-trip

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 14: Contract doc comments and full verification

- [x] Task 14 complete: Contract doc comments and full verification

**Files:**
- Modify: `internal/tracker/tracker.go` (doc comments only: `Task.DependsOn`, `Tracker.LinkDependsOn`)

- [x] **Step 1: Update the doc comments**

`tracker.go`, in the `Task.DependsOn` comment, replace `читается обратно через Get/List/ListReady на обеих\n\t// реализациях: mock хранит и читает то же поле, jira разбирает` with wording that covers three implementations:
```go
	// DependsOn — ключи задач, от которых зависит эта. Пишется
	// LinkDependsOn, читается обратно через Get/List/ListReady на всех
	// реализациях: mock хранит и читает то же поле, yougile — список id
	// в apiData.virtual_office.depends_on, jira разбирает
	// issuelinks в toTask (см. его доккомент про направление
	// outward/inward) и запрашивает это поле явно в searchFields() —
	// без него List/ListReady отдавали бы пустой DependsOn даже при
	// верном Get(). Гейт очерёдности по этому полю —
	// internal/pipeline/deps.go, Office.claim().
```
In the `LinkDependsOn` interface comment, change `на обеих\n\t// реализациях: mock хранит то же поле, jira разбирает issuelinks` to `на всех\n\t// реализациях: mock хранит то же поле, yougile — depends_on в apiData, jira разбирает issuelinks`. Change the idempotency sentence `mock сверяется с уже записанным сам, jira полагается…` to `mock и yougile сверяются с уже записанным сами, jira полагается…`. Keep the rest verbatim, and keep `gofmt` line wrapping sensible.

- [x] **Step 2: Full verification**

Run:
```bash
go build ./... && go vet ./... && test -z "$(gofmt -l .)" && go test ./... && go vet -tags yougile_live ./internal/tracker/yougile/
```
Expected: everything PASS, with no gofmt output.

Run: `grep -n "\- \[ \]" docs/openspec/changes/yougile-dependencies-attachments/tasks.md`
Expected: no output. The only exception is 3.4/4.3, if Task 13's live round-trip was network-blocked, and that must be stated in the report.

- [x] **Step 3: Commit**

```bash
git add internal/tracker/tracker.go
git commit -m "docs(tracker): name the yougile implementation in DependsOn/LinkDependsOn contracts

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## Self-review record

- **Spec coverage:** dependency blocks claim (Tasks 5, 6, 13); self-dependency rejected (Task 5); visible to a human (Task 5 note, Task 13 live); attachment round-trip (Tasks 11, 13); unknown id rejected (Task 11); human file listed and retrievable (Tasks 8, 11); no credentials to the file host (Tasks 10, 11); foreign keys survive (Tasks 2, 5); one malformed card doesn't stop the queue and is reported (Task 3); newer data not overwritten (Task 2). Design §6 test list: every bullet maps to a named test above.
- **Type consistency:** `fileLink{ID, Segment, Name}`, `chat(key) []messageDTO`, `comments(msgs)`, `postChat(key, text, textHTML)`, `send(…, body io.Reader, contentType string, out any)`, `upload(name, data) (string, error)`, `newFileClient(*url.URL)`, `fileHostAllowed(base, target *url.URL)`, `officeAPIData`/`officeOf`, and `uploads map[string]fakeFile` are used identically in every task that names them.
- **Known expected survivors:** noted inline (Task 7 `html.UnescapeString` on `href`). Every other probe must go red.
