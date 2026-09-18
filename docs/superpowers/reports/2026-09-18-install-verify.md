# Verification Report: install

Date: 2026-09-18 · Mode: **full** (`comet state scale`: 18 tasks, 2 capabilities, 59 files) · Range: `cee778f...303a57b` (plan `base-ref` … build-complete) · Verifier: coordinator session (Opus), evidence gathered fresh in this phase.

## Summary

| Dimension    | Status |
|--------------|--------|
| Completeness | 18/18 tasks `[x]`; 10/10 requirements implemented |
| Correctness  | 27/27 scenarios covered — 24 by automated tests, 6 additionally by the recorded live snapshot check, 2 owner-gated by construction (first tag), 1 by construction + kit presence (bake not run live) |
| Coherence    | D1–D9 followed; Design Doc followed; 3 wording lags in `design.md` recorded as Implementation Divergence (D3, D4, D6); no delta-spec ↔ design contradictions |

**Final assessment:** No CRITICAL or IMPORTANT issues. 1 WARNING (design wording lag — repaired in place), 2 SUGGESTIONS (owner-gated live checks). Ready for archive.

## Fresh evidence (run in this phase)

- `go clean -testcache && go test ./...` → 18 packages `ok`, 0 FAIL.
- Scenario-mapped tests by name (`-v`): 37 `--- PASS`, 0 FAIL (list below).
- `sh scripts/install-test.sh` and `dash scripts/install-test.sh` → `все четыре сценария прошли`.
- Build-phase exit checks (recorded via `comet state record-check … build`, exit 0): `gofmt -l .` empty; `go build/vet/test ./...`; `go test -race ./internal/office/`; `sh scripts/build-validators.sh && go build -tags release ./... && go vet -tags release . && go test -tags release . ./internal/runner/` (checkers 3.70/3.81/3.63 MB, removed after); `validate-result` 3 715 394 B (< 5 MB — payload not linked); `git status --porcelain` empty; `grep -rn "ConfigRoot\b"` → two test names only.
- Secrets scan over the change diff (`sk-ant-`, `ghp_`, AKIA, private keys): 0 hits. New `os.RemoveAll` calls act only on temp dirs the code created; the only new `exec.Command` is the test's `go build`.
- Live snapshot verification (Task 13, `docs/notes/install.md` §Проверка снапшота 2026-09-18): identity `v0.0.1-SNAPSHOT-338d2e7` in GoReleaser log, `runner version`, `run-agent --dry-run` `config_sha:`; checker table exact per D5; `install-test.sh dist` 4× ok; real install into a fresh `OFFICE_HOME`; `runner init`; host checker written by the local dry-run with no `go build`; sbx dry-run wrote an ELF aarch64 `validate-result-linux-arm64`.

## Completeness

- tasks.md: 18/18 checked (`grep -c '^- \[x\]'` = 18, `'^- \[ \]'` = 0); plan checkboxes 0 unchecked; `comet guard install build --apply` passed "tasks.md all tasks checked" and "Superpowers plan all tasks checked".
- Requirements → implementation:

| Requirement | Implementation |
|---|---|
| The runner carries the office and unpacks it by version | `payload.go` (`//go:embed all:roles all:skills all:hooks all:bootstrap/sbx-kits …`), `internal/office/unpack.go` (temp-then-rename), `internal/runner/office.go` `ResolveOffice` (unpack when `Root` absent), `cmd/runner/office.go` + `cmd/run-agent/main.go` wired |
| Unpacked hooks and skill scripts are executable | `unpack.go` `copyTree` (`#!` → 0755 + explicit chmod); repository test `TestExecutablePayloadFilesStartWithShebang` |
| The result checker ships with the runner | `validators_{darwin_arm64,linux_amd64,linux_arm64}.go` (`//go:build release`), `validators_dev.go`, `internal/runner/validator.go` `embeddedValidator` / `noEmbeddedValidator` |
| Every run is signed with the office version | `ResolveOffice` identity → `pipeline.Office.ConfigSHA = office.Identity` (`cmd/runner/office.go:141`), passport `ConfigSHA: office.Identity` (`cmd/run-agent/main.go:170`); `internal/tracker/marker.go` `commitHash` / `shortenSHA` |
| A configuration root keeps the clone behaviour | `ResolveOffice` branch 1 (`ConfigSHA(root)`, nothing unpacked); `EnsureValidator` `SourceClone` → `buildValidator` (previous body); cwd fallback deleted from both commands |
| The sandbox kit is available from the unpacked office | payload includes `bootstrap/sbx-kits` → unpacked as `sbx-kits/`; `bake-comet-template.sh` finds `KIT_DIR` next to itself; `TAG` = `sbx.Template` (`office-claude-comet:0.4.0-beta.20`) |
| `runner init` lays out the home without touching what exists | `cmd/runner/init.go` (`Stat` → `оставлен`/`создан`; samples from `payload.Payload`) |
| `runner version` reports what is installed | `cmd/runner/version.go` (`Resolve{Unpack: false}`) |
| A release provides one archive per platform and a checksum file | `.goreleaser.yaml` (3 targets, `-tags release`, `-X …Version=v{{ .Version }}`, tar.gz per target, `checksums.txt`, `install.sh` extra file, `git.ignore_tags`), `.github/workflows/release.yml`, `scripts/build-validators.sh`, `scripts/release-snapshot.sh` |
| `install.sh` places the binaries without a clone or Go | `install.sh` (platform → fetch → checksum → extract → two-phase install → `runner version` → PATH hint → `дальше: runner init`; `OFFICE_INSTALL_FROM`) |

