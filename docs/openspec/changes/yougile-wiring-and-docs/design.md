## Context

See `proposal.md` - Why. This change depends on
`yougile-dependencies-attachments` (which itself depends on
`yougile-adapter-core`) — `internal/tracker/yougile` must fully satisfy
`tracker.Tracker` before it can be wired in, since Go interface
satisfaction is all-or-nothing.

`office/tracker.example.yaml` (JIRA's connection file) is a flat,
JIRA-shaped schema: `base_url`, `auth.mode`, `accounts.*`, `status_map`,
`fields.customfield_*` (instance-specific ids `scripts/jira-setup.sh`
prints), `human_flag_label`, `issue_type`, `depends_on_link`. It is not
namespaced by tracker type — the filename itself (`tracker.yaml`) is
JIRA's file, per the existing `runner-multi-tracker` requirement text.

## Goals / Non-Goals

**Goals:**
- A project can declare `tracker: yougile` in `projects.local.yaml` and
  the runner opens it the same way it opens `jira`/`mock` projects today.
- `docs/notes/yougile-setup.md` is sufficient, on its own, to provision a
  working YouGile-backed project from nothing.

**Non-Goals:**
- Restructuring `tracker.yaml` itself or changing anything about how
  JIRA projects are configured.
- Live validation that the wiring actually works end-to-end against a
  real project (`yougile-live-validation`).

## Decisions

**A separate `tracker-yougile.yaml` file, not a namespaced section inside
`tracker.yaml`.** `tracker.yaml` is already a committed, documented,
flat JIRA schema (`office/tracker.example.yaml`); renaming or
restructuring it to `jira: {...}` would be a breaking change to every
existing JIRA setup for no behavioral benefit. A sibling file, opened only
when `tracker: yougile` is declared (mirroring the existing JIRA
requirement exactly), keeps each tracker's connection file independently
optional and avoids touching JIRA's schema at all. Alternative considered:
one `tracker.yaml` namespaced by tracker type — rejected as a needless
breaking change to something that already works.

**`tracker-yougile.yaml` is simpler than `tracker.yaml`: no
instance-specific field ids.** JIRA's `fields.customfield_*` exist because
JIRA custom fields get instance-specific ids at creation time
(`scripts/jira-setup.sh` prints them). YouGile's lease/dependency/
attachment-manifest data all lives in the adapter-owned `apiData` JSON
field (`yougile-adapter-core`, `yougile-dependencies-attachments`), which
needs no per-instance id — only the project and its columns do
(`project_id` and `columns`, a map from graph status to column id: status
is a column, as settled in `yougile-adapter-core`; this replaces the
sticker-based status assumed when this change was opened — see the Design
Doc, §8). This makes the YouGile setup script (if one ends
up being needed at all) smaller in scope than `scripts/jira-setup.sh`.

**`openYouGile` opens one shared `Tracker` per role, following `openMock`'s
shape rather than `openJira`'s per-role-account one.** Per-role accounts
are an optional JIRA nicety for human-readable ticket history and
JIRA-side permission separation (`docs/contracts/tracker-protocol.md`,
"Кто человек" — "одна учётка на всех агентов — штатный режим"); YouGile
roles are already distinguished by the comment marker, not by account, so
defaulting to one shared account per `openMock`'s pattern is simpler and
loses nothing functionally. Per-role YouGile accounts are not ruled out by
this decision, just not built in this change — nothing here would need to
change to add them later if wanted purely for human readability.

## Risks / Trade-offs

- [A second connection-file convention (`tracker-yougile.yaml` alongside
  `tracker.yaml`) adds a small amount of conceptual surface] → Mitigated
  by making it a near-exact structural mirror of the JIRA file and
  documenting both the same way, rather than inventing a new pattern.
- [No real dedicated non-human YouGile account exists yet as of this
  change being designed] → `docs/notes/yougile-setup.md` documents how to
  create one; provisioning the actual account is a prerequisite for
  `yougile-live-validation`, not blocking this change's own content.
