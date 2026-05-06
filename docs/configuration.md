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
claude_bin: claude
terminal: iterm  # or "ghostty"

# Prefix for feature branches created by `zen work new`.
# If unset, falls back to `git config user.name` (spaces → hyphens), then no prefix.
branch_prefix: mgreau

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

## Terminal

`terminal: iterm` (default) or `terminal: ghostty`.

For Ghostty tab creation on macOS:

1. **Ghostty must be running** — open it manually before invoking zen.
2. **Accessibility permissions** — System Preferences → Security & Privacy → Accessibility.
3. **Automation permissions** — System Preferences → Security & Privacy → Automation.
4. **Ghostty focus** — the window should be focused for reliable tab creation.

If any of these aren't met, zen falls back to opening new windows.

## State files

All state lives in `~/.zen/state/`:

| File | Purpose |
|------|---------|
| `watch.pid` | Daemon PID |
| `watch.log` | Daemon logs (rotated at 10MB; previous log kept as `watch.log.1`) |
| `last_check.json` | Timestamp of last GitHub poll |
| `pr_cache.json` | PR titles/authors for display |
| `sessions.json` | Cached Claude session states (updated every 10s by daemon) |
