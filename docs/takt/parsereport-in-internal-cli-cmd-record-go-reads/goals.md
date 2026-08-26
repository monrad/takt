# Goals — parsereport-in-internal-cli-cmd-record-go-reads

## Anchor
```text
parseReport in internal/cli/cmd_record.go reads the implementer's trailer with strings.HasPrefix on "STATUS:", "SUMMARY:" and "BLOCKERS:", so a final message whose trailer is markdown-decorated — "**STATUS:** done", "- STATUS: done", "STATUS: **done**", "`STATUS:` done" — records nothing and takt rejects the digest with "digest status must be done, failed or blocked". Make the trailer parsing tolerant of leading list markers and of bold, italic or backtick decoration around the key and around the value, while exact-prefix lines keep working; add table-driven tests for every decorated shape and for a body line that merely mentions STATUS: mid-sentence (must not match); and mention the tolerance in the implementer agent's report contract in agents/implementer.md.
```

## Goals
- G1 — parseReport extracts STATUS, SUMMARY and BLOCKERS from a trailer decorated with leading list, quote or heading markers and with bold, italic or backtick decoration around the key, around the value, or around the whole line. · signal: test · evidence: table-driven cases in internal/cli/cmd_record_test.go covering every shape in the spec's "Shapes accepted" table pass under `go test -race ./internal/cli/...`
- G2 — Undecorated trailer lines and the existing digest behaviour are unchanged: exact-prefix lines still parse, the last occurrence still wins, and --status/--summary/--blockers still override. · signal: test · evidence: the plain-shape and last-occurrence-wins cases pass and the pre-existing internal/cli suite stays green under `go test -race ./...`
- G3 — A line that merely mentions STATUS: mid-sentence, a lowercase key, or a key without a colon never records a digest. · signal: test · evidence: the must-not-match table rows in internal/cli/cmd_record_test.go assert the fields stay empty
- G4 — The implementer's report contract states the tolerance, and the generated Copilot agent stays in parity with it. · signal: docs · evidence: the tolerance sentence in agents/implementer.md, the regenerated hosts/copilot/agents/takt-implementer.agent.md, and `go run ./internal/tools/hostgen --check` reporting nothing stale
- G5 — The spec of record describes the tolerant parse. · signal: docs · evidence: §5.1's `takt record --task` row in docs/superpowers/specs/2026-08-24-takt-design.md names the tolerated markers and decoration
- G6 — The change lands clean under the repo's gates. · signal: command · evidence: `go test -race ./...` green and `golangci-lint run ./...` reporting 0 issues
