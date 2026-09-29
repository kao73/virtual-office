---
comet_change: yougile-dependencies-attachments
role: technical-design
canonical_spec: openspec
---

# yougile-dependencies-attachments — deep technical design

This is the second of four changes in the `yougile-tracker-adapter` batch. It
completes `tracker.Tracker` for `internal/tracker/yougile` by adding
`LinkDependsOn`, `AddAttachment` and `GetAttachment`. Before adding those
methods, it moves the office's data in `apiData` into its own namespace,
which change 1 left as open (archived
`2026-09-28-yougile-adapter-core/tasks.md`, "Build review notes").

Scope, goals and non-goals come from `proposal.md` and `design.md` in
`docs/openspec/changes/yougile-dependencies-attachments/`. This document turns
them into concrete behavior. Where it departs from those artifacts, §7 says so.

## 1. Facts this design rests on

All of these were confirmed live on `office-polygon` on 2026-09-28/29.

- **`apiData` is invisible in the YouGile UI.** The API spec calls it
  «Данные конфигуратора». YouGile has no native "blocks / depends on" link
  between tasks: `subtasks` is composition, and no other field relates two
  tasks.
- **Uploads.** `POST /api-v2/upload-file` (multipart, field `file`) returns
  `{result, url, fullUrl}`, where `url` is
  `/user-data/<uuid>/<percent-encoded name>` on the API host. The bytes come
  back exact, both immediately and after 20 s.
- **Where a human-attached file appears, and how it is encoded** (probe task
  `c8f34ec2-…`):
  - **Chat.** The file becomes a message whose `text` is exactly
    `/root/#file:/user-data/<uuid>/<name>`, and the name is percent-encoded
    **twice** (`%25D0…`). A listing returns no `textHtml` for it.
  - **Description.** The file becomes an HTML anchor:
    `<a href="https://ru.yougile.com/user-data/<uuid>/<name encoded once>?previews[]=…">Name.ext</a>`.
    The host is `ru.yougile.com`, not the API host.
- **An office-posted file message renders as a file.** A message posted
  through the API with `text` = `textHtml` = `/root/#file:<upload url>` looks
  like a file in the UI (owner-confirmed).
- **Downloads need no credentials.** Every `/user-data/…` URL (either host,
  name encoded once or twice) answers `302` to
  `https://prod-user-data.yougile.com/<uuid>/<name encoded once>`, a separate
  storage host, and serves the file without the API key. A file URL is
  therefore a capability URL.
- **Go leaks the key on redirect by default.** `net/http` forwards
  `Authorization` on a redirect to a subdomain of the original host, and
  `prod-user-data.yougile.com` is a subdomain of `yougile.com`. Downloads must
  therefore not reuse the API client.

## 2. `apiData` namespace

All office data moves under one top-level key:

```json
{
  "crm": { "…": "foreign, untouched" },
  "virtual_office": {
    "v": 1,
    "lease": { "owner": "implementer", "run_id": "…", "lease_until": "…" },
    "attempts": 2,
    "human_wait": false,
    "labels": ["split:VO-1:a"],
    "depends_on": ["<task id>"]
  }
}
```

**Codec.** `decodeAPIData` parses the top-level object, keeps every key
except `virtual_office` verbatim as `extra`, and decodes `virtual_office`
strictly. `encode` writes `extra` plus a full `virtual_office` object: all
of its keys, `lease: null` when free, and `labels`/`depends_on` as `[]` when
empty. The read-modify-write invariant from change 1 stays the same. It just
applies to the namespaced object now.

**Rules.**

| Situation | Behavior |
|---|---|
| `apiData` empty, `null`, or without `virtual_office` | zero value: the task is free and the office has never touched it |
| `virtual_office.v` absent or `1` | normal |
| `virtual_office.v > 1` | an error of type `ErrOfficeData` naming the version, on every path that reads it. We never overwrite a newer schema |
| `virtual_office` present but malformed (not an object, wrong field types) | `ErrOfficeData` naming the task |
| top-level `apiData` not an object (a string, an array) | `ErrOfficeData`, marked as "not an object": such a card cannot carry office data |
| top-level `lease`/`attempts`/`human_wait`/`labels` left over from change 1 (only on `office-polygon`) | foreign keys: preserved, not read, not migrated |

