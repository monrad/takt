# Review: sweep-the-plan-4-plan-5-deferred-minors-backlog task 1 — approve

The change fully satisfies the task: explicit empty --expect values fail correctly, version matching is evaluated once, subject-specific wording is correct, and coverage addresses the requested paths.


_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:consistency] nit internal/cli/cmd_version.go:137 — `subject` reuses a name that already means something else in this package: manifestFailure's new `subject` parameter (and its doc comment at lines 127-130) names the noun a mismatch error uses ("skill"/"plugin"). internal/cli/cmd_close_wave.go already establishes `subject` to mean the git commit subject line (waveSubject, commitSubjectSoftLimit, cmd_next.go:461-494) throughout the same `cli` package. The overlap is harmless today but a grep for "subject" in this package now turns up two unrelated concepts.