## Correctness — scenario coverage

office-distribution:

| Scenario | Evidence |
|---|---|
| First run on a fresh office home unpacks the payload | `TestOfficesUnpackPayloadWithoutConfigRoot` (real payload, listing names `office/<v>/workflow.yaml`), `TestResolveOfficeReleaseVersionUnpacksOnce` |
| An unpacked version is left alone | `TestUnpackLeavesExistingOfficeAlone`, `…ReleaseVersionUnpacksOnce` (hand edit kept), `…UnpackPayloadWithoutConfigRoot` (marker file survives) |
| Two versions live side by side | `TestUnpackVersionsSideBySide`, `TestOfficesKeepVersionsSideBySide` |
| A failed unpack leaves no half office behind | `TestUnpackFailureLeavesNoTarget` (+ `TestUnpackRaceYieldsOneOffice` under `-race`) |
| A role with hooks loads from the unpacked office | `TestUnpackLaysOutTreeWithModes`, `…UnpackPayloadWithoutConfigRoot` (`LoadRole(root, "implementer")`), `TestPayloadModeReadsUnpackedOfficeNotCwd` (no "не исполняемый") |
| A non-executable payload script is caught in the repository | `TestExecutablePayloadFilesStartWithShebang` |
| A sandbox run on Apple Silicon needs no Go | `TestEnsureValidatorPayloadWritesEmbeddedOnce` (empty `PATH`, linux/arm64) + live sbx dry-run (ELF aarch64 checker written) |
| A local run uses the host checker | `embeddedValidator` keyed by target; live local dry-run wrote `validate-result-darwin-arm64`, `grep -c 'go build'` = 0 |
| A missing platform is refused loudly | `TestEnsureValidatorPayloadRefusesMissingPlatform`, `…RefusesWhenNothingEmbedded`, `TestPayloadModeReadsUnpackedOfficeNotCwd` (exit 2, names os/arch) |
| A release run is marked with its version | `TestResolveOfficeReleaseVersionUnpacksOnce` (Identity), `TestMarkerShortensOnlyCommitHashes` (`v0.7.0`), wiring above; live `config_sha: v0.0.1-SNAPSHOT-338d2e7` |
| A long version survives the marker | `TestMarkerShortensOnlyCommitHashes` (`v10.12.3`, `v0.0.1-SNAPSHOT-9f2e1c4` round-trip) |
| A source build is marked with its revision | `TestResolveOfficeDirtyBuildKeysDirByContent` (`rev-dirty`), `TestResolveOfficeBuildInfoRevision`, marker test (`<sha>-dirty` → 8 + `-dirty`) |
| A marker without any identity is refused | `TestResolveOfficeRefusesWithoutIdentity` (no build info; no `vcs.revision`) |
| The wrappers behave as before | `TestOfficesCloneModeUnpacksNothing`, `TestResolveOfficeCloneFollowsConfigRoot`, `TestEnsureValidatorBuildsExecutableForHost` / `…CrossCompilesForSandbox` / `…ReplacesStaleBinary` (clone path unchanged) |
| A hand-built binary without the variable uses its payload | `TestPayloadModeReadsUnpackedOfficeNotCwd` (real `go build` of run-agent, run from the clone root) |
| The image bakes from the unpacked kit | by construction: script relocatable (`KIT_DIR` next to script, `sh -n` clean), tag equals `sbx.Template`, kit present in the unpacked office (`…UnpackPayloadWithoutConfigRoot` asserts `sbx-kits/comet-cli/spec.yaml` and the bake script; Task 13 `ls`). **Not run live** — sandbox network is deny-all on this machine (SUGGESTION below) |

office-install:

