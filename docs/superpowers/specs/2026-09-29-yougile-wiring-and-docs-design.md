---
comet_change: yougile-wiring-and-docs
role: technical-design
canonical_spec: openspec
---

# yougile-wiring-and-docs — deep technical design

This is change 3 of 4 in the `yougile-tracker-adapter` batch. After changes 1
and 2, `internal/tracker/yougile.Tracker` implements all of
`tracker.Tracker`, but the runner cannot reach it yet. This change connects it
to the runner and `doctor`, and documents how to set it up. It also takes the
five follow-ups carried over from PR #25.

Scope comes from `proposal.md` and `design.md` in
`docs/openspec/changes/yougile-wiring-and-docs/`. §8 lists where this document
departs from them.

## 1. Facts this design rests on

- **Status is a column.** Change 1 maps each graph status to a column id
  (`yougile.Config.ColumnIDs`). A status sticker is not used. `Open` reads
  every board and column of the project and refuses when a configured column
  is missing.
- **One `yougile.Tracker` serves one YouGile project.** `checkProject`
  compares the `project` argument with `Config.ProjectID`. `Task.Project` is
  set to `ProjectID`.
- **Whoami.** `(*Tracker).Whoami()` calls `GET /users/me` and returns the
  key owner's email. Comment authors are resolved to emails too
  (`comment.go`), so human-vs-office is decided by comparing emails.
- **Logf.** `Tracker.Logf` defaults to `log.Printf` and reports only what the
  adapter tolerated: unreadable cards skipped by a listing or by
  `FindByMarker`.
- **The `ru.` host.** `/user-data/…` answers `302` to
  `prod-user-data.yougile.com`. The file client only follows redirects to the
  host of `BaseURL` or one of its subdomains, so with `ru.yougile.com` every
  attachment download is refused.
- **Runner shape.** `newOffices` opens one office per tracker name in
  `projects.TrackersInUse()`. Every opener returns `opened{tasks, byRole,
  accounts}`. The office receives `projects.For(name)`. `tracker.yaml` is
  listed in the sources header and loaded only when some project declares
  `jira`.

## 2. Connection file

`${OFFICE_HOME}/tracker-yougile.yaml`, constant `yougile.TrackerFile`. The
sample `office/tracker-yougile.example.yaml` is `yougile.ExampleFile`.

```yaml
base_url: https://yougile.com
api_key_env: YOUGILE_API_KEY
also_agents: []
projects:
  shop:
    project_id: 5b1c…
    columns:
      Backlog: …
      Analysis: …
      Ready: …
      InProgress: …
      Review: …
      Approved: …
      Done: …
      Blocked: …
    create_status: Backlog
```

- `api_key_env` holds the name of the variable, not the key, just as
  `tracker.yaml` stores `secret_env`.
- `also_agents` lists emails of other automation, such as bots. Their
  comments are not human either. This is the same idea as JIRA's list.
- `projects` is keyed by the project key from `projects.local.yaml`. It is a
  map now so that supporting several projects later does not change the
  file's shape.

`yougile.LoadConfig(path) (FileConfig, error)` lives in a new
`internal/tracker/yougile/config.go`. It follows `jira.LoadConfig`:

- A missing file is refused with a message that points at the sample and
  `runner init`.
- Decoding is strict (`KnownFields(true)`), and an empty document is not a
  parse error.
- The file is validated as a whole, and all errors are joined into a single
  "`<path>` breaks the contract" error. It is refused when any of these
  hold:
  - `base_url` is empty, or the host is `ru.yougile.com`. That error says
    attachments would not download and tells the user to write
    `https://yougile.com`.
  - `api_key_env` is empty.
  - `projects` does not have exactly one entry. With more than one, the
    error says one YouGile project per runner is supported for now.
  - The project has no `project_id`, or its `columns` are empty.

`FileConfig.Tracker(key string) (Config, error)` builds the adapter's
`Config` from the single project: `Key`, `ProjectID`, `ColumnIDs`,
`CreateStatus`, `BaseURL`, and `APIKey` read from `os.Getenv(api_key_env)`.
An empty key is an error that names the variable.

