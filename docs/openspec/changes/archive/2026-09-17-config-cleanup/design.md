## Context

See `proposal.md` — Why. What the code looks like today, as far as it
shapes the approach:

- `tracker.LoadProjects(officePath, machinePath)` decodes two strict halves
  — `officeProject{default_branch, branch_prefix, Rules}` and
  `machineProject{repo_url, worktree_root, tracker, forge, auto_merge,
  Rules}` — pairs them by key, refuses on any mismatch, and unions the
  rule layers into each `Project` (`internal/tracker/config.go`).
  `tracker.MergeProjectRules(project, role)` then unions the project's
  rules into the role (`rules.go`); `cmd/run-agent` calls it only with
  `--project`, so a manual run without one carries no repo-wide rules.
- `runner.LoadRole(configRoot, name)` reads `roles/<name>/role.yaml`
  strictly and resolves `includes` (every shipped role includes
  `../_base/base.md`); nothing machine-readable is shared between roles.
- `cmd/runner/office.go:office()` is the single constructor every
  subcommand calls (`tick`, `loop`, `reap`, `complete-splits`, `ls`,
  `worktree rm`). It reads `--tracker` (default `mock`), opens **one**
  tracker, filters projects with `Projects.For(tracker)`, and returns one
  `pipeline.Office`. Inside the pipeline everything is already per-office:
  `TickAll`, `Reap`, `PRPass`, `HumanReplies`, `sweepWorktrees` (filters
  folders by `o.Projects`), and `Loop` (reap → tick → complete-splits,
  errors logged, sleep, stop on signal).
- `scripts/jira-setup.sh` is bash + inline python3 over REST v2; it already
  creates statuses, fields, screens and accounts idempotently
  (`add_status`, `add_field`, `add_user`) and prints the field ids at the
  end.
- `internal/tracker/boundary_test.go` walks the shipped office files
  (`workflow.yaml`, `projects.yaml`, `budgets.yaml`, `roles/*/*`, `hooks`,
  `skills`, `bin`) for absolute paths and `customfield_*`.

## Goals / Non-Goals

**Goals:**
- Zero edits to committed files to run the office on a new machine; the
  repository is the framework only.
- The pipeline package does not change behaviour; multi-tracker is a
  `cmd/runner` concern.
- Every refusal names the file and key to fix, as the loader does today.

**Non-Goals:**
- A composite tracker abstraction, parallel offices, or any change to how
  one office claims and runs a task.
- Any machine-level default for `--backend`; the flag stays as it is.

## Decisions

### D1. `projects.yaml` is deleted; shared rules become `roles/_base/base.yaml`

The only content the office half carried besides project names was the
repo-wide `defaults` — and those are rules about what *roles* may do
(never push, never switch branches, never `rm -rf`; may reach the usual
package registries). That is the machine-readable twin of
`roles/_base/base.md`, so it moves there, same shape as the `network`/
`tools` blocks of a `role.yaml`. `LoadRole` unions it into every role
when `roles/_base/` exists (required then; absent directory means test
fixtures without a base). `LoadProjects` takes the machine file alone;
`ProjectsFile`, `officeProject`, `checkOfficeHalf`, and the pairing checks
go away. A leftover `projects.yaml` is refused with a pointer to both new
homes, so a stale clone or branch cannot run on half a configuration.

*Alternative — ship the rules only as `projects.local.example.yaml`*:
rejected. The denies are the framework's guarantee that the runner, not
the agent, owns git remotes; a guarantee that survives only until someone
trims a copied file is not one.

*Alternative — keep `projects.yaml` with `defaults` only*: rejected by
the owner; a file named "projects" that lists none, in a repository that
should carry no instance configuration, is exactly the confusion this
change removes.

### D2. `default_branch` required, `branch_prefix` optional with default `agent/`

`machineProject` gains the two fields; `Project` is built from it alone.
Empty `branch_prefix` → `agent/` at load time, so `Project.Branch()` never
sees an empty prefix. Empty `default_branch` → refusal naming key, project
and file, mirroring today's `repo_url` check.

*Alternative — derive `default_branch` from `origin/HEAD`*: rejected by
the owner for this change; it moves a load-time value into the workspace
lifecycle and adds a network call.

### D3. `defaults` guard: one allow-list

Today `extractDefaultsMachine` checks four named fields and misses
`auto_merge`. Replace it with one rule: the `defaults` entry, decoded
loosely, may contain only `network` and `tools`; anything else is named in
the refusal. One check cannot fall behind the struct again.

### D4. Multi-tracker = N offices walked by an `offices` type in `cmd/runner`

`office()` becomes `offices()`. Load order: workflow → projects → for each
distinct `tracker` value, sorted: open it (mock: `mock.Default()`; jira:
`jira.LoadConfig(tracker.yaml)` + `Open` + per-role `OpenAs` + account
checks, exactly as today) → forges over all projects → shared
`Workspaces`, `Ledger`, `Budgets`, `Sandboxes`, `ConfigSHA` → one
`pipeline.Office` per tracker with `projects.For(name)`. Since
`pipeline.Office` has no tracker name, `cmd/runner` wraps it:
`namedOffice{name string; *pipeline.Office}`.

