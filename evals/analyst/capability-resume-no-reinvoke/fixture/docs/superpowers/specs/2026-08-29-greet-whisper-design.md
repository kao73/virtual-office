# greet-whisper — Design

## Context

`greet.Greet` returns a friendly, exclamation-marked greeting. The task asks for a
second exported function, `Whisper`, that signals a hushed tone. The package has no
existing convention for tone variants, so the exact shape of "hushed" is a real design
choice, not something the repo already answers.

## Approaches considered

1. **Lowercase + ellipsis.** `Whisper("Ada")` → `"...hello, ada..."`. Cheap, obviously
   "quiet" visually, no new dependency.
2. **Parenthetical stage direction.** `Whisper("Ada")` → `"(hello, ada)"`. Reads as a
   stage direction rather than a hushed voice; rejected — doesn't actually look quiet.
3. **Reduced punctuation, no case change.** `Whisper("Ada")` → `"hello, ada"` (no `!`,
   no case change). Too close to `Greet` with the `!` stripped; doesn't read as a
   distinct tone on its own.

## Decision

**Q1 (asked as `needs_human`, yes/no): "Approach 1 — lowercase, wrapped in `...` —
proceed?"** Human answered **yes** on 2026-08-29 (see this task's tracker history).
Approach 1 is adopted: `Whisper` lowercases the name, formats it through the same
greeting shape as `Greet`, and wraps the result in `...`.

`Whisper("Ada")` → `"...hello, ada..."`.

## What we touch

- `greet/greet.go`: add `func Whisper(name string) string`.
- `greet/greet_test.go`: add a test pinning `Whisper("Ada") == "...hello, ada..."`.

## Risks

None beyond normal test coverage — this is a pure, dependency-free string function.

## How this is verified

`go test ./...` in the fixture module; the new test in `greet/greet_test.go` is the
acceptance check for this spec.
