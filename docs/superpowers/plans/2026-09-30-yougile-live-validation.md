---
change: yougile-live-validation
design-doc: docs/superpowers/specs/2026-09-30-yougile-live-validation-design.md
base-ref: a78eca9f9326a2c52c3fbf22e8c149b5df0e6bcf
---

# yougile-live-validation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove, with recorded evidence, that the `yougile` tracker drives the real `analyst → implementer → reviewer` chain, the PR pass, the human-reply path, attachment download and split-with-dependencies against the real YouGile API, and write the findings into `docs/notes/yougile-live-retro.md`.

**Architecture:** No product code. The runner is built from this worktree by the `./bin/runner` wrapper (which sets `OFFICE_CONFIG_ROOT` to the worktree root and builds `${OFFICE_HOME}/bin/runner-dev`), runs against `OFFICE_HOME=~/.office`, the YouGile project `office-wiring` under runner key `YGW`, and the git repo `kao73/office-pr-probe`. Each "implementation" task is an operational step: run a command or wait for a HUMAN step, capture evidence into an evidence log, check the pass condition. The only repository file this change adds besides this plan is the retro note.

**Tech Stack:** Go runner (`./bin/runner`), `sbx` sandboxes, YouGile REST API v2 (`https://yougile.com/api-v2`), `curl` + `jq`, `gh`, GitHub.

**Spec:** `docs/superpowers/specs/2026-09-30-yougile-live-validation-design.md` (read it together with this plan). Scope background: `docs/openspec/changes/yougile-live-validation/{proposal,design,tasks}.md`.

## Global Constraints

- **No product code in this change.** A defect is diagnosed, recorded, and becomes a **new follow-up change** against the affected capability. It is never patched in this branch (Design Doc §6).
- **A defect that blocks the run stops the run.** Report the diagnosis to the owner and wait for the choice: separate hotfix change now then resume, or record and stop.
- **`Clens` is never touched.** No command, MCP call or UI action against it. The only YouGile project is `office-wiring` (`22664340-315a-44a6-92a6-a774e8a892d4`).
- **Human account:** `kao@uplinesoft.com` (company admin). **Office account:** `kao@simbirsoft.com` (project role `worker`, key in `YOUGILE_OFFICE_API_KEY`).
- **Runner key `YGW`**, repo `kao73/office-pr-probe`, default branch `master`, `OFFICE_HOME=~/.office`.
- **Every `runner tick` / `complete-splits` is started with Bash `run_in_background: true` and awaited through its task notification.** Never a foreground `timeout`, never `ScheduleWakeup`, never `runner loop` (rate limit: 50 req/min per company).
- **Credentials are inlined per command** (the non-interactive shell does not read `~/.zshrc`), and no secret value is ever printed. The one allowed presence check is `[ -n "$VAR" ] && echo set`.
- **Debris stays as evidence:** `agent/*` branches and PRs in `office-pr-probe`, cards on `office-wiring`, `~/.office/worktrees`. Cleanup only when the owner asks.
- **Budgets are in `warn` mode.** Record spend, do not stop on it.
- Retro note in Russian; identifiers, terms and config keys in English (repo convention).

## Review Focus

1. **Lease left behind after a platform kill of a background tick.** Expected: the recovery procedure (Task 4 "On failure") is followed, and the lease is cleared through `apiData.virtual_office`, not ignored. The next tick claims the task again.
2. **Rate-limit `429` in the middle of a tick.** Expected: the runner reports "превышен rate limit YouGile" for that pass and does not retry. The step is re-run after a minute. The retro records it as an observation, not a defect.
3. **A human comment written under the office account** (owner logged in as the wrong user). Expected: the office does not hear it and the task stays `Blocked`. Task 5 checks the chat author email before the tick so this is not misread as a defect.
4. **`analyst` does not ask a question or does not propose a split** (nondeterministic role output). Expected: this is recorded as nondeterminism, not an adapter defect. The card is reworded and re-issued (Tasks 4 and 7).
5. **An attachment that shows up in the task but does not reach the role.** Expected: the pass condition in Task 6 requires the attachment's content in the committed result, not just the presence of the attachment.

