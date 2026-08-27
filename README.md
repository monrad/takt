# takt

I wanted a better way to run long coding sessions. Not a bigger context window or a cleverer prompt,
but somewhere durable to keep the work itself: what I'd agreed to build, which parts were done, what was
still open, and what had already been reviewed. Something that survives closing the laptop.

Without that, the plan lives in the transcript, and the transcript is the first thing to go. A crash, a
compaction, or just coming back the next morning meant reconstructing where I was from a diff and a bad
memory, and usually redoing a thing or two I had already done.

takt keeps the plan on disk instead. A run is a durable bundle on its own branch, moving through
**brainstorm → plan → execute → finish**. Claude Code drives it and subagents do the work, but a Go
binary decides and records every state change, so `/takt` always picks the run back up exactly where it
stopped. Nothing is lost, nothing is done twice.

Design notes: [docs/superpowers/specs/2026-08-24-takt-design.md](docs/superpowers/specs/2026-08-24-takt-design.md)

## Install

### The binary

`takt` needs to be on `PATH`. Pick one:

```sh
nix profile install github:monrad/takt
brew install monrad/tap/takt
go install github.com/monrad/takt/cmd/takt@latest
```

`takt version` reads its version from Go's build info, so it is accurate however you installed. Pin a
tag (`@v0.1.0`) if you want the same binary on every machine.

As a flake input, for a home-manager or NixOS configuration:

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    takt.url = "github:monrad/takt";
  };

  outputs =
    { nixpkgs, ... }@inputs:
    {
      homeModules.default =
        { pkgs, ... }:
        {
          home.packages = [ inputs.takt.packages.${pkgs.system}.default ];
        };
    };
}
```

takt also exports `overlays.default`, so `nixpkgs.overlays = [ inputs.takt.overlays.default ]` gives you
`pkgs.takt` anywhere in the configuration instead.

### The plugin

The `/takt` command and its four agent definitions ship from the same repository, via its own
marketplace. In Claude Code:

```
/plugin marketplace add monrad/takt
/plugin install takt@monrad-takt
```

Or from a shell, `claude plugin marketplace add monrad/takt` then
`claude plugin install takt@monrad-takt`. The plugin depends on `superpowers`
(`claude-plugins-official`), which Claude Code installs for you.

## A run

`/takt "add retry to the fetch client"` creates the bundle and starts the loop. From then on `/takt`
resumes it, and `/takt status` tells you where it is:

```
add-retry-to-the-fetch-client  phase=execute  branch=takt/add-retry-to-the-fetch-client (base main)
session: 4ff3a6ed-a18b-4750-b221-1d069192dc12@dev1, heartbeat 42s ago
tasks: 4 total — pending 2, done 2, failed 0, blocked 0, waived 0
  #1 wave 1 done (implement)
  #2 wave 1 done (implement)
  #3 wave 2 pending (test)
  #4 wave 2 pending (docs)
