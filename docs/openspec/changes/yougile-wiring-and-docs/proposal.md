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
- `docs/notes/yougile-setup.md`, parallel to `docs/notes/jira-setup.md`:
  how to provision a YouGile project for the office, including creating a
  dedicated non-human account/API key for `accounts.default` (the office
  writes under it; a human's own personal YouGile login must be a
  different account, or their replies are indistinguishable from the
  office's own writes — `docs/contracts/tracker-protocol.md`, "Кто
  человек"), and how to set up the status string-sticker.
- README/`docs/ONBOARDING.md`: mention `yougile` alongside `jira`/`mock` as
  a supported tracker option.

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
  `office/tracker.example.yaml`), new `docs/notes/yougile-setup.md`.
- Requires provisioning a real dedicated non-human YouGile account/API key
  before this change can be verified end-to-end (done in
  `yougile-live-validation`, but the account itself should exist by the
  time this change's docs are written, so the setup doc can be followed
  and checked rather than merely described).
