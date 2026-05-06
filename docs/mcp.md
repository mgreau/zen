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
| `zen_agent_status` | Claude session info per worktree |
| `zen_who_am_i` | Work summary (merged PRs, in-progress, reviews) |
| `zen_config_repos` | Configured repositories |
| `zen_review` | Create a worktree for a PR (auto-detects repo, injects context) |
| `zen_review_resume` | Get worktree path and sessions for an existing PR review |