## Evidence log (used by every task)

Evidence is appended as it happens to `~/.office/yougile-live-evidence.md`. It lives outside the repo, next to the ledger, and outlives the session. Each entry has a UTC timestamp, the step id (for example `P1` or `T1.3`), the command, the trimmed output and the pass/fail verdict. Task 10 condenses it into the retro. It never contains secret values.

### Shared command fragments

Credential prefix. Paste it at the start of every Bash call that talks to YouGile, GitHub or the agent:

```sh
eval "$(grep -hE '^ *export +(YOUGILE_OFFICE_API_KEY|CLAUDE_CODE_OAUTH_TOKEN)=' ~/.zshrc)"; export GITHUB_TOKEN=$(gh auth token)
```

Working directory for every runner command: `/Users/aleksejkolesnikov/IdeaProjects/virtual-office/.worktrees/yougile-live-validation`. The runner is always started as `./bin/runner …` from there.

Card inspection with the office key. `<task-id>` is the YouGile task id that `runner ls` prints as the key:

```sh
curl -fsS -H "Authorization: Bearer $YOUGILE_OFFICE_API_KEY" \
  "https://yougile.com/api-v2/tasks/<task-id>" |
  jq '{id, idTaskProject, title, columnId, archived, office: .apiData.virtual_office}'
```

Chat inspection (system messages appear only with `includeSystem=true`):

```sh
curl -fsS -H "Authorization: Bearer $YOUGILE_OFFICE_API_KEY" \
  "https://yougile.com/api-v2/chats/<task-id>/messages?includeSystem=true&limit=100" |
  jq '.content[] | {id, fromUserId, text: (.text[0:200])}'
```

Map `fromUserId` to an email with `GET /api-v2/users/<id>` (office key). The office account is `kao@simbirsoft.com`. Anything else is human.

Ledger tail and YouGile log lines of the last tick (tick stdout/stderr goes to the background task output file):

```sh
tail -n 5 ~/.office/ledger.jsonl | jq -c '{task, role, outcome, cost: .cost_usd}'
grep -n 'yougile' <background-output-file>
```

If the ledger field names differ from the ones above, print the raw line with `tail -n 5 ~/.office/ledger.jsonl` and record the real names in the evidence log instead of guessing.

Admin-key reads (column ids, project shape as admin) use the owner's key through the `yougile-mcp` MCP tools (`get_boards`, `get_columns`, `get_project`). They already hold the admin key, and it never passes through the shell.

---

### Task 1: Setup `~/.office` for `YGW` (Design Doc §2)

**tasks.md:** 2.2, and the config half of 1.2.

**Files:**
- Create (outside repo): `~/.office/tracker-yougile.yaml`, `~/.office/projects.local.yaml`, `~/.office/yougile-live-evidence.md`
- Repo: none

**Interfaces:**
- Produces: a working `OFFICE_HOME` with exactly one project `YGW` (`tracker: yougile`). No `tracker.yaml`, so JIRA is not opened. Later tasks rely on the column-id → status map recorded in the evidence log.

- [ ] **Step 1: Confirm the starting state of `~/.office`**

Run: `ls -la ~/.office; ls ~/.office/projects.local.yaml ~/.office/tracker*.yaml 2>&1`
Expected: `bin/`, `ledger.jsonl`, `runs/`, `yougile-issue-key.sh`. No `projects.local.yaml` and no `tracker*.yaml`. If a `projects.local.yaml` exists with other projects (for example `EXP` on JIRA), **stop and ask the owner**: every tracker a project names is opened on each tick, and one that cannot be opened stops all offices.

- [ ] **Step 2: Lay down the samples**

Run: `./bin/runner init`
Expected: prints what it created under `~/.office` (samples `tracker-yougile.example.yaml`, `projects.local.example.yaml`, `scheduler/`). It builds `~/.office/bin/runner-dev` from this worktree.