**Where `ErrOfficeData` goes.**
- `ListReady`, `List` and `ListExpired` **skip** the card and report it
  through the new `Tracker.Logf` hook: `"yougile: задача %s пропущена: %v"`.
  One card must not stop a column's queue or the reaper.
- `Get`, `Claim`, `owned` (and so every mutator) and `FindByMarker` **fail
  loudly**. `FindByMarker` is the idempotency source for split children, and
  skipping a card there would create a duplicate child.
- One exception in `FindByMarker`: a card whose top-level `apiData` is not an
  object is **skipped** with the same `Logf` line. It has no
  `virtual_office`, so it cannot be one of our children, and one foreign card
  must not fail the search for the whole project. A `virtual_office` that is
  present but malformed, or of a newer version, still fails loudly. The codec
  tells the two apart with an unexported sentinel wrapped next to
  `ErrOfficeData`; for every other path both are just `ErrOfficeData`.

**Archived and deleted cards.** Deleted cards are dropped from every
listing. Archived cards are dropped from `ListReady` and `ListExpired`: an
archived card is never claimed and never reaped. `List` **keeps** them, with
the status of their column. The claim gate reads `List` over the graph's
statuses (`pipeline.UnmetDependencies` over `projectByKey`) and treats a
dependency missing from it as unmet. Archiving a finished card is ordinary
YouGile housekeeping, so without archived cards in `List` an archived
dependency in a terminal column would block its dependents forever.
`FindByMarker` still drops archived cards; `idempotencyKey` covers them.

**`Logf` hook.** It is a `Tracker` field, set up like `Now`, that defaults
to `log.Printf`. `yougile-wiring-and-docs` points it at the runner's logger.
Trackers have no logging today. This is the first place where the adapter
tolerates an error instead of returning it, so it needs a channel.

## 3. Dependencies

**Storage.** `virtual_office.depends_on` holds the ids of the tasks this
task depends on. `toTask` fills `Task.DependsOn`, and through `Ref()` it
reaches the `TaskRef`s from `ListReady`/`List`. The claim gate
(`pipeline.UnmetDependencies`) and `runner ls` need no change.

**`LinkDependsOn(key, dependsOnKey, by)`:**

1. Reject an empty `dependsOnKey` and `dependsOnKey == key`, before any
   request.
2. `owned(key, by)` checks CheckOwner. The caller is normally `BySystem()` on
   a fresh split child with no lease.
3. If `dependsOnKey` is already in `depends_on`, return nil (idempotent, as
   in `mock`).
4. `getRaw(dependsOnKey)`, not `load`: a dependency may sit outside this
   graph's columns, and an unmapped column must not be an error here. A
   missing or deleted dependency fails with `ErrNotFound`.
5. **Visibility note first.** Post to the dependent task's chat:
   `Зависит от: <idTaskProject> «<title>»`. If `idTaskProject` is empty, the
   id is shown instead.
6. Write `depends_on` with the new id appended, through the usual
   read-modify-write.

The note goes before the write because `pipeline.linkChildren` skips ids
already present in `Get(key).DependsOn`. If the note came after the write,
a failure between the two would lose the note for good. With this order the
worst case is a duplicated note, which a human can see.

The note is an ordinary office-authored comment. It appears in
`Task.Comments`, so the agent also sees what the task waits for, and it does
not count as a human reply.

## 4. Attachments

Attachments live where the UI puts them: in the task's chat and description.
`apiData` holds no manifest.

### 4.1 Discovery: `Task.Attachments`

`Get` already reads the chat. It also collects attachment references:

- **From the description:** every `href` in the HTML whose path matches
  `^/user-data/<uuid>/<segment>`, on any host and ignoring the query. The
  name is the anchor text when it is non-empty, otherwise the decoded segment.