## 3. Adapter change: `Config.Key`

The runner addresses a project by its key in `projects.local.yaml` (`shop`),
not by the YouGile UUID. `yougile.Config` gets `Key string`. When `Key` is
empty it defaults to `ProjectID`, so existing tests and `live_test.go` do not
change. `checkProject` compares with `Key`, and `Task.Project` and
`TaskRef.Project` report `Key`. API paths and query parameters still use
`ProjectID`. Worktree paths, branch names, the ledger and
`projects.For` all see the same key as for JIRA and mock.

## 4. Runner wiring

- `internal/tracker/config.go`: `trackers = {"mock", "jira", "yougile"}`.
- `cmd/runner/office.go` gains a new case,
  `case "yougile": o, err = openYouGile(sources.machine(home,
  yougile.TrackerFile), workflow, projects.For("yougile"), out)`. The sources
  header therefore lists the file only when a yougile project exists.
- `openYouGile`:
  1. `LoadConfig`.
  2. The config's single project key must equal the single project in
     `projects.For("yougile")`. If `projects.local.yaml` declares two yougile
     projects, or the keys differ, the error names both sides.
  3. Every status in `workflow` must have a column. Missing statuses are
     named in the error.
  4. `yougile.Open(cfg)`, which already checks the project and its columns
     on the server.
  5. `Whoami()` confirms the key and yields the office email. The error is
     wrapped as "office account: …", like JIRA's `CheckAccount`.
  6. `tr.Logf = func(f string, a ...any) { fmt.Fprintf(out, "yougile: "+f+"\n", a...) }`.
  7. `opened{tasks: tr, byRole: every role → tr, accounts: [email] +
     also_agents}`.
- Every role shares one account, as in `openMock`. Roles are distinguished by
  the comment marker. Per-role YouGile accounts are not built.

`cmd/runner/init.go` adds `{yougile.ExampleFile, yougile.TrackerFile,
"project_id and the column ids; needed only by projects with tracker:
yougile"}` to `samples`. The sample must be in the embedded payload next to
`tracker.example.yaml`, and `scripts/doc-recipe-test.sh` checks for it.

## 5. Doctor

`doctor` runs a new stage after the JIRA stage, and only when
`projects.For("yougile")` is non-empty:

| id | fail condition | cascade |
|---|---|---|
| `config:tracker-yougile.yaml` | `LoadConfig` or the project-key match fails | `skip:yougile` (warn) and stop |
| `cred:<api_key_env>` | the variable is empty | `skip:yougile-checks` and stop |
| `yougile:open` | `yougile.Open` fails (project, columns, network) | `skip:yougile-checks` and stop |
| `yougile:account` | `Whoami` fails | none |

The column-coverage check from §4 step 3 runs under
`config:tracker-yougile.yaml`: it reads only local files. Doctor never
writes to YouGile. `Open` and `Whoami` are the only calls, and both are
reads. The file header comment lists the new ids.

## 6. Documentation

The repository uses Diátaxis, so the YouGile docs mirror the JIRA ones:

- **New `docs/reference/yougile-requirements.md`**, paired with
  `jira-requirements.md`. It covers:
  - Account: a dedicated non-human user and its API key. A human's own login
    must be a different account, or their replies look like the office's
    own writes.
  - The key goes into the variable named by `api_key_env`.
  - Project and columns: one column per graph status, and how to read the
    ids with `curl` against `/api-v2/projects`, `/boards` and `/columns`.
    No setup script is written.
  - `base_url`: always `https://yougile.com`, never `ru.`.
  - To drop a task stuck in Blocked, move it to a column outside the graph.
    Do not archive it: `CompleteSplits` still completes a split whose parent
    is archived. This residual was accepted in change 2.
  - `runner ls` lists archived cards too: `List` includes them so that an
    archived dependency does not block.
- `docs/guide/project-setup.md`: a YouGile variant of the "tracker
  connection" step, plus the expected `tracker-yougile.yaml` line in the
  sources header.
