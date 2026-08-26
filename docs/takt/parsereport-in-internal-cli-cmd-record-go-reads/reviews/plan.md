# Review: plan — rework

Coverage is strong, but final Go validation is sequenced before all Go changes exist.

- **major** plan.md:0 — Tasks 1–2 run repository gates in the wrong order: Task 1 claims it alone touches Go code and runs the full test/lint gates, but Task 2 subsequently modifies execute_test.go. Therefore the assembled Go change is never checked by `golangci-lint run ./...` or the specified full-suite command. Move or repeat those gates in Task 2, or add a terminal verification task depending on both.
- **minor** plan.md:0 — Task 2 omits active-wave initialization: `executeRun` leaves `ActiveWave` unset; `record` rejects until `next(t, root, nil)` dispatches wave 0. Task 2 should explicitly include that setup step so its prescribed test is executable as written.

_copilot / gpt-5.6-sol_