- [ ] **Step 3: Read the board and column ids with the admin key**

Use `mcp__yougile-mcp__get_boards` with project id `22664340-315a-44a6-92a6-a774e8a892d4`. Then use `mcp__yougile-mcp__get_columns` for the board titled `office`. Record board id and the eight `{id, title}` pairs in the evidence log.
Pass: exactly one column per graph status `Backlog, Analysis, Ready, InProgress, Review, Approved, Done, Blocked`. Also record any extra (off-graph) column. T2 C2 needs one for "move out of the graph" (Task 9). If none exists, note that the owner must add one before Task 9.

- [ ] **Step 4: Write `~/.office/tracker-yougile.yaml`**

```yaml
base_url: https://yougile.com
api_key_env: YOUGILE_OFFICE_API_KEY
also_agents: []
projects:
  YGW:
    project_id: 22664340-315a-44a6-92a6-a774e8a892d4
    columns:
      Backlog: <id from Step 3>
      Analysis: <id>
      Ready: <id>
      InProgress: <id>
      Review: <id>
      Approved: <id>
      Done: <id>
      Blocked: <id>
    create_status: Backlog
```

Fill all eight ids with the real values from Step 3. None may stay a placeholder.

- [ ] **Step 5: Write `~/.office/projects.local.yaml`**

```yaml
YGW:
  repo_url: https://github.com/kao73/office-pr-probe.git
  default_branch: master
  tracker: yougile
  forge: github
  auto_merge:
    enabled: false
    target_branch: master
```

`forge: github` is required for the PR pass to open a PR. Without it the pass degrades to `pr-skipped` → `Done`. `auto_merge` stays off because the human merges. `network`: start with no project additions. After the first sandboxed run, check `sbx policy log` for blocked hosts. If a role needed GitHub from inside the sandbox, add the hosts under `YGW.network` and record that in the evidence log (Design Doc §6 "Network": YouGile is reached only by the runner on the host).

- [ ] **Step 6: Check credentials and repo access without printing values**

```sh
eval "$(grep -hE '^ *export +(YOUGILE_OFFICE_API_KEY|CLAUDE_CODE_OAUTH_TOKEN)=' ~/.zshrc)"; export GITHUB_TOKEN=$(gh auth token)
[ -n "$YOUGILE_OFFICE_API_KEY" ] && echo yougile-set
[ -n "$CLAUDE_CODE_OAUTH_TOKEN" ] && echo oauth-set
[ -n "$ANTHROPIC_API_KEY" ] && echo "WARNING anthropic api key set"
curl -fsS -H "Authorization: Bearer $GITHUB_TOKEN" https://api.github.com/repos/kao73/office-pr-probe | jq '.permissions.push'
git ls-remote --heads https://github.com/kao73/office-pr-probe.git | awk '{print $2}'
```

Expected: `yougile-set`, `oauth-set`, no warning line, `true`. The heads list has `master` and `agent/PROBE-1..4`, and no branch that could collide with the office's new ones. Record the heads list as the "before" snapshot.

- [ ] **Step 7: Open the evidence log**

Create `~/.office/yougile-live-evidence.md` with a header (date, worktree commit `git rev-parse HEAD`, column map from Step 3, repo heads snapshot).

**On failure:** config rejected on read (bad key, column mismatch) → fix `~/.office` files (operator config, not product code) and re-run. Credentials missing → stop and ask the owner.

---

### Task 2: Preflight P1 (`doctor`) and P2 (project shape) (Design Doc §3)

**tasks.md:** 1.1; new 1.3, 1.4 (see "tasks.md additions").

**Files:** evidence log only.

**Interfaces:**
- Consumes: Task 1 `~/.office` config.
- Produces: office account email as `yougile:account` prints it, used by Task 5 to tell human from office.

- [ ] **Step 1: P1 — run `doctor`**

```sh
eval "$(grep -hE '^ *export +(YOUGILE_OFFICE_API_KEY|CLAUDE_CODE_OAUTH_TOKEN)=' ~/.zshrc)"; export GITHUB_TOKEN=$(gh auth token)
./bin/runner doctor
```

