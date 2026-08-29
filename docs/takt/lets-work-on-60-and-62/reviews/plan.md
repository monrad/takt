# Review: plan — rework

The decomposition is broadly complete, but it contains one unresolved spec contradiction and one documentation task that would encode behavior opposite to B2. These must be corrected before execution.

- **blocking** plan.md:0 — Task 2 changes A2.3 instead of resolving the specification conflict: Task 2 explicitly tests Close as non-increasing in MaxParallel, while spec A2.3 requires Close to be monotonically non-decreasing in every input. The formula itself makes the literal requirement impossible for MaxParallel, so the plan's interpretation is mathematically sensible, but it is still an unratified change to the spec. Amend A2.3 to exempt MaxParallel and state its required direction, or otherwise record an authoritative spec correction before execution.
- **major** plan.index.json:0 — Task 8 documents the wrong ancestry case: Task 8 says a present remote-tracking ref that is "behind or diverged" should produce `git push origin <branch>`. Under B2, when the local branch is behind—meaning it is an ancestor of the remote-tracking ref—cleanup must be empty. The push applies when the local branch is ahead or histories have diverged. Replace "behind" with "ahead" or describe the ancestry predicate directly.
- **minor** plan.index.json:0 — Task 7's IsAncestor grep does not prove a second call site: The command `grep -c 'IsAncestor' internal/cli/archive.go | grep -qvx 1` succeeds for 0 and for any count other than 1, despite the task claiming it requires a second call site. Require a count of at least 2, for example with `awk '$1 >= 2 { found=1 } END { exit !found }'`; retain the behavioral tests as the primary proof.

_copilot / gpt-5.6-sol_
