# Review: sweep-the-plan-4-plan-5-deferred-minors-backlog task 8 — approve

All four callers report read and append losses as warnings while preserving exit 0 and existing JSON keys. Tests distinctly cover both failure seams and clean records; no actionable findings.


_copilot / gpt-5.6-sol_

## Internal findings (confirmed)

- [lens:simplicity,consistency] minor internal/cli/cmd_record_test.go:338 — Fault-injection test helpers duplicated verbatim across two test files: readOnlyEventLog/directoryEventLog (internal/cli/cmd_record_test.go:338-368) and breakEventAppend/breakEventRead (internal/cli/record_reviewer_test.go:549-579) are byte-for-byte identical implementations (same os.Chmod(0o400)/OpenFile probe/os.Remove+os.Mkdir dance), differing only in name. They live in different Go packages (cli vs cli_test) so they can't call each other directly, but internal/testutil (a package already imported by both, e.g. testutil.NewRepo/testutil.Git) is the natural home for a single testutil.BreakEventAppend(t, bdir)/testutil.BreakEventRead(t, bdir) pair — confirmed via grep that os.Chmod(p, 0o400) and Mkdir(p, 0o750) each appear in exactly these two new locations and nowhere pre-existing in the tree. Not blocking, but ~30 duplicated lines that a shared helper would remove.
- [lens:consistency] nit internal/cli/facts.go:281 — warnStreakLoss inlines its warning string instead of following the local excludeWarning(err) helper convention: cmd_init.go's excludeWarning(err) (internal/cli/cmd_init.go:425-427) is a small named function returning "info/exclude not written: " + err.Error(), used by both cmd_init.go and cmd_next.go for the same warnings-contract pattern this task extends. warnStreakLoss instead builds "attempt-streak reset not recorded: "+err.Error() inline inside the append call. Not wrong, but it departs from the one-sentence-per-named-function shape the warnings contract otherwise uses for its two other producers.
