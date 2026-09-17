## Why

Setting up one project today touches five places: a committed file in the
office repository (`projects.yaml`), the machine file
(`${OFFICE_HOME}/projects.local.yaml`), a copied `tracker.yaml` that must be
trimmed by hand, a JIRA link type nobody's script creates, and a `--tracker`
flag that must be repeated on every command. The first of these is the debt
`docs/notes/stage-5-config.md` left open: every clone except the author's
must edit a committed file to start, so its `config:…-dirty` marker stops
meaning anything on the very first run. The owner reviewed the configuration
surface (2026-09-17) and chose to close the debt fully rather than document
around it: the repository is the framework, the instance is configured in
`${OFFICE_HOME}` alone.

## What Changes

- **BREAKING** `projects.yaml` is deleted. Projects are declared only in
  `${OFFICE_HOME}/projects.local.yaml`. A leftover `projects.yaml` under the
  configuration root is a start-up refusal that says where its contents
  went, not a file silently ignored.
- **BREAKING** `default_branch` becomes a required per-project key in
  `projects.local.yaml`; `branch_prefix` an optional one defaulting to
  `agent/`. The office↔machine key-pairing check disappears with the office
  half; a task from an unlisted project is still refused at claim time.
- The rules every role shares — the `tools.deny` that keeps git remotes,
  branches and history in the runner's hands, and the package registries
  any implementer needs — move from `projects.yaml: defaults` to
  `roles/_base/base.yaml`, the machine-readable half of `roles/_base/base.md`
  ("действуют всегда, роль может добавить, но не отменить"). Every role
  receives them at load, so a manual `run-agent` without `--project` now
  gets the same denies as a pipeline run. The machine adds its own rules
  through `projects.local.yaml` (`defaults` and per-project entries).
- The reserved `defaults` key in `projects.local.yaml` accepts only
  `network` and `tools`; `auto_merge` (today silently dropped) and the
  relocated project keys under it are refused by name.
- **BREAKING** The `--tracker` flag is removed from every `runner`
  subcommand. The runner serves every tracker named by its projects: one
  office per tracker, walked in turn by `tick`, `loop`, `reap`,
  `complete-splits`, `ls`, and `worktree rm`. Building the offices stays
  strict (a tracker that cannot be opened refuses the command); inside one
  `tick`, an error in one office does not stop the others. `tracker.yaml`
  is opened only when at least one project names `jira`.
- `tracker.example.yaml` becomes copy-ready: its header reads correctly once
  the file is `${OFFICE_HOME}/tracker.yaml`, and the optional keys
  (`accounts.roles`, `also_agents`, `issue_type`) ship commented out, so a
  fresh copy needs exactly `base_url` and the four `customfield_*` ids.
- `scripts/jira-setup.sh` creates the `Depends` issue link type
  idempotently and prints it alongside the field ids, so a fresh instance set
  up by the shipped script satisfies everything the example references.
- `docs/DESIGN.md` §2.5 is rewritten for the new boundary. README and
  ONBOARDING are edited only where they would become wrong (the
  `projects.yaml` step, the Б5 snippets, `--tracker` on command lines, the
  `-dirty` symptom); the reference document, the machine-half example file,
  and the `bootstrap/` scheduler examples are a separate follow-up change.

Non-goals: deriving `default_branch` from the remote's HEAD; a machine-level
default for `--backend` (the flag stays as it is); multi-machine ledgers;
de-duplicating `roles/*/role.yaml`; the follow-up documentation change.

## Capabilities

### New Capabilities
- `config-boundary`: the repository carries the framework and no instance
  configuration; `${OFFICE_HOME}` carries every project; the shared role
  rules live with the roles; how the loader enforces that; the contract
  between the shipped `tracker.example.yaml` and `jira-setup.sh`.
- `runner-multi-tracker`: the runner serves every tracker its projects name,
  with no tracker selection flag; per-tracker offices; failure isolation
  between them; when `tracker.yaml` is required.

### Modified Capabilities
- `role-sandbox-permissions`: the repo-wide layer (level 1) is
  `roles/_base/base.yaml` and applies to every role load; the project layer
  (level 2) is read from `projects.local.yaml`; scenarios restated
  accordingly.

## Impact

- `internal/runner/role.go` (+ `role_test.go`): `LoadRole` unions
  `roles/_base/base.yaml` into the role when `roles/_base/` exists.
- `internal/tracker/config.go` (+ `config_test.go`, `boundary_test.go`):
  `LoadProjects` takes the machine file alone; `default_branch`,
  `branch_prefix` default; one allow-list for `defaults`; leftover
  `projects.yaml` guard; `ProjectsFile` and the office half go away.
- `cmd/runner/office.go`, `main.go`, `board.go`, `worktree.go` (+ tests):
  `--tracker` removed; an `offices` type walks one `pipeline.Office` per
  tracker; loop driver moves here from `pipeline.Office.Loop`.
- `cmd/run-agent/main.go` (+ test): base rules arrive through `LoadRole`;
  `--project` still merges project rules.
- `internal/pipeline`: `Office.Loop`/`tickOnce` deleted; nothing else.
  Tests that build `Projects` by hand (`pipeline_test.go`,
  `prpass_test.go`, `board_test.go`, `jira/jira_test.go`) follow the new
  constructor.
- `roles/_base/base.yaml` (new), `projects.yaml` (deleted),
  `tracker.example.yaml`, `scripts/jira-setup.sh`, `docs/DESIGN.md`,
  `README.md`, `docs/ONBOARDING.md`.
- Operational, outside the repo: this machine's
  `~/.office/projects.local.yaml` gains `default_branch` for `OFFICE`,
  `VO`, `EXP`, and the EXP-specific hosts (Playwright, MCR, bun, apt
  mirrors) move from the old repo-wide list into the `EXP` entry; the
  generic registries stay in `roles/_base/base.yaml`.
