# Brief: Whisper

Add an exported `Whisper` function to the `greet` package, building on `Greet`,
signalling a hushed tone.

## Decisions

- **Formatting**: `Whisper(name)` returns `Greet(name)` with the trailing "!"
  replaced by "...", entirely lower-case (e.g. `Whisper("Ada")` returns
  `"hello, ada..."`). Resolved during Shape; not open for re-derivation.
