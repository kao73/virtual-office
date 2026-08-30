# Greet Whisper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `Whisper`, a hushed-tone counterpart to `Greet`, to the `greet` package.

**Architecture:** One pure function, `Whisper(name string) string`, implemented in
`greet/greet.go` alongside `Greet`, reusing `Greet`'s own formatting and wrapping the
result in `...` after lowercasing.

**Tech Stack:** Go (module `fixture`, go 1.22), standard library only (`fmt`, `strings`).

**Spec:** `docs/superpowers/specs/2026-08-29-greet-whisper-design.md`

## Global Constraints

- Output format is fixed by the spec's Decision section: `Whisper("Ada")` ==
  `"...hello, ada..."` — lowercase, wrapped in `...`, reusing `Greet`'s own shape.

---

## Task 1: Add `Whisper`

**Files:**
- Modify: `greet/greet.go`
- Test: `greet/greet_test.go`

**Interfaces:**
- Produces: `func Whisper(name string) string` — the package's only other exported
  function besides `Greet`.

- [ ] **Step 1: Write the failing test**

```go
func TestWhisper(t *testing.T) {
	if got := Whisper("Ada"); got != "...hello, ada..." {
		t.Errorf("Whisper(Ada) = %q, want %q", got, "...hello, ada...")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./greet/... -run TestWhisper -v`
Expected: FAIL — `Whisper` undefined.

- [ ] **Step 3: Implement `Whisper`**

```go
// Whisper returns a hushed-tone greeting for name: Greet's own shape, lowercased
// and wrapped in "...".
func Whisper(name string) string {
	return "..." + strings.ToLower(Greet(name)) + "..."
}
```

Add `"strings"` to the existing `import` block in `greet/greet.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./greet/... -run TestWhisper -v`
Expected: PASS

- [ ] **Step 5: Run the full package suite**

Run: `go test ./...`
Expected: PASS — `TestGreet` and `TestWhisper` both green.

- [ ] **Step 6: Commit**

```bash
git add greet/greet.go greet/greet_test.go
git commit -m "feat(greet): add Whisper, a hushed-tone greeting"
```

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-08-29-greet-whisper.md`. Two
execution options:

1. **Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review
   between tasks, fast iteration
2. **Inline Execution** - Execute tasks in this session using executing-plans, batch
   execution with checkpoints

Which approach?