Pass: every finding is `ok`, including `config:tracker-yougile.yaml`, `cred:YOUGILE_OFFICE_API_KEY`, `yougile:open`, `yougile:account` (email `kao@simbirsoft.com`). Record the full output. It is the first live `doctor` after the PR #26 pr-converge fixes. A `warn` unrelated to YouGile (for example old office snapshots) is recorded and analysed. It is not automatically a fail.

- [ ] **Step 2: P2 — project shape as seen by the office key (read-only)**

```sh
eval "$(grep -hE '^ *export +YOUGILE_OFFICE_API_KEY=' ~/.zshrc)"
curl -fsS -H "Authorization: Bearer $YOUGILE_OFFICE_API_KEY" \
  https://yougile.com/api-v2/projects/22664340-315a-44a6-92a6-a774e8a892d4 | jq '{keys: keys, users}'
curl -fsS -H "Authorization: Bearer $YOUGILE_OFFICE_API_KEY" https://yougile.com/api-v2/users/me | jq '{id, email}'
```

Record: the top-level keys of the response, and whether `users` is present and contains the office user's id with role `worker`. For comparison, run the same read as admin through `mcp__yougile-mcp__get_project` and record only its keys and whether `users` shows the office role.
Pass: the shape is recorded. Either answer ("role visible" / "role not visible") passes. "Not visible" is a finding for the deferred "doctor cannot see `observer`" item and is **not** fixed here.

**On failure:** `doctor` shows `fail` on a YouGile check → diagnose. A config error on our side → fix `~/.office` and re-run. An adapter/doctor defect → stop, report to the owner (Global Constraints).

---

### Task 3: P3 (`runner ls`) and T1 card creation (Design Doc §3 P3, §4 "Card")

**tasks.md:** 1.2, 2.1 (superseded wording, see "tasks.md additions").

**Files:** evidence log only.

**Interfaces:**
- Produces: T1 YouGile task id and `idTaskProject`, used by Tasks 4–6.

- [ ] **Step 1: P3 on the empty board**

```sh
eval "$(grep -hE '^ *export +YOUGILE_OFFICE_API_KEY=' ~/.zshrc)"
./bin/runner ls --project YGW
```

Pass: exits 0, no error, and lists nothing, or only whatever cards the owner already has on `office-wiring`. Record the output. Note: `ls` also shows archived cards (by design, `yougile-requirements.md`).

- [ ] **Step 2: HUMAN — create T1 (pause point)**

Ask the owner to create, **logged in as `kao@uplinesoft.com`**, a card in column `Backlog` on board `office` of project `office-wiring`:
- **Title:** for example `Greeting CLI`.
- **Description** with a deliberate ambiguity, for example: "Add a `greet` command that prints the greeting from the attached spec. The output may be plain text or JSON. Choose one."
- **Attachment** `greeting-spec.txt` with content the implementation must use verbatim. For example, a unique line such as `Hello from YGW, token 7f3a` that cannot be guessed.

Wait for the owner to confirm. Record in the evidence log the exact attachment text and a hash: the owner pastes the text, or the file is read through the chat message. Also record the card id.

- [ ] **Step 3: P3 with T1 present**

Run `./bin/runner ls --project YGW` again (office key inlined).
Pass: T1 is listed with status `Backlog` (derived from its column), no lease. Record the key exactly as `ls` prints it. Every later step refers to it as `<T1>`.

**On failure:** `ls` errors or shows T1 in a wrong status → capture the output, then run the card inspection command to compare `columnId` with the Task 1 map. A mismatch in the adapter is a blocking defect → stop and ask the owner.

---

### Task 4: T1 — human column move, `analyst` claim, question, negative control (Design Doc §4 steps 1–3)

**tasks.md:** 3.1 (claim/comment/transition part); new 3.5.

**Files:** evidence log only.

**Interfaces:**
- Consumes: `<T1>` from Task 3.
- Produces: T1 in `Blocked` with a typed question from `analyst`. Task 5 answers it.