| Scenario | Evidence |
|---|---|
| A fresh machine gets a home and two samples | `TestInitLaysOutFreshHome` (exact set, byte-equal to payload, `создан` ×2) + live Task 13 |
| A configured home is left untouched | `TestInitLeavesConfiguredHomeAlone` (+ the reverted live incident on `~/.office`: working files untouched) |
| The projects sample works with four edits | `TestShippedProjectsExampleLoadsAfterFourEdits` |
| A release runner names its version and office directory | `TestVersionPrintsIdentityAndOfficeDirWithoutUnpacking` (home still absent) + live |
| A snapshot build yields three runners with their checkers | live Task 13 checker table (darwin/arm64 → darwin-arm64+linux-arm64; each Linux → itself) + `TestValidatorsCarryHostChecker` under `-tags release` |
| A tag triggers the release | by construction (`release.yml`: `push: tags: ['v*']`, `goreleaser release --clean`); owner-gated (SUGGESTION below) |
| Install on a supported platform from a release | shape: install-test scenario 1; live snapshot install; a real release does not exist yet (owner-gated) |
| Install from a local snapshot | live `install-test.sh dist` (4× ok) and real `OFFICE_INSTALL_FROM=dist sh install.sh` |
| A checksum mismatch aborts the install | install-test scenario 3 (exit ≠ 0, `контрольная сумма`, `bin/` absent) |
| An unsupported platform is refused by name | install-test scenario 4 (`darwin/amd64`, lists `linux/arm64`) |
| Updating replaces the binaries and nothing else | install-test scenario 2 (working files and `office/v0.0.0-old/` byte-identical) |

## Coherence

- **design.md D1–D9:** D1 root package + `all:` ✓; D2 shebang rule + repository test ✓; D3 temp-then-rename keyed by identity, dirty builds by content (`<rev12>-dirty-<hash8>`) ✓ (suffix wording lag → recorded); D4 four branches in order, refusal, marker rule ✓ (field name `Source`, snapshot base `0.0.1` → recorded); D5 per-target `//go:build release` files, dev empty set, `EnsureValidator` split ✓; D6 GoReleaser + one workflow + `install.sh` extra file ✓ (archive name without version → recorded); D7 `install.sh` behaviour ✓ (plus `main()` wrap, two-phase install, `|| die` from the final review); D8 `init`/`version` ✓; D9 docs ✓ (DESIGN §2.5, README, sbx-kits README, contracts, `docs/notes/install.md`).
- **Design Doc (`docs/superpowers/specs/2026-09-17-install-design.md`):** exists, is the change's `design_doc`; §1.1 `Source`, §2.2 no-version archive names agree with the code; §1.3 `<pid>` lags (same D3 note).
- **Delta spec ↔ design contradictions:** none (specs name no archive format or field names). No incremental spec edits happened during build.
- **proposal.md goals:** office ships in the binary ✓; checker embedded ✓; identity is a version ✓; `OFFICE_CONFIG_ROOT` developer switch ✓ (cwd fallback removed — documented BREAKING for hand-built binaries without the variable); `runner init`/`version` ✓; GitHub releases tooling ✓ (first tag owner-gated); DESIGN §2.5 ✓. "Not in this change" list honoured (no `doctor`, self-update, brew, darwin/amd64, auto-bake, plist fixes, `eval-roles` untouched).
- **Code patterns:** Russian doc-comments/CLI strings, English identifiers, gofmt-clean, one file per subcommand in `cmd/runner`, injectable seams (`payloadFS`, `validatorsFS`, `readBuildInfo`) mirror existing test style; attribution trailers name the model that wrote each commit.

## Issues

### CRITICAL
None.

### WARNING
1. **`design.md` wording lagged the implementation on D3/D4/D6** (temp suffix, `Mode`→`Source`, archive name). Repaired in this phase: "Implementation Divergence" section appended to `docs/openspec/changes/install/design.md` (verify-phase allowed artifact). No behaviour change.

### SUGGESTION
1. **Owner-gated live checks** — "A tag triggers the release" and "Install on a supported platform from a release" can only be proven by the first `v*` tag after the repository is public (design.md Migration Plan). Before that tag: run `go test ./...` once on Linux (the suite has only ever run on darwin/arm64; `docs/notes/install.md` §Перед первым тегом).
2. **"The image bakes from the unpacked kit"** was not executed live (sandbox network closed on this machine); the script is unchanged apart from `KIT_DIR` and the tag equals `sbx.Template`. Run `sh "${OFFICE_HOME}/office/<версия>/sbx-kits/bake-comet-template.sh"` once on a machine with registry access when convenient.

## Build-phase review trail

Per-task reviews (`review_mode: standard`, risk-triggered): Tasks 2, 3, 4, 5, 7, 8, 9, 10, 11, 12 reviewed — all Approved; final whole-branch review (Opus): "With fixes" — 0 Critical, 2 Important (test portability under `-tags release`; Linux-run gap → note), 8 Minor; one fix wave (`26e3fb8`), scoped re-review: all addressed, no new breakage. Ledger with rulings R1–R13: `.superpowers/sdd/2026-09-17-install/progress.md`.
