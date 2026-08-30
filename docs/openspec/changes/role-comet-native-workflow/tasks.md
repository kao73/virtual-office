## 1. Dependency and bootstrap investigation

- [x] 1.1 Determine where/how the `sbx` sandbox image is customized (or confirm it is outside this repo's control) and how Node 22+ / `comet` would reach it
- [ ] 1.2 Confirm the exact `--runner-input dispatch-verifier` check schema Comet Native's Build→Verify handoff expects (blocked on `Native Runtime check 0 fields are invalid` during design-time experiments)
- [x] 1.3 Decide the archive `--finish` mode and its call site relative to the existing PR-merge detection in the runner

## 2. Adapter changes

- [x] 2.1 Add `PreToolUse` hook support to `internal/adapters/claude/adapter.go`'s `buildSettings`, alongside the existing `Stop` hook
- [x] 2.2 Wire the vendored `comet-hook-router.mjs` path (from the mounted `comet` skill's plugin dir) into the generated settings for roles that declare it

## 3. Vendor Comet skills

- [x] 3.1 Vendor `skills/comet/` and `skills/comet-native/` (unmodified copies, `.source.yaml` pin, same pattern as `skills/brainstorming`/`skills/writing-plans`)

## 4. Role updates

- [x] 4.1 `roles/analyst`: mount `comet`/`comet-native`, rewrite `role.md`'s skill dispatcher for the Shape phase, drop `brainstorming`/`writing-plans`
- [ ] 4.2 `roles/implementer`: mount `comet`/`comet-native`, rewrite `role.md` to read `brief.md`/`spec.md` instead of `tasks.md`/`design.md`, keep one-step-one-commit discipline
- [ ] 4.3 `roles/reviewer`: mount `comet`/`comet-native`, rewrite `role.md` to dispatch the mandated read-only Verifier and mark acceptance items

## 5. Runner archive step

- [x] 5.1 Implement the deterministic post-merge `comet native archive --confirmed --finish keep` call in the runner

## 6. Contracts and docs

- [ ] 6.1 Update `docs/contracts/agent-io.md` to note `docs/comet/changes/<name>/` replacing `docs/changes/<KEY>/` for these three roles

## 7. Verification

- [ ] 7.1 Extend `evals/` with golden cases exercising the new Shape/Build/Verify flow through `cmd/eval-roles`
