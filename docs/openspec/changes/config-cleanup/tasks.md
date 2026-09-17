## 1. Shared role rules move to roles/_base (config-boundary D1)

- [x] 1.1 Create `roles/_base/base.yaml` with the git/`rm` denies and the generic registries from today's `projects.yaml: defaults` (Docker Hub, GitHub, ghcr, GitHub releases, npm, PyPI, Go proxy), with their evidence comments; EXP-specific hosts are not carried over
- [x] 1.2 `runner.LoadRole`: union `roles/_base/base.yaml` into the role's `network`/`tools` when `roles/_base/` exists; missing file with existing directory is a refusal naming it (`internal/runner/role.go`, `role_test.go`)
- [x] 1.3 Shipping test: `roles/_base/base.yaml` exists and contains the `Bash(git *push*)` deny (so the guarantee cannot vanish silently); `boundary_test.go` drops `projects.yaml` from its named files

## 2. Loader: projects live on the machine only (config-boundary D1–D3)

- [x] 2.1 `LoadProjects(machinePath)`: single file; `machineProject` gains `default_branch` (required) and `branch_prefix` (default `agent/`); `Project` built from it alone; drop `ProjectsFile`, `officeProject`, `checkOfficeHalf`, the pairing checks; zero projects refused (`internal/tracker/config.go`, `config_test.go`)
- [x] 2.2 One allow-list guard for `defaults` (`network`, `tools`); `auto_merge`, `default_branch`, `branch_prefix` under it refused by name
- [x] 2.3 Leftover `projects.yaml` under the configuration root is refused with a pointer to `projects.local.yaml` and `roles/_base/base.yaml` (checked where projects are loaded)
- [x] 2.4 Messages that named `projects.yaml` (`Projects.Get`, `SkipUnknownProject`, `board.go`) now name `projects.local.yaml`; every test that builds `Projects` by hand (`pipeline_test.go`, `prpass_test.go`, `board_test.go`, `jira/jira_test.go`, `cmd/run-agent/main_test.go`) follows the new constructor; `go test ./...` green

## 3. Runner: one office per tracker (runner-multi-tracker D4–D6)

- [x] 3.1 Remove `--tracker`; `office()` → `offices()` with load order workflow → projects → trackers in use → forges → shared resources; `namedOffice` wrapper; `tracker.yaml` opened and listed only when `jira` is in use (`cmd/runner/office.go`)
- [x] 3.2 `offices` type: `each` (header only when >1, `errors.Join`), `byProject`; unit tests over two mock trackers in temp dirs (`cmd/runner/offices.go`, `offices_test.go`)
- [x] 3.3 `tick`, `reap`, `complete-splits` use `each`; `tick --role` prints "работы нет" per office
- [x] 3.4 `loop`: driver moves from `pipeline.Office.Loop` to `offices.loop` with a `ctx` check between offices; `Office.Loop`/`tickOnce` deleted; loop test updated
- [x] 3.5 `ls`: configuration sources once, then each tracker's board under its name; `--project` via `byProject` (`board.go`, `board_test.go`)
- [x] 3.6 `worktree rm KEY`: office via `byProject(entry.Project)` (`worktree.go`, `worktree_test.go`)
- [x] 3.7 `office_test.go`: mock-only machine needs no `tracker.yaml`; jira project without it is refused; rejected jira credentials refuse the whole command; `--tracker` is an unknown flag

## 4. Shipped example and setup script (config-boundary D7–D8)

- [ ] 4.1 Rewrite the `tracker.example.yaml` header for the copied file; ship `accounts.roles`, `also_agents`, `issue_type` as commented blocks; `depends_on_link: Depends` stays active
- [ ] 4.2 `scripts/jira-setup.sh`: `add_link_type` find-or-create for `Depends` (outward `depends on`, inward `is depended on by`), printed with the field ids; second run creates nothing
- [ ] 4.3 Live check of 4.2 against the polygon JIRA (or a fresh `bootstrap/jira/` container): one link type after two runs; record the result in the verification report

## 5. Repository and this machine (design — Migration Plan)

- [ ] 5.1 Delete `projects.yaml`; `git grep projects.yaml` outside `docs/notes/` and the OpenSpec archive shows only the leftover-file refusal and the docs edited in group 6
- [ ] 5.2 `~/.office/projects.local.yaml`: `default_branch` for `OFFICE`, `VO` (`master`) and `EXP` (`main`); EXP-specific hosts (MCR, bun, Playwright, apt mirrors) into the `EXP` entry's `network`
- [ ] 5.3 `./bin/runner ls` prints both trackers without a flag; `./bin/run-agent --role analyst --workdir <probe> --task <task> --dry-run` prints a `config_sha` without `-dirty` and lists the git denies among the effective tools rules

## 6. Documentation that would otherwise be false (design D9)

- [ ] 6.1 `docs/DESIGN.md` §2.5: repo ↔ `${OFFICE_HOME}` lists rewritten around `roles/_base/base.yaml` and `projects.local.yaml`, "debt not fully closed" removed, no-flag runner named
- [ ] 6.2 `README.md`: drop quick-start step 3, add `default_branch` to step 4, remove `--tracker jira` from command lines, fix "Где что лежит" (no `projects.yaml`, add `roles/_base/base.yaml`) and the `-dirty` paragraph
- [ ] 6.3 `docs/ONBOARDING.md`: Б5 snippets (no `projects.yaml` edit, `default_branch` in the machine half, `Depends` created by the script), Б7 command lines without `--tracker`, the `-dirty` symptom paragraph
