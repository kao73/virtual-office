## Context

See proposal.md — Why. What shapes the approach:

- The clone is touched in exactly four places: `configRoot()` in
  `cmd/runner/office.go` and `cmd/run-agent/main.go` (env var, else cwd);
  `runner.EnsureValidator` (`go build ./cmd/validate-result` from that root
  on every run, per backend platform); `runner.ConfigSHA` (`git rev-parse`
  + `git status --porcelain`); and `eval-roles`, which stays a clone tool.
  Everything downstream — `LoadRole`, `SkillDirs`, `HookFiles`, the Claude
  adapter that *copies* hooks and skills into a per-run temp dir — reads
  from whatever root it is handed.
- `embed.FS` has no file modes and skips `_`/`.`-prefixed paths unless the
  pattern carries `all:`. `roles/_base` is one such path, and five payload
  files are executable (`hooks/*.sh`, `skills/comet/scripts/comet-hook-router.mjs`,
  two `.sh` under `skills/brainstorming`); `LoadRole` refuses a
  non-executable hook by design.
- The marker format shortens `ConfigSHA` to eight runes (`shortenSHA`);
  `ParseMarker` takes any non-empty value under `config:`.
- The `sbx` backend runs a Linux microVM of the host's architecture
  (Apple Silicon → linux/arm64; x86 hosts → linux/amd64), so the checker
  platform is a function of the host platform, not a runtime choice.
- The sandbox image tag is a constant in `internal/backends/sbx`
  (`office-claude-comet:<comet version>`) and is baked from
  `bootstrap/sbx-kits/comet-cli` by a script; both are versioned with the
  vendored `comet` package, not with the office.

## Goals / Non-Goals

**Goals:**
- One code path resolves "where is the office and what is its identity" for
  both `runner` and `run-agent`, with two modes: clone (`OFFICE_CONFIG_ROOT`
  set) and payload (unset).
- A release build is self-sufficient: no `go`, no `git` needed at run time.
- A developer's `go build ./cmd/runner` still compiles and still works from
  a clone through the wrappers.

**Non-Goals:**
- Changing what the office *contains* or how roles are loaded, copied, or
  mounted.
- Versioning the sandbox image with the office (it stays tied to `comet`).
- Any upgrade or cleanup of old `office/<version>/` directories.

## Decisions

### D1. A root package holds the `go:embed` payload
`package office` at the module root (`payload.go`) embeds
`all:roles all:skills all:hooks workflow.yaml budgets.yaml
tracker.example.yaml all:bootstrap/sbx-kits/comet-cli` and exposes the FS
plus a payload content hash. *Why here:* embed patterns cannot leave the
package directory and cannot contain `..`; only the root can see all of
those paths without copying them. *Alternatives:* a `go generate` step that
copies the payload under `internal/office/` (two copies of the roles in the
tree, drift risk); embedding a tarball built at release time (dev builds
from source would have no payload at all).

### D2. Executable bits come back from the shebang, and a test guards it
On unpack, a file whose first two bytes are `#!` is chmodded `0755`; every
other file is `0644`. A repository test walks the payload and fails for any
file that is executable in git but does not start with `#!`. *Why:* no
manifest to generate or keep in sync; the invariant is checked where the
payload lives. *Alternatives:* a manifest from `git ls-files -s` (another
generated artifact); tar with modes (see D1).

### D3. Unpack is temp-then-rename, keyed by identity; dirty builds key by content
The payload is copied to `${OFFICE_HOME}/office/.unpack-<id>-<pid>/`, then
renamed to `office/<dir>/`; if the rename finds the target already there,
the temp dir is removed and the existing one is used. `<dir>` is the
identity for a release or a clean source build. For a `-dirty` build the
payload content is not a function of the identity, so `<dir>` is
`<identity>-<payload hash, 8 hex>` — idempotent, never rewriting, and no
two dirty builds collide. *Alternatives:* always re-unpack dirty builds
(violates "an unpacked version is left alone"); content hash for every
build (users would not find `office/v0.7.0`).

### D4. Identity: ldflags version, else build info, else clone git
`runner.ResolveOffice()` returns `{Root, Identity, Mode}`:
1. `OFFICE_CONFIG_ROOT` set → `Mode: clone`, root is that directory,
   identity is `git rev-parse HEAD` + `-dirty` as today.
