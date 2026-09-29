## Why

`yougile-adapter-core` implements the core CRUD/lease surface of
`internal/tracker/yougile` but deliberately leaves out `LinkDependsOn` and
`AddAttachment`/`GetAttachment` — the two methods of `tracker.Tracker` that
YouGile has no structural equivalent for (only `subtasks`, which is
composition, not a blocking dependency; and `upload-file`, which has no
matching `GET /files/{id}`). Go interface satisfaction is all-or-nothing:
`internal/tracker/yougile` does not compile as a `tracker.Tracker` until
both methods exist. This change (2 of 4 in
`.comet/batches/yougile-tracker-adapter.json`, depending on
`yougile-adapter-core`) closes that gap.

## What Changes

- Implement `LinkDependsOn`: store a reference to the dependency task's id
  inside the dependent task's `apiData` (namespaced alongside the lease
  sub-object from `yougile-adapter-core`), and gate `claim()` through
  `internal/pipeline.UnmetDependencies` the same way the `jira` adapter
  already does for split-created subtasks (`design.md` decision #3,
  `docs/notes/analyst-task-splitting.md`).
- Implement `AddAttachment`/`GetAttachment`: upload raw bytes via YouGile's
  `upload-file` endpoint, and maintain an id→url manifest inside the same
  task's `apiData`, since the real API has no `GET /files/{id}`.
  `GetAttachment` resolves the id through that manifest, then performs a
  plain HTTP GET on the stored URL.
- Complete `var _ tracker.Tracker = (*Tracker)(nil)` — `internal/tracker/yougile`
  becomes a fully compiling `Tracker` implementation for the first time.

## Capabilities

### Modified Capabilities
- `tracker-yougile`: adds the dependency-link and attachment requirements
  that `yougile-adapter-core`'s spec deliberately left out.

## Impact

- Extends `internal/tracker/yougile/*.go` (from `yougile-adapter-core`);
  no new package.
- No changes to `internal/tracker/config.go` or `cmd/runner/office.go` yet
  — wiring is `yougile-wiring-and-docs`.
- Depends on `yougile-adapter-core` being merged (or at least its `apiData`
  lease sub-schema being stable) so the dependency/attachment sub-objects
  can be added without colliding with lease fields.