- [ ] **Step 1: HUMAN — move T1 from `Backlog` to `Analysis` (pause point)**

The owner drags the card in the YouGile UI. Then run `./bin/runner ls --project YGW` (office key inlined).
Pass: T1 shows `Analysis`. This proves that a human column move reads as a status change.

- [ ] **Step 2: Tick (background)**

Bash with `run_in_background: true`:

```sh
cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office/.worktrees/yougile-live-validation && eval "$(grep -hE '^ *export +(YOUGILE_OFFICE_API_KEY|CLAUDE_CODE_OAUTH_TOKEN)=' ~/.zshrc)" && export GITHUB_TOKEN=$(gh auth token) && ./bin/runner tick
```

Wait for the completion notification. Do not poll with sleep.

- [ ] **Step 3: Capture evidence of claim and question**

While the run is going (optional), use the card inspection command: `apiData.virtual_office.lease` should be set (owner/role and `lease_until`). After completion, record:
- the card's column (expected `Blocked`);
- `apiData.virtual_office` (lease cleared: `lease` null);
- the chat: a claim comment whose first line carries the `analyst` role marker, authored by `kao@simbirsoft.com`, then the question comment (typed question, for example `Q1: …`), also from the office email;
- the ledger tail (one `analyst` run, its outcome `needs_human`, cost);
- `grep yougile` lines from the background output;
- `git ls-remote --heads https://github.com/kao73/office-pr-probe.git`: whether analyst pushed a plan branch, and its name;
- request count estimate: 3 + N + 1 per runner process plus 2–3 per attachment download. Record N as observed.

Pass: all of the above as expected, and the attachment was downloaded without error (no attachment-related `yougile:` error line).

- [ ] **Step 4: Negative control — tick with no reply (background)**

Run the same background tick as Step 2 and await the notification.
Pass: T1 stays in `Blocked`, no new ledger run for T1, no new office comment in its chat, no lease in `apiData`. Record the output (for example that there was no work, or silence).

**On failure:**
- `analyst` went straight to `Ready` without asking (nondeterminism) → record it. Ask the owner to create a reworded card with a sharper ambiguity, and redo Task 3 Step 2 onward with the new card. Do not treat this as a defect.
- The background task was killed by the platform → recovery:
  1. `sbx ls`.
  2. If running, `sbx exec <name> -- ps aux`. If `claude` is running, wait until `sbx ls` shows it stopped. If only `sleep infinity` is running, go on.
  3. `git -C <workspace path from sbx ls> log --oneline -3`.
  4. `sbx rm <name> --force`.
  5. Clear the lease in `apiData`: GET the task, set `.apiData.virtual_office.lease = null` with `jq`, keep the rest of `apiData` intact, `PUT /api-v2/tasks/<id>` with `{"apiData": …}` using the office key, then re-read to confirm.
  6. Tick again.

  Record the kill.
- The negative control moved or re-ran T1 → blocking defect (human/office distinction wrong) → stop and ask the owner.

---

### Task 5: T1 — human reply recognized, `analyst` continues to `Ready` (Design Doc §4 step 4)

**tasks.md:** 3.3.

**Files:** evidence log only.

- [ ] **Step 1: HUMAN — reply in the T1 chat as `kao@uplinesoft.com` (pause point)**

The owner answers the question in the task chat, for example `Q1: plain text`, logged in as `kao@uplinesoft.com`.
Before ticking, run the chat inspection command and confirm that the reply's `fromUserId` maps to `kao@uplinesoft.com`, **not** `kao@simbirsoft.com`. If it is the office email, the owner is logged in as the wrong user. Ask them to re-post and do not tick.

- [ ] **Step 2: Tick (background)**

Same command as Task 4 Step 2. Await the notification.

- [ ] **Step 3: Capture evidence**

