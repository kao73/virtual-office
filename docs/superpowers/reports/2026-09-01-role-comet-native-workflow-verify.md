## Verification Report: role-comet-native-workflow

Full verification (verify_mode: full — 12 tasks, 1 delta-spec capability, 142 files changed per `git show 56c6226 --stat`). Change is already merged to `master` (PR #4, merge commit `56c6226`) and passed three external `pr-converge` review rounds; this is Comet's own retroactive verify, run against `master` after `comet state rebind` moved `bound_branch` from the deleted `comet/role-comet-native-workflow` branch to `master`.

### Summary

| Dimension    | Status                                                             |
|--------------|---------------------------------------------------------------------|
| Completeness | 12/12 tasks done; all 4 delta-spec requirements have code evidence |
| Correctness  | 3/4 requirements match implementation exactly; 1 requirement's scenario text contradicts the actual (corrected) behavior |
| Coherence    | design.md and code agree with each other; the delta spec disagrees with both |

### Issues

#### CRITICAL — resolved during this verify pass

1. **Delta spec's Archive requirement described a scenario that never happens; real behavior was the opposite and was already documented correctly in `design.md`.** — **FIXED**
   - `docs/openspec/changes/role-comet-native-workflow/specs/role-native-workflow/spec.md:22-30` ("Requirement: Archive is deterministic runner code, not a role") stated Archive runs "as part of the runner's own deterministic **post-merge** processing" with scenario "**WHEN** the runner detects that a task's pull request has been merged **THEN** the runner itself calls `comet native archive`".
   - Actual implementation: `internal/pipeline/prpass.go:108` and `:135` call `o.archiveIfReady(task, project)` **before** `impl.OpenPR(...)` at `prpass.go:143` — i.e. Archive runs before the PR is even opened, not after a human merges it.
   - `design.md:50-60` ("Decisions") documents this exact correction and explains why: archiving after merge would mean pushing an unreviewed commit straight to the default branch, which `docs/DESIGN.md` §2.8 forbids; before the PR, the archive commit is just part of the same PR a human already reviews.
   - `proposal.md:28-32` even flags that the delta line's original "after merge" wording was "superseded already during Design" — but the delta spec file itself was never edited to match.
   - **Resolution (user decision, 2026-09-01):** the delta spec's Requirement/Scenario text was corrected in this verify pass to say Archive runs "before the task's pull request is opened", matching `design.md` and the shipped code exactly (`specs/role-native-workflow/spec.md:22-30`, current version). This is a verify-phase-allowed artifact edit — the delta spec now agrees with `design.md` and code; no return to build was needed.

#0 WARNING

1. **No dedicated golden case for Requirement 3's core scenario ("Truncated Build run resumes from the same change").**
   - `specs/role-native-workflow/spec.md:32-41` requires that an interrupted `implementer` run resume the same Comet Native change from `comet-state.yaml` rather than starting a new one.
   - `evals/implementer/` has three cases (`capability-basic-bugfix`, `capability-reads-brief-and-spec`, `escalation-ambiguous-task`) — none simulate a truncated/mid-Build resume (fixture starting with an in-progress `.comet/runtime/**` state and a partial commit).
   - This scenario *did* happen live and worked correctly (`docs/notes/stage-5-live-backlog.md`, EXP-2, implementer's third attempt continuing after two aborted runs), so the behavior is empirically validated, just not regression-guarded by `cmd/eval-roles`.
   - Recommendation: add `evals/implementer/capability-resume-mid-build/` (fixture with a pre-seeded `.comet/runtime/native/changes/<name>/state.json` mid-Build and one already-committed step) mirroring the pattern `evals/analyst/capability-resume-no-reinvoke` already uses for the analyst side of resumability. Not a blocker for archive — existing task 7.1 is satisfied by the other cases — but worth a follow-up task.

#### SUGGESTION

1. `proposal.md:64-66` ("Impact") still lists "bootstrap ownership is a design-time open question, not yet confirmed" for the `comet` CLI reaching the `sbx` sandbox image. Task 1.1 resolved this (`bootstrap/sbx-kits/comet-cli/`, vendored `.tgz` packages, `bake-comet-template.sh`). Cosmetic only — proposal.md is a point-in-time document and this doesn't affect the archived spec, but worth a one-line update if anyone edits this file again before archive.

### Design/Contract cross-checks (no issues found)

- `docs/contracts/agent-io.md:68-92` correctly documents `docs/comet/changes/<name>/` alongside the still-supported legacy `docs/changes/<KEY>/` (task 6.1 confirmed by text, not just by checkbox).
- `internal/adapters/claude/adapter.go:337-410` and `adapter_test.go:581-636` confirm the `PreToolUse` hook is wired for all three roles (`roles/{analyst,implementer,reviewer}/role.yaml` each declare `hooks.pre_tool_use`) without displacing the existing `Stop` hook — matches design.md's decision and delta-spec Requirement "Phase-scoped writes are hook-enforced" exactly, including the documented limitation (Edit on an existing file is blocked; Write of a brand-new file is not) — the delta spec's scenario only claims the Edit case, so no contradiction there.
- `roles/analyst/role.md` and `roles/implementer/role.md` confirm Requirement 1's two scenarios verbatim (analyst self-confirms Shape → `done`/`next_owner: implementer` without writing code; implementer treats an existing `build`-phase brief/spec as given, returns to `analyst` only on genuine drift instead of re-litigating).
- `roles/reviewer/role.md` confirms the mandated read-only Verifier dispatch (`dispatch-verifier` → `final-result` → `--accept-result`) and that `archive-ready` is reached only after that, matching both design.md and the (correct) "before PR opens" code path.
- The known third-party bug in `docs/notes/stage-5-live-backlog.md` ("lock coordinator Comet Native не переживает разборку sbx-песочницы") is a `comet` CLI defect (confirmed already reported upstream), not a defect of this change's code or specs — not counted against this verification.

### Final Assessment

**1 CRITICAL issue found and fixed during this verify pass** (delta spec's Archive requirement corrected to match `design.md` and shipped code — see above). No CRITICAL issues remain. 1 WARNING (missing regression coverage for one resumability scenario — not a blocker, recorded as a follow-up) and 1 cosmetic SUGGESTION (stale line in `proposal.md`, does not affect the archived spec) remain open but do not block archive.

**Ready for archive.**
