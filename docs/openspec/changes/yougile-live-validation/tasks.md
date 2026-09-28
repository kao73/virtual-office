## 1. Preconditions

- [ ] 1.1 Confirm the dedicated non-human YouGile office account from
      `yougile-wiring-and-docs` exists and works (`Whoami` returns it, it
      is distinct from any human's personal login).
- [ ] 1.2 Confirm `tracker-yougile.yaml` is in place for a chosen project
      and the runner opens it cleanly (`runner ls` against it).

## 2. Sandbox setup

- [ ] 2.1 Confirm/refresh `office-polygon`'s status sticker and add
      whatever demo task(s) the chosen role graph needs.
- [ ] 2.2 Declare the sandbox project in `projects.local.yaml` with
      `tracker: yougile`.

## 3. Live run

- [ ] 3.1 Run the runner against the sandbox project and let at least one
      role claim, work, comment, and transition a real task end to end.
- [ ] 3.2 If time allows, run the full `analyst -> implementer -> reviewer`
      graph on one task rather than a single role in isolation.
- [ ] 3.3 Exercise a human-reply scenario: post a reply from a
      distinguishable human account (not the office account) and confirm
      the runner recognizes it as human input.

## 4. Findings

- [ ] 4.1 Write up what worked, what didn't, and any follow-up fixes
      needed, in a retro note under `docs/notes/` (mirroring prior
      live-run retros for this project).
- [ ] 4.2 If a structural problem was found, open follow-up work against
      the specific change (`yougile-adapter-core`,
      `yougile-dependencies-attachments`, or `yougile-wiring-and-docs`)
      that owns the affected behavior, rather than patching it here.