Record: T1 column (expected `Ready`, or further if `analyst` resumed and finished in the same tick), the chat (office acknowledgement or plan comment with role marker), `apiData.virtual_office` (lease cleared, `human_wait` or equivalent cleared), the ledger tail, `yougile:` lines, and the plan branch/commit in `office-pr-probe`.
Pass: the reply was treated as human input and `analyst` continued to `Ready`.

**On failure:** T1 stays `Blocked` after a verified human-account reply → blocking defect → stop and ask the owner. `analyst` asked a second question → answer it the same way (another HUMAN pause) and record the extra round.

---

### Task 6: T1 — `implementer`, `reviewer`, PR, human merge, `Done` (Design Doc §4 steps 5–6)

**tasks.md:** 3.1 (end to end), 3.2; new 3.4.

**Files:** evidence log only.

**Interfaces:**
- Consumes: T1 in `Ready`.
- Produces: merged PR URL and final commit on `master` of `office-pr-probe`.

- [ ] **Step 1: Tick → `implementer` (background)**

Same background tick command. Await. Record column (expected `Review`), chat comments (role marker, office email), lease set/cleared, ledger run, and `git ls-remote` heads (the new task branch `agent/<key>`, recorded exactly as pushed).

- [ ] **Step 2: Verify the attachment reached the role**

```sh
git clone --quiet https://github.com/kao73/office-pr-probe.git ~/.office/probe-check 2>/dev/null || git -C ~/.office/probe-check fetch --quiet
git -C ~/.office/probe-check log --oneline origin/<task branch> -5
git -C ~/.office/probe-check grep -n '<unique token from greeting-spec.txt>' origin/<task branch>
```

Pass: the unique token from the attachment is present in the committed code or its output fixture. Presence in the task `Attachments` alone does not pass. If absent, first check the run log under `~/.office/runs/` to see whether the file was delivered. Absent from the input means a blocking adapter/pipeline defect → stop. Delivered but not used means role nondeterminism → record it.

- [ ] **Step 3: Tick → `reviewer` (background)**

Same command. Await. Record column (expected `Approved`, or back to `InProgress`/`Ready` if the reviewer returned it), chat, ledger, and any reviewer fix-up commits. If the reviewer returned the work, repeat Step 1 and this step until `Approved` (the return-round limit is 3). Record every round.

- [ ] **Step 4: Tick → PR pass (background)**

Same command. Await. Pass: a PR is open in `office-pr-probe` from the task branch into `master`. The chat has an office comment with the PR link. The card stays in `Approved`. Record:

```sh
gh pr list --repo kao73/office-pr-probe --state open --json number,title,headRefName,url
```

(The PR pass may already have happened in the Step 3 tick. If so, skip this tick and record that.)

- [ ] **Step 5: HUMAN — merge the PR (pause point)**

The owner merges the PR on GitHub. Confirm with `gh pr view <n> --repo kao73/office-pr-probe --json state,mergedAt,mergeCommit`.

- [ ] **Step 6: Tick → `Done` (background)**

Same command. Await. Pass: T1 is in column `Done`, the lease is empty, a final office comment is in the chat, and `./bin/runner worktree ls` no longer lists T1's worktree. Record everything plus `./bin/runner ledger --since 24h` totals for T1.

**On failure:** the PR is not opened (for example a token without `Pull requests: write`) → record it, stop and ask the owner. The merge is not seen → run one more tick after a minute. If it is still not seen, that is a blocking defect → stop.

---

### Task 7: T2 — split proposal and double confirmation (Design Doc §5 step 1)

**tasks.md:** new 3.6.

**Files:** evidence log only.

- [ ] **Step 1: HUMAN — create T2 in `Backlog` and move it to `Analysis` (pause point)**

The owner creates, as `kao@uplinesoft.com`, a card that is deliberately too big for one cycle, with two parts where the second builds on the first. For example: "Add module `stats` that counts greetings per name; then add a `report` command that prints a table using `stats`." Then they drag it to `Analysis`. Confirm with `./bin/runner ls --project YGW`. Record `<T2>`.

- [ ] **Step 2: Tick (background)**

