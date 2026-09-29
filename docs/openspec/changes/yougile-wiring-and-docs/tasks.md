## 1. Config and connection file

- [ ] 1.1 Add `"yougile"` to `internal/tracker/config.go`'s `trackers` list
      and any related validation.
- [x] 1.2 Define the `tracker-yougile.yaml` schema (`base_url`,
      `api_key_env`, `also_agents`, one `projects.<key>` with
      `project_id`/`columns`/`create_status`) with a strict `LoadConfig`,
      and write `office/tracker-yougile.example.yaml`; `runner init`
      places it.
- [x] 1.4 Add `Key` to `yougile.Config` (runner project name, defaults to
      `ProjectID`); `checkProject` and `Task.Project` use it.
- [ ] 1.3 Implement the "opened only when used" behavior in config loading,
      mirroring `tracker.yaml`'s existing handling.

## 2. Runner wiring

- [ ] 2.1 Implement `openYouGile` in `cmd/runner/office.go`, mirroring
      `openMock`'s one-shared-account shape.
- [ ] 2.2 Confirm `printBoard`/`runner ls` and the rest of the per-office
      machinery in `cmd/runner/office.go` need no changes beyond dispatch
      (they should already be tracker-agnostic per `runner-multi-tracker`).

## 3. Non-human office account

- [ ] 3.1 Create a dedicated YouGile account/API key for office use,
      distinct from any human's personal login (needed for the account-
      based human/office distinction, `docs/contracts/tracker-protocol.md`
      "Кто человек").
- [ ] 3.2 Create the target YouGile project with one column per graph
      status, following the reference doc (owner does 3.1/3.2 by the doc).

## 4. Documentation

- [ ] 4.1 Write `docs/reference/yougile-requirements.md`, paired with
      `docs/reference/jira-requirements.md`; add YouGile steps to
      `docs/guide/project-setup.md`, `docs/guide/machine-setup.md` and
      `docs/reference/configuration.md`.
- [ ] 4.3 Add the minimal YouGile doctor stage (`config:tracker-yougile.yaml`,
      `cred:<api_key_env>`, `yougile:open`, `yougile:account`) with tests.
- [ ] 4.2 Update README and `docs/ONBOARDING.md` to mention `yougile` as a
      supported tracker option alongside `jira`/`mock`.

## 5. Tests

- [ ] 5.1 Config/wiring unit tests: a project declaring `tracker: yougile`
      is accepted, opens through `openYouGile`, and a missing
      `tracker-yougile.yaml` is refused with the right error, mirroring
      the existing JIRA tests' shape.

## 6. Carried over from `yougile-dependencies-attachments` (PR #25)

- [ ] 6.1 Add the API-key user's email to the runner's `Accounts` for
      YouGile projects, so the office's own chat notes and file messages
      are not counted as human replies; cover with a test.
- [ ] 6.2 Route the YouGile `Tracker.Logf` to the runner's logger.
- [x] 6.3 Refuse `base_url: https://ru.yougile.com` in `LoadConfig` with a
      hint to use `https://yougile.com` (it breaks attachment downloads via
      the `prod-user-data.yougile.com` redirect).
- [ ] 6.4 Docs: to drop a task stuck in Blocked, move it to a column
      outside the graph; do not archive it.
- [ ] 6.5 Docs: `runner ls` shows archived cards too.
