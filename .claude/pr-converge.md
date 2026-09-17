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
- Author is `claude[bot]` (not `claude`). **Each `@claude` trigger comment gets its own new reply comment** — a fresh placeholder ("Claude Code is working…") that gets edited in place to the final review once done. It does NOT reuse or edit a previous round's comment. Confirmed empirically across two rounds in one session: round 1's comment (its own id) stayed finished at its own timestamp; round 2's trigger produced a brand-new comment id, starting as its own fresh placeholder.
- **Polling trap (fell into this once):** right after posting round N's trigger comment, capture its exact comment `id` (`gh pr comment <N> --body-file ... ` prints the comment URL, whose trailing `#issuecomment-<id>` is the id — or re-fetch via `gh api repos/OWNER/REPO/issues/<PR>/comments --jq '.[] | select(.user.login=="claude[bot]") | {id, created_at}'` right after posting and take the newest). Poll that specific id (`gh api repos/OWNER/REPO/issues/comments/<id> --jq .body`) for the `Claude finished` substring. Do NOT poll "the last/newest claude[bot] comment" from scratch — if round N's placeholder hasn't been created yet at the moment you start polling, "last comment" still resolves to round N-1's already-finished comment, and the loop exits immediately without ever seeing round N's real result.
- **Second polling trap (fell into this 2026-09-17):** do NOT add a `?since=<timestamp>` filter guessed from local time — GitHub timestamps are UTC and the local clock here is UTC+4, so a `since` set to "now" in local time excludes the bot's comment forever and the wait times out while the review is already posted. Filter by comment `id > <trigger id>` and `user.login=="claude[bot]"` only.
- Typical turnaround: 5-6 minutes for a diff in the few-hundred-line range (7m47s for the ~1.5k-line config-cleanup diff).
- Model: Opus 5 (`.github/workflows/claude-review.yml` sets `--model claude-opus-5` explicitly). Reviews in Russian, blockers-first format (per the workflow's `--append-system-prompt`).
- Round cap (3 external rounds) is real here: rounds 1-3 of a real session each found genuine new blockers, including in the *fixes* from the previous round (a dedup mechanism was buggy twice in a row — round 2 fixed round 1's bug, round 3 found round 2's fix was itself still buggy). Read every round's findings against the actual code before trusting them, including your own prior round's fix. Separately, in another session, round 1 found 4 confirmed important findings across 15 total, and round 2 both found 1 new confirmed bug (mirroring round 1's own fix) *and* correctly rebutted two of round 1's own "skip as minor" triage calls — treat an external round's pushback on your own prior triage as seriously as a fresh finding.

## Notes for next time
- **Commit before you probe** — bit again 2026-09-17: a `git checkout -- internal/tracker/config.go` after a mutation probe wiped an uncommitted fix in the same file. Either commit first or undo the probe by re-editing the exact lines.
- Mutation probes that stall (a loop waiting on a timer) hang `go test` for the default 10 minutes; give the probing test a `context.WithTimeout` safety net and run `go test -timeout 60s`.
- This repo has real security-relevant surface in `internal/tracker/jira` (host-validation on attachment downloads, REST-path construction from externally-supplied ids) and `internal/tracker/mock` (filesystem-path construction from the same ids) — worth a closer read than average on any PR touching attachment or marker-parsing code.
- `internal/tracker/marker.go`'s `ParseMarker` is the sole choke point validating marker-tag values (e.g. `attachment:<id>`) — but downstream consumers (`mock.GetAttachment`, `jira.GetAttachment`) should also independently validate, not just trust the choke point, since defense-in-depth here is cheap (see `tracker.ValidAttachmentID`).