active wave: 2 (attempt 1, since 01:05:00)
gates: spec=approved plan=approved
```

Tasks are grouped into waves from their declared dependencies, and each wave dispatches as concurrent
subagents. Between phases sit gates: a headless reviewer judges the spec before planning and the plan
before execution, reviews each task at the end of its wave, and at finish checks the branch against the
goals the run declared for itself.

### Commands

- `/takt "<topic>"` starts a new run.
- `/takt` resumes the run in progress, or asks which one if there are several.
- `/takt status` gives the report above: phase, branch, task counts, open gate, goal verdicts.
- `/takt doctor` runs read-only health checks across every bundle in the repository, exiting non-zero on
  an ERROR finding.
- `/takt waive <N> "<reason>"` marks a blocked or failed task waived so the run can continue past it.
- `/takt unlock` clears a session lock left behind by a session that died mid-run.

Everything else happens inside the binary. The plugin prompt runs the single op that `takt next` hands
it and asks you when a gate needs an answer.

The commands that drive one specific run (`next`, `status`, `answer`, `record`, `done`, `waive`,
`unlock`) take `--slug` once the repository holds more than one non-archived run, and always for an
archived one. The plugin asks which run before the first ambiguous call. `takt doctor` is the exception:
it judges every bundle at once and takes no `--slug`.

### Where a run lives

The bundle sits at `<dir>/<slug>/`, where `<dir>` defaults to `docs/takt`. A relative `dir` is committed
alongside the code; an absolute or `~`-prefixed one keeps bundles out of git entirely.

An advisory session lock lives in `docs/takt/<slug>/logs/session.json`, untracked and refreshed on every
`takt next`. Two live sessions on one run get asked who owns it; a stale or unreadable lock is cleared
with `takt unlock`. `init` adds that directory to `.git/info/exclude` so it stays invisible across branch
switches.

## Configuration

Repo config (`<repo>/.takt.json`, committed) holds what a team shares: the bundle directory, which gates
are on. User config (`~/.config/takt/config.json`) holds machine and account facts: models, backend
timeouts. Precedence runs flags › environment › `.takt.json` › user config › these defaults:

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

Most keys mean what they look like. The ones that don't:

| key | meaning |
|---|---|
| `autonomy` | `"auto"` runs ops back to back. `"step"` asks "continue?" before each wave dispatch. Neither skips a gate. |
| `review.tasks` | Cross-vendor review of each task at the end of its wave. |
| `goals` | A goal-assessor verdict per declared goal at finish, before disposition. |
| `alignment` | An end-of-planning audit of the merged plan against the original request. |
| `max_rework` | Rework rounds a task gets from review before it counts as a wave failure. |
| `max_files_per_task` | Plan validation rejects a task declaring more files than this. Split it instead. |
| `wave_stale_after` | How old an `active_wave` with a dead session must be before it counts as abandoned. |
| `lock_ttl` | How long a session's heartbeat lease is honored before another session (or `--force`) can take over. |
| `default_branch` | Empty auto-detects from `origin/HEAD`. Set it when that can't be resolved. |
| `backends.reviewer` | Fallback order. The first healthy reviewer in the list runs. |
| `agents.implementer.by_class` | Per-task-class model override. Classes missing here use `implementer.model`. |
| `agents.implementer.escalate_on_retry` | On a reworked task's retry, bump the model one tier (haiku→sonnet→opus). Opus stays opus, and Fable is never chosen automatically. |
| `agents.planner.model` | Set this to `opus` on an account without Claude Fable 5. |

`autonomy`, `review.*`, `goals`, `alignment`, `max_parallel` and `max_rework` are frozen into the
bundle's `state.json` at `init`, so editing `.takt.json` mid-run leaves that run's behaviour alone.

## Reviewers

Every gate runs through the ordered chain in `config.backends.reviewer`, shipped as
`["copilot", "claude"]`. takt uses the first backend whose CLI is on `PATH` and answers `--version`. A
review that runs and comes back `error` is surfaced as a failure; it does not fall through to the next
vendor.

If no backend in the chain is healthy, `takt review spec|plan` fails rather than passing the gate. Its
hint names why each backend was skipped, so the message alone is enough to diagnose. Recording an
evidenced skip is a separate, deliberate command:

```sh
takt review spec --skip --reason "<why>" --evidence <file>
```

`<file>` holds the failing backend's error output. Both flags are required, and the evidence is copied
into the bundle's receipt so the skip stays auditable.

## GitHub Copilot CLI

The same op loop runs under the Copilot CLI (1.0.80 or newer) as a skill plus four custom agents.
Install the binary as above, then:

```sh
copilot skill add /path/to/takt/hosts/copilot/skills/takt   # or symlink it into ~/.copilot/skills/takt
mkdir -p ~/.copilot/agents && cp /path/to/takt/hosts/copilot/agents/*.agent.md ~/.copilot/agents/
```

Start `copilot` at the repository root, then `takt: <topic>` to begin a run, `takt` to resume, and
`takt status` / `takt doctor` / `takt waive <N> <reason>` / `takt unlock` for the verbs.

Four things differ from the Claude Code plugin:

- Questions arrive through Copilot's `ask_user` tool.
- The op's `model` is advisory. Copilot picks subagent models from its `/subagents` setting, and the
  agent files carry no `model`.
- The agents install with `tools: ["*"]`, so the read-only agents (assessor, auditor) are held read-only
  by their own text rather than by the host.
- Brainstorming is a plain conversation, without the superpowers skill.

## Contributing

takt is a tool I built for myself and put in the open, not a project I'm running. I use it daily and
I'll keep it working for my own workflow, which makes me selective about outside changes: I'm unlikely
to merge features I don't need, and I'd rather say no in a day than leave a PR open for months.

Bug reports and small fixes are welcome. For anything larger, open an issue before you write code. If it
doesn't fit, fork it. It's MIT, and that's a perfectly good outcome.

[CONTRIBUTING.md](CONTRIBUTING.md) covers the development setup and the release process.

## Acknowledgements

takt's workflow (the durable run bundle, the one-op-at-a-time decision loop, hash-bound review gates,
wave assignment from task dependencies, the goals check and the alignment audit) is a from-scratch
redesign of ideas from [masterplan](https://github.com/rasatpetabit/masterplan) by Richard A Steenbergen.
masterplan is published under the MIT license; no code or text from it is shared.
