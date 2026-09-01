# Brief: Double

Add an exported `Double` function to the `calc` package.

## Decisions

- `Double(x)` returns `x * 2`. No edge cases beyond ordinary `int` overflow
  semantics — not in scope for this change.
