## 1. Preconditions

- [x] 1.1 Confirm the dedicated non-human YouGile office account from
      `yougile-wiring-and-docs` exists and works (`Whoami` returns it, it
      is distinct from any human's personal login).
- [x] 1.2 Confirm `tracker-yougile.yaml` is in place for a chosen project
      and the runner opens it cleanly (`runner ls` against it).
- [x] 1.3 P1: run live `runner doctor` against `office-wiring`/`YGW` after the
      PR #26 fixes; every finding, including the YouGile stage, is `ok`.
      (Done for the YouGile stage; `sbx:network` was `warn` — environment, see retro.)
- [x] 1.4 P2: record the shape of `GET /api-v2/projects/{id}` under the office
      key, including whether `users` exposes the office account's own role
      (read-only; a missing role is a finding, not a fix).

## 2. Sandbox setup

- [x] 2.1 Confirm/refresh `office-polygon`'s status sticker and add
      whatever demo task(s) the chosen role graph needs.
      (Replaced by `office-wiring` with board columns; Design Doc §7.)
- [x] 2.2 Declare the sandbox project in `projects.local.yaml` with
      `tracker: yougile`.

## 3. Live run

- [x] 3.1 Run the runner against the sandbox project and let at least one
      role claim, work, comment, and transition a real task end to end.
- [x] 3.2 If time allows, run the full `analyst -> implementer -> reviewer`
      graph on one task rather than a single role in isolation.
- [x] 3.3 Exercise a human-reply scenario: post a reply from a
      distinguishable human account (not the office account) and confirm
      the runner recognizes it as human input.
- [x] 3.4 T1 carries an attachment the work depends on; confirm its content
      reaches the role (present in the committed result), not only that it
      is listed in the task.
- [x] 3.5 Negative control: a tick with no human reply leaves the task in
      `Blocked` with no new run, comment or lease.
- [x] 3.6 T2 split: `analyst` proposes `outcome:split`, the human answers
      `Q1: yes`, and the repeated split is taken as the second confirmation.
- [x] 3.7 `runner complete-splits` creates C1 and C2 in `create_status` with
      "C2 depends on C1" recorded in `apiData`; a second run creates no
      duplicates. (Idempotency not proven live — parent already Done; retro finding 6.)
- [x] 3.8 Dependency gate: with C1 not `Done` a tick claims C1 only; after C1
      reaches `Done` via a human-merged PR the next tick claims C2; C2 is
      finished or parked in an off-graph column (not archived).

## 4. Findings

- [x] 4.1 Write up what worked, what didn't, and any follow-up fixes
      needed, in a retro note under `docs/notes/` (mirroring prior
      live-run retros for this project).
- [x] 4.2 If a structural problem was found, open follow-up work against
      the specific change (`yougile-adapter-core`,
      `yougile-dependencies-attachments`, or `yougile-wiring-and-docs`)
      that owns the affected behavior, rather than patching it here.
