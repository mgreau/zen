# Configuration

Zen reads `~/.zen/config.yaml`. Run `zen setup` for an interactive walkthrough, pass it flags for an [unattended install](#unattended-install), or edit the file directly.

## Minimal example

```yaml
repos:
  app:
    full_name: octo-sts/app
    base_path: ~/git/repo-octo-sts-app

authors:
  - mattmoor
  - wlynch
```

That's enough to start the daemon (`zen watch start`) and check your inbox (`zen inbox`). `authors` is who the daemon auto-creates worktrees for. You still get notified for other review requests; `zen review` can open those by hand.

## Full example

```yaml
repos:
  app:
    full_name: octo-sts/app
    base_path: ~/git/repo-octo-sts-app

authors:
  - mattmoor
  - wlynch

poll_interval: "5m"

# Which coding agent zen launches in worktrees: "claude" (default) or "codex".
agent: claude
claude_bin: claude   # executable used when agent: claude
codex_bin: codex     # executable used when agent: codex

terminal: iterm  # or "ghostty", "kitty", "macos", or "tmux"; unset means tmux inside tmux, else iterm

# Prefix for feature branches created by `zen work new`.
# If unset, falls back to `git config user.name` (spaces → hyphens), then no prefix.
branch_prefix: mgreau

# Skip draft PRs in `zen inbox`, watch notifications, and the MCP inbox tool.
# Defaults to false (drafts are shown). Set to true to skip drafts so you don't
# review something that isn't ready. Override on a single run with
# `--ignore-drafts=false`. Watch does not refresh a hidden draft (or a closed
# PR); when the PR is ready again, SHA comparison catches up. A live check at
# reconcile time covers the race where a PR is queued while ready then drafted.
ignore_drafts: true

watch:
  dispatch_interval: "10s"      # How often to process queued work
  cleanup_interval: "1h"        # How often to scan for merged PRs
  cleanup_after_merge: "1h"     # Time after a PR merges before its review worktree is removed
  session_scan_interval: "10s"  # How often to scan Claude session states
  concurrency: 2                # Parallel worktree setups
  max_retries: 5                # Max retry attempts for git failures

# Slack task watcher (opt-in, off by default) — see "Slack task watcher" below.
slack:
  enabled: true
  emoji: claudecode      # reaction name (no colons), default "claudecode"
  default_repo: mono     # short repo name worktrees are created in
  poll_interval: "5m"
  ack_reaction: eyes      # reaction added on pickup, default "eyes"
```

The daemon re-reads `config.yaml` on every poll tick. Changes to `poll_interval`, `authors`, `repos`, and other settings take effect without restarting.

On each poll, setup re-runs if GitHub's head SHA differs from the worktree. Linear updates fast-forward. Local edits and a live agent are left alone. A head that cannot be fast-forwarded onto — rewritten, or behind the worktree — waits for `zen review`, which asks before `reset --hard` and only from a terminal.

## Merged-review cleanup

Every `cleanup_interval`, the daemon removes the review worktree of each PR that merged at least `cleanup_after_merge` ago. The delay is a Go duration such as `30m` or `6h`, measured from GitHub's merge time; it defaults to `1h`, and `0s` removes the worktree on the next scan.

Only PR reviews are removed: worktrees named `<repo>-pr-<N>` that are checked out on the `pr-<N>` branch zen created, or on a detached HEAD. Work streams are never removed by the daemon, even when their name ends in `-pr-<N>`; `zen cleanup` is the manual command for those. PRs closed without merging are left alone too.

A merged review is kept, and the skip is logged, when any of these is true:

- It has local changes, or untracked files other than zen's generated context.
- An agent is running in it.
- `HEAD` has commits that the PR's last head on GitHub does not contain. Zen never pushes from a review worktree, so those commits exist only there.
- The comparison can't be made, for example because `pull/<N>/head` can't be fetched.

`cleanup_after_days` is deprecated and ignored. It delayed removal until a merged review had no new commits for that many days, which let merged reviews pile up.

## Repos

Each repo key (e.g. `app`) is a short name you choose — it doesn't have to match the GitHub repo name. It's used for worktree naming (`app-pr-42`), queue keys (`app:42`), and display. The `full_name` is the actual `owner/repo` used for GitHub API calls. The daemon polls review requests across every repo in this file.

If two orgs have a repo with the same name, pick different keys:

```yaml
repos:
  octo-app:
    full_name: octo-sts/app
    base_path: ~/git/repo-octo-sts-app
  other-app:
    full_name: other-org/app
    base_path: ~/git/other/repo-app
```

All repos and authors must be configured — there are no hardcoded defaults.

## Agent

`agent: claude` (default) or `agent: codex` selects the coding agent zen launches in each worktree. Override per command with `--agent`:

```bash
zen review 42 --agent codex
zen work new app my-feature --agent codex
zen review resume 42 --agent codex
```

What changes per agent:

| | `claude` | `codex` |
|--|----------|---------|
| Launch / resume | `claude … --resume <id>` | `codex … / codex resume <uuid>` |
| Model flag | `--model` | `-m` |
| Context file | `CLAUDE.local.md` | `AGENTS.md`, or `.zen/PR_CONTEXT.md` if the repo already ships an `AGENTS.md` |
| Slash-command prompts | `~/.claude/commands/` | `~/.codex/prompts/` |
| Sessions on disk | `~/.claude/projects/<encoded-path>/*.jsonl` | `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl` |

zen never overwrites a repo's committed `AGENTS.md`; if one exists, the context goes to `.zen/PR_CONTEXT.md` instead. The zen-owned `.zen/` directory is added to the repo's shared `info/exclude` (git has no per-worktree exclude file), while an injected `AGENTS.md` shows as an untracked file — the same way Claude's injected `CLAUDE.local.md` does. The `--model` value is passed through verbatim, so use model names your chosen agent understands (e.g. `opus` for Claude, `gpt-5-codex` for Codex).

Note that the background daemon always uses the **configured** agent: if you run `zen review 42 --agent codex` while `agent: claude` is set in config, the daemon's context injection and session tracking for that worktree still act as Claude. Set `agent:` in config when switching agents for more than a one-off session.

`zen agent status` (and the `zen_agent_status` MCP tool) is the exception to single-agent behaviour: it lists sessions from **all** agents in one table with an AGENT column; pass `--agent claude|codex` to narrow it. "waiting" detection comes from the daemon's snapshot and is only available for the configured agent — other agents' sessions show as running/stopped.

### Session migration between agents

`zen review resume`/`zen work resume` can carry a session across agents: when the selected agent has no session for the worktree but the other agent does, zen offers to migrate the most recent one and resume it (pass `--migrate` to skip the prompt). Claude → Codex goes through Codex's own importer via `codex app-server` (with its limits: last 30 days, ~50 most recent sessions); Codex → Claude is a zen-side translation of the rollout into a Claude session file (shell calls become `Bash` tool history, other tool calls become annotated text, encrypted reasoning is dropped, and files are capped below Claude's 5 MB resume threshold). Migration formats were verified against Claude Code 2.1.200 and Codex CLI 0.142.0; both agents' session formats are undocumented internals, so a future agent update can require adjustments. Deep dive: [session-migration.md](session-migration.md).

