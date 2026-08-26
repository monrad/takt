# takt

takt runs long, multi-step coding work as a resumable **brainstorm → plan → execute → finish** loop on a
durable run bundle. It is a Claude Code plugin (a command prompt plus four agent definitions) and a Go
binary: Claude Code drives every phase, but the binary decides and records every state change, subagents
implement, and headless reviewers judge. Every phase is resumable — a crash, a compaction, or a brand new
session picks the run back up with `/takt` and nothing is lost or done twice.

Design: [docs/superpowers/specs/2026-08-24-takt-design.md](docs/superpowers/specs/2026-08-24-takt-design.md)

---

## Install

The `takt` binary needs to be on `PATH`. Pick one:

**Nix** (imperative):

```sh
nix profile install github:monrad/takt
```

**Nix** (flake input, for a home-manager or NixOS configuration):

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    takt.url = "github:monrad/takt";
  };

  outputs =
    { nixpkgs, ... }@inputs:
    {
      # A home-manager module. `pkgs` comes from the module system; `inputs`
      # is your own flake's, passed in through home-manager's
      # `extraSpecialArgs` (or NixOS's `specialArgs`).
      homeModules.default =
        { pkgs, ... }:
        {
          home.packages = [ inputs.takt.packages.${pkgs.system}.default ];
        };
    };
}
```

takt also exports `overlays.default`, so `nixpkgs.overlays = [ inputs.takt.overlays.default ]` makes it
`pkgs.takt` anywhere in the configuration instead.

**Homebrew** (macOS and Linuxbrew — a cask, not a formula: goreleaser 2.17 deprecated `brews` in favour of
`homebrew_casks`):

```sh
brew install monrad/tap/takt
```

**Go** (anyone with a Go toolchain):

```sh
go install github.com/monrad/takt/cmd/takt@latest
```

`@latest` resolves to a concrete tag before Go builds anything, and `go install` stamps nothing into
the binary either way — `takt version` recovers whichever tag was actually resolved from the module's own
build info, so it reports correctly whether you pinned or not. Pin a version (`@v0.1.0`) for
reproducibility — the same binary on every machine and every re-install — not because `@latest` would
report wrong.

## Install the plugin

The plugin (the `/takt` command and its four agent definitions) is installed from the same repository, via
its own marketplace. In Claude Code:

```
/plugin marketplace add monrad/takt
/plugin install takt@monrad-takt
```

(or, from a shell: `claude plugin marketplace add monrad/takt` then
`claude plugin install takt@monrad-takt`.)

For local development against a checkout instead of GitHub, add the marketplace from the path:

```
/plugin marketplace add /path/to/takt
```

`claude plugin validate .`, run from the repo root, checks the **marketplace** manifest without installing
anything — that is what a directory holding `.claude-plugin/marketplace.json` resolves to. The plugin
manifest and the components are separate targets:
`claude plugin validate .claude-plugin/plugin.json`, `claude plugin validate agents` and
`claude plugin validate commands`. Add `--strict` to any of them to fail on what the runtime would
otherwise tolerate.

The plugin depends on `superpowers` (`claude-plugins-official`); Claude Code installs it automatically as
part of installing `takt`.

## GitHub Copilot CLI

The same op loop runs under the Copilot CLI (1.0.80 or newer) as a skill plus four custom agents. Install
the binary as above, then:

```sh
copilot skill add /path/to/takt/hosts/copilot/skills/takt          # or symlink it into ~/.copilot/skills/takt
mkdir -p ~/.copilot/agents && cp /path/to/takt/hosts/copilot/agents/*.agent.md ~/.copilot/agents/
```

Start `copilot` at the repository root and say `takt: <topic>` to begin a run, `takt` to resume,
`takt status` / `takt doctor` / `takt waive <N> <reason>` / `takt unlock` for the verbs. Differences from
the Claude Code plugin: questions arrive through Copilot's `ask_user` tool; the op's `model` is advisory —
Copilot picks subagent models from its `/subagents` setting (the agent files carry no `model`); the agents
are installed with `tools: ["*"]`, so the read-only agents (assessor, auditor) are read-only by their own
text, not by the host; the brainstorm step is a plain conversation (no superpowers skill). The agent files
are generated from `agents/*.md` by `task hosts:gen` and checked by `task hosts:check` and the test suite;
the skill's `takt version --expect <version>` line is stamped by `task version:set`; a `0.0.0-dev`
development build passes it with `"dev": true`.

## Use

- `/takt "<topic>"` — starts a new run: `takt init` creates the bundle, then the loop begins.
- `/takt` — resumes whatever run is in progress (or asks which, if there's more than one).
- `/takt status` — one-screen report: phase, branch, task counts, the open gate, goal verdicts.
- `/takt doctor` — read-only health checks across every bundle in the repository; exits non-zero on an
  ERROR finding.
- `/takt waive <N> "<reason>"` — marks a blocked or failed task waived, so the run can continue past it.
- `/takt unlock` — clears a stale session lock (another session died mid-run and left it held).

Everything else — dispatching agents, running gate reviews, verifying, committing — happens inside the
binary; the plugin prompt only executes the op `takt next` hands it and asks the user when a gate needs an
answer.

Every command above that drives one specific run (`next`, `status`, `answer`, `record`, `done`, `waive`,
`unlock`) needs `--slug` once there's more than one non-archived run in the repository, and always needs
it for an archived run — the plugin asks which run before the first such call if it's ambiguous.
`takt doctor` is the exception: it judges every bundle in the workspace at once and takes no `--slug` at
all.

The advisory session lock lives in `docs/takt/<slug>/logs/session.json` (untracked, refreshed on every
`takt next`). Two live sessions on one run get the owner question; a stale or unreadable lock is cleared
with `takt unlock`. `init` also lists that directory in the repository's `.git/info/exclude`, so it stays
invisible after a branch switch.

The run bundle lives at `<dir>/<slug>/` in the repository, where `<dir>` defaults to `docs/takt` (see `dir`
below) — a relative `dir` is committed with the code; an absolute or `~`-prefixed one keeps bundles outside
git entirely.

### `.takt.json`

Repo config (`<repo>/.takt.json`, committed) is for what a team shares — the bundle directory, which gates
are on. User config (`~/.config/takt/config.json`) is for machine/account facts — models, backend timeouts.
Precedence is flags › environment › `.takt.json` › user config › the defaults below. Every key and its
shipped default:

```json
{
  "dir": "docs/takt",
  "autonomy": "auto",
  "review": { "spec": true, "plan": true, "tasks": true },
  "goals": true,
  "alignment": true,
  "max_parallel": 8,
  "max_rework": 1,
  "max_files_per_task": 12,
  "wave_stale_after": "30m",
  "lock_ttl": "10m",
  "verify_timeout": "10m",
  "default_branch": "",
  "backends": {
    "reviewer": ["copilot", "claude"],
    "copilot": { "model": "gpt-5.6-sol", "effort": "high", "timeout": "5m" },
    "claude":  { "model": "opus", "effort": "high", "timeout": "5m" }
  },
  "agents": {
    "implementer": {
      "model": "opus",
      "by_class": { "mechanical": "haiku", "bounded": "sonnet", "test": "sonnet", "docs": "sonnet" },
      "escalate_on_retry": true
    },
    "planner": { "model": "fable" },
    "goal-assessor": { "model": "sonnet" },
    "alignment-auditor": { "model": "sonnet" }
  }
}
```

| key | default | meaning |
|---|---|---|
| `dir` | `"docs/takt"` | Where run bundles live — relative (committed) or absolute/`~` (external, gitignored). |
| `autonomy` | `"auto"` | `"auto"` runs ops back to back; `"step"` asks "continue?" before each wave dispatch. Neither skips a gate. |
| `review.spec` | `true` | Headless review gate on the brainstormed spec before planning. |
| `review.plan` | `true` | Headless review gate on the plan index before execution. |
| `review.tasks` | `true` | Per-task cross-vendor review at the end of each wave. |
| `goals` | `true` | goal-assessor verdict per goal at finish, before disposition. |
| `alignment` | `true` | End-of-planning audit of the merged plan against the original request. |
| `max_parallel` | `8` | Most tasks dispatched as concurrent subagents in one wave. |
| `max_rework` | `1` | Rework rounds a task gets from review before it counts as a wave failure. |
| `max_files_per_task` | `12` | Plan validation caps how many files one task may declare — over this, split the task. |
| `wave_stale_after` | `"30m"` | How old an `active_wave` with a dead session must be before it's treated as abandoned. |
| `lock_ttl` | `"10m"` | How long a session's heartbeat lease is honored before another session (or `--force`) may take over. |
| `verify_timeout` | `"10m"` | Deadline for one verify command. |
| `default_branch` | `""` | Empty means unset (`origin/HEAD` is auto-detected); set it when takt can't resolve that on its own. |
| `backends.reviewer` | `["copilot","claude"]` | Fallback order: the first healthy reviewer in the list runs. |
| `backends.copilot.model` | `"gpt-5.6-sol"` | Model the `copilot` reviewer is invoked with. |
| `backends.copilot.effort` | `"high"` | Effort level passed to the `copilot` reviewer. |
| `backends.copilot.timeout` | `"5m"` | Deadline for one `copilot` review call. |
| `backends.claude.model` | `"opus"` | Model the `claude` reviewer is invoked with. |
| `backends.claude.effort` | `"high"` | Effort level passed to the `claude` reviewer. |
| `backends.claude.timeout` | `"5m"` | Deadline for one `claude` review call. |
| `agents.implementer.model` | `"opus"` | Implementer model for the `implement` class and any class missing from `by_class`. |
| `agents.implementer.by_class` | mechanical→haiku, bounded/test/docs→sonnet | Per-task-class model override (spec D22). |
| `agents.implementer.escalate_on_retry` | `true` | On a reworked task's retry, bump the model one tier (haiku→sonnet→opus); opus stays opus, Fable is never chosen automatically. |
| `agents.planner.model` | `"fable"` | Model for the planner agent — set to `opus` on an account without Claude Fable 5. |
| `agents.goal-assessor.model` | `"sonnet"` | Model for the goal-assessor agent. |
| `agents.alignment-auditor.model` | `"sonnet"` | Model for the alignment-auditor agent. |

`autonomy`, `review.*`, `goals`, `alignment`, `max_parallel` and `max_rework` are frozen into the bundle's
`state.json` at `init`, so editing `.takt.json` mid-run never changes that run's behaviour.

## Reviewers

Every gate (spec, plan, per-task) runs through `config.backends.reviewer`'s ordered chain — shipped as
`["copilot", "claude"]`. takt tries each in order and uses the first one whose CLI is on `PATH` and answers
`--version`; a review that actually runs and returns `error` does **not** fall through to the next backend
— that failure is surfaced, not silently retried on another vendor.

If no backend in the chain is healthy, `takt review spec|plan` fails outright rather than silently passing
the gate — its hint names *why* each backend in the chain was skipped (not found, or its health probe
failed), so the cause is diagnosable from the message alone. Recording an evidenced skip instead is a
separate, deliberate command, not something the failed review does on its own:
`takt review spec|plan --skip --reason "<why>" --evidence <file>`, where `<file>` holds the failing
backend's error output. Both `--reason` and `--evidence` are required, and the evidence file is copied into
the bundle's receipt (spec §9) so the skip stays auditable.

## Releasing

This is the maintainer's checklist — none of it is automatic for a fresh clone of this repository. Manual,
one-time setup:

1. Create `github.com/monrad/takt` and add it as the `origin` remote.
2. Create `monrad/homebrew-tap` (empty is fine — goreleaser creates `Casks/takt.rb` there on first
   release).
3. Add a repo secret `HOMEBREW_TAP_GITHUB_TOKEN` — a PAT with `contents:write` on `monrad/homebrew-tap` —
   to the `takt` repo. Without it, the release workflow's Homebrew cask step is skipped and the GitHub
   release still happens; with it, `brew install monrad/tap/takt` starts working after the first tag.

Per release:

1. `task version:set VERSION=x.y.z` — rewrites `.claude-plugin/plugin.json` and
   `.claude-plugin/marketplace.json`'s version fields (the only two files that carry the version by hand).
2. Commit the manifests.
3. `git tag vx.y.z` — this is the tag `.github/workflows/release.yml` watches for (`tags: ["v*"]`).
   `claude plugin tag --dry-run` is worth running first, as an extra manifest-agreement check beside
   `task version:set` — but it names its own tag `takt--v<version>`, a different shape than this repo's
   release workflow expects, so it's a validation step here, not a substitute for the `git tag` above.
4. `git push origin vx.y.z` (and the commit, if not already pushed).

Pushing the tag is what `.github/workflows/release.yml` reacts to (`on: push: tags: ["v*"]`). It:

1. Re-derives the version from the tag and fails immediately if `plugin.json` or `marketplace.json`
   disagrees with it — before running `go vet`, lint or tests.
2. Runs `go vet`, `golangci-lint` (pinned to v2.13.1) and `go test ./... -race -count=1`.
3. Runs `goreleaser release --clean`: builds `takt` for linux/darwin × amd64/arm64, publishes a GitHub
   release with a changelog grouped by commit type, and pushes a Homebrew cask to `monrad/homebrew-tap`
   (skipped without the secret above).

v1 supports stable `vX.Y.Z` tags only — there is no prerelease channel. A `v0.2.0-rc1` tag fails the
version-agreement gate before goreleaser ever runs, because the version is pinned to the `x.y.z` shape in
four places (`setversion`, the two manifests, and the release gate itself) that would all need to relax
together to support it.

`task snapshot` builds an unpublished, unpushed snapshot with goreleaser locally (`dist/`) — useful for
checking the release build works before tagging anything.

## Coexistence with masterplan

takt ignores `docs/masterplan/**` entirely. Existing masterplan bundles are not imported — finish them
under masterplan or archive them by hand. The masterplan plugin can stay installed alongside takt; the two
share no commands, agents, or state, but only one of them should be driving a given branch at a time.

## Acknowledgements

takt's workflow — the durable run bundle, the one-op-at-a-time decision loop, hash-bound review gates,
wave assignment from task dependencies, the goals check and the alignment audit — is a from-scratch
redesign of ideas from [masterplan](https://github.com/rasatpetabit/masterplan) by Richard A Steenbergen.
masterplan is published under the MIT license; no code or text from it is shared.

## Development

```sh
task check     # go build ./... && go test ./... -race -count=1 && golangci-lint run ./...
nix develop    # a shell with go, golangci-lint, goreleaser, go-task and gh on PATH
```

`nix develop`'s golangci-lint is pinned through `flake.lock` to match `.golangci.yml`'s golden config
(v2.13.1) exactly; running `nix flake update` can move it to a newer nixpkgs release and off that pin —
check `.golangci.yml`'s header comment after updating, and if it's drifted, either hold nixpkgs back or
update the golden config to match.

The hermetic suite (`go test ./...`) never touches the network or spends money. Two more suites are
opt-in, gated behind environment variables specifically so `go test ./...` alone never runs them:

```sh
# Reviewer smokes: needs both the `copilot` and `claude` CLIs on PATH and logged in.
TAKT_LIVE=1 go test ./internal/backend/ -run TestLive

# The full loop against real agents: a throwaway repo, two `haiku` implementers doing
# real work, and a real reviewer at each gate. Roughly 90 seconds, a few cents to a
# dollar in API usage.
TAKT_E2E=1 go test ./internal/cli/ -run TestLiveEndToEnd -timeout 45m
```

Set `TAKT_E2E_LOGDIR=<dir>` to keep the live implementers' prompts, stdout and stderr after the run —
without it they go to a temp directory the test framework deletes on the way out.

Both suites, and takt itself, identify the driving session as `CLAUDE_CODE_SESSION_ID` if it is set and
`TAKT_SESSION` only otherwise — so inside a Claude Code session, exporting `TAKT_SESSION` changes nothing
(spec §4.6).

The live implementer runs `claude -p` with `--permission-mode acceptEdits` and Bash access, in the
throwaway repo, under the developer's real `HOME` (the reviewer CLIs need real credentials to answer at
all). Nothing sandboxes that process itself: the brief says what to touch and the wave's own scope check
reverts anything outside it, but it's that confinement doing the work, not the process.
