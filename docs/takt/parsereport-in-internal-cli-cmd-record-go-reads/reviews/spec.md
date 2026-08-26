# Review: spec — rework

The approach is sound, but the stated tests do not fully prove the broader parsing requirement.

- **major** goals.md:9 — Success evidence omits required marker and decoration variants: G1 requires every listed marker and bold, italic, and backtick decoration around keys, values, and whole lines. The accepted-shapes table omits several variants, including `*`, `+`, and `#` markers, `2)` ordered markers, and multiple decoration/location combinations. An implementation could fail these while all specified evidence passes. Define an exhaustive test matrix or narrow G1.
- **minor** spec.md:37 — Marker-run grammar is ambiguous: Clarify whether a marker run is one or more repetitions of the same character or any sequence of marker characters, and whether ordered markers permit zero, leading zeros, or arbitrary digit counts. Different implementations could otherwise accept different inputs.

_copilot / gpt-5.6-sol_
