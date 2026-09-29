# Verification Report: yougile-dependencies-attachments

- Date: 2026-09-29
- Mode: full (`comet state scale`: 13 tasks, 1 capability, 22 changed files)
- Branch: `comet/yougile-dependencies-attachments`, base `69c5b89` (master after `yougile-adapter-core`)
- Verification rounds: 2. Round 1 found 0 critical and 2 warnings. Both were fixed through a verify-fail → build repair (docs only). Round 2 is this report.

## Summary

| Dimension | Status |
|---|---|
| Completeness | 13/13 `tasks.md` items `[x]`, including the added 4.4; 14/14 plan tasks checked. Reframed items carry notes: 1.1, 1.2, 2.1, 3.1, 3.2, 3.3, 3.4 |
| Correctness | All 12 delta-spec scenarios are covered by unit tests, the live test, or both, and those tests fail if the behavior breaks (independent verifier) |
| Coherence | The implementation follows the Design Doc. Its departures from the Open-phase `design.md` are recorded in Design Doc §7 |

## Evidence (fresh runs, 2026-09-29)

| Check | Command | Result |
|---|---|---|
| Build | `go build ./...` | exit 0 |
| Vet | `go vet ./...`; `go vet -tags yougile_live ./internal/tracker/yougile/` | clean |
| Format | `gofmt -l .` | no output |
| Tests | `go test -count=1 ./...` | 21 packages `ok`; `cmd/validate-result` has no test files |
| Spec validation | `comet classic openspec -- validate yougile-dependencies-attachments --strict` | valid, re-run after the spec wording fix |
| Live smoke (new) | `go test -tags yougile_live -run TestLiveDependenciesAndAttachments` against `office-polygon` | PASS (22.9 s): dependency gate blocks, then releases; a single idempotent chat note; attachment round-trip byte-exact (NUL and 0xFF bytes) with the Cyrillic name `отчёт split.json` |
| Live smoke (change 1 lifecycle, under the new namespace) | `go test -tags yougile_live -run TestLiveLifecycle` against `office-polygon` | PASS (6.4 s) |

Both live tests were re-run at verify time on the final code. The first run, in Build task 13, came before the final-review fix wave (commits `53763b9`, `69b7d8f`, `4ecd05d`), so it does not count as evidence. `TestLiveLifecycle` had not been run since the namespace migration until this verify run.

## Checklist (full verification)

1. **Tasks complete: PASS.** Every box is checked. Round 1 flagged that 3.2 and 3.3 still said "manifest" and had no reframing note; both notes were added.
2. **Matches the Open-phase `design.md` decisions: PASS.** The departures are no manifest, the `virtual_office` namespace, the chat note, `List` including archived cards, and the V1 ruling. All are recorded in Design Doc §7.
3. **Matches the Design Doc: PASS.**
   - §2: codec and namespace rules.
   - §3: `LinkDependsOn` step order.
   - §4.1: attachment discovery.
   - §4.2: `AddAttachment`.
   - §4.3: `GetAttachment`, with the keyless client, same-host redirects capped at 5, and the URL rebuilt on `BaseURL`.
   - §5: the `var _ tracker.Tracker` assertion.
   - One difference, recorded in code comments: `v <= 0` is rejected, which is stricter than the §2 wording `v > 1`.
4. **All delta-spec scenarios pass: PASS.** Each scenario is mapped to its tests in the independent verifier's report (Build review records).
5. **`proposal.md` goals met: PASS.**
   - `LinkDependsOn` feeds the unchanged `pipeline.UnmetDependencies` gate.
   - Attachments round-trip.
   - `tracker.Tracker` is satisfied.
6. **Delta spec vs Design Doc: PASS.** Round 1 flagged the newer-schema scenario wording ("reading … fails") against listings that skip such cards. The scenario now says a single read or a change fails, and a listing skips the card and reports it. This matches §2 and `TestListingsSkipCardWithUnreadableOfficeData`.
7. **Design Doc locatable: PASS.** `docs/superpowers/specs/2026-09-29-yougile-dependencies-attachments-design.md`, frontmatter `comet_change: yougile-dependencies-attachments`.

## Security

- The API key never goes to the file host. Downloads use a separate client with no headers, and it deletes `Authorization` on redirect. Tests check this on the server side, and a mutation probe proves that using the API client would be caught.
- A rebuilt download URL cannot leave `/user-data/<uuid>/` on the API host:
  - encoded separators, dot segments and malformed segments are rejected;
  - the exact wire path is pinned by tests.
- External input is handled defensively:
  - description HTML;
  - chat text;
  - the upload answer;
  - `textHtml`, which is HTML-escaped.
- There are no hardcoded secrets. The live test reads its credentials from the environment.

## Build-phase reviews

`review_mode: standard`. Per-task risk reviews ran for tasks 2, 5, 7, 8, 9, 10, 11 and 12. Two tasks needed one fix round each:
- Task 7: encoded separators and malformed segments.
- Task 11: the wire path was not asserted.

The final lightweight review returned "With fixes":
- An archived dependency blocked its dependents forever. The owner chose to make `List` include archived cards.
- `FindByMarker` failed the whole project on a non-object `apiData`.
- `textHtml` was not escaped.

One fix wave resolved all three, and the re-review found every item addressed.

## Accepted residuals (not blocking; owner visibility)

- **An archived split parent still gets its split completed.** `CompleteSplits` (`internal/pipeline/splits.go:61`) now also sees archived parents in Blocked. It will create children and close the parent of a confirmed split that a person has archived. A fix needs an `Archived` flag on `tracker.TaskRef`/`Task`, which is an interface change outside this change.
- **Carried to `yougile-wiring-and-docs`:**
  - put the API-key user's email into the runner `Accounts`, or the office's notes and file messages count as human replies;
  - route `Tracker.Logf` to the runner's logger;
  - live-check a file named with `&` (server URL encoding, UI rendering).
- **Deferred minors from reviews:**
  - body reads are unbounded (60 s timeout only);
  - names containing a literal `%XX` may not round-trip;
  - link text used as the name can drop the extension;
  - `GetAttachment` costs 2 requests for a description link and 3 for a chat file (after the PR #25 cleanup) against the 50 req/min limit, with no 429 handling;
  - several test-hygiene items.
  
  All were triaged "can wait" by the final reviewer.

## Final assessment

0 critical issues and 0 open warnings. Ready for archive.