Same command. Await. Pass: T2 in `Blocked`. The chat has an `analyst` comment with `outcome:split` and a confirmation question `Q1` listing two children with "second depends on first". Record it.

- [ ] **Step 3: HUMAN — answer `Q1: yes` as `kao@uplinesoft.com` (pause point)**

Verify the author with the chat inspection command before ticking.

- [ ] **Step 4: Tick (background) — second confirmation**

Same command. Await. Pass: `analyst` repeats the split, which counts as the second confirmation. T2 has a confirmed split with no children yet. Record the chat and ledger.

**On failure:** `analyst` does not propose a split (nondeterminism) → record it. The owner rewords T2 to be more obviously two-stage and re-issues it. Do not count this as a defect.

---

### Task 8: T2 — `complete-splits` creates children, dependency link, idempotency (Design Doc §5 steps 2–3)

**tasks.md:** new 3.7.

**Files:** evidence log only.

**Interfaces:**
- Produces: `<C1>`, `<C2>` task ids.

- [ ] **Step 1: `complete-splits` (background)**

```sh
cd /Users/aleksejkolesnikov/IdeaProjects/virtual-office/.worktrees/yougile-live-validation && eval "$(grep -hE '^ *export +(YOUGILE_OFFICE_API_KEY|CLAUDE_CODE_OAUTH_TOKEN)=' ~/.zshrc)" && export GITHUB_TOKEN=$(gh auth token) && ./bin/runner complete-splits
```

Await. Pass: two new cards C1 and C2 in the `create_status` column (`Backlog`), created by the office account. The card inspection command on C2 shows, under `apiData.virtual_office`, the "depends on C1" link (record the real field name and value as found). Record the parent's chat and `apiData` changes too.

- [ ] **Step 2: Idempotency — second `complete-splits` (background)**

Same command. Await. Pass: `./bin/runner ls --project YGW` still shows exactly two children, no duplicates. Also check the YouGile board for duplicate titles through `mcp__yougile-mcp__get_tasks` on the Backlog column, read-only. Record it.

**On failure:** duplicates created → blocking defect (idempotencyKey not honoured by the live API or not sent) → stop, ask the owner. Leave the duplicates in place as evidence.

---

### Task 9: T2 — dependency gate closed, then open; C2 optional (Design Doc §5 steps 4–6)

**tasks.md:** new 3.8.

**Files:** evidence log only.

- [ ] **Step 1: HUMAN — move C1 and C2 to `Ready` (pause point)**

Confirm with `runner ls`.

- [ ] **Step 2: Gate closed — tick (background)**

Same tick command. Await. Pass: C1 is claimed (lease or run for C1, `implementer` role). C2 has **no lease and no ledger run**. Record the ledger tail filtered on both ids and the `apiData` of C2.

- [ ] **Step 3: Drive C1 to `Approved` + PR**

Repeat Task 6 Steps 1, 3 and 4 for C1: background ticks, await each, and record column, chat, ledger and PR. Before each tick, check that C2 is still unclaimed.

- [ ] **Step 4: HUMAN — merge C1's PR (pause point), then tick (background)**

Pass: C1 is in `Done`. In the same or the next tick C2 gets claimed. Record the tick in which C2 was first claimed.

- [ ] **Step 5: C2 is optional — finish or park**

If C2 runs smoothly, continue it through Task 6 Steps 1–6 (merge by HUMAN). Otherwise:
1. Wait for any run to end, or recover as in Task 4.
2. Confirm `apiData.virtual_office.lease` is null. Clear it as in Task 4 recovery if not.
3. Ask the owner to move C2 to the off-graph column from Task 1 Step 3. Do not archive it.

Record which path was taken.

**On failure:** C2 claimed before C1 is `Done` → blocking defect (dependency gate) → stop, ask the owner.

---

### Task 10: Retro note, follow-ups, commit (Design Doc §6)

**tasks.md:** 4.1, 4.2.

**Files:**
- Create: `docs/notes/yougile-live-retro.md`

- [ ] **Step 1: Write the retro in Russian**

