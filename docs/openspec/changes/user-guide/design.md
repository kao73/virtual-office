## Context

See proposal.md — Why. Three facts about the current state shape everything
below.

`docs/ONBOARDING.md` was a verified deliverable once: the archived Comet change
`2026-08-21-onboarding-doc` accepted it against the criterion "рассчитан на
того, у кого нет ничего, кроме клона офиса". That assumption is what `install`
(PR #11) removed. The document is not neglected — it is correct for a reader
who no longer exists.

The documentation tree is 12 725 lines across ~45 files. `docs/notes/` is
8 000+ of them and is a dated laboratory journal, not documentation; the owner
scoped it out. `docs/contracts/` and `docs/DESIGN.md` are already clean
quadrants — reference and explanation — and are cited from Go comments and from
OpenSpec capability specs, so moving them costs link breakage and buys a
directory name.

The two shipped configuration examples carry ~90 lines of per-key commentary
and ride inside the binary through `office/payload.go`. They cannot drift from
the release. Any hand-written reference that repeats those keys can.

## Goals / Non-Goals

**Goals:**

- Every document the target reader needs works without a clone, without Go and
  without reading source.
- Each document has one Diátaxis job. The mixing that matters — a tutorial that
  stops to explain, a checklist that doubles as a reference — is removed from
  the user path.
- One copy of each shipped artifact. Where two copies exist today, the change
  leaves one.
- Paths that outsiders may already have linked keep working where that costs
  nothing.

**Non-Goals:**

- A directory per quadrant. Diátaxis is a rule about what a document does, not
  a filesystem layout; renaming `docs/contracts/` to `docs/reference/` would
  break citations from code to buy a label.
- Verifying documentation by test. The one thing this change does pin by test
  is the shipped samples' behavior, because that is code.

## Decisions

### D1. Quadrants are declared by the index, enforced inside documents

`docs/README.md` is new and states, for every document, which of the four jobs
it does. Existing directories keep their names: `guide/` (tutorial and how-to),
`contracts/` (reference), `DESIGN.md` (explanation), `notes/` (journal, named
as outside the scheme).

New reference material goes to a new `docs/reference/` — `configuration.md` and
`jira-requirements.md`. A new directory for new files breaks nothing.

*Alternative considered:* move everything into `tutorial/`, `how-to/`,
`reference/`, `explanation/`. Rejected — `docs/contracts/*` is cited from Go
comments and from three capability specs under `docs/openspec/specs/`, and the
rename would either break those or force edits to archived specs.

### D2. `ONBOARDING.md` survives as a spine, not as a container

It keeps its path — README, `bootstrap/README.md`, `bootstrap/jira/README.md`
and `docs/guide/quickstart.md` all point at it, and it is the most likely
externally linked document after README. Its job narrows to one: the ordered
checklist for putting the office on a real project, where each step is a
sentence and a link, not a copy of the document that owns it.

Its current content disperses:

| Section | Goes to | Job |
|---|---|---|
| A1–A5 machine, creds, sandbox network, home, check | `guide/` how-to | how-to |
| Б1–Б2 tracker instance, instance setup | `reference/jira-requirements.md` | reference |
| Б2 polygon container half | `bootstrap/jira/README.md` | how-to |
| Б3 accounts | `reference/jira-requirements.md` | reference |
| Б4 client repository | `guide/` how-to | how-to |
| Б5 configuration | `reference/configuration.md` | reference |
| Б6 budgets | `docs/notes/budgets.md` (exists) | reference |
| Б7 four checks by rising cost | `guide/` how-to | how-to |

### D3. The scheduler samples get one home, and it is the payload

`office/scheduler/` holds `local.office.runner.plist`, `office-runner.service`
and `office-runner.timer`. `office/payload.go` gains one embed directive —
a new top-level entry needs one; files inside an already embedded directory do
not. `runner init` writes the directory into `${OFFICE_HOME}/scheduler/` under
the never-overwrite rule that already governs the configuration samples.

The copies under `bootstrap/` are deleted, not fixed. `bootstrap/README.md`
keeps the explanation of what launchd and systemd each need and why the systemd
side is a oneshot under a timer, and points at `office/scheduler/` for the
files. A reader with a clone reads them there; a reader without one gets them
from `runner init`.

*Alternative considered:* keep both copies and add a test asserting they carry
the same command. Rejected — the test exists only to police a duplication that
has no reason to exist. Deleting the second copy removes the defect class, not
just today's instance of it.

*Why all three files on every platform:* the machine that edits a unit is not
always the machine that runs it, and per-platform selection would put build
tags on data files to save a reader from two files they can ignore.

### D4. The configuration reference owns structure, not keys

It documents what an example cannot show from inside itself: the three layers
(`roles/_base/base.yaml` → project entry → `defaults`) and which wins; the
environment variables and what each one moves; which file is read from where,
and how `OFFICE_CONFIG_ROOT` changes that. Per-key documentation stays in
`office/tracker.example.yaml` and `office/projects.local.example.yaml`, and the
reference links to them as the authority rather than restating them.

*Alternative considered:* a full key-by-key reference, optionally pinned by a
test against the structs' yaml tags. The owner chose the narrow document. The
trade-off is recorded under Risks.

### D5. The JIRA document is an extract, and `jira-setup.md` stays

`reference/jira-requirements.md` answers one question for someone else's
admin: what must be true of a JIRA instance for the office to work — statuses,
the four custom fields and their types, the link type, `/myself`, the token's
push right. It states each as a requirement with a way to check it, and names
`scripts/jira-setup.sh` as one way to get there rather than the way.

`docs/notes/jira-setup.md` keeps its job — why the setup is shaped like that,
which rakes the polygon hid, why the workflow is done by script when REST
cannot. That is explanation, it is honest as a note, and the new document links
to it.

### D6. README follows the measured conventions, not taste

`docs/notes/readme-conventions.md` measured twelve comparable projects. The
open items this change can close are: platforms named inside the install
section rather than two sections below (item 11), the index at `docs/README.md`
(item 9), and a named channel for problem reports (item 14) — stated as the
owner decided: Issues are off on purpose, one maintainer, vulnerabilities to
`SECURITY.md`. Items 13 (a screenshot or cast) and 17 (CONTRIBUTING) stay open;
neither is blocked by this change.

### D7. Frozen stage plans join their retrospectives

`docs/STAGE-1…5-*.md` move to `docs/notes/stages/`, filenames unchanged.
`docs/notes/` already holds `stage-N-retro.md` for each of them; a frozen plan
and the retrospective that judges it belong in the same journal. This avoids a
new top-level `docs/archive/`, which would collide in the reader's head with
the `archive/*` git tags that `CLAUDE.md` gives a specific meaning.

## Risks / Trade-offs

**The configuration reference drifts from the code** → It is written about
structure, which changes on the order of once per stage, not about keys, which
change per release. Keys stay in the embedded examples, where drift is
impossible. No test; accepted deliberately.

**Moving `STAGE-*.md` breaks links from `docs/comet/archive/`** → Links from
`docs/notes/` are fixed. The Comet archive is not rewritten: editing an
archived verification report to keep a link alive falsifies the record of what
was verified and when. Accepted, and stated in the proposal.

**An outsider has linked a document that moves** → Only ONBOARDING and README
are plausible targets, and both keep their paths. The rest are new or already
obscure.

**`runner init` writing a new directory into a user's home** → Under the
existing never-overwrite rule, with a scenario pinning that an edited unit
survives a second init. An existing installation gains `scheduler/` and loses
nothing.

**Deleting `bootstrap/*.plist|.service|.timer` breaks a reader mid-setup** →
`bootstrap/README.md` gains the new path in the same commit, and the files are
one `runner init` away. Nobody's installed unit is touched; only the samples
in the clone move.

## Migration Plan

No migration. The init change is additive and idempotent: an existing
`${OFFICE_HOME}` gains `scheduler/` on the next `runner init` and nothing else
changes. Rollback is reverting the branch; no state is written that an older
runner would misread.
