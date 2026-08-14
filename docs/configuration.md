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

That's enough to start the daemon (`zen watch start`) and check your inbox (`zen inbox`).

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

terminal: iterm  # or "ghostty" or "kitty"

# Prefix for feature branches created by `zen work new`.
# If unset, falls back to `git config user.name` (spaces → hyphens), then no prefix.
branch_prefix: mgreau

# Skip draft PRs in `zen inbox`, watch notifications, and the MCP inbox tool.
# Defaults to false (drafts are shown). Set to true to skip drafts so you don't
# review something that isn't ready. Override on a single run with
# `--ignore-drafts=false`.
ignore_drafts: true

watch:
  dispatch_interval: "10s"      # How often to process queued work
  cleanup_interval: "1h"        # How often to scan for merged PRs
  session_scan_interval: "10s"  # How often to scan Claude session states
  cleanup_after_days: 5         # Days after merge before removing worktree
  concurrency: 2                # Parallel worktree setups
  max_retries: 5                # Max retry attempts for git failures
```

The daemon re-reads `config.yaml` on every poll tick. Changes to `poll_interval`, `authors`, `repos`, and other settings take effect without restarting.

## Repos

Each repo key (e.g. `app`) is a short name you choose — it doesn't have to match the GitHub repo name. It's used for worktree naming (`app-pr-42`), queue keys (`app:42`), and display. The `full_name` is the actual `owner/repo` used for GitHub API calls.

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

## Terminal

`terminal: iterm` (default), `terminal: ghostty`, or `terminal: kitty`.

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

## State files

All state lives in `~/.zen/state/`:

| File | Purpose |
|------|---------|
| `watch.pid` | Daemon PID |
| `watch.log` | Daemon logs (rotated at 10MB; previous log kept as `watch.log.1`) |
| `last_check.json` | Timestamp of last GitHub poll |
| `pr_cache.json` | PR titles/authors for display |
| `sessions.json` | Cached agent session states (updated every 10s by daemon) |
