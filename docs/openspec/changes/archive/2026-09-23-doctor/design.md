## Context

See `proposal.md` - Why for the motivation. Relevant existing pieces this
design reuses rather than reimplements:

- `internal/tracker/jira.LoadConfig` already validates `tracker.yaml`'s
  *shape* (base_url present, four `customfield_*`-shaped ids, non-empty
  `status_map`, `human_flag_label` set) on every load, with actionable
  errors. It does not, and cannot, confirm those ids exist on the live
  instance or carry the right type.
- `Tracker.CheckAccount` / `Tracker.Whoami` (account identity vs `/myself`)
  and `Tracker.CheckWorkflow` (self-entry transition) already exist on
  `*jira.Tracker`, called today from `cmd/runner/office.go` (account) and
  `internal/pipeline/pipeline.go` (workflow) as inline side effects of a
  real cycle.
- `internal/backends/sbx.BasePolicy{}.Notice()` already performs the exact
  "is the machine's base sandbox network open" check and returns a
  ready-to-print human message, or empty when closed.
- `internal/runner.ResolveOffice` + `runner.OfficeDir` + `runner.Home()`
  already know where `${OFFICE_HOME}/office/<version>/` is and which
  `<version>` the running binary is.
- `tracker.LoadProjects` / `Projects.TrackersInUse()` already know which
  trackers (`mock`, `jira`) are actually in play on this machine.
- The office pipeline shells out to exactly four external binaries:
  `git` (everywhere), `claude` (`internal/adapters/claude`, the only
  adapter), `sbx` (`internal/backends/sbx`, only when the `sbx` backend is
  selected), and `comet` (`internal/pipeline/archive.go`, only for a
  project whose `forge` is set — a forge-less project's change goes
  straight to `Done` as `pr-skipped`, per README). GitHub access goes
  through `internal/forge/github.go`'s direct REST calls, not a `gh` CLI.

## Goals / Non-Goals

**Goals:**
- Reuse every existing check verbatim where one already exists; add new
  code only where proposal.md identifies a real gap (field/type existence,
  link-type existence, tool/credential presence, stale-snapshot listing).
- Keep `runner doctor` a plain function returning `error`, dispatched from
  `main.go`'s switch exactly like every other subcommand — no new CLI
  parsing pattern, no cobra.

**Non-Goals:**
- Changing any existing check's behavior or call site (e.g. `office.go`
  still calls `CheckAccount` inline; `doctor` calls the same method, it
  does not replace the inline call).
- Making `tracker.Tracker` (the generic interface `mock` and `jira` both
  satisfy) grow the new JIRA-only methods. `mock` has no server to check
  fields or link types against, so these stay on `*jira.Tracker`.

## Decisions

### Command shape
`cmd/runner/doctor.go` adds `doctorCommand(args []string, out io.Writer)
error`, wired into `main.go`'s switch as `case "doctor"`. It takes the same
`--backend` flag the other commands accept (default `sbx`, same as `tick`,
`loop`, `reap` and the rest — a bare `runner doctor` previews exactly what
a bare `runner tick` would need), used only to decide whether the `sbx`
tool and the sandbox-network check apply — it performs no sandboxed exec
itself. No `--role`, no `--json`.

Each check is a small `func() finding` that never returns early on
failure — `doctorCommand` runs all of them, prints one line per check
(`ok`/`warn`/`fail` plus message) to `out`, and at the end returns
`fmt.Errorf("doctor: %d check(s) failed", n)` when `n > 0`, `nil`
otherwise. `main.go`'s existing `runner: %v` wrapper and `os.Exit(exitInfra)`
handle the exit code the same way every other command's error does — no
new exit-code plumbing.

**Alternative considered**: stop at the first failure (matches how a
plain shell script preflight usually behaves). Rejected — proposal.md's
spec requires "reports that specific tool as missing and does not stop at
the first missing tool," because a new user fixing one thing at a time off
a single truncated error is exactly the friction this change removes.