2. Else a version injected at build time (`-X <pkg>.Version=v0.7.0`) →
   `Mode: payload`, identity is that string.
3. Else `debug.ReadBuildInfo()` settings `vcs.revision` + `vcs.modified` →
   `Mode: payload`, identity `<revision>` with `-dirty` when modified.
4. Else refuse: "this build carries no identity".
The identity travels through the existing `ConfigSHA` fields (passport,
ledger, `Office`, markers); their name is misleading now and a rename is a
mechanical follow-up left to the plan if it stays within scope. The marker
shortens a value only when it is a 40-hex commit hash (optionally followed
by `-dirty`); anything else is written whole. GoReleaser templates the
version as `v{{ .Version }}` so a tag `v0.7.0` yields `v0.7.0` and a
snapshot yields `v0.7.1-SNAPSHOT-<sha>`.

### D5. Checkers are embedded per target under a build tag
Cross-built `validate-result` binaries live in a gitignored
`payload/validators/` (a tracked `README.md` keeps the directory). Files
`validators_darwin_arm64.go`, `validators_linux_amd64.go`,
`validators_linux_arm64.go` carry `//go:build release` and per-target embed
directives (darwin/arm64 embeds darwin/arm64 + linux/arm64; each Linux
target embeds itself); `validators_dev.go` (`//go:build !release`) exposes
an empty set. `EnsureValidator` becomes: clone mode → `go build` as today;
payload mode → write the embedded checker for the requested platform to
`office/<dir>/bin/validate-result-<os>-<arch>` (once, 0755) or refuse
naming the platform and the tag. *Why:* GOOS/GOARCH file suffixes give
per-target embedding without templating, and the `release` tag keeps
`go build` from a clone compiling with nothing to embed. *Alternative:*
embed all three checkers in every runner (+5 MB each, and the spec's
"carries what its platform needs" becomes "carries everything").

### D6. GoReleaser + one workflow; `install.sh` is a release asset
`.goreleaser.yaml` (v2): a `before` hook cross-builds the three checkers
with `CGO_ENABLED=0` into `payload/validators/`; two builds (`runner`,
`run-agent`) for the three targets with `-tags release` and the version
ldflag; `archives` as `tar.gz` named `virtual-office_<version>_<os>_<arch>`;
`checksums` (sha256); `release.extra_files: [install.sh]`.
`.github/workflows/release.yml` runs on `push: tags: ['v*']` with
`goreleaser/goreleaser-action` and `GITHUB_TOKEN`. Verification before any
tag: `goreleaser release --snapshot --clean` locally, then
`install.sh` with `OFFICE_INSTALL_FROM=dist/`. *Alternative:* a hand-rolled
`scripts/release.sh` (more to maintain, no checksums/notes for free).

### D7. `install.sh`: POSIX sh, GitHub Releases, `${OFFICE_HOME}/bin`
Detects `uname -s`/`uname -m`, maps to the three targets, refuses others by
name listing the supported ones. Resolves the version (`OFFICE_VERSION` or
first argument, default `latest` via the GitHub `releases/latest/download`
redirect), fetches archive + checksum file, verifies with `sha256sum` or
`shasum -a 256`, extracts to a temp dir, moves `runner` and `run-agent`
into `${OFFICE_HOME:-$HOME/.office}/bin` with `0755`, prints the location,
whether it is on `PATH`, and "next: runner init". `OFFICE_INSTALL_FROM=<dir>`
short-circuits the download for snapshot verification. Needs `curl` or
`wget`, `tar`, and one of the two checksum tools.