## Terminal

`terminal: iterm`, `terminal: ghostty`, `terminal: kitty`, `terminal: macos`, or
`terminal: tmux`. When `terminal` is unset, zen uses `tmux` if it runs inside a
tmux session (`$TMUX` is set) and `iterm` otherwise.

For Ghostty tab creation on macOS:

1. **Ghostty must be running** — open it manually before invoking zen.
2. **Accessibility permissions** — System Preferences → Security & Privacy → Accessibility.
3. **Automation permissions** — System Preferences → Security & Privacy → Automation.
4. **Ghostty focus** — the window should be focused for reliable tab creation.

If any of these aren't met, zen falls back to opening new windows.

kitty (Linux and macOS) opens each session in a new OS window. When zen runs
from inside kitty with `allow_remote_control yes` set in `kitty.conf` (or a
socket configured via `listen_on`), the window is opened from the running
kitty instance; otherwise zen starts a separate kitty instance per session.

`terminal: macos` uses the built-in macOS Terminal.app. If Terminal already has
a window, zen opens a new tab via Cmd+T (needs Accessibility, same as Ghostty);
if none exist, or tab creation fails, it opens a new window. macOS-only.

`terminal: tmux` opens each session as a new window in the tmux session zen
runs in, named after the worktree directory. It suits a remote host reached
over ssh, such as a cloud workstation, where there is no terminal application
to drive. The command runs through your login shell (`$SHELL -l -c`), and the
window keeps a shell in the worktree when the agent exits. Outside tmux, zen
reports an error; use `--no-terminal` there instead.

