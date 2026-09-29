---
comet_change: yougile-live-validation
role: technical-design
canonical_spec: openspec
---

# yougile-live-validation — deep technical design

Change 4 of 4 in the `yougile-tracker-adapter` batch. Changes 1–3 built the
`yougile` tracker, wired it into the runner and `doctor`, and documented the
setup. This change runs the office against the real YouGile API with real
roles and records what happened. It adds no code.

Scope comes from `proposal.md` and `design.md` in
`docs/openspec/changes/yougile-live-validation/`. §7 lists where this
document departs from them.

## 1. Fixed inputs

- **Tracker project:** `office-wiring`
  (`22664340-315a-44a6-92a6-a774e8a892d4`), board `office`, one column per
  graph status. It was provisioned in change 3 for exactly this run.
- **Office account:** `kao@simbirsoft.com`, project role `worker`, key in
  `YOUGILE_OFFICE_API_KEY`. **Human account:** `kao@uplinesoft.com`
  (company admin). Comments from the human account are the human; comments
  from the office account are the office.
- **Git repository:** `kao73/office-pr-probe`, default branch `master`.
  Branches `agent/PROBE-1..4` already exist there, so the project key is
  `YGW`. With that key the office's branches are `agent/YGW-*` and cannot
  collide with the old ones.
- **Scope:** the full `analyst → implementer → reviewer` chain through
  Approved + PR, a human merge, then Done. Also the human-reply path,
  attachment download, and split with dependencies.
- **`Clens` is never touched.**

## 2. Setup

`OFFICE_HOME` is `~/.office`. It is the default location, it already holds
`ledger.jsonl` and `runs/`, and it outlives the session. The runner is built
from this worktree and run with `OFFICE_CONFIG_ROOT` pointing at it, so every
run is signed with this branch's commit.

- `~/.office/tracker-yougile.yaml`:
  - `base_url: https://yougile.com`
  - `api_key_env: YOUGILE_OFFICE_API_KEY`
  - `also_agents: []`
  - `projects.YGW`: `project_id`, the eight column ids (re-read with an
    admin key from `/api-v2/boards?projectId=…` and `/columns?boardId=…`),
    and `create_status: Backlog`.
- `~/.office/projects.local.yaml`: `YGW` with `tracker: yougile`,
  `repo_url` for `kao73/office-pr-probe`, and `default_branch: master`.
  Project `network` includes whatever GitHub access the roles need.
  `auto_merge` is off because the human merges.
- Credentials are inlined into every command, because the non-interactive
  shell does not read `~/.zshrc`: `YOUGILE_OFFICE_API_KEY`,
  `CLAUDE_CODE_OAUTH_TOKEN`, `GITHUB_TOKEN=$(gh auth token)`.

## 3. Preflight evidence

| id | action | pass condition |
|---|---|---|
| P1 | `runner doctor` | every finding, including the YouGile stage, is `ok`. This is the first live `doctor` after the PR #26 pr-converge fixes. |
| P2 | `GET /api-v2/projects/{id}` with the office key | the response shape is recorded, and in particular whether `users` exposes the office's own role. Read-only. If the role is not visible, that is a finding for the deferred "doctor cannot see `observer`" item, not something fixed here. |
| P3 | `runner ls` | runs cleanly on the empty board, and after T1 is created it lists T1 with its column-derived status. |

## 4. T1 — the main task, through Done

**Card.** The human creates T1 under `kao@uplinesoft.com` in `Backlog` with:

- **An attachment the work depends on.** A small text file, for example
  `greeting-spec.txt`, holds content the implementation must use. This
  proves the file is downloaded, including the redirect to
  `prod-user-data.yougile.com`, and reaches the role. Showing up in
  `Attachments` alone would not prove that.
- **A deliberate ambiguity.** For example, two acceptable result formats,
  so `analyst` has to ask a typed question.

**Steps.** Each step is one `runner tick --tracker yougile` started with
`run_in_background` and awaited through its notification, never with a
foreground `timeout`.

1. The human moves the card from `Backlog` to `Analysis`. This checks that a
   human column move reads as a status change.
2. `analyst` claims the task:
   - the lease fields appear in `apiData`;
   - the claim comment carries the role marker and the office email as
     author;
   - `analyst` asks its question and the task goes to `Blocked`.
3. **Negative control.** A `tick` without any reply does nothing, and the
   task stays `Blocked`.
