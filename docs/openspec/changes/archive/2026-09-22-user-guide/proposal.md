## Why

The roadmap agreed on 2026-09-17 makes the next reader a developer with their
own repository and their own JIRA, who installs from a release and never
clones. The documentation they land on was written for a contributor, and on
three points it is not merely verbose but wrong for them:

- `docs/guide/operations.md` sends them to `bootstrap/` for a scheduler
  sample. The release archive carries the two binaries and nothing else
  (`.goreleaser.yaml:57`, `files: [none*]`), so that directory does not exist
  on their machine. The office has no supported way to run on a schedule for
  the user it is aimed at.
- The samples themselves pin `loop --role implementer`. `cmd/runner/office.go:286`
  defines the flag as "one cycle per role when absent", so a copied sample runs
  implementer forever and analyst and reviewer never start. The pipeline does
  not fail; it stalls, and the reason is invisible.
- `docs/ONBOARDING.md` is 635 lines that mix tutorial, how-to and reference,
  overlap the new 213-line `docs/guide/quickstart.md`, and still walk through a
  clone. Half of it sets up the JIRA polygon, which the target reader does not
  want and cannot use.

Two things a stranger needs are missing outright. An admin asked to prepare a
JIRA instance for the office has no document stating what the office requires
of it — only `scripts/jira-setup.sh`, and an admin who will not run a
stranger's script with admin rights has nowhere to turn. And the configuration
has no reference: the two shipped examples document their own keys well, but
nothing states the three layers, what overrides what, or which environment
variable is read where.

Now, because `install` shipped (PR #11) and the repository is public: the next
person to arrive is a stranger, and every one of these is a wall they hit
before the product gets a chance to work.

## What Changes

**Shipped behavior**

- `runner init` SHALL also lay out the two scheduler samples — a launchd
  `.plist` and a systemd `.service`/`.timer` pair — in `${OFFICE_HOME}`,
  from the embedded payload, by the mechanism that already places the two
  configuration samples (`cmd/runner/init.go:62`). The target reader gets real
  files at real paths instead of a pointer into a directory they do not have.
- The scheduler samples stop pinning `loop --role implementer`. A sample that
  starves two of three roles is a defect, not a default.
- `runner`'s usage text gains `--role` on the `loop` line. Both `tick` and
  `loop` accept it; only `tick` says so (`cmd/runner/main.go:19`).

**Documentation**

- A guide written for `install.sh`: no clone, no Go, no source reading, from
  download to the first task through the pipeline against the reader's own
  repository and tracker.
- A configuration reference that owns what the examples cannot show: the three
  layers (`roles/_base/base.yaml` → project → `defaults`) and their precedence,
  the environment variables (`OFFICE_HOME`, `OFFICE_CONFIG_ROOT`,
  `OFFICE_BACKEND`, `OFFICE_INSTALL_FROM`, the credential variables), and which
  file is read from where. Per-key documentation stays in
  `office/tracker.example.yaml` and `office/projects.local.example.yaml`:
  those ride inside the binary and cannot drift from the release.
- "What the office needs from your JIRA", extracted from
  `docs/notes/jira-setup.md` and addressed to an admin rather than to us:
  statuses, the four custom fields and their types, the link type, `/myself`,
  and the token's push right. The script stays one way to get there, not the
  only way.
- `docs/ONBOARDING.md` loses its polygon half to `bootstrap/jira/README.md`,
  which already documents the container.
- Diátaxis applied to the user path: README, `docs/guide/`, ONBOARDING and the
  new references sorted into tutorial / how-to / reference / explanation, with
  `docs/README.md` as the index the directory has never had.
- The five frozen `docs/STAGE-*.md` (1521 lines of implementation plans for
  Claude Code, referenced only from retrospectives and the Comet archive) leave
  the `docs/` root for an archive subdirectory.
- README names the channel for problem reports: Issues stay disabled on
  purpose, the maintainer is one person, vulnerabilities go to `SECURITY.md`.
  Silence currently reads as an oversight.

## Capabilities

### New Capabilities

None. Documentation structure is not behavior, and inventing a capability to
carry it would put prose under a contract that cannot be verified.

### Modified Capabilities

- `office-install`: the `runner init` requirement changes. It currently states
  that init places **two** samples, and its first scenario asserts the home
  contains **exactly** `projects.local.example.yaml` and
  `tracker.example.yaml`. Both must widen to include the scheduler samples,
  under the same never-overwrite rule that governs the existing two.

## Impact

**Code** — `cmd/runner/init.go` (sample list), `office/payload.go` (a new
top-level entry needs its own embed directive; files inside an already
embedded directory do not), the new sample files under `office/`,
`cmd/runner/main.go:19` (usage text), and the `runner init` tests that assert
the exact contents of a fresh home.

**Shipped artifacts** — `bootstrap/local.office.runner.plist`,
`bootstrap/office-runner.service`. These remain for clone users; the copies
`runner init` writes become the ones the documentation points at, and the two
must not disagree.

**Documentation** — `README.md`, `docs/guide/*`, `docs/ONBOARDING.md`,
`bootstrap/README.md`, `bootstrap/jira/README.md`, `docs/notes/jira-setup.md`
(source of an extract, not deleted), and every relative link into the files
that move.

**Known breakage, accepted** — moving `docs/STAGE-*.md` breaks links from
`docs/notes/` and from `docs/comet/archive/`. The notes get fixed; the Comet
archive does not. Rewriting an archived verification report to keep a link
alive would falsify the record of what was verified and when.

**Not split into several changes** — a separate `bootstrap` hotfix and a
separate Diátaxis branch were both offered to the owner and both declined in
favour of one full cycle carrying everything. Recorded here so the decision is
not rediscovered as an oversight.
