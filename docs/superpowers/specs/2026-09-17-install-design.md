---
comet_change: install
role: technical-design
canonical_spec: openspec
---

# install — technical design

Deepens `docs/openspec/changes/install/design.md` (D1–D9). Requirements
live in `docs/openspec/changes/install/specs/office-distribution/spec.md`
and `specs/office-install/spec.md`; this document says how they are met,
where the edges are, and how each unit is tested. It does not restate
them.

## 1. Units and interfaces

```
module root: package office            ← data only, imports nothing internal
  payload.go        Payload   embed.FS  all:roles all:skills all:hooks workflow.yaml
                                        budgets.yaml tracker.example.yaml
                                        projects.local.example.yaml all:bootstrap/sbx-kits
  version.go        Version   string    set by -X github.com/kao73/virtual-office.Version
  validators_darwin_arm64.go  //go:build release   Validators = payload/validators/{darwin-arm64,linux-arm64}
  validators_linux_amd64.go   //go:build release   Validators = payload/validators/linux-amd64
  validators_linux_arm64.go   //go:build release   Validators = payload/validators/linux-arm64
  validators_dev.go           //go:build !release  Validators = empty fs

internal/office                        ← behaviour over the payload, imports root + stdlib
  Unpack(src fs.FS, officeDir, name string) (root string, err error)
  Hash(src fs.FS) string                 sha256 over (path, content) in WalkDir order

internal/runner                        ← owns the resolution, imports internal/office
  type Office struct{ Root, Identity string; Source Source }   // Source: SourceClone | SourcePayload
  ResolveOffice(opts Resolve) (Office, error)                  // Resolve{Unpack bool}
  EnsureValidator(o Office, target Platform) (string, error)

cmd/runner, cmd/run-agent              ← both call ResolveOffice; no private configRoot()/officeRoot()
```

### 1.1 `office.Office` replaces `configRoot string`

Every place that carries the config root today carries an `Office`:
`runagent.Options.ConfigRoot`, `pipeline.SandboxAgent.ConfigRoot`,
`pipeline.Office.ConfigRoot`. `pipeline.Office.ConfigSHA` keeps its name
and receives `Office.Identity`. The wire names do not change: the marker
key `config:` is the comment protocol already written into tickets, and
`config_sha` in `ledger.jsonl` is in rows already on disk. The Go
identifiers `ConfigSHA` stay too — twenty-odd references — with their
doc-comments rewritten to "office identity: release version or commit
hash". Renaming them is not part of this change.

### 1.2 `ResolveOffice` — four branches, first match wins

