# MCP server

Zen exposes its commands as a Model Context Protocol server, so a running Claude session can query your inbox, list worktrees, and open reviews directly.

## Run

```
zen mcp serve
```

Speaks MCP over stdio. Register it once with Claude Code and any session can call zen tools:

```
claude mcp add --scope user zen -- zen mcp serve
```

## Tools

| Tool | Purpose |
|------|---------|
| `zen_inbox` | Fetch pending PR reviews |
| `zen_worktree_list` | List worktrees |
| `zen_pr_details` | PR metadata |
| `zen_pr_files` | Files changed in a PR |
| `zen_agent_status` | Agent session info per worktree (Claude and Codex) |
| `zen_who_am_i` | Work summary (merged PRs, in-progress, reviews) |
| `zen_config_repos` | Configured repositories |
| `zen_review` | Create a PR worktree, or fast-forward an existing one, and inject context |
| `zen_review_resume` | Fast-forward an existing PR worktree, then return its path and sessions |

`zen_review` and `zen_review_resume` use the same catch-up rules as the CLI (`git merge --ff-only`, skip if dirty or an agent is live). They never `git reset --hard` on a rewritten head, or on one that is behind the worktree — confirm that from a TTY with `zen review <n>`.

> **Note:** `zen_inbox` uses the `ignore_drafts` setting from your config. Unlike the `zen inbox` CLI, there is no per-call override. Change the config (see [docs/configuration.md](configuration.md)) to toggle draft filtering for MCP callers.
