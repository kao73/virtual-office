## 1. Config and connection file

- [x] 1.1 Add `"yougile"` to `internal/tracker/config.go`'s `trackers` list
      and any related validation.
- [x] 1.2 Define the `tracker-yougile.yaml` schema (`base_url`,
      `api_key_env`, `also_agents`, one `projects.<key>` with
      `project_id`/`columns`/`create_status`) with a strict `LoadConfig`,
      and write `office/tracker-yougile.example.yaml`; `runner init`
      places it.
- [x] 1.4 Add `Key` to `yougile.Config` (runner project name, defaults to
      `ProjectID`); `checkProject` and `Task.Project` use it.
- [x] 1.3 Implement the "opened only when used" behavior in config loading,
      mirroring `tracker.yaml`'s existing handling.

## 2. Runner wiring

- [x] 2.1 Implement `openYouGile` in `cmd/runner/office.go`, mirroring
      `openMock`'s one-shared-account shape.
- [x] 2.2 Confirm `printBoard`/`runner ls` and the rest of the per-office
      machinery in `cmd/runner/office.go` need no changes beyond dispatch
      (they should already be tracker-agnostic per `runner-multi-tracker`).

## 3. Non-human office account

- [x] 3.1 Create a dedicated YouGile account/API key for office use,
      distinct from any human's personal login (needed for the account-
      based human/office distinction, `docs/contracts/tracker-protocol.md`
      "Кто человек").
- [x] 3.2 Create the target YouGile project with one column per graph
      status, following the reference doc (owner does 3.1/3.2 by the doc).

## 4. Documentation

- [x] 4.1 Write `docs/reference/yougile-requirements.md`, paired with
      `docs/reference/jira-requirements.md`; add YouGile steps to
      `docs/guide/project-setup.md`, `docs/guide/machine-setup.md` and
      `docs/reference/configuration.md`.
- [x] 4.3 Add the minimal YouGile doctor stage (`config:tracker-yougile.yaml`,
      `cred:<api_key_env>`, `yougile:open`, `yougile:account`) with tests.
- [x] 4.2 Update README and `docs/ONBOARDING.md` to mention `yougile` as a
      supported tracker option alongside `jira`/`mock`.

## 5. Tests

- [x] 5.1 Config/wiring unit tests: a project declaring `tracker: yougile`
      is accepted, opens through `openYouGile`, and a missing
      `tracker-yougile.yaml` is refused with the right error, mirroring
      the existing JIRA tests' shape.

## 6. Carried over from `yougile-dependencies-attachments` (PR #25)

- [x] 6.1 Add the API-key user's email to the runner's `Accounts` for
      YouGile projects, so the office's own chat notes and file messages
      are not counted as human replies; cover with a test.
- [x] 6.2 Route the YouGile `Tracker.Logf` to the runner's logger.
- [x] 6.3 Refuse `base_url: https://ru.yougile.com` in `LoadConfig` with a
      hint to use `https://yougile.com` (it breaks attachment downloads via
      the `prod-user-data.yougile.com` redirect).
- [x] 6.4 Docs: to drop a task stuck in Blocked, move it to a column
      outside the graph; do not archive it.
- [x] 6.5 Docs: `runner ls` shows archived cards too.

## Build notes

- Departure from the Design Doc §4 step 6: the runner's `Logf` prints the
  adapter's message as is, without its own `yougile: ` prefix — every
  adapter message already starts with `yougile: `, and a second prefix would
  read `yougile: yougile: …`. Pinned by a test in `cmd/runner/office_test.go`.
- The doctor stage extracted the JIRA stage into `doctorJira` so that a JIRA
  failure no longer ends the report before the YouGile checks.
- 3.1/3.2 done 2026-09-29. The owner created the office account
  `kao@simbirsoft.com` (invitation to an email the owner controls — YouGile
  has no bot/service users) and issued its key into `YOUGILE_OFFICE_API_KEY`.
  Project `office-wiring` with board `office` and eight columns was created
  via the API with the owner's admin key; the office account was added as
  `worker`. `runner ls` and `runner doctor --backend local` on a separate
  `OFFICE_HOME` (project key `WIRE`), key redacted:

  ```
  ok   config:tracker-yougile.yaml проект WIRE, колонка на каждый статус графа
  ok   cred:YOUGILE_OFFICE_API_KEY задана
  ok   yougile:open                проект и колонки на месте
  ok   yougile:account             учётка офиса: kao@simbirsoft.com
  ```

  Before `runner ls` unpacked the office snapshot, the first finding was
  `warn` (graph not read). Gaps found while provisioning were fixed in the
  reference doc (`a9b7926`): creating the user, adding it to the project,
  an API recipe for project/board/columns, a distinct `api_key_env`, and the
  fresh-`OFFICE_HOME` warn. No `tick`/`loop` was run — live runs belong to
  `yougile-live-validation`.
