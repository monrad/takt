# Contributing

Read the note in [README.md](README.md#contributing) first: takt is a personal tool, and I'm selective
about outside changes. Bug reports and small fixes are welcome; open an issue before writing anything
larger.

This file covers the development setup and the release process.

## Development

```sh
task check     # go build ./... && go test ./... -race -count=1 && golangci-lint run ./...
nix develop    # a shell with go, golangci-lint, goreleaser, go-task and gh on PATH
```

`nix develop`'s golangci-lint is pinned through `flake.lock` to match `.golangci.yml`'s golden config.
`nix flake update` can move it to a newer nixpkgs release and off that pin, so check `.golangci.yml`'s
header comment afterwards. If it has drifted, either hold nixpkgs back or update the golden config.

### Tests

`go test ./...` is hermetic: no network, no money. Two further suites are gated behind environment
variables so that they never run as part of it.

```sh
# Reviewer smokes. Needs both the `copilot` and `claude` CLIs on PATH and logged in.
TAKT_LIVE=1 go test ./internal/backend/ -run TestLive

# The full loop against real agents: a throwaway repo, two `haiku` implementers doing
# real work, and a real reviewer at each gate. Roughly 90 seconds, and a few cents to a
# dollar in API usage.
TAKT_E2E=1 go test ./internal/cli/ -run TestLiveEndToEnd -timeout 45m
```

Set `TAKT_E2E_LOGDIR=<dir>` to keep the live implementers' prompts, stdout and stderr after the run.
Without it they go to a temp directory the test framework deletes on the way out.

Both suites, and takt itself, identify the driving session as `CLAUDE_CODE_SESSION_ID` when it is set,
falling back to `TAKT_SESSION` otherwise. Inside a Claude Code session, exporting `TAKT_SESSION` changes
nothing.

The live implementer runs `claude -p` with `--permission-mode acceptEdits` and Bash access, in the
throwaway repo, under your real `HOME` (the reviewer CLIs need real credentials to answer at all). That
process is not sandboxed. Its confinement is the brief saying what to touch plus the wave's scope check
reverting anything outside it.

### The plugin

`claude plugin validate .` from the repo root checks the **marketplace** manifest without installing
anything, since a directory holding `.claude-plugin/marketplace.json` resolves to that. The plugin
manifest and the components are separate targets:

```sh
claude plugin validate .claude-plugin/plugin.json
claude plugin validate agents
claude plugin validate commands
```

Add `--strict` to any of them to fail on what the runtime would otherwise tolerate.

To develop against a local checkout rather than GitHub, add the marketplace from its path:

```
/plugin marketplace add /path/to/takt
```

### Copilot host files

`hosts/copilot/agents/*.agent.md` are generated from `agents/*.md` by `task hosts:gen`, and checked by
`task hosts:check` and the test suite. The skill's `takt version --expect <version>` line is stamped by
`task version:set`. A `0.0.0-dev` development build satisfies it via `"dev": true`.

## Releasing

Maintainer only. None of this is automatic for a fresh clone.

### One-time setup

1. Create `github.com/monrad/takt` and add it as the `origin` remote.
2. Create `monrad/homebrew-tap`. Empty is fine; goreleaser creates `Casks/takt.rb` there on the first
   release.
3. Add a repo secret `HOMEBREW_TAP_GITHUB_TOKEN`, a PAT with `contents:write` on `monrad/homebrew-tap`.
   Without it the release workflow skips the Homebrew cask step and the GitHub release still happens.
   With it, `brew install monrad/tap/takt` starts working after the first tag.

### Per release

1. `task version:set VERSION=x.y.z` rewrites the version fields in `.claude-plugin/plugin.json` and
   `.claude-plugin/marketplace.json`, the only two files carrying the version by hand.
2. Commit the manifests.
3. `git tag vx.y.z`. This is the tag `.github/workflows/release.yml` watches for (`tags: ["v*"]`).
4. `git push origin vx.y.z`, and the commit if it isn't pushed already.

`claude plugin tag --dry-run` is worth running at step 3 as an extra manifest-agreement check beside
`task version:set`. It names its own tag `takt--v<version>`, a different shape from what this repo's
release workflow expects, so treat it as validation rather than a replacement for the `git tag` above.

Pushing the tag triggers `.github/workflows/release.yml`, which:

1. Re-derives the version from the tag and fails immediately if `plugin.json` or `marketplace.json`
   disagrees, before running `go vet`, lint or tests.
2. Runs `go vet`, `golangci-lint` and `go test ./... -race -count=1`.
3. Runs `goreleaser release --clean`, which builds `takt` for linux/darwin × amd64/arm64, publishes a
   GitHub release with a changelog grouped by commit type, and pushes a Homebrew cask to
   `monrad/homebrew-tap` (skipped without the secret above).

`task snapshot` builds an unpublished, unpushed snapshot locally into `dist/`, which is useful for
checking the release build before tagging anything.

### Versioning

v1 supports stable `vX.Y.Z` tags only, with no prerelease channel. A `v0.2.0-rc1` tag fails the
version-agreement gate before goreleaser runs: the `x.y.z` shape is pinned in four places (`setversion`,
the two manifests, and the release gate itself) that would all have to relax together.
