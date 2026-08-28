# Review: sweep-the-open-issue-backlog-fix-the task 4 — rework

The implementation otherwise matches the task, but it does not fully guarantee the explicit never-bare alignment invariant.

- **major** internal/cli/cmd_status.go:604 — Unknown verdict values still produce a bare alignment label: VerdictsPresent becomes true for any non-empty verdict array, while alignmentLine renders only the five recognized verdict strings. applyVerdicts currently accepts non-empty arrays without validating Verdict values, so an auditor response containing only an unknown or empty verdict reaches this branch and renders "alignment: ". Validate verdict values when recording them or make alignmentStatusLine provide a non-empty fallback when no recognized counts, contraction, or creep text is produced; add a regression test for this accepted input.

_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:simplicity] major internal/cli/select.go:119 — Dead defensive nil-check on ws.Repo: bundleHint's `if ws.Repo != nil { ... }` guard is unreachable: its only caller, openTarget (select.go:100-102), only reaches bundleHint after openWorkspace (select.go:92) has already succeeded, and openWorkspace (workspace.go:29-44) only returns a non-nil *workspace when gitx.Open succeeded — gitx.Open (internal/gitx/git.go:31-37) returns either (nil, error) or (&Repo{...}, nil), never (nil, nil), and openWorkspace propagates any Open error before constructing the workspace. So ws.Repo is provably non-nil on every path that reaches bundleHint; the branch can never take the false side. This is a dead fallback (rubric item 4) introduced by this diff's new bundleHint helper — it silently guards against a condition that cannot occur instead of trusting (or asserting) the invariant, adding a branch with no real effect and no test can ever exercise the false side.
