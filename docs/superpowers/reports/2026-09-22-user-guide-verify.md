# user-guide — verification report

- Change: `user-guide`
- Branch: `comet/user-guide`, 48 commits over `18d040c`
- Mode: full (31 tasks, 38 changed files, 1 delta capability)
- Date: 2026-09-22
- Verdict: **pass**, with two residual risks and one bookkeeping correction recorded below

## How this was verified

The build phase gave every one of the sixteen plan tasks its own reviewer, and a
final whole-branch review on the most capable model found four Important
findings — two of them blockers — which were fixed in one wave and confirmed by
a scoped re-review.

None of that is what this report rests on. Verification was run by an
independent execution that had not driven the change, and it was told to treat
every prior claim as a claim to test. It reproduced all four checks itself and
covered each acceptance item of the delta spec separately. Where it could not
verify something, it said so, and those places are listed here rather than
rounded up.

## Checks

Run fresh in the verify phase, after the repair described below:

| Check | Result |
|---|---|
| `gofmt -l .` | no output, exit 0 |
| `go build ./...` | exit 0 |
| `go test -count=1 ./...` | 20 packages ok, no failures, cache disabled |
| `sh scripts/doc-recipe-test.sh` | five steps ok, exit 0 |

The harness itself was checked against a broken recipe twice, by two parties who
broke it differently: the implementer reverted the `tracker: mock` edit and saw
the edit-count assertion fail, and the verifier removed the configuration copy
in a scratch copy of the script and saw it fail at step 3. A check nobody has
seen fail is not evidence; this one has been seen failing on two distinct
genuine breakages, each time naming the real cause.

## Acceptance items

`docs/openspec/changes/user-guide/specs/office-install/spec.md` carries two
requirements and six scenarios.

| Scenario | Verified by |
|---|---|
| A fresh machine gets a home and two samples | `cmd/runner/init_test.go:21` — asserts the top level is exactly the two samples plus `scheduler/`, that `scheduler/` holds exactly the three units, that each is byte-equal to the payload, and that the output names all five |
| A configured home is left untouched | `cmd/runner/init_test.go:99` |
| An edited scheduler sample survives a second init | `cmd/runner/init_test.go:140`, with `place()` using `O_EXCL` at `cmd/runner/init.go:62` |
| The projects sample works with four edits | run, not read: `scripts/doc-recipe-test.sh:80` asserts the diff is exactly four lines and then `runner ls` lists the project |
| No shipped sample pins a role | `office/payload_test.go:166`, and independently `grep -rn -- --role office/scheduler/` with no hits; `git ls-files` finds the three unit files only under `office/scheduler/` |
| One unit, one file | the file half verified as above; the documentation half required a repair, below |
| A copied sample moves a task through every role | **not verified end to end** — see residual risks |

## The repair this phase required

Verification returned one warning that was objectively repairable and in scope,
so the change went back to build under the retry rule rather than accepting it.

`docs/guide/development.md` still mapped `bootstrap/` as holding the scheduler
jobs. It does not — this change moved them into the payload — and that line was
the last surviving instance of the claim the change set out to remove. It also
worked directly against the second half of the *One unit, one file* scenario,
which requires the documentation explaining the units to point at the one place
they live. Fixed in `f2f618c`, together with three smaller things the verifier
raised: the manual-check block in `bootstrap/README.md` now leads with the
roleless `tick` rather than a role-pinned one that is easy to copy into a unit
file; `docs/guide/operations.md` gives a release reader a URL they can follow
instead of a repository path they do not have; and the tutorial drops a source
citation aimed at a reader who never clones.

Where the repair landed was checked afterwards: the only remaining mention of
scheduler jobs under `bootstrap/` is in `docs/comet/archive/`, which standing
decision leaves untouched because editing an archived verification record to
keep a link alive falsifies the account of what was verified and when.

## Residual risks

**The last scenario has no automated coverage.** «A copied sample moves a task
through every role» was verified statically and the chain holds: a sample
carries no `--role`, `tickCommand` and `loopCommand` therefore pass an empty
role (`cmd/runner/office.go:240,286`), `TickAll` walks `Workflow.Order()` over
three roles (`internal/pipeline/pipeline.go:268`, pinned by
`TestTickAllWalksThreeRoles`), and the harness runs a real roleless `tick` that
moves a task from `Analysis` to `Ready`. What is not covered is that repeated
cycles reach a terminal status, and that a real launchd or systemd installation
runs at all. The verifier also established that `TestEndToEndTaskThroughThreeRoles`
is *not* evidence for this scenario, because it drives roles by name rather than
through `TickAll`. Closing this needs a macOS login session, a Linux box with
systemd, and a paid agent run; none was available.

**The JIRA reference cannot be checked against a live instance.** The statuses,
the transition count, the four field types, the link type and the default
account rights in `docs/reference/jira-requirements.md` were verified against
this repository's scripts and code, not against a third-party JIRA Server. The
two facts that come from one polygon rather than from code — the
`customfield_10000`/`Rank` numbering and the `jira-software-users` default
rights — state their own provenance in the document and tell the reader to
trust their own script's output over the number printed there.

**`EnvironmentFile=-` semantics were read, not executed.** The systemd sample's
optional-file prefix was taken from documentation; this is a macOS machine.

## Bookkeeping corrections

`tasks.md` item 4.6 ticks «no step requires a clone, Go, or reading source».
That goal is met from the first release tag onward, not today: until a tag
exists, the only install path that works is the local snapshot, which needs both
a clone and Go. The documents say so plainly — `docs/guide/quickstart.md` leads
with the caveat and `docs/guide/machine-setup.md` was corrected during the final
fix wave to stop claiming Go is needed only by people building from source. The
tick is recorded here as accurate about the documents and optimistic about the
date.

The plan directed the `auto_merge` pointer in `docs/guide/project-setup.md` at
`docs/reference/configuration.md`. The implementation sends it to
`docs/guide/roles-and-flow.md` instead, on a controller ruling: design decision
D4 keeps per-key semantics out of the configuration reference, that file says so
in as many words, and the planned link would have resolved while answering
nothing. The divergence is from the plan, not from the Design Doc.

## Coherence

Decisions D1–D7 of `docs/openspec/changes/user-guide/design.md` are followed,
and the implementation matches the technical Design Doc at
`docs/superpowers/specs/2026-09-21-user-guide-design.md`. No contradiction was
found between the delta spec and either design document. All 31 items of
`tasks.md` are checked, and eight were spot-checked against the branch diff by
the verifier, chosen where an optimistic tick was most plausible.
