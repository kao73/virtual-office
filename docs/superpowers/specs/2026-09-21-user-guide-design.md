---
comet_change: user-guide
role: technical-design
canonical_spec: openspec
---

# user-guide — technical design

Deep refinement of the framework accepted in the open phase. The authority on
what must be true is `docs/openspec/changes/user-guide/specs/office-install/spec.md`;
the authority on why is `proposal.md`; the framework decisions D1–D7 live in
`design.md` beside them. This document settles how, and nothing here restates
those three.

## Context

Two facts from the tree shape every decision below.

`cmd/runner/init.go` places its samples from a `samples` slice of
`{example, working, edit}` triples and prints, for each, a `cp example working`
line telling the reader what to edit. Unit files do not fit that shape: they
are not copied to a working name, they are installed into launchd or systemd,
and what the reader edits is an account and a path, not four YAML values.

The runner does not spawn a `run-agent` binary. `internal/pipeline/agent.go:26`
holds `var executeAgent = runagent.Execute` — the call is in-process, and Go
tests substitute the variable. A shell harness cannot reach that seam; its only
substitution point is the agent CLI itself, which
`internal/backends/local/local.go:35` runs as `exec.CommandContext(ctx, l.Argv[0], …)`
with `Argv[0]` coming from `internal/adapters/claude`, where
`Executable = "claude"` — resolved through `PATH`.

## Implementation

### The payload gains a scheduler directory

`office/scheduler/` holds the three unit files. `office/payload.go` gains one
directive:

```go
//go:embed all:scheduler
```

A new top-level entry needs its own directive; files added inside an already
embedded directory ride along without one. Plain `scheduler` would suffice
today — the directory has no `_`- or `.`-prefixed entries — but `all:` is what
the file's own comment warns is needed the moment one appears, and a sample
that silently stops shipping is precisely the failure this change exists to
remove.

### `runner init` places units beside the samples

The `samples` loop keeps its shape. A second slice lists the unit files, and
both loops call one extracted helper:

```go
// place writes one payload file into the home, leaving an existing file alone.
func place(out io.Writer, payloadPath, dst string) error
```

`place` carries the `O_EXCL` open, the "оставлен"/"создан" reporting and the
remove-on-partial-write cleanup exactly as the current loop does — extracted,
not reimplemented, so the two kinds of file cannot drift in their overwrite
semantics.

`${OFFICE_HOME}/scheduler/` is created with `MkdirAll` and then an explicit
`Chmod(0o755)`, mirroring what the home itself does at `init.go:52-59`. Under a
strict umask the directory would otherwise come out `0700`. The previous branch
found this class of bug by cure-half-applied; the explicit chmod is cheap and
the comment beside it says why it is not redundant.

The closing output grows a second stanza. The existing one says "copy each
sample to its working name and edit it"; the new one names the unit files and
says what to do with them — install into launchd or systemd after correcting
the account and `${OFFICE_HOME}` path. The two instructions are different and
must not be merged into one sentence that is true of neither.

### The units themselves

`--role implementer` leaves the launchd `ProgramArguments` array and the
systemd `ExecStart` line. Nothing else about the units changes: the systemd
side stays a `Type=oneshot` under a timer, the launchd side stays a `loop`, and
the reasoning for that asymmetry stays in `bootstrap/README.md` where it
already is.

### `bootstrap/` keeps the explanation and loses the files

`git mv` moves the three files so history follows them. `bootstrap/README.md`
is rewritten to point at `office/scheduler/` for a reader with a clone and at
`runner init` for one without, and every sentence claiming the units live in
`bootstrap/` goes. `bootstrap/` keeps `README.md` and `jira/`.

### What each document owns

| Document | Job | Source |
|---|---|---|
| `README.md` | shop window | exists; platforms move into the install section, problem channel named |
| `docs/README.md` | index, quadrant per document | new |
| `docs/guide/quickstart.md` | tutorial | exists; explanation removed |
| `docs/guide/` machine how-to | how-to | ONBOARDING A1–A5, rewritten for a release install |
| `docs/guide/` project how-to | how-to | ONBOARDING Б4, Б7 |
| `docs/guide/operations.md` | how-to | exists; scheduler section rewritten around `${OFFICE_HOME}/scheduler/` |
| `docs/guide/development.md` | how-to, contributor | exists, unchanged |
| `docs/guide/roles-and-flow.md` | explanation | exists; receives what leaves the tutorial |
| `docs/reference/configuration.md` | reference | new: layers, precedence, environment |
| `docs/reference/jira-requirements.md` | reference | new: extract of `docs/notes/jira-setup.md` + ONBOARDING Б2–Б3 |
| `docs/ONBOARDING.md` | how-to spine | shrinks to an ordered checklist of links |
| `docs/contracts/` | reference | unchanged, cited from code |
| `docs/DESIGN.md` | explanation | unchanged |
| `docs/notes/` | dated journal | unchanged; gains `stages/` |

