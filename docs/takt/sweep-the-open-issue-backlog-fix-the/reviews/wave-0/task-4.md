# Review: sweep-the-open-issue-backlog-fix-the task 4 — rework

The main behavior is implemented, but the promised never-bare alignment line still has a reachable failure case.

- **major** internal/cli/cmd_status.go:604 — Unknown recorded verdicts can still produce a bare alignment label: When VerdictsPresent is true, alignmentStatusLine delegates directly to alignmentLine. That function only renders the five known verdict names, so a non-empty verdict list containing an unrecognized value returns an empty string and renderStatus prints `alignment: ` bare. This is reachable through the normal recorder because applyVerdicts currently accepts arbitrary verdict strings. Add a non-empty fallback or validate/represent unknown verdicts, and cover this case with a test.

_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:simplicity] major internal/cli/select.go:119 — Dead defensive nil-check on ws.Repo: bundleHint's `if ws.Repo != nil { ... }` guard is unreachable: its only caller, openTarget (select.go:100-102), only reaches bundleHint after openWorkspace (select.go:92) has already succeeded, and openWorkspace (workspace.go:29-44) only returns a non-nil *workspace when gitx.Open succeeded — gitx.Open (internal/gitx/git.go:31-37) returns either (nil, error) or (&Repo{...}, nil), never (nil, nil), and openWorkspace propagates any Open error before constructing the workspace. So ws.Repo is provably non-nil on every path that reaches bundleHint; the branch can never take the false side. This is a dead fallback (rubric item 4) introduced by this diff's new bundleHint helper — it silently guards against a condition that cannot occur instead of trusting (or asserting) the invariant, adding a branch with no real effect and no test can ever exercise the false side.
