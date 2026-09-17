## Why

Today the only way to run the office is to clone the repository and have Go
on the machine: the `bin/*` wrappers `go build` the runner on every call,
`EnsureValidator` `go build`s the Stop-hook checker on every run, and the
run marker `config:<sha>` is read with `git` from that clone. The roadmap
agreed on 2026-09-17 makes the next user a developer with their own GitHub
repository and their own JIRA — not a contributor to this one — and for them
"clone, install Go, edit nothing" is not an install path. `config-cleanup`
(PR #10) already moved every instance value out of the tree; what is left is
to make the tree itself unnecessary on the target machine.

## What Changes

- **The office ships inside the runner binary.** `roles/`, `skills/`,
  `hooks/`, `workflow.yaml`, `budgets.yaml`, `tracker.example.yaml` and the
  sandbox kit `bootstrap/sbx-kits/comet-cli` (spec, vendored tarballs, bake
  script) are embedded with `go:embed`. On first use the runner unpacks them
  to `${OFFICE_HOME}/office/<version>/` and reads roles, skills, hooks, the
  workflow graph and default budgets from there. An existing unpacked
  version is never rewritten; a different version unpacks next to it.
- **`validate-result` is built at release time and embedded.** The runner
  carries the Stop-hook checker for its host platform and, where the `sbx`
  backend runs a Linux microVM on that host, for the sandbox platform as
  well. A release user needs no Go toolchain. Building from a clone keeps
  the present `go build` path.
- **Run identity is a version, not a git SHA.** A release build stamps every
  marker, ledger row and run passport with `config:v<X.Y.Z>`. A build from
  source without a release version uses the module's `vcs.revision`, with
  `-dirty` when `vcs.modified` is set — the binary, not a working tree, is
  what the office was taken from. The marker format keeps versions intact
  (the eight-character shortening applies to SHAs only).
- **`OFFICE_CONFIG_ROOT` stays the developer switch.** When set, the runner
  behaves exactly as today: roles from that tree, `git rev-parse` + `-dirty`
  for the marker, `go build` for the validator. The `bin/*` wrappers keep
  setting it. When unset, the embedded office is used — the current
  fallback to the working directory is removed. **BREAKING** only for
  someone running a hand-built binary from the clone root without the
  wrappers and without the variable.
- **`runner init`** creates `${OFFICE_HOME}` and drops two copy-ready
  samples — `projects.local.example.yaml` (new, today the sample lives only
  in README prose) and `tracker.example.yaml` — creating only what is
  missing and touching nothing that exists. **`runner version`** prints the
  version and where the office of that version is unpacked.
- **Releases on GitHub.** A GoReleaser configuration and a tag-triggered
  workflow build `runner` and `run-agent` for darwin/arm64, linux/amd64 and
  linux/arm64 and publish them as GitHub Release assets together with
  `install.sh`. `install.sh` picks the platform, downloads the archive,
  verifies its checksum and places the binaries in `${OFFICE_HOME}/bin`
  (respecting `OFFICE_HOME`), then says how to put that directory on `PATH`.
  Cutting the first tag is the owner's action after merge; the change
  delivers the tooling and verifies it against a snapshot build. The
  download is unauthenticated, which assumes a public repository: the
  repository is private today and the owner decided (2026-09-17) to make it
  public before the first release — adding a `LICENSE` and checking the
  `@claude` review workflow's permission gate on the way (design.md,
  Migration Plan). Neither step is part of this change.
- **DESIGN §2.5** loses the paragraph about `git pull` of the config repo
  and states the new update model: one version for binary, office and
  sandbox image; updating is running `install.sh` again and restarting the
  loop; the runner never updates itself.

Not in this change: `brew`, nightly builds, `doctor`, self-update, migration
of an existing `${OFFICE_HOME}`, darwin/amd64 (no `sbx` there), automatic
baking of the sandbox image (the kit ships, the bake stays a manual command),
the user guide and `bootstrap/*.plist|.service` corrections (roadmap item 2),
and `eval-roles` — it reads `evals/` from a clone and stays a developer tool.

## Capabilities

### New Capabilities
- `office-distribution`: the office payload embedded in the runner, its
  versioned unpacking into `${OFFICE_HOME}/office/<version>/`, executable
  bits restored on unpack, the embedded `validate-result` per platform,
  the version-or-revision run identity, and the `OFFICE_CONFIG_ROOT`
  developer switch that preserves today's clone behaviour.
- `office-install`: `runner init` and `runner version`, the release
  artifacts (three platforms, checksums, `install.sh`) and what `install.sh`
  guarantees on a machine without Go or a clone.

### Modified Capabilities
- (none) — `config-boundary` keeps every requirement as written: the clone
  path is preserved unchanged under `OFFICE_CONFIG_ROOT`, and a release
  build satisfies "no `-dirty` suffix" trivially. `tracker.example.yaml`
  stays copy-ready; `runner init` merely places it.

## Impact

- **New root package** (`package office` at the module root) holding the
  `go:embed` directives — embed patterns cannot climb out of their package
  directory, and the payload spans `roles/`, `skills/`, `hooks/`,
  `bootstrap/sbx-kits/`, and the root YAML files. `roles/_base` needs the
  `all:` prefix (underscore-prefixed paths are skipped by default);
  `embed.FS` carries no file modes, so unpacking restores `+x` from the
  shebang, guarded by a repository test that every executable in the payload
  starts with one.
- `cmd/runner` (`office.go`: config-root resolution; new `init`, `version`),
  `cmd/run-agent` (same resolution), `internal/runner` (`configsha.go`
  becomes identity resolution; `validator.go` gains the embedded path),
  `internal/tracker/marker.go` (`shortenSHA` must not truncate a version).
- New: `.goreleaser.yaml`, `.github/workflows/release.yml`, `install.sh`,
  `projects.local.example.yaml`, a gitignored directory for cross-built
  validators that GoReleaser fills before the runner build.
- Docs: `docs/DESIGN.md` §2.5, README ("what the machine needs" — Go only
  for building from source; install path first), `bootstrap/sbx-kits/README.md`
  (bake from the unpacked office, not only from a clone).
- Tests: `internal/tracker/boundary_test.go` covers the payload list;
  new tests for unpack idempotence, executable bits, identity resolution,
  marker formatting with a version, and `runner init` creating only what is
  missing.
- Runtime dependencies: none new in Go; GoReleaser runs in CI only.