| # | Condition | Root | Identity | Source |
|---|---|---|---|---|
| 1 | `OFFICE_CONFIG_ROOT` set | that directory | `git rev-parse HEAD` + `-dirty` if `git status --porcelain` is non-empty (today's `ConfigSHA`) | clone |
| 2 | `office.Version != ""` | `${OFFICE_HOME}/office/<Version>` | `Version` (e.g. `v0.7.0`, `v0.0.1-SNAPSHOT-abc1234`) | payload |
| 3 | build info has `vcs.revision` | `${OFFICE_HOME}/office/<rev[:12]>` or `<rev[:12]>-dirty-<Hash(Payload)[:8]>` | `<rev>` or `<rev>-dirty` (full 40 hex) | payload |
| 4 | none | — | — | refusal |

Branch 3 reads `debug.ReadBuildInfo()` through a package variable
`readBuildInfo` so tests can inject settings. The refusal text names both
fixes: build from a git checkout (or with `-buildvcs`), or set
`OFFICE_CONFIG_ROOT`.

In branches 2–3, `Resolve{Unpack: true}` (used by everything that goes
through `newOffices` — `tick`, `loop`, `reap`, `complete-splits`, `ls`,
`worktree` — and by `run-agent`) calls `office.Unpack` when `Root` does
not exist. `Resolve{Unpack: false}` only computes; `version` uses it.
`ledger`, `mock` and `init` never resolve the office: the first two read
only `${OFFICE_HOME}`, and `init` takes its samples straight from the
payload (1.6). The cwd fallback is gone from both
commands; `cmd/eval-roles` keeps its own `officeRoot()` untouched — it is a
clone tool reading `evals/`.

The dirty directory name carries a content hash because two dirty builds
from the same commit can embed different roles; keying the directory by
content keeps "an unpacked version is left alone" true without ever
rewriting one.

### 1.3 `office.Unpack` — temp, walk, rename

```
tmp := <officeDir>/.unpack-<name>-<pid>
fs.WalkDir(src): for each file
    rel := strip "bootstrap/" prefix if present          // bootstrap/sbx-kits/… → sbx-kits/…
    mode := 0644; if first two bytes == "#!" { mode = 0755 }
    os.MkdirAll(dir(tmp/rel)); os.WriteFile(tmp/rel, data, mode)
os.Rename(tmp, <officeDir>/<name>)
    ok                        → return
    target exists (EEXIST / ENOTEMPTY) → os.RemoveAll(tmp); return existing
    other error               → os.RemoveAll(tmp); return error
```

Own walk, not `os.CopyFS`: modes and the prefix rewrite happen in one pass,
and `CopyFS` refuses to write over anything, which the temp directory
never needs. `WriteFile` sets the mode on creation; umask may drop group
and world bits, so an explicit `os.Chmod` follows for `0755` files (the
same precaution `copyExecutable` in the adapter takes). Rename is atomic on
one filesystem; the temp lives in the same parent as the target for that
reason.

Orphan temp directories from a killed process are left alone. Two `tick`s
under cron may unpack at the same time, and a sweep would delete a live
temp. The cost is a stray `.unpack-*` directory after a crash — visible,
harmless, removable by hand.

### 1.4 `EnsureValidator`

| Source | Behaviour |
|---|---|
| clone | `go build` from `Root` into `${OFFICE_HOME}/bin/validate-result-<os>-<arch>` — today's code, unchanged |
| payload | `office.Validators.Open("payload/validators/validate-result-<os>-<arch>")`; write once to `<Root>/bin/validate-result-<os>-<arch>` with `0755`; if the entry is absent, refuse: "раннер собран без ограждения под <os>/<arch>: соберите с `-tags release` или задайте OFFICE_CONFIG_ROOT" |

The payload-mode file lives under `office/<dir>/bin/`, next to the office
it belongs to, so two versions never share a checker. Which platforms a
runner carries follows from the build target (GOOS/GOARCH filename
suffixes + the `release` tag): darwin/arm64 → host and linux/arm64;
each Linux target → itself, which serves both `local` and `sbx`.

### 1.5 Marker

`shortenSHA` shortens only values matching `^[0-9a-f]{40}(-dirty)?$`
(the hash part to eight characters, `-dirty` kept); every other value is
written whole. `ParseMarker` is unchanged. `v10.12.3` round-trips intact.

### 1.6 `runner init`, `runner version`, start-up listing

- `init`: `runner.Home()`, `os.MkdirAll`; for each of
  `projects.local.example.yaml` and `tracker.example.yaml`: if `os.Stat`
  says absent, write the copy read straight from `office.Payload` with
  `0644` and print `создан`, else print `оставлен`. It does not call
  `ResolveOffice` and unpacks nothing: the samples are the embedded ones
  in every mode, including a clone build. Never touches
  `projects.local.yaml`, `tracker.yaml`, `budgets.yaml`. Ends with the
  next step: copy each sample to its working name and edit. Exit 0 in
  every non-error case.
- `version`: `ResolveOffice(Resolve{Unpack:false})`; prints
  `runner <identity>` and `офис: <Root>`; opens no configuration file.
- The `конфигурация:` listing printed by `newOffices` gains a first line:
  `офис: v0.7.0 → /Users/x/.office/office/v0.7.0` or
  `офис: клон /path/to/clone (git 9f2e1c-dirty)`. The existing per-file
  lines then name paths under that root.

### 1.7 `projects.local.example.yaml`

New tracked file at the repository root, in the payload, and listed among
the boundary test's named files. Shows one project for the target user —
own GitHub repository, own JIRA — with the four keys that must be edited
(`<KEY>`, `repo_url`, `default_branch`, `tracker: jira`) and every
optional key commented with one line of purpose (`branch_prefix`,
`worktree_root`, `forge: github`, `auto_merge`, `network`, `tools`, and
the reserved `defaults`). No absolute paths, no `customfield_`, so the
boundary test passes as written.

### 1.8 `bake-comet-template.sh`

`KIT_DIR` becomes `$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)/comet-cli`
so the script works from `bootstrap/sbx-kits/` in a clone and from
`office/<dir>/sbx-kits/` after unpack. Tag and behaviour unchanged.

## 2. Release pipeline

### 2.1 `scripts/build-validators.sh`

```sh
for target in darwin/arm64 linux/amd64 linux/arm64; do
  GOOS=${target%/*} GOARCH=${target#*/} CGO_ENABLED=0 \
    go build -trimpath -o "payload/validators/validate-result-${target%/*}-${target#*/}" ./cmd/validate-result
done
```

`payload/validators/` is gitignored except `README.md` (which explains the
directory and keeps it present so the `release` embed patterns have a
directory to look in).

### 2.2 `.goreleaser.yaml` (version 2)

- `before.hooks: [sh scripts/build-validators.sh]`
- `builds`: ids `runner` (`./cmd/runner`) and `run-agent`
  (`./cmd/run-agent`); `env: [CGO_ENABLED=0]`; `flags: [-trimpath,
  -tags=release]`; `ldflags: [-s -w -X github.com/kao73/virtual-office.Version=v{{ .Version }}]`;
  `targets: [darwin_arm64, linux_amd64, linux_arm64]`.
- `archives`: one, `formats: [tar.gz]`, `ids: [runner, run-agent]`,
  `name_template: "{{ .ProjectName }}_{{ .Os }}_{{ .Arch }}"` — **no version
  in the name**, so `releases/latest/download/virtual-office_darwin_arm64.tar.gz`
  resolves without an API call to learn the tag.
- `checksum.name_template: checksums.txt`.
- `release.extra_files: [{glob: ./install.sh}]`.
- `snapshot.version_template` left at the default
  (`{{ incpatch .Version }}-SNAPSHOT-{{ .ShortCommit }}`); with no tags
  yet that yields `0.0.1-SNAPSHOT-<sha>` → identity `v0.0.1-SNAPSHOT-<sha>`.

`run-agent` embeds the payload too — it imports the root package through
`internal/runner` — so each archive carries the payload twice. Accepted:
compressed it is a few megabytes, and a `run-agent` that depended on a
`runner` having unpacked first would be a second code path to explain.

### 2.3 `.github/workflows/release.yml`

`on: push: tags: ['v*']`; `permissions: contents: write`; steps:
`actions/checkout@v4` with `fetch-depth: 0`, `actions/setup-go@v5` with
`go-version-file: go.mod`, `go test ./...`,
`goreleaser/goreleaser-action@v7` with `args: release --clean` and
`GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}`. Nothing else: no signing, no
Homebrew, no changelog configuration (GoReleaser's default commit list is
fine).

### 2.4 `scripts/release-snapshot.sh`

`go run github.com/goreleaser/goreleaser/v2@v2.18.2 release --snapshot --clean`
— GoReleaser is not installed on the development machine, `go run` with a
pinned version needs nothing but Go, and the pin keeps two developers
building the same `dist/`. Output: `dist/` with three archives,
`checksums.txt`, and per-target binaries.

## 3. `install.sh`

POSIX `sh`, `set -eu`, `trap 'rm -rf "$tmp"' EXIT` around `mktemp -d`.

| Input | Meaning | Default |
|---|---|---|
| `$1` or `OFFICE_VERSION` | tag to install | `latest` |
| `OFFICE_HOME` | office home | `$HOME/.office` |
| `OFFICE_INSTALL_FROM` | directory holding archives + `checksums.txt` instead of the network | unset |

Steps:
1. Platform: `uname -s` → `darwin`/`linux`; `uname -m` → `arm64`/`aarch64`
   → `arm64`, `x86_64`/`amd64` → `amd64`. The pair must be one of the three
   targets; otherwise exit 1 naming the pair and listing the supported ones.
2. Source: with `OFFICE_INSTALL_FROM`, copy `virtual-office_<os>_<arch>.tar.gz`
   and `checksums.txt` from it; otherwise download both with `curl -fsSL`
   (or `wget -qO`) from `https://github.com/kao73/virtual-office/releases/latest/download/<file>`
   or `…/releases/download/<tag>/<file>`. Neither tool → exit 1 naming both.
3. Verify: `expected=$(grep " *virtual-office_<os>_<arch>.tar.gz$" checksums.txt | cut -d' ' -f1)`,
   `actual` via `sha256sum` or `shasum -a 256`; mismatch or empty expected
   → exit 1 naming the file, nothing installed. No checksum tool → exit 1.
4. Extract into `$tmp`; both `runner` and `run-agent` must be present.
5. Install: `mkdir -p "$bin"`; for each binary `cp` to `$bin/.<name>.tmp`,
   `chmod 0755`, `mv -f` over `$bin/<name>` — replacing a running binary is
   safe on POSIX, and `mv` within one directory is atomic.
6. Report: `"$bin/runner" version` output (proves the binary runs on this
   platform and shows what was installed); whether `$bin` is on `PATH`
   (`case ":$PATH:" in *":$bin:"*)`), with the `export PATH=…` line to add
   when it is not; last line: `дальше: runner init`.

Messages are in Russian like every other user-facing text in the office.

## 4. Testing

| Unit | Tests |
|---|---|
| root `office` | every file on disk under the payload directories is in `Payload` (catches a missing `all:`); every file with `mode&0111` on disk starts with `#!` |
| `internal/office` | with `fstest.MapFS`: fresh unpack (modes, `bootstrap/` stripped); idempotence (edit a file, unpack again, unchanged); side-by-side names; mid-walk failure via an `fs.FS` whose `Open` fails for one path (no target, no temp); two goroutines unpacking the same name (one directory, no temp); `Hash` stable across calls and sensitive to one byte |
| `internal/runner` | `ResolveOffice`: branch 1 with a temporary git repository (as `ConfigSHA` tests today), branch 2 by setting `office.Version`, branch 3 by replacing `readBuildInfo`, branch 4 refusal text, `Unpack:false` creates nothing; `EnsureValidator` payload mode with an injected `fs.FS` (write once, `0755`, second call reuses) and the refusal for a missing platform |
| `internal/tracker` | marker: `v0.7.0`, `v10.12.3` whole; `<40 hex>` → 8; `<40 hex>-dirty` → 8 + `-dirty`; `ParseMarker` round-trip |
| `cmd/runner` | `init` on an empty home (both samples created, message, exit 0) and on a configured home (nothing changed, "оставлен"); `version` on an empty home creates no directory; existing `office_test.go` under `OFFICE_CONFIG_ROOT` unchanged; one test with the variable unset and `office.Version` set that runs `newOffices` against the real payload in a temp home and finds the `офис:` line and unpacked paths; loader accepts `projects.local.example.yaml` after the four edits |
| `scripts/install-test.sh` | builds a fake `dist/` (two dummy executables, tarball for the current platform, `checksums.txt`) unless a real one is given; runs the spec's four scenarios: install, update in place (config files and an old `office/` untouched), corrupted checksum (exit ≠ 0, nothing in `bin`), unsupported platform via a `uname` shim on `PATH` |
| live (task 5.4) | `scripts/release-snapshot.sh`; `OFFICE_INSTALL_FROM=dist OFFICE_HOME=<fresh> sh install.sh`; `runner version`; `runner init`; a mock project; one `runner tick`; `runner mock show` shows `config:v0.0.1-SNAPSHOT-…` |

`go test ./...` stays hermetic: no network, no GoReleaser, no real agent.
The shell test and the live check run by hand and are recorded in
`docs/notes/install.md`.

## 5. Risks and edges

- **Size.** Payload ≈ 13 MB (skills 6.6, kit tarballs 6.4) + checkers
  2–5 MB, twice per archive. Fine for a CLI; noted so nobody is surprised.
- **Stray `.unpack-*` after a crash.** Accepted; see 1.3.
- **`-buildvcs=false` or a build outside git** → refusal, branch 4.
- **`release` tag without cross-built files** → compile error. Intended:
  only `build-validators.sh` (run by GoReleaser's hook) produces a
  release-tagged build.
- **Archives without a version in the name** → a downloaded file does not
  say which version it is; it is a temp file, and `runner version` says.
- **Two runners racing on first unpack** → handled by rename semantics,
  covered by a test.
- **Snapshot identity has no tag behind it** → `v0.0.1-SNAPSHOT-<sha>` in
  markers of the live check; harmless and self-describing.
- **`hooks/debug-env.sh` and the `brainstorming`/`writing-plans` skills
  ship in the payload** although no role references them — the owner
  postponed that audit; the payload mirrors the tree as is.

## 6. Out of scope, restated for the plan

No cleanup of old `office/<dir>/`, no `doctor`, no self-update, no
Homebrew, no darwin/amd64, no automatic bake, no changes to
`bootstrap/*.plist|.service` or the user guide, no first tag (owner, after
merge and after the repository is public with a `LICENSE`).