4. The human replies in the task chat from `kao@uplinesoft.com`. The next
   `tick` recognizes that as human input, and `analyst` continues to `Ready`.
5. `implementer` takes the task to `Review`, and `reviewer` takes it to
   `Approved`. The PR pass opens a PR in `office-pr-probe` from
   `agent/YGW-<n>`.
6. The human merges the PR. The next `tick` sees the merge, moves the card
   to `Done`, and removes the worktree.

**Recorded at each step:**

- the card's column;
- the `apiData` lease fields, set on claim and cleared on release;
- comments, with role marker and author email;
- the branch, commits and PR;
- the ledger entries;
- any `yougile:` log lines;
- roughly how many requests were made against the 50/min limit.

## 5. T2 — split and dependencies

**Card.** The human creates T2 in `Backlog`. It is deliberately too big for
one cycle: two parts, the second of which builds on the first (for example,
"add module X" and "add a report that uses X").

1. **Split.**
   - The human moves the card to `Analysis`.
   - `analyst` answers `outcome:split` with a confirmation question, and the
     human answers `Q1: yes`.
   - The repeated split is the second confirmation.
2. **Children.** `runner complete-splits` creates C1 and C2 in the
   `create_status` column. The link "C2 depends on C1" is recorded in
   `apiData`.
3. **Idempotency.** A second `complete-splits` creates no duplicates, because
   `idempotencyKey` deduplicates them.
4. **Gate closed.** With both children in `Ready` and C1 not `Done`, a
   `tick` claims C1 only. C2 has no lease and no ledger run.
5. **Gate opens.** C1 goes through the chain to `Approved`, the human merges
   its PR, and C1 reaches `Done`. The next `tick` claims C2.
6. **C2 is optional.**
   - If it runs smoothly, it may finish.
   - If not, its lease is cleared and the card is moved to a column outside
     the graph. It is not archived, following `yougile-requirements.md`.

## 6. Defects, debris, findings

- **No code in this change.**
  - `proposal.md` scopes fixes back into the owning changes. All three are
    already merged and archived.
  - In practice a defect therefore becomes a **new follow-up change**
    against the affected capability. The retro records the capability and
    the change it traces to.
- **A defect that blocks the run** stops the run. The diagnosis goes to the
  owner, who picks one of:
  - a separate hotfix change now, then resume;
  - record it and stop.

  It is never patched silently in this branch.
- **Nondeterministic role output** is not an adapter defect, but the retro
  mentions it. For example, `analyst` might not ask its question or might
  not propose a split. In that case the card is reworded and re-issued.
- **Debris stays as evidence:**
  - `agent/YGW-*` branches and PRs in `office-pr-probe`;
  - cards on `office-wiring`;
  - `~/.office/worktrees`.

  Cleanup happens only when the owner asks.
- **Findings note:** `docs/notes/yougile-live-retro.md`, in Russian like the
  earlier retros. For each of P1–P3, T1 and T2 it records:
  - what was checked;
  - the evidence: card ids, commits, PR links, ledger excerpts;
  - what failed;
  - follow-ups.

### Risks

- **Rate limit.** YouGile allows 50 requests per minute.
  - One runner process costs 3 + N + 1 requests (Open + Whoami).
  - Each `GetAttachment` costs 2–3.
  - So runs are single `tick`s with no tight `loop`.
- **Background kills.** Long runs can be killed by the platform. Recovery
  steps:
  1. `sbx ls`, then `sbx exec … ps` to see whether `claude` is running.
  2. `sbx rm`.
  3. Clear the lease. Here that means the `apiData` fields, not JIRA
     custom fields.
  4. `tick` again.
- **Network.** The sandbox needs GitHub only. YouGile is reached by the
  runner on the host.
- **Budgets** are in `warn` mode. Spend is recorded and does not stop the
  run.

## 7. Departures from the open-phase artifacts

- **Project.** The tracker project is `office-wiring`, not `office-polygon`,
  and status is a **column**, not the `office_status_test` sticker. Change 3
  made status a column and provisioned `office-wiring` for this run.
- **Scope.** The chosen scope is the full chain plus merge, attachments,
  human reply and split/dependencies. `design.md` treated the full chain as
  optional.
- **Where fixes go.** A found defect goes to a new follow-up change, because
  the owning changes are archived.
- **Added checks.** The preflight adds P1 and P2. These are the PR #26
  deferred items: re-running live `doctor` and checking whether a non-admin
  can see its project role.
