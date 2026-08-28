# Review: spec — rework

The design has three correctness defects that must be resolved before planning, plus a scope inconsistency.

- **blocking** spec.md:0 — omitempty will not omit a zero time.Time: Section C requires committed_at only for committed attempts, but specifies a zero time.Time with json omitempty. encoding/json does not omit zero-valued structs, so this produces a year-1 timestamp. Use *time.Time, omitzero where supported, or custom marshaling.
- **blocking** spec.md:0 — Follow-up identity encoding can collide: The raw pipe-joined Key is not injective because file and title may contain pipes. For example file="a|2", line=3, title="x" collides with file="a", line=2, title="3|x", causing unrelated findings to be dropped or upgraded. Specify an escaped or structural key.
- **blocking** spec.md:0 — Citation containment does not cover symlink escapes: Rejecting absolute paths and .. segments does not ensure a citation remains inside the repository: an in-repo symlink can resolve to an external regular file. Specify whether symlinks are rejected or require resolved-path containment, and test it.
- **major** goals.md:0 — The anchor omits issue #31: The anchor lists 17 in-run issues and omits #31, while the spec declares eighteen, includes #31 in scope, and goals.md adds G9 for it. Reconcile the authoritative scope.
- **minor** goals.md:0 — “Nothing written” contradicts the required invalid event: G6 and section E require a goals_invalid event but also say nothing is written. Clarify that no goal verdict artifact is written.

_copilot / gpt-5.6-sol_
