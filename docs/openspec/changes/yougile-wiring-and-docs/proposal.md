## Why

`yougile-adapter-core` + `yougile-dependencies-attachments`
(`.comet/batches/yougile-tracker-adapter.json`) produce a fully compiling
`internal/tracker/yougile.Tracker`, but nothing in the runner can reach it
yet: `internal/tracker/config.go` only accepts `tracker: mock`/`tracker:
jira`, and `cmd/runner/office.go` only knows how to open those two. This
change (3 of 4) wires the adapter into the runner's existing multi-tracker
plumbing (`runner-multi-tracker`, shipped in PR #10) and documents how to
set one up, so a project can actually declare `tracker: yougile`.

## What Changes

- `internal/tracker/config.go`: add `"yougile"` to the accepted `trackers`
  list; accept a project declaring `tracker: yougile`.
- A new `${OFFICE_HOME}/tracker-yougile.yaml` connection file, structurally
  parallel to `tracker.yaml` (JIRA's) but its own file rather than a
  namespaced section of `tracker.yaml` — see design.md Decisions for why.
  Opened only when at least one project declares `tracker: yougile`,
  mirroring the existing JIRA requirement.
- `cmd/runner/office.go`: add `openYouGile`, mirroring `openJira`/`openMock`
  in shape (open the shared office account, check it, open one
  `tracker.Tracker` per role — even though YouGile roles are distinguished
  by the comment marker, not by account, matching `openMock`'s pattern more
  than `openJira`'s per-role-account one, since a per-role account is
  optional there too).
- `docs/reference/yougile-requirements.md`, paired with
  `docs/reference/jira-requirements.md` (the repository's Diátaxis layout):
  how to provision a YouGile project for the office, including creating a
  dedicated non-human account/API key for `accounts.default` (the office
  writes under it; a human's own personal YouGile login must be a
  different account, or their replies are indistinguishable from the
  office's own writes — `docs/contracts/tracker-protocol.md`, "Кто
  человек"), and how to find the project and column ids (status is a
  column, as settled in `yougile-adapter-core`). Guide pages
  (`project-setup.md`, `machine-setup.md`) and
  `reference/configuration.md` gain the YouGile steps.
- `runner doctor` gains a minimal YouGile stage (config, credential, open,
  account), run only when a project declares `tracker: yougile`.
- One YouGile project per runner; the adapter's `Config` gains `Key` (the
  runner's project name) separate from `ProjectID`.
- README/`docs/ONBOARDING.md`: mention `yougile` alongside `jira`/`mock` as
  a supported tracker option.
- Items carried over from `yougile-dependencies-attachments` (PR #25):
  - The API-key user's email SHALL be part of the runner's `Accounts` for
    YouGile projects; otherwise the office's own chat notes and file
    messages are read as human replies.
  - `Tracker.Logf` is routed to the runner's logger instead of being
    dropped.
  - `base_url: https://ru.yougile.com` breaks attachment downloads (the
    `/user-data/` redirect goes to `prod-user-data.yougile.com`, which is
    not a subdomain of `ru.yougile.com`, so the file client refuses it).
    The config loader refuses it with a hint to use
    `https://yougile.com` (chosen in Design).
  - Setup/user docs: to drop a task stuck in Blocked, move it to a column
    outside the graph — do not archive it (`CompleteSplits` still
    completes a split whose parent is archived; accepted residual).
  - Setup/user docs: `runner ls` lists archived cards too (the adapter's
    `List` includes them so an archived dependency does not block).

## Capabilities

### Modified Capabilities
- `runner-multi-tracker`: adds one requirement (the YouGile connection
  file is opened only when used), mirroring the existing JIRA one. No
  existing requirement's text changes — this capability's other
  requirements are already tracker-agnostic ("the set of distinct tracker
  values", "for each tracker in use") and already cover `yougile` without
  any wording change.

## Impact

- `internal/tracker/config.go`, `cmd/runner/office.go`: small, additive
  changes mirroring the existing JIRA code paths.
- New file `office/tracker-yougile.example.yaml` (parallel to
  `office/tracker.example.yaml`), new `docs/reference/yougile-requirements.md`.
- Requires provisioning a real dedicated non-human YouGile account/API key
  before this change can be verified end-to-end (done in
  `yougile-live-validation`, but the account itself should exist by the
  time this change's docs are written, so the setup doc can be followed
  and checked rather than merely described).
