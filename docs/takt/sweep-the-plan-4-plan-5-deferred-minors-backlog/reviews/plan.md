# Review: plan — rework

T6’s verification can pass without a stamped binary, and T2 omits a required integration case.

- **major** :0 — T6 verification does not prove version stamping: T6 verifies the binary through `version --expect`, but T1 explicitly preserves the dev-build exception, so an unstamped `0.0.0-dev` binary can pass. Compare plain `./takt version` output exactly with `setversion --print` instead.
- **minor** :0 — T2 drops the backslash --dir integration test: The spec requires testing both a metacharacter-containing `--dir` and one containing a backslash. T2 only assigns the backslash case to the escaper unit test; add a CLI-level backslash directory case to prove path handling and rule construction together.

_copilot / gpt-5.6-sol_
