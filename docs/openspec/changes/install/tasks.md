## 1. Payload: embed, unpack, executable bits

- [x] 1.1 Add root `package office` (`payload.go`) embedding `all:roles all:skills all:hooks workflow.yaml budgets.yaml tracker.example.yaml all:bootstrap/sbx-kits/comet-cli`; expose the FS and a content hash; add `projects.local.example.yaml` to the tree and to the payload
- [x] 1.2 Repository test: every payload file that is executable in git starts with `#!` (D2), and the payload list covers every role directory, `roles/_base/base.yaml`, both hook scripts, and the kit's bake script
- [x] 1.3 `office.Unpack(home, dir)`: temp-then-rename into `${OFFICE_HOME}/office/<dir>/`, `0755` for shebang files, existing target wins, interrupted unpack leaves no target (D3); tests for first unpack, idempotence, side-by-side versions, and the failure path

## 2. Identity and office resolution

- [x] 2.1 `runner.ResolveOffice()`: clone mode from `OFFICE_CONFIG_ROOT` (git identity as today), payload mode from ldflags version → `vcs.revision`/`vcs.modified` → refusal; directory name per D3 for `-dirty`; tests for each branch with injected build info
- [x] 2.2 Marker: shorten only 40-hex commit hashes (with optional `-dirty`), write versions whole; round-trip tests for `v0.7.0`, `v10.12.3`, `<sha>-dirty`
- [x] 2.3 Wire `cmd/runner/office.go` and `cmd/run-agent/main.go` to `ResolveOffice`; remove the cwd fallback; configuration listing prints the unpacked office paths; boundary test and existing tests still pass under `OFFICE_CONFIG_ROOT`

## 3. Embedded result checkers

- [x] 3.1 `payload/validators/` with tracked `README.md` and gitignore rule; per-target `//go:build release` files embedding the checkers each platform needs, plus the `!release` empty set (D5)
- [x] 3.2 `EnsureValidator`: clone mode → `go build` as today; payload mode → write the embedded checker to `office/<dir>/bin/validate-result-<os>-<arch>` once, or refuse naming the platform and the `release` tag; tests with a fake embedded set

## 4. `runner init` and `runner version`

- [x] 4.1 `runner init`: create the home, place `projects.local.example.yaml` and `tracker.example.yaml` only when absent, never touch the working files, print created/kept/next step; tests for fresh home and configured home
- [x] 4.2 `runner version`: print identity and resolved office directory without unpacking or opening configuration; test that the directory is not created
- [x] 4.3 Loader test: a copy of `projects.local.example.yaml` with only key, `repo_url`, `default_branch`, `tracker` edited loads and lists the project

## 5. Release tooling

- [x] 5.1 `.goreleaser.yaml`: `before` hook cross-building the three checkers, builds for `runner` and `run-agent` on darwin/arm64, linux/amd64, linux/arm64 with `-tags release` and `-X …Version=v{{ .Version }}`, tar.gz archives, sha256 checksums, `install.sh` as extra release file (D6)
- [x] 5.2 `.github/workflows/release.yml` on `push: tags: ['v*']` running GoReleaser with `GITHUB_TOKEN`
- [ ] 5.3 `install.sh`: platform detection and refusal by name, version resolution (`latest` default), download or `OFFICE_INSTALL_FROM`, checksum verification before unpack, install into `${OFFICE_HOME}/bin`, closing lines (D7); shell test against a snapshot `dist/` covering install, update-in-place, checksum mismatch, unsupported platform
- [ ] 5.4 Snapshot verification: `goreleaser release --snapshot --clean`, then `install.sh` from `dist/` on this machine, `runner version`, `runner init`, and a `runner tick` on the mock project showing `config:v…-SNAPSHOT-…` in the ticket

## 6. Documentation

- [ ] 6.1 DESIGN §2.5: replace the `git pull` bullet with the version/update model (D9)
- [ ] 6.2 README: install path first, Go marked as source-build only, `runner init`/`runner version`, wrappers as the developer path; `bootstrap/sbx-kits/README.md`: bake from the unpacked office
- [ ] 6.3 `docs/notes/install.md`: what the release contains, how identity is derived in each mode, and how the snapshot verification in 5.4 was run
