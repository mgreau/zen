# Configuration

Zen reads `~/.zen/config.yaml`. Run `zen setup` for an interactive walkthrough, or edit the file directly.

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

# Run a command for each daemon notification, e.g. to push it to your phone.
# See "Push notifications" below.
notify:
  command: 'curl -fsS -d "$ZEN_MESSAGE" https://ntfy.sh/<random-topic>'
  events: [session-waiting]
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

zen tags each window it opens with the window option `@zen-worktree`, so one
worktree gets one window. Opening a worktree that already has a window, as
`zen review resume` or `zen work resume` do, reuses it. If the window is at its
shell prompt, the agent starts there. If something is still running in it, or
you split it into several panes, zen only switches to it and says so. Windows the daemon opens for Slack tasks open in the
background, so they don't take focus from the window you're typing in.

### Highlighting windows that need you

zen keeps the window option `@zen-status` on each of its windows. The value is
`running`, `waiting` (the agent needs you), or `stopped`. Two things set it:

- For Claude, hooks that zen passes with `--settings` when it opens the agent
  in a tmux window. They set the status as soon as Claude starts, finishes a
  turn or asks for a permission, and when you reply, and mark it `stopped`
  when Claude exits. Nothing is written into
  the worktree, your own hooks still run, and the commands zen prints for
  `--no-terminal` don't include them.
- The daemon's session scan. This works for every agent, after a delay of up
  to `session_scan_interval` plus the idle threshold. Where Claude's hooks set
  the status, the daemon marks the window `stopped` once Claude exits, and
  `waiting` after a turn you interrupted with Esc, which no hook reports. It
  tells that from a long, quiet tool by the interrupt marker Claude records
  in the session. A session idle for 10 minutes is marked `waiting` whatever
  the hooks last said, for waits no hook reports, unless its last step is a
  tool call still running, such as a subagent.

After you approve a permission, the window stays `waiting` until the
approved tool finishes: no hook fires on the approval itself.

The daemon reaches tmux the way any tmux client does: through `$TMUX` if it
was started inside tmux, else the default server. If you run tmux on a custom
socket (`tmux -L` or `-S`), start the daemon from inside that tmux, or it
won't find the windows to update.

tmux doesn't style windows by user options on its own. `zen setup tmux` adds
the settings for that to your tmux config, and `zen setup` offers to when you
pick tmux. It writes them between `# >>> zen >>>` and `# <<< zen <<<` lines in
`~/.tmux.conf` (or `~/.config/tmux/tmux.conf`, if only that exists), and
reloads tmux when run inside it. Running it again updates that block and
leaves the rest of the file alone. The block:

```tmux
# Track which terminals have focus, so zen doesn't notify you about the
# window you are looking at.
set -g focus-events on
# Mark windows whose agent is waiting on you: tmux's default format, with
# a reverse "● " in front.
set -g window-status-format '#{?#{==:#{@zen-status},waiting},#[reverse]● ,}#I:#W#{?window_flags,#{window_flags}, }#[default]'
# prefix + W: pick from the windows waiting on you.
bind W choose-tree -Zw -f '#{==:#{@zen-status},waiting}'
```

### Desktop notifications over ssh

When you ssh into the host from a Mac, zen sends a desktop notification for
the Claude in each of its windows, through tmux and ssh to iTerm2 or Ghostty:

- When Claude finishes a turn: the start of its reply, right away.
- When Claude asks for a permission or an answer: its own text, such as
  "Claude needs your permission to use Bash".

The title names the window's worktree, such as `Claude · app-fix-login`, or
for a PR review its repo, number, and title, such as
`Claude · app#1042 · Add pagination to ListUsers`.

zen does this with its own hook (`zen hook notify`), passed with the other
hooks. It turns off Claude's own notifications for those sessions only, so
you don't get each one twice; your Claude settings are not changed. With
tmux's `focus-events` on, as `zen setup tmux` sets it, there's no notification
for the window you're looking at in a focused terminal. Claude's reply can
quote untrusted text, so zen drops control and invisible formatting
characters from it before it reaches your terminal.

```yaml
notify:
  terminal: true            # false: no zen notifications; Claude keeps its own
  terminal_events: [stop, permission_prompt, elicitation_dialog]   # the default
  terminal_sequence: auto   # or osc777 (Ghostty), osc9 (iTerm2 and others)
```

`terminal_events` takes `stop` (a turn ended) and Claude's notification types,
such as `permission_prompt`, `elicitation_dialog`, or `idle_prompt`. An empty
list turns them all off. `auto` sends OSC 777, which has a separate title,
when every terminal attached to the session is Ghostty, and OSC 9, which puts
the title in front of the text, otherwise.

These notifications get through tmux because zen sets `allow-passthrough all`
on each window it opens. With `all`, tmux lets them through from windows you
aren't looking at too. tmux 3.3 only knows `on`, which covers the visible
window only, and older tmux has no passthrough. Passthrough gives programs in
zen's windows the same access to your terminal they would have without tmux.

On the Mac, the terminal has to show them:

- iTerm2: turn on Settings → Profiles → Terminal → *Notification Center
  Alerts*, and under *Filter Alerts* tick *Send escape sequence-generated
  alerts*.
- Ghostty shows them by default. It uses its window title as the subtitle;
  set `title = <name>` in Ghostty's config for a fixed one.
- Either way, the terminal app needs notification permission in System
  Settings.

This only works while a tmux client is attached over ssh. To be notified
while you're detached, use a [push command](#push-notifications).

## Push notifications

The daemon shows its notifications (a session waiting on you, a review ready,
and so on) on the desktop of the host it runs on. On a remote host that is
nowhere you would see them. `notify.command` is a shell command the daemon
also runs for each notification, for example a curl to a push service with
apps for the Mac and the phone:

```yaml
notify:
  # Use a long random topic: anyone who knows it can read the messages.
  command: 'curl -fsS -H "Title: $ZEN_TITLE" -d "$ZEN_MESSAGE" https://ntfy.sh/zen-k3v9q2xw7'
  events: [session-waiting]   # optional; empty means every event
```

The command runs through `/bin/sh` with the notification in its environment.
Quote these variables, as above, rather than pasting them into the command:

| Variable | Contents |
|---|---|
| `ZEN_EVENT` | `pr-review`, `pr-updated`, `worktree-ready`, `pr-merged`, `stale-worktrees`, `session-waiting`, or `digest` |
| `ZEN_TITLE` | The notification title |
| `ZEN_MESSAGE` | The notification text |
| `ZEN_SUBTITLE` | The subtitle, such as the model or the repo; may be empty |
| `ZEN_RESUME` | The zen command that opens the worktree, for `session-waiting` and `worktree-ready` |

`events` limits the command to those events. A command that fails, or runs
longer than 30 seconds, is logged in the daemon log (`zen watch logs`). The
desktop notification is still sent either way.

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
