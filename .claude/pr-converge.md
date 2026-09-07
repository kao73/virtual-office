# pr-converge adapter

## What CI proves
- Nothing automatically. `.github/workflows/claude-review.yml` runs ONLY on manual `@claude` mention in a PR/issue comment — no push/PR-triggered job at all. `gh pr checks <N>` will report "no checks reported" even on a fully broken branch. The check set below is the real gate; a green PR page proves nothing on its own.

## Checks
| Command | Run from | Notes |
|---|---|---|
| `go build ./...` | repo root | |
| `go vet ./...` | repo root | |
| `go test ./...` | repo root | ~16 packages; all hermetic (fake HTTP servers / in-memory mock tracker) — no live network, nothing to exclude from a normal cycle |
| `gofmt -l .` | repo root | one pre-existing false positive: `internal/pipeline/prpass_test.go` (a unicode right-quote in a comment, identical on `master`, unrelated to any PR) — `grep -v` it out, don't try to fix it |

No Makefile; no lint step beyond `go vet`.

## Excluded from a normal cycle
- Nothing — the whole suite is hermetic and fast enough (~2 min including cached packages) to run in full every round.

## False-green traps
- None found. `go test ./...` genuinely fails on a real regression (verified repeatedly via temporary-revert red-checks during this session).

## Conventions
- Commit messages: conventional-commits style (`fix(scope): ...`, `docs(scope): ...`, `chore: ...`, `refactor(scope): ...`), written in **English**, `Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>` trailer.
- Docs and code comments in this repo are Russian (project convention, see root `CLAUDE.md`); commit messages themselves are English (observed practice in `git log`).
- No tech-debt registry file in this repo. Accepted/declined findings get recorded as: (a) a doc comment at the relevant call site explaining the tradeoff, and (b) a dated section in `docs/notes/analyst-task-splitting.md` (or the equivalent notes file for whatever feature area the PR touches) — search `docs/notes/` for an existing file on-topic before creating a new one.
- Specs live under `docs/openspec/` (Comet Classic workflow) — a PR that changes behavior governed by an existing spec should update it in the same PR, but pr-converge fixes to an already-shipped feature don't need a new OpenSpec change of their own.

## External reviewer
- GitHub App review bot, triggered by `@claude <message>` in a PR comment (`gh pr comment <N> --body "@claude ..."`).
- Author is `claude[bot]` (not `claude`). Posts a placeholder first ("Claude Code is working…"), then edits that same comment in place — poll for the edit, not a new comment. Readiness = substring `Claude finished` in the body.
- Typical turnaround: 5-6 minutes for a diff in the few-hundred-line range.
- Model: Opus 5 (`.github/workflows/claude-review.yml` sets `--model claude-opus-5` explicitly). Reviews in Russian, blockers-first format (per the workflow's `--append-system-prompt`).
- Round cap (3 external rounds) is real here: rounds 1-3 of a real session each found genuine new blockers, including in the *fixes* from the previous round (a dedup mechanism was buggy twice in a row — round 2 fixed round 1's bug, round 3 found round 2's fix was itself still buggy). Read every round's findings against the actual code before trusting them, including your own prior round's fix.

## Notes for next time
- This repo has real security-relevant surface in `internal/tracker/jira` (host-validation on attachment downloads, REST-path construction from externally-supplied ids) and `internal/tracker/mock` (filesystem-path construction from the same ids) — worth a closer read than average on any PR touching attachment or marker-parsing code.
- `internal/tracker/marker.go`'s `ParseMarker` is the sole choke point validating marker-tag values (e.g. `attachment:<id>`) — but downstream consumers (`mock.GetAttachment`, `jira.GetAttachment`) should also independently validate, not just trust the choke point, since defense-in-depth here is cheap (see `tracker.ValidAttachmentID`).
