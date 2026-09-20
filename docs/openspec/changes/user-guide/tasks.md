## 1. Scheduler samples: one home, no pinned role

- [ ] 1.1 Create `office/scheduler/` and move `bootstrap/local.office.runner.plist`,
      `bootstrap/office-runner.service` and `bootstrap/office-runner.timer` into it
      with `git mv`, so history follows the files
- [ ] 1.2 Remove `--role implementer` from the launchd `ProgramArguments` array and
      from the systemd `ExecStart` line; confirm by reading both files that the
      remaining invocation is `loop`/`tick` with no role flag
- [ ] 1.3 Add `all:scheduler` to the `//go:embed` directive in `office/payload.go` —
      a new top-level entry needs its own directive — and add a case to
      `office/payload_test.go` asserting the three unit files are readable from
      `Payload`
- [ ] 1.4 Write the failing test first in `cmd/runner/init_test.go`: a fresh
      `${OFFICE_HOME}` contains `scheduler/` with exactly the three unit files, and
      a second `runner init` over an edited `scheduler/office-runner.service`
      leaves it byte-identical. Run it, see it fail
- [ ] 1.5 Extend `cmd/runner/init.go` to write `${OFFICE_HOME}/scheduler/` from the
      payload under the same never-overwrite rule as the two configuration samples,
      and to name the created files in its output. Run the tests, see them pass
- [ ] 1.6 Update the existing `runner init` tests that assert a fresh home contains
      *exactly* the two configuration samples — the assertion is now wrong, and it
      must widen rather than be deleted
- [ ] 1.7 Add a test that no shipped unit file contains `--role`, so the defect
      cannot come back silently
- [ ] 1.8 Add `--role` to the `runner loop` line of the usage text in
      `cmd/runner/main.go:19`; both `tick` and `loop` accept it and only `tick`
      says so
- [ ] 1.9 Rewrite `bootstrap/README.md` to explain what launchd and systemd each
      need and why systemd is a oneshot under a timer, pointing at
      `office/scheduler/` for the files and at `runner init` for readers without a
      clone. Remove every claim that the units live in `bootstrap/`
- [ ] 1.10 Run `gofmt -l .`, `go build ./...` and `go test ./...`; commit code and
      sample moves together, documentation separately

## 2. Reference documents

- [ ] 2.1 Write `docs/reference/configuration.md`: the three layers
      (`roles/_base/base.yaml` → project entry in `projects.local.yaml` →
      `defaults`) and which wins; the environment variables (`OFFICE_HOME`,
      `OFFICE_CONFIG_ROOT`, `OFFICE_BACKEND`, `OFFICE_INSTALL_FROM`, the credential
      variables read through `tracker.yaml`) and what each one moves; which file is
      read from where and how `OFFICE_CONFIG_ROOT` changes that. Link
      `office/tracker.example.yaml` and `office/projects.local.example.yaml` as the
      authority on individual keys instead of restating them
- [ ] 2.2 Verify every environment variable named in 2.1 against the code
      (`grep -rn OFFICE_ --include='*.go'`) and every layer claim against
      `internal/tracker/config.go`; a reference that is wrong is worse than absent
- [ ] 2.3 Write `docs/reference/jira-requirements.md` for someone else's admin:
      statuses the graph needs, the four custom fields and their types, the link
      type, `/myself`, and the token's push right — each stated as a requirement
      with a way to check it. Name `scripts/jira-setup.sh` as one way to satisfy
      them, not the only one. Source material is `docs/notes/jira-setup.md`
      sections «Учётки», «Поля аренды», «Workflow», and `docs/ONBOARDING.md` Б2–Б3
- [ ] 2.4 Leave `docs/notes/jira-setup.md` in place as the explanation of why the
      setup is shaped that way, and link it from 2.3

## 3. Polygon half out of ONBOARDING

- [ ] 3.1 Move the container-JIRA material from `docs/ONBOARDING.md` Б1–Б2 into
      `bootstrap/jira/README.md`, which already documents the container; keep that
      file's existing sections and fold the new material in rather than appending
      a second account of the same thing
- [ ] 3.2 Replace `bootstrap/jira/README.md`'s two pointers at
      `docs/ONBOARDING.md` (lines 10 and 69) — the material they point at now lives
      in this file

## 4. The user path

- [ ] 4.1 Purify `docs/guide/quickstart.md` into a tutorial: one path that works,
      no alternatives, no explanation of why. Move the explanatory passages to
      `docs/guide/roles-and-flow.md` or drop them where the link suffices
- [ ] 4.2 Rewrite the «По расписанию» section of `docs/guide/operations.md` around
      `${OFFICE_HOME}/scheduler/`: the files are already on the reader's machine
      after `runner init`, and the edit is the account and the path. Remove the
      claim that samples live in `bootstrap/`
- [ ] 4.3 Write the machine-preparation how-to from `docs/ONBOARDING.md` A1–A5
      (tools, credentials, sandbox network, the runner's home, the machine check),
      rewritten for a reader who installed from a release and has no clone
- [ ] 4.4 Write the client-project how-to from `docs/ONBOARDING.md` Б4 and Б7 (the
      client repository, and the four checks by rising cost — the ladder is good
      and should survive intact)
- [ ] 4.5 Reduce `docs/ONBOARDING.md` to its spine: the ordered checklist for
      putting the office on a real project, each step a sentence and a link to the
      document that owns it. Keep the path — README, `bootstrap/README.md`,
      `bootstrap/jira/README.md` and `quickstart.md` all point at it
- [ ] 4.6 Check the whole user path against the spec's own claim: no step requires
      a clone, Go, or reading source

## 5. Index and README

- [ ] 5.1 Write `docs/README.md`: every document listed with the job it does —
      tutorial, how-to, reference, explanation — and `notes/` named as a dated
      journal that is outside the scheme
- [ ] 5.2 Move the platform list into the install section of `README.md`
      (checklist item 11), name the channel for problem reports (item 14): Issues
      are disabled on purpose, one maintainer, vulnerabilities to `SECURITY.md`,
      and update the «Документация» section to point at `docs/README.md`

## 6. Frozen stage plans out of the docs root

- [ ] 6.1 `git mv docs/STAGE-*.md docs/notes/stages/` — five files, names unchanged
- [ ] 6.2 Fix the references from `docs/notes/*` to the moved files; leave
      `docs/comet/archive/*` untouched and record that in the change's verification

## 7. Closing checks

- [ ] 7.1 Walk every relative link in `README.md`, `docs/README.md`,
      `docs/guide/*`, `docs/reference/*`, `docs/ONBOARDING.md`, `bootstrap/README.md`
      and `bootstrap/jira/README.md`, and confirm each target exists
- [ ] 7.2 Run the recipes the documents give — `runner init` into a scratch
      `${OFFICE_HOME}`, then the first-task sequence — and confirm each command
      works as written, in order, from a clean state
- [ ] 7.3 Run `gofmt -l .`, `go build ./...`, `go test ./...` and confirm the tree
      is clean