## Testing strategy

### The code change

TDD, in the order the tasks list it: the failing `cmd/runner/init_test.go`
cases first — a fresh home contains `scheduler/` with exactly three files, and
a second `init` over an edited unit leaves it byte-identical — then the
implementation. The existing assertion that a fresh home contains *exactly* the
two configuration samples is now false and widens rather than disappears;
deleting it would remove the only check that init writes nothing extra.

One further test asserts that no shipped unit file contains `--role`. It is
cheap, it names the defect, and it is the only thing standing between this fix
and its silent return.

### The documented recipe becomes executable

`scripts/doc-recipe-test.sh`, modelled on `scripts/install-test.sh`: a shell
harness that walks the documented path in a scratch `${OFFICE_HOME}`, with no
network and no paid run.

1. fresh temporary `${OFFICE_HOME}`, `runner init`, assert all five files —
   two configuration samples and three units — and the `scheduler/` directory
   exist and the exit code is zero;
2. create the bare client repository the tutorial creates;
3. copy `projects.local.example.yaml` to its working name and edit exactly the
   four values the documentation says to edit — no more, which is the claim
   being tested;
4. `runner mock add`, `runner ls`;
5. one `runner tick --backend local` with a fake `claude` earlier on `PATH`.

The fake is a shell script: it writes the role's result file into the workdir
and exits zero. That is sufficient because `runagent.fromLog`
(`internal/runagent/runagent.go:397-402`) returns a zero value when the run log
cannot be opened rather than failing — usage and ending come back unknown, and
nothing downstream treats unknown as an error.

What this catches is exactly what shipped broken one branch ago: a README whose
recipe died at «projects.local.yaml не заведён» because `runner init` writes
only examples. A human walking the recipe once catches it once; this catches it
every run.

### What stays unverified, deliberately

The harness stops after the first tick. Driving a task to `Done` needs a fake
that satisfies three roles' output contracts and the PR pass — a small eval
harness, brittle, and a second home for knowledge that
`cmd/eval-roles` already owns. Prose quality, link targets and the accuracy of
the JIRA reference are checked by reading, and the closing tasks say so
plainly rather than implying a machine does it.

## Boundary conditions

- **An existing installation.** Gains `scheduler/` on the next `runner init`
  and nothing else. Both loops leave existing files alone; the added scenario
  pins that an edited unit survives.
- **A home whose `scheduler/` exists but is empty.** `MkdirAll` succeeds, each
  file is created. No special case.
- **A home whose `scheduler` is a regular file.** `MkdirAll` fails and init
  reports it. Not worth a scenario: the failure is loud and the cause is on
  the reader's disk, not in the office.
- **`OFFICE_CONFIG_ROOT` pointing at a clone.** Irrelevant to init — it reads
  the embedded payload in every mode, which `init.go`'s own comment states.
- **A reader who already installed a unit from `bootstrap/`.** Their installed
  copy is untouched; only the sample in the clone moves.

## Risks

**The fake `claude` turns out to need more than a result file** → the harness
degrades to stopping before the tick and asserting the configuration loads.
The four steps before the tick still cover the defect that shipped last time.
Decided during implementation against a real run, not assumed.

**`git mv` of the units breaks someone mid-setup** → `bootstrap/README.md` is
rewritten in the same commit, and the files are one `runner init` away.

**The configuration reference drifts** → it is written about structure, not
keys; keys stay in the embedded examples. No test, accepted in `design.md`.

**The branch grows too large to review** → commits are split by kind, code and
samples separately from documentation, and the task groups already follow that
split.

## Task list impact

`tasks.md` gains one task in group 7 for `scripts/doc-recipe-test.sh`. No other
task changes; the harness is additive and depends on group 1 being finished.