Follow the heading style of `docs/notes/stage-5-live-retro.md`: `# …` title with scope, a short intro paragraph, then `##` sections. Required sections:
- `## Статус`: one line per P1, P2, P3, T1, T2 with a pass/fail verdict.
- `## Окружение`: worktree commit, `office-wiring`, `YGW`, `office-pr-probe`, `OFFICE_HOME`, accounts (emails only, no keys).
- `## P1–P3`, `## T1`, `## T2`: for each, what was checked, the evidence (card ids and `idTaskProject`, branches, commits, PR links, ledger excerpts, `yougile:` log lines, request-count estimate) and what failed.
- `## Недетерминизм ролей`: any re-issued cards and why.
- `## Находки и follow-up`: each defect with the affected capability and the archived change it traces to (`yougile-adapter-core`, `yougile-dependencies-attachments`, `yougile-wiring-and-docs`), and the proposed follow-up change name. Include the P2 finding for the deferred "doctor cannot see `observer`" item.
- `## Мусор, оставленный как доказательство`: branches, PRs, cards, worktrees.

No secret values. Condensed from `~/.office/yougile-live-evidence.md`.

- [ ] **Step 2: Follow-up work (4.2)**

For each structural defect, prepare a one-paragraph follow-up change proposal (name, capability, symptom, evidence link into the retro) and **present it to the owner**. Do not open changes or edit code without the owner's go-ahead. If no structural defect was found, write that explicitly in the retro.

- [ ] **Step 3: Commit the retro**

```bash
git add docs/notes/yougile-live-retro.md
git commit -m "docs(yougile-live-validation): live validation retro

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

## tasks.md mapping

| tasks.md | Plan task |
|---|---|
| 1.1 | Task 2 Step 1 (`yougile:account` = office email) and Step 2 (`/users/me`) |
| 1.2 | Task 1 (config), Task 3 Steps 1 and 3 (`runner ls`) |
| 2.1 | Task 3 Step 2 (demo card). The `office-polygon` sticker wording is superseded by Design Doc §7 (`office-wiring`, status = column) |
| 2.2 | Task 1 Step 5 |
| 3.1 | Tasks 4–6 |
| 3.2 | Task 6 |
| 3.3 | Task 5 |
| 4.1 | Task 10 Step 1 |
| 4.2 | Task 10 Step 2 |

## tasks.md additions (to append; this plan does not edit tasks.md)

Under `## 1. Preconditions`:

```
- [ ] 1.3 P1: run live `runner doctor` against `office-wiring`/`YGW` after the
      PR #26 fixes; every finding, including the YouGile stage, is `ok`.
- [ ] 1.4 P2: record the shape of `GET /api-v2/projects/{id}` under the office
      key, including whether `users` exposes the office account's own role
      (read-only; a missing role is a finding, not a fix).
```

Under `## 3. Live run`:

```
- [ ] 3.4 T1 carries an attachment the work depends on; confirm its content
      reaches the role (present in the committed result), not only that it
      is listed in the task.
- [ ] 3.5 Negative control: a tick with no human reply leaves the task in
      `Blocked` with no new run, comment or lease.
- [ ] 3.6 T2 split: `analyst` proposes `outcome:split`, the human answers
      `Q1: yes`, and the repeated split is taken as the second confirmation.
- [ ] 3.7 `runner complete-splits` creates C1 and C2 in `create_status` with
      "C2 depends on C1" recorded in `apiData`; a second run creates no
      duplicates.
- [ ] 3.8 Dependency gate: with C1 not `Done` a tick claims C1 only; after C1
      reaches `Done` via a human-merged PR the next tick claims C2; C2 is
      finished or parked in an off-graph column (not archived).
```

## Execution notes

- The order is strict: Task 1 → 2 → 3 → … → 10. Every HUMAN pause point blocks the next step until the owner confirms.
- Any "blocking defect" outcome ends execution at that step. Write what is known into the evidence log, report to the owner and wait. The retro (Task 10) is still written if the owner chooses "record it and stop".