## Slack task watcher

An opt-in daemon loop that polls for the user's own emoji reaction (`slack.emoji`, default `claudecode`) via `reactions.list`, then for each new hit: adds an ack reaction in-thread (`slack.ack_reaction`, default `eyes`), fetches the thread, creates a feature worktree in `slack.default_repo` from `origin/main`, and immediately launches the agent in a terminal tab with the thread as the initial prompt — the launched session does all the "figure out what to do" work, not the daemon. Unlike the PR-review flow, this does open a terminal tab automatically: a Slack reaction is a deliberate, comparatively rare signal, not the high-volume PR-review firehose.

When that worktree's session later goes idle ("waiting", the same signal that drives the desktop `notify.SessionWaiting`), the daemon also sends a Slack DM to the user with the worktree name, resume command, and a link back to the original thread. That DM stops once the task is actually done: either a merged PR from the worktree's branch, or the configured `slack.done_emoji` (default `done_check`) reacted onto the original message.

Setup:

1. `slack.enabled: true` in config, plus `slack.default_repo` (required — no hardcoded default).
2. Create a Slack app (a dedicated personal one is recommended over reusing a shared org bot) with these token scopes: `reactions:read`, `channels:history`, `groups:history`, `im:history`, `mpim:history`, `chat:write`.
3. Export the resulting token as `ZEN_SLACK_TOKEN` in the environment the daemon starts in (`zen watch start` inherits your shell's env). The token is never written to `config.yaml` or any state file.

If `slack.enabled` is true but `ZEN_SLACK_TOKEN` is unset, or `auth.test` fails at startup, the daemon logs a warning and runs with the Slack watcher disabled rather than failing to start.

## Environment

`ZEN_HOME` overrides the config/state directory (default `~/.zen`). Functional tests and a second daemon use this so they do not share `config.yaml` or `last_check.json` with a day-to-day install.

## State files

All state lives in `~/.zen/state/` (or `$ZEN_HOME/state/`):

| File | Purpose |
|------|---------|
| `watch.pid` | Daemon PID |
| `watch.log` | Daemon logs (rotated at 10MB; previous log kept as `watch.log.1`) |
| `last_check.json` | Last GitHub poll: timestamp, inbox count, `notified_new` (`repo:n` keys already announced), `applied_shas` (last-seen worktree HEAD) |
| `pr_cache.json` | PR titles/authors for display |
| `sessions.json` | Cached agent session states (updated every 10s by daemon) |
| `slack_seen.json` | Message keys already processed by the Slack task watcher |
| `slack_origins.json` | Worktree path → originating Slack thread, for the completion DM |

## Upgrading

Install the new binary and restart the daemon so it is the process that actually polls GitHub. There is no migrate command and `config.yaml` does not need to change.

```bash
make build                 # or however you install zen
zen watch stop
zen watch start
```

Existing review worktrees stay in place. On the next poll they fast-forward if GitHub has moved and the tree is idle. In-flight reviews (local edits or a live agent) are left alone. You will not get a second “new review request” for PRs that were already in the inbox. If an author force-pushed while you were away, run `zen review <n>` — it asks before resetting.

`cleanup_after_days` can stay in `config.yaml`; it is ignored. After the restart, the first cleanup scan (one `cleanup_interval` later) removes every review whose PR merged more than `cleanup_after_merge` ago and passes the checks in [Merged-review cleanup](#merged-review-cleanup).

Older zen wrote `seen_prs` (PR numbers only) in `last_check.json`. The first poll after upgrade absorbs that list into `notified_new` and never writes `seen_prs` again.

## Unattended install

`zen setup` runs without prompting when given the config as flags or as a file, for machine provisioning (cloud-init, Ansible, dotfiles, a Nix activation script):

```bash
zen setup \
  --repo 'app=octo-sts/app:~/git/repo-octo-sts-app' \
  --repo 'wolfi-dev/os:~/git/repo-os' \
  --author mattmoor,wlynch \
  --terminal tmux

# or install a config file you manage yourself (comments are kept)
zen setup --config-file ./zen-config.yaml
generate-config | zen setup --config-file -
```

| Flag | Meaning |
|------|---------|
| `--repo [short=]owner/name:base_path` | A repository (repeatable). `short` defaults to `name`. Quote the value to keep `~` literal; zen expands it when it loads the config. |
| `--author <login>` | GitHub username for PR filtering (repeatable or comma-separated) |
| `--agent claude\|codex` | Coding agent, default `claude` |
| `--terminal iterm\|ghostty\|kitty\|macos\|tmux` | When omitted, zen picks at runtime: `tmux` inside a tmux session, `iterm` otherwise |
| `--branch-prefix <prefix>` | Feature branch prefix, default git `user.name` |
| `--config-file <path>` | Install this file (`-` for stdin) instead of building one from flags. Cannot be combined with the flags above. |
| `--force` | Replace an existing config that differs |
| `--skip-prompts` | Do not install the agent's slash-command prompts |
| `--non-interactive` | Never prompt, even with no other flags (fails instead) |

Behaviour suited to provisioning:

- **Idempotent.** If `~/.zen/config.yaml` already has the same content, setup leaves it alone and exits 0. If the content differs, setup exits 1 without touching it, unless you pass `--force`. Slash-command prompts that are already installed are kept.
- **Validated up front.** The config is checked with the rules `zen` uses to load it. It also needs at least one repo whose short name is letters, digits, `.`, `_` or `-` (it names worktree directories), with an `owner/name` `full_name` and a `base_path`. Unknown keys such as a misspelt `authers:` are errors too, and so is a file with more than one YAML document (`---`), since zen reads only the first. A bad value fails the install rather than the first command after it. Interactive `zen setup` applies the same rules and asks again on a bad answer.
- **Atomic.** The file is written to a temporary file, flushed to disk and renamed, so a running daemon never reads a partial config and a crash right after provisioning cannot leave an empty one. If `~/.zen/config.yaml` is a symlink (stow, chezmoi, home-manager), setup writes to the link's target, even one that does not exist yet, and keeps the link and the file's permissions.
- **No hang without a terminal.** Interactive `zen setup` with stdin closed (for example `</dev/null`) exits with an error instead of waiting forever.
- **Run it as the user who runs zen.** Setup writes `~/.zen` and the agent's prompts under `$HOME`, and the files it creates belong to whoever runs it. Provisioning that runs as root should switch to the target user first: `sudo -u alice -H zen setup ...`, or `become_user: alice` in Ansible. Pointing `HOME` or `ZEN_HOME` at another user's home from root leaves root-owned files there that the user's daemon cannot write.
- `ZEN_HOME` moves the config and state directory away from `~/.zen`, for example to test a config without touching your own.

Setup does not authenticate GitHub or start the daemon. Run `gh auth login --with-token` (or set `GH_TOKEN`) and `zen watch start` as separate provisioning steps.