### D8. `runner init` and `runner version` are plain subcommands
`init` uses `runner.Home()`, `os.MkdirAll`, and writes each sample only
when `os.Stat` says it is absent; the two sample texts come from the
payload (`tracker.example.yaml`) and a new tracked
`projects.local.example.yaml` (added to the payload list and to the
boundary test's named files). `version` prints the identity and the
resolved `office/<dir>` path via `ResolveOffice` without unpacking.

### D9. Docs move with the code
DESIGN §2.5: replace the `git pull` bullet with the version model (one
version for binary + office; identity in every marker; update = run
`install.sh` again and restart the loop; the runner never updates itself;
`${OFFICE_HOME}` is never touched by an update; incompatibility is a
refusal with an address, not a migration). README: "what the machine needs"
gains the install path first and marks Go as source-build only;
`bootstrap/sbx-kits/README.md` says the kit also lives in the unpacked
office.

## Risks / Trade-offs

- [Binary size ≈ 13 MB of payload (6.6 MB skills, 6.4 MB kit tarballs) plus
  2–5 MB of checkers] → acceptable for a CLI; the kit tarballs are the
  largest single item and were an explicit choice (one version for
  binary + office + image).
- [Dirty dev builds accumulate `office/<sha>-dirty-<hash>/` directories]
  → bounded by how often a developer runs a hand-built binary without the
  wrappers; cleanup is out of scope and cheap by hand (`rm -r office/*-dirty-*`).
- [`vcs.revision` is absent when built outside a git checkout or with
  `-buildvcs=false`] → D4 step 4 refuses loudly instead of signing runs
  with nothing; the message names both fixes (build with vcs, or set
  `OFFICE_CONFIG_ROOT`).
- [A `release`-tagged build without the cross-built files fails to compile]
  → that is the intent (an incomplete release must not build); the
  GoReleaser `before` hook is the only supported way to produce one.
- [Removing the cwd fallback] → only hand-built binaries run from the clone
  root without the wrappers change behaviour; the wrappers and every
  documented command are unaffected.
- [`os.CopyFS` from `embed.FS` yields 0644 files and refuses to overwrite]
  → unpack always targets a fresh temp dir; modes are set afterwards (D2).

## Migration Plan

No user migration: this is the first release. Developers keep using
`bin/*`. Rollout: merge → owner pushes the first tag → workflow publishes
the release → `install.sh` works against `latest`. Rollback: delete the
release/tag; nothing on any machine changes until `install.sh` is run
again.

**Before the first tag — owner's actions, outside this change.** D7's
unauthenticated download assumes a public repository; today
`kao73/virtual-office` is private, and release assets of a private
repository are not reachable without a token. Decided 2026-09-17: the
repository goes public before the first release. Checklist:
1. Add a `LICENSE` (the vendored `@rpamis/comet` and `@fission-ai/openspec`
   are MIT with their license files in the tarballs; a history scan for key
   patterns found nothing; tracked docs mention only the owner's own
   polygon).
2. Switch the repository to public. Archive tags and their concept
   documents become visible; this is not reversible in practice.
3. Confirm `.github/workflows/claude-review.yml` answers only users with
   write permission (the action's default) — on a public repository anyone
   can comment `@claude` on a pull request.
Until these are done, `install.sh` against `latest` fails with a 404, and
the snapshot path (`OFFICE_INSTALL_FROM`) is the only way to install.

## Open Questions

- Whether `run-agent` also gets a `version` subcommand (harmless either
  way; the plan may add it if it costs one function).
- Whether the ledger/passport field keeps the name `config_sha` for
  compatibility with existing `ledger.jsonl` rows or is renamed with a
  read-side alias; either satisfies the spec.

## Implementation Divergence

Recorded at verify (2026-09-18). Each item is a wording lag of this file
behind the Design Doc (`docs/superpowers/specs/2026-09-17-install-design.md`)
and the plan; the decisions themselves are followed.

- **D3 — temp directory suffix.** The unpack temp dir is
  `office/.unpack-<name>-<random>/` from `os.MkdirTemp`, not `-<pid>`: two
  goroutines of one process (the `TestUnpackRaceYieldsOneOffice` case) must
  not share a temp dir. Stated in the plan (Task 2 Interfaces).
- **D4 — field name.** `runner.Office` carries `Source` (`SourceClone` |
  `SourcePayload`), not `Mode`; the Design Doc §1.1 already uses `Source`.
  The snapshot identity for a tree with no release tag is
  `v0.0.1-SNAPSHOT-<sha>` (`incpatch` of `0.0.0`), and `git.ignore_tags:
  ["archive/*"]` keeps the archive tags from being taken as the current tag.
- **D6 — archive names.** Archives are `virtual-office_<os>_<arch>.tar.gz`
  with no version in the name, so `releases/latest/download/<name>` resolves
  without an API call for the tag (Design Doc §2.2). The version travels in
  the binaries (`runner version`) and in `checksums.txt`'s release.