- `docs/guide/machine-setup.md`: the `YOUGILE_API_KEY` export next to
  JIRA's.
- `docs/reference/configuration.md`: the new file and its keys.
- README and `docs/ONBOARDING.md`: `yougile` next to `jira` and `mock`.

The owner will provision the account and project by following the reference
doc during Build (tasks 3.1/3.2). That doubles as a check of the doc. Live
`doctor`/`tick` against it belongs to `yougile-live-validation`.

## 7. Testing

- `internal/tracker/yougile/config_test.go`, table tests:
  - a valid file loads;
  - a missing file points at the sample;
  - an unknown field is rejected;
  - each validation error, including `ru.yougile.com` and two projects;
  - errors are joined;
  - an empty API-key variable is rejected by `Tracker()`.
- Adapter: `Key` is used in `checkProject` and `Task.Project`, and an empty
  `Key` falls back to `ProjectID`.
- `cmd/runner/office_test.go` runs `openYouGile` against an `httptest` server
  that serves `/users/me`, `/projects/…`, `/boards` and `/columns`. It
  checks:
  - accounts contain the email and `also_agents`;
  - `Logf` output lands in `out` with the prefix;
  - it refuses on a key mismatch, on two yougile projects in
    `projects.local.yaml`, and on a missing graph-status column;
  - a mock+jira-only setup never touches `tracker-yougile.yaml` (spec
    scenario).
- `cmd/runner/doctor_test.go`: the four new findings and their skip
  cascades, in the same shape as the JIRA tests.
- `scripts/doc-recipe-test.sh`: `runner init` places the sample.

## 8. Departures from the open-phase artifacts

- The status is a **column**, not a sticker. `sticker_id` and a
  sticker-state `status_map` are replaced by `columns`. `proposal.md` and
  `design.md` are corrected.
- There is **one project per runner**, keyed like `projects.local.yaml`, and
  the adapter gains `Config.Key`.
- The setup doc is `docs/reference/yougile-requirements.md`, not
  `docs/notes/yougile-setup.md`.
- Doctor gets a minimal YouGile stage. The proposal did not mention doctor.
- `ru.yougile.com` is refused, not normalized, not only warned about, and not
  documented alone.

## 9. Implementation Divergence

Recorded at Verify (2026-09-29) against the built change; owner chose to
record rather than redesign.

- **§4 step 6, Logf prefix.** The runner prints the adapter's message as is,
  without adding `yougile: `: every adapter message already starts with it,
  and a second prefix would read `yougile: yougile: …`. Pinned by a test in
  `cmd/runner/office_test.go`.
- **§5, doctor stage shape.** To keep a failed JIRA stage from ending the
  report before the YouGile checks, the JIRA stage was extracted into
  `doctorJira`; its findings, order and exit code are unchanged.
- **§5, unreadable graph.** When the office workflow cannot be read (office
  not resolved, or the snapshot not unpacked yet on a fresh `OFFICE_HOME`),
  `config:tracker-yougile.yaml` reports `warn` — column coverage not
  checked — and the stage continues to the key and server checks. After
  `runner ls` (read-only) unpacks the snapshot, the finding is `ok`. Seen
  live during provisioning.
- **§6, reference doc after provisioning.** Following the doc on a real
  account exposed gaps, fixed in `a9b7926`: YouGile has no bot/service
  users, so the office user is invited to an email the owner controls and
  sets a password (the key is issued only with login and password); the
  office user must be a project member (`worker` is enough) — `PUT
  /projects/{id}` replaces the whole `users` map; an optional API recipe for
  project, board and columns with an admin's key; naming `api_key_env`
  differently (e.g. `YOUGILE_OFFICE_API_KEY`) when a personal
  `YOUGILE_API_KEY` is already in the environment; and the fresh-`OFFICE_HOME`
  warn above.
- **Open-phase risk now closed.** `design.md` Risks said no dedicated office
  account existed; one does now (`kao@simbirsoft.com`, project
  `office-wiring`), see `tasks.md` Build notes.