- **From the chat:** every non-deleted message whose trimmed `text` matches
  `^/root/#file:(/user-data/<uuid>/<segment>)$`. The name is the segment
  percent-decoded **up to twice**, stopping as soon as a decode fails or
  changes nothing.

References are ordered description first, then chat oldest-first, and
deduplicated by uuid with the first occurrence winning.
`AttachmentRef{ID: uuid, Name: name}`. A uuid that fails
`tracker.ValidAttachmentID` is skipped, because it cannot be referenced by a
marker anyway. `<uuid>` is matched as `[0-9a-fA-F-]{36}`.

**File messages in `Task.Comments`.** They are kept, and their `Body` is
rendered as `[вложение: <name>]` instead of the raw `/root/#file:…`:
- a human who answers a question by attaching a file has answered;
- the office's own file message (for example `split.json`) is authored by
  the office and never counts as a human reply.

### 4.2 `AddAttachment(key, by, name, data)`

1. `owned(key, by)`: CheckOwner, same as `Comment`.
2. `POST /api-v2/upload-file` as `multipart/form-data`, field `file`,
   `filename=name`. This goes through a new `upload` helper next to `call`,
   with the same error mapping and the same rule that the key never appears
   in error text.
3. Extract the uuid from the returned `url`. If it does not match the
   `/user-data/<uuid>/…` shape or fails `ValidAttachmentID`, fail with an
   error. The file is uploaded but unreferenced, which is harmless.
4. `POST /chats/{key}/messages` with `text` = `/root/#file:<url>`, the url
   exactly as the server returned it, and `textHtml` = the same string
   HTML-escaped (`html.EscapeString`). The segment class lets `&`, `<`, `>`
   and `"` through, and they must not reach the markup raw. A well-formed
   url is unchanged by escaping.
5. Return the uuid.

A failure between steps 2 and 4 leaves an invisible orphan upload and
returns an error. The retry uploads again.

### 4.3 `GetAttachment(key, id)`

1. Reject an id that fails `ValidAttachmentID`, with no request.
2. `getRaw(key)` and read the chat. Collect references exactly as in §4.1
   and find `id`. If absent, return `ErrNotFound`.
3. **Rebuild** the URL as `BaseURL + "/user-data/" + uuid + "/" + <segment
   as found>`. The host from a description link is never used, so the
   adapter cannot be pointed at an arbitrary host.
4. Fetch it with a **separate `http.Client`** that sends no `Authorization`
   header. Its `CheckRedirect` allows only `https` targets whose host is the
   `BaseURL` host or a subdomain of it, which is how the request reaches
   `prod-user-data.yougile.com`, and caps the chain at 5 hops. In tests the
   `BaseURL` is `http://127.0.0.1:…`, so the scheme rule is "same scheme as
   `BaseURL`". Non-2xx fails. `404` becomes `ErrNotFound`.

**Cost.** One `GetAttachment` makes three requests (task, chat, file).
`pipeline.fetchAttachments` calls it once per human attachment. Against
50 req/min this is acceptable for a few files, and there is deliberately no
cache (YAGNI).

## 5. Interface completion

`contract_test.go` drops `coreTracker`, and `yougile.go` gains
`var _ tracker.Tracker = (*Tracker)(nil)`. The package doc comment loses the
"not yet a full Tracker" paragraph.

## 6. Testing

Unit tests against the fake server, extending `yougile_test.go`'s fake with
`upload-file`, the storage redirect and a storage host:

- **Namespace:**
  - office data lands under `virtual_office` and foreign top-level keys
    survive every mutator;
  - `v: 2` is refused and nothing is written;
  - a malformed card is skipped by `ListReady`/`List`/`ListExpired` (with a
    `Logf` call) and makes `Get`/`FindByMarker` fail loudly;
  - a card whose top-level `apiData` is not an object is skipped by
    `FindByMarker` (with a `Logf` call);
  - top-level legacy keys are ignored;
  - the existing change-1 tests move to the namespaced fixture.
