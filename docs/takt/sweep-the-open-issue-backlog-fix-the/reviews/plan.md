# Review: plan — rework

The decomposition is broadly complete, but Tasks 10 and 12 contain correctness gaps that must be resolved before execution.

- **major** :0 — T12 silently treats a missing goals.md as goals being disabled: T12 maps fs.ErrNotExist for goals.md to nil goals even when Config.Goals is true. The spec omits the Goals section only when goals are disabled, and plan.md itself says goals.md read errors must fail. This would silently generate an incomplete PR body. Require goals.md when goals are enabled and add a missing-file test.
- **major** :0 — T10's write order does not prove its claimed failure guarantee: T10 claims any failure leaves the receipt unwritten, but commitBundle runs after gate.WriteReceipt. A commit failure therefore leaves a cacheable receipt, and the next review need not rerun. The proposed test injects only an event-append failure. Specify recovery or narrower semantics for commit failure and verify that behavior.
- **major** :0 — T5's '..' segment validation is not filepath-safe: Checking strings.Split(path, "/") misses native backslash separators on Windows. A citation such as dir\..\a.go:1 can contain the forbidden segment yet resolve inside the repository, so containment checking will accept it. Use filepath-aware segment validation and add a platform-appropriate regression case.
- **minor** :0 — T10 has an unexplained file scope: internal/cli/cmd_next_test.go is declared in T10's files but no change to it is described or required by the listed tests. Remove it or state the precise necessary edit so the task cannot silently widen its scope.

_copilot / gpt-5.6-sol_
