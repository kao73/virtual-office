## Context

See `proposal.md` - Why. This change depends on `yougile-adapter-core`
(`.comet/batches/yougile-tracker-adapter.json`), which establishes
`internal/tracker/yougile`'s package structure and its `apiData` lease
sub-schema. This change's worktree was prepared before `yougile-adapter-core`
merged, so its `specs/tracker-yougile/spec.md` delta is written as `ADDED`
Requirements rather than `MODIFIED`, since the canonical
`docs/openspec/specs/tracker-yougile/spec.md` does not exist on this branch
yet. Process note for Build: before implementation starts here, rebase this
branch on top of `yougile-adapter-core` once it has archived, and re-check
whether the delta should instead be expressed as `MODIFIED` against the by
-then-real base spec so archive-time reconciliation is clean.

Confirmed live in `office-polygon` this session: YouGile's real REST API has
no structural task-to-task blocking/dependency relation (only `subtasks`,
which is parent/child composition) and no `GET /files/{id}` (only
`POST /upload-file`, which returns a URL).

## Goals / Non-Goals

**Goals:**
- `internal/tracker/yougile` fully satisfies `tracker.Tracker`
  (`var _ tracker.Tracker = (*Tracker)(nil)` compiles) after this change.
- `LinkDependsOn` integrates with the same `pipeline.UnmetDependencies`
  gate the `jira` adapter already feeds, with no changes to `pipeline`
  itself expected.

**Non-Goals:**
- Runner/config wiring (`yougile-wiring-and-docs`).
- Live end-to-end validation (`yougile-live-validation`).
- Reproducing YouGile's `subtasks` composition semantics — this change adds
  a separate, purpose-built dependency reference; it does not repurpose
  `subtasks`.

## Decisions

**`depends_on` is a task-id reference inside `apiData`, not `subtasks`.**
`subtasks` is YouGile's own parent/child composition feature; overloading
it for blocking dependencies would fight its native semantics (and native
UI behavior) instead of using a field we fully control. Alternative
considered: encode the dependency in `description` — rejected for the same
human-content-collision reason lease fields avoid `description` in
`yougile-adapter-core`.

**Attachments are a self-maintained id→url manifest in `apiData`, not a
new external store.** `upload-file` already returns a stable URL; storing
the mapping alongside the task avoids introducing a second system of
record for attachment metadata. Alternative considered: re-derive
attachment locations from YouGile's task-chat history — rejected as
fragile (chat history is for comments, not a manifest, and pagination/
ordering guarantees were not part of this session's recon).

**`UnmetDependencies` integration is read-only from this adapter's side.**
`LinkDependsOn` only records the reference; the actual gating logic already
lives in `internal/pipeline` and is tracker-agnostic (it already serves the
`jira` adapter). This change does not modify `internal/pipeline`.

## Risks / Trade-offs

- [Cross-change specs delta sequencing — this change's `ADDED` delta and
  `yougile-adapter-core`'s own delta touch the same capability path from
  different branches] → Mitigated by dependency ordering (build/archive
  `yougile-adapter-core` first) and the rebase-and-recheck step noted in
  Context; if reconciliation still conflicts at archive time, resolve by
  hand rather than forcing the automated merge.
- [Attachment URLs from `upload-file` might not be permanently stable] →
  Not verified this session (recon only confirmed the upload call's
  response shape, not long-term URL stability); worth a quick live check
  early in Build before relying on it architecturally.