The `offices` type owns the walk:

| Method | Does |
|---|---|
| `each(fn)` | visits offices in order, prints `== трекер <name> ==` before each **only when there is more than one**, collects errors with `errors.Join` |
| `loop(ctx, every, role)` | the driver moved out of `pipeline.Office.Loop`: per iteration, per office — `Reap`, `TickAll`/`Tick(role)`, `CompleteSplits`, each error logged; checks `ctx` between offices so a stop signal ends after the current office's run, not after the whole iteration |
| `byProject(key)` | the office whose `Projects` contains the key, for `worktree rm` and `ls --project`; unknown project → the refusal used today |

`pipeline.Office.Loop` and `tickOnce` are deleted — the only pipeline
edit. With one office the output of every command is byte-for-byte
today's.

*Alternative — `pipeline.Offices`*: rejected; the pipeline would learn a
concept it does not need. *Alternative — a fan-out `tracker.Tracker`*:
rejected; `Accounts`, per-role trackers and human-reply attribution are
per tracker, and "one task per role per tick" would need a new definition.

### D5. Failure isolation (owner's choice)

Building is strict: any tracker that fails to open refuses the command
before any office works — a wrong credential under a scheduler must be a
refusal, not a log line. Inside `tick`, `each` visits every office and
joins the errors: a broken JIRA does not stop the mock polygon. `loop`
logs per office and continues, as today.

### D6. `tracker.yaml` is opened only when `jira` is in use

The set of trackers is known once projects are loaded, so the file is
neither read nor listed unless some project says `jira`. The
configuration-source printout lists it after `projects.local.yaml`.

### D7. `jira-setup.sh` creates the link type by name

A new `add_link_type` in the style of `add_field`: GET
`/rest/api/2/issueLinkType`, look for `name == "Depends"`; if absent, POST
`{name: "Depends", outward: "depends on", inward: "is depended on by"}`.
The final summary prints `depends_on_link: Depends` next to the field
ids. The polygon finding that this server renders outward/inward reversed
(`docs/notes/analyst-task-splitting.md`) is compensated in
`jira.LinkDependsOn`, not here.

### D8. `tracker.example.yaml`: header written for the copy

The header describes the working file and points back to the example for
the copy command; the "sample, the runner does not read this" sentence
goes. `accounts.roles`, `also_agents`, `issue_type` ship as commented
blocks with a one-line "uncomment to …". `depends_on_link: Depends` stays
active because D7 guarantees the type.

### D9. Docs: only what would become false

`docs/DESIGN.md` §2.5 is rewritten (repo ↔ `${OFFICE_HOME}` lists, the
"debt not fully closed" paragraph removed, `roles/_base/base.yaml` and
the no-flag runner named). In `README.md` and `docs/ONBOARDING.md`: the
"add your project to `projects.yaml`" steps, the `projects.local.yaml`
snippets (gain `default_branch`), every `--tracker jira` on a command
line, and the `-dirty` symptom paragraph. Notes under `docs/notes/` are
history and are not edited.

## Risks / Trade-offs

- [`projects.local.yaml` on existing machines lacks `default_branch`] →
  loud refusal at start naming the key; this machine's file is updated as
  part of the change (migration below).
- [EXP-specific hosts leave the repository] → they belong to one project
  on one machine and move into that project's entry; the evidence
  comments stay reachable in git history (`c7ccf17`) and
  `docs/notes/followup-network-and-permissions.md`.
- [Rollback leaves `default_branch` in the machine file, which the old
  strict decoder rejects] → the migration note says to drop the new keys
  and restore `projects.yaml` on rollback.
- [Two trackers lengthen one `tick`] → runs are sequential by design; the
  owner already runs both trackers on one machine as two invocations.
- [`runner ls` output shape changes with two trackers] → `board_test.go`
  covers the per-tracker header; single tracker is unchanged.
- [Live check of `jira-setup.sh` needs a reachable polygon] → the
  polygon JIRA address is in `~/.office/tracker.yaml`; the script is
  designed for re-runs. If it is down, a fresh `bootstrap/jira/` container
  is the documented path.
- [Docs drift between this change and the documentation follow-up] →
  every command line in README/ONBOARDING that would now fail is fixed
  here; only additive docs are deferred.

## Migration Plan

1. Before building on this machine, edit `~/.office/projects.local.yaml`:
   `default_branch: master` for `OFFICE` and `VO`, `default_branch: main`
   for `EXP`; add to `EXP` a `network` list with the Playwright, MCR,
   bun and apt hosts that today sit in `projects.yaml: defaults`.
2. Land the change; `projects.yaml` is deleted, `roles/_base/base.yaml`
   carries the git denies and the generic registries.
3. `./bin/runner ls` prints both trackers without a flag;
   `./bin/run-agent --role analyst --dry-run` prints a `config_sha`
   without `-dirty`.
4. Rollback: revert the commits, remove `default_branch` and the `EXP`
   `network` block from the machine file, restore `projects.yaml`.