- **Dependencies:**
  - the link is recorded and `DependsOn` reaches `ListReady` refs;
  - `List` returns an archived card and the gate releases a task whose
    dependency is archived in a terminal column; `ListReady` and
    `ListExpired` still drop archived cards;
  - a repeated call is a no-op, with no second note and no write;
  - empty and self keys are rejected with no request;
  - a missing dependency gives `ErrNotFound`;
  - the note is posted before the write: a failing PUT leaves the note and
    no `depends_on`;
  - ownership goes through the shared contract table.
- **Attachment discovery:**
  - a chat file message with a double-encoded name;
  - a description anchor on `ru.` with a `previews[]` query;
  - dedup across both sources;
  - an invalid uuid is skipped;
  - file messages appear in `Comments` as `[вложение: …]`.
- **`AddAttachment`:**
  - multipart upload with the filename;
  - chat post with `text` verbatim and `textHtml` HTML-escaped;
  - the uuid is returned;
  - ownership is enforced;
  - a malformed upload answer is an error.
- **`GetAttachment`:**
  - the URL is rebuilt on `BaseURL` even when the description link names
    another host;
  - **the storage fake asserts that no `Authorization` header arrives**;
  - a redirect to a foreign host is refused;
  - an unknown id gives `ErrNotFound`;
  - an invalid id makes no request;
  - bytes round-trip exactly.
- `var _ tracker.Tracker` compiles.
- Every new test is mutation-probed (break the guarded line, see red,
  restore).

**Live** (`-tags yougile_live`, `office-polygon`):
- a dependency recorded by `LinkDependsOn` is visible in `ListReady` refs,
  and its note is in the chat;
- an `AddAttachment` → `GetAttachment` byte round-trip.

The round-trip needs this machine to reach `prod-user-data.yougile.com`
(`111.88.104.34`), which requires an AmneziaVPN split-tunnel entry.

## 7. Divergences from the Open artifacts

- **No attachment manifest in `apiData`** (`design.md` "Attachments are a
  self-maintained id→url manifest"). Human files already live in chat links,
  so storing office files the same way gives one mechanism for both, makes
  office files visible in the UI as they are in JIRA, and needs no second
  record. This drops tasks 3.1 and 3.4 as written. URL stability becomes the
  byte round-trip check.
- **The change-1 schema is moved under `virtual_office`**, a change to code
  that change 1 shipped. `proposal.md` already assumed a namespaced lease
  sub-object, and task 2.1 already said "namespaced alongside the lease".
- **`LinkDependsOn` posts a chat note and fails on a missing dependency.**
  Neither was in the Open artifacts. The owner asked for UI visibility on
  2026-09-29.
- **Top-level `apiData` that is not an object is `ErrOfficeData`.** The
  §2 table did not name this case. The coordinator ruled it during Build
  (ruling V1): the adapter never overwrites such a card, listings skip it,
  and after the final review `FindByMarker` skips it too (§2).
- **`List` includes archived cards** (owner, 2026-09-29). The final review
  found that an archived dependency blocked its dependents forever. `List`
  now keeps archived cards; `ListReady` and `ListExpired` do not (§2).
- **The delta spec stays `ADDED`** (task 1.2). The new requirements add
  behavior and change none of the existing text.

## 8. Risks

- **Formats seen on one sample.** The double-encoded chat name and the
  description anchor shape come from one UI sample each. A wrong decode
  changes only the display name; the id (uuid) is unaffected.
- **Orphan uploads.** A failure between upload and chat post leaves a file
  nobody references.
- **Visible notes.** Duplicate dependency notes are possible after a failure
  between the note and the write.
- **Capability URLs.** Anyone holding a file link can read the file, as in
  the UI. The API key never leaves the API host.
- **Request cost.** `GetAttachment` costs three requests.
- **Local network.** The storage host needs its own split-tunnel entry on
  this machine.
- **The office's own chat messages.** `yougile-wiring-and-docs` must put the
  API-key user's email into the runner's `Accounts`, or the office's own
  dependency notes and file messages would count as human replies
  (`HumanReply`).
- **Archived cards in `List`.** `runner ls` and `CompleteSplits` (which lists
  the blocked status) now see archived cards too.