### Tool presence
Required set is computed, not hardcoded to "check everything always":
`git` and `claude` unconditionally; `sbx` when `--backend sbx`; `comet`
when `Projects.Get(key).Forge != ""` for at least one project. Each is
`exec.LookPath`, reported by name on failure (mirrors `cometExecutable`
being a `var` for the same reason `archive.go` already documents: a
missing optional tool must not block unrelated projects).

### Credential presence
For each account in `tracker.yaml`'s `Accounts` (`Default` plus every
entry in `Roles`, deduplicated) that is actually reachable — i.e. only
when at least one project uses `tracker: jira` — check `os.LookupEnv` on
`UserEnv` and `SecretEnv`, reporting the *variable name*, never the value,
consistent with `run-agent --dry-run`'s existing boundary. This does not
reuse `Config.AgentAccounts()`: that method resolves identities for the
"who is an agent" comment-attribution list and only checks `UserEnv`; a
credential-presence check additionally needs `SecretEnv` and must not fail
just because the account is unused by any configured role.

### JIRA reachability and shape
Reuse `Tracker.CheckAccount()` as-is for reachability + identity match.
Add two new exported methods on `*jira.Tracker`, next to `CheckWorkflow`,
using the existing unexported `t.call` helper:

- `CheckFields() []FieldCheck` — one `GET /field` call, matched against
  the four configured `customfield_*` ids in `cfg.Fields` and an
  expected-type table (`lease_until` → date/datetime, `run_id`/`owner` →
  string, `attempts` → number) kept next to the `Fields` struct. Reports
  each configured field that is absent or has an unexpected
  `schema.type`.
- `CheckLinkType() error` — one `GET /issueLinkType` call, checks
  `cfg.DependsOnLink` (when set) names an existing type by `name`. When
  `DependsOnLink` is empty, this check is skipped, not failed — the field
  is already optional per its own doc comment (only `LinkDependsOn` needs
  it).

Both run once per distinct JIRA instance (one `tracker.yaml`, not once per
project), matching the existing single-instance assumption `LoadConfig`
already makes.

### Sandbox network
Call `sbx.BasePolicy{}.Notice()` directly and print its message as a
`warn`-level finding when non-empty; skip entirely when `--backend` is not
`sbx` (no sandbox in play, nothing to check) or when the `sbx` tool itself
is missing (already reported by the tool-presence check; a second,
confusing error about the same absent binary adds nothing).

### Stale office snapshots
Only applies when `runner.ResolveOffice(runner.Resolve{}).Source ==
runner.SourcePayload` (a release install; `SourceClone`/dev mode has no
`${OFFICE_HOME}/office/<version>/` tree to list). List
`filepath.Join(home, runner.OfficeDir)`, skip the current identity's
directory name, sum `du`-style size of the rest with `filepath.WalkDir`,
report count and total bytes as a `warn`-level finding. This is a listing,
not the `office prune` command itself — see Migration Plan.

## Risks / Trade-offs

- **New JIRA API surface** (`GET /field`, `GET /issueLinkType`) is
  untested against JIRA Server 8.13 until this change's tasks run it
  against the local polygon. [Risk] → the response shapes above are the
  well-known JIRA REST v2 shapes; verify against the polygon during
  implementation and adjust the expected-type table if 8.13 differs, same
  as `LinkDependsOn`'s doc comment already records one such empirical
  correction for this exact instance.
- **Credential presence gives false confidence** — a variable being set
  does not mean its value is valid; that gap already exists in
  `run-agent --dry-run` and is accepted there for the same reason (an
  invalid secret is an external system's answer, not something a local
  check can verify without spending a real credential).

## Migration Plan

No migration — new subcommand, no changed behavior for existing ones. The
`office ls`/`office prune` question `docs/notes/install.md` flagged as
"decide together with doctor" is resolved here: `runner doctor` reports
stale snapshots (this change); a separate `runner office prune` command
that deletes them is deliberately **not** built in this change — the
report-only listing already lets someone clean up by hand
(`rm -r office/<hash8>`), and a deletion command earns its own change once
someone has actually used the listing and wants the second step
automated too. This keeps the decision from staying open a second time
(it now has an answer: "listing now, deletion later, on demand") without
growing this change's scope past what proposal.md agreed.
