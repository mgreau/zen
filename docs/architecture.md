# Architecture

How zen is put together internally. Read this if you're contributing to zen, debugging the daemon, or curious about the design choices.

## Source of truth

**Git worktrees are the single source of truth.** All inventory — which PRs have local worktrees, which feature branches exist, worktree paths and types — is derived from `git worktree list` via `worktree.ListAll()`. There is no external database or registry to drift out of sync.

PR metadata (titles, authors) is cached in a lightweight JSON file (`~/.zen/state/pr_cache.json`) written by the daemon during setup. This cache is purely for display — if it's missing or stale, commands still work (they just show PR numbers instead of titles).

## Worktree naming

| Type | Worktree pattern | Branch pattern | Example |
|------|------------------|----------------|---------|
| PR review | `<repo>-pr-<number>` | (fetched from remote) | `app-pr-42` |
| Feature | `<repo>-<branch>` | `<branch_prefix>/<branch>` | `app-add-oidc-claims` → `mgreau/add-oidc-claims` |

The git branch for feature worktrees uses `branch_prefix` from config (falling back to `git config user.name`, then no prefix). The worktree directory name itself is always `<repo>-<branch>` regardless of prefix.

## Daemon architecture

The daemon uses [driftlessaf](https://github.com/driftlessaf) workqueues with two reconcilers — one for setup, one for cleanup:

```
                          ┌─────────────────────────────────────────────────────────┐
                          │                   watchDaemon()                         │
                          └────┬──────────────────┬──────────────────┬──────────────┘
                               │                  │                  │
                       poll_interval    dispatch_interval    cleanup_interval
                               │                  │                  │
                               v                  v                  v
                        ┌─────────────┐   ┌──────────────┐   ┌──────────────┐
                        │reloadConfig │   │  dispatcher  │   │ scanMerged   │
                        │+ pollOnce() │   │   .Handle()  │   │   PRs()      │
                        └──────┬──────┘   │              │   │              │
                               │          └──────┬───────┘   └──────┬───────┘
                               │                 │                  │
              ┌────────────────┼─────────┐       │                  │
              v                v         v       v                  v
     ┌────────────────┐ ┌──────────┐ ┌───────────────┐     ┌───────────────┐
     │ GitHub GraphQL │ │  macOS   │ │  setupQueue   │     │ cleanupQueue  │
     │ GetReview      │ │  notify  │ │               │     │               │
     │ Requests()     │ │          │ │  "app:42" │     │  "app:42" │
     └────────┬───────┘ └──────────┘ │  "app:87" │     │  "app:35" │
              │                      └───────┬───────┘     └───────┬───────┘
              │  new PRs from                │                     │
              │  configured authors          │                     │
              └──────────────────────────────┘                     │
                    StorePRData() +                                │
                    Queue(key)                                     │
                                                                   │
    ┌──────────────────────────────────────┐    ┌──────────────────┴───────────────┐
    │       SetupReconciler.Reconcile()    │    │    CleanupReconciler.Reconcile()  │
    │                                      │    │                                   │
    │  key ──→ ParsePRKey("app:42")    │    │  key ──→ ParsePRKey("app:35") │
    │          repo=app, pr=42         │    │          repo=app, pr=35      │
    │                                      │    │                                   │
    │  Step 1: ensureWorktree             │    │  Step 1: removeWorktree           │
    │  ┌─────────────────────────────┐    │    │  ┌─────────────────────────────┐  │
    │  │ if exists? skip             │    │    │  │ if missing? skip            │  │
    │  │ git fetch origin pull/N/head│    │    │  │ git worktree remove --force │  │
    │  │ git worktree add            │    │    │  └─────────────────────────────┘  │
    │  │ rm index.lock               │    │    │         │                         │
    │  └─────────────────────────────┘    │    │         v on error: RETRY         │
    │         │                           │    │                                   │
    │         v on error: RETRY           │    └───────────────────────────────────┘
    │                                      │
    │  Step 2: ensureContextInjected      │
    │  ┌─────────────────────────────┐    │     ┌──────────────────────────────────┐
    │  │ if CLAUDE.local.md? skip   │    │     │         Error Handling           │
    │  │ fetch PR details + files   │    │     │                                  │
    │  │ render CLAUDE.local.md     │    │     │  Invalid key    ──→ SKIP (permanent)
    │  └─────────────────────────────┘    │     │  Unknown repo   ──→ SKIP (permanent)
    │         │                           │     │  Git failure    ──→ RETRY         │
    │         v on error: LOG, CONTINUE   │     │                     30s → 60s →   │
    │                                      │     │                     ... → 10m cap │
    │  Step 3: cachePRMeta                │     │                     max 5 attempts│
    │  ┌─────────────────────────────┐    │     │  Context/cache  ──→ LOG, CONTINUE │
    │  │ prcache.Set(repo, pr,       │    │     │                                  │
    │  │   title, author)            │    │     └──────────────────────────────────┘
    │  └─────────────────────────────┘    │
    │                                      │
    │  ✓ notify.WorktreeReady()           │
    │                                      │
    └──────────────────────────────────────┘
```

Each step is **idempotent** — safe to re-run if interrupted. Git failures retry with exponential backoff (30s..10m, max 5 attempts). Context injection and PR cache writes are non-blocking — failures are logged but don't prevent the worktree from being created.

## Source tree

```
zen
├── cmd/                          # CLI commands (cobra)
├── commands/                     # Claude Code commands (embedded in binary)
├── internal/
│   ├── config/                   # YAML config (~/.zen/config.yaml)
│   ├── context/                  # CLAUDE.md generation for PR reviews
│   ├── ghostty/                  # Ghostty tab/window management via AppleScript
│   ├── github/                   # GitHub API (GraphQL + REST, 30s call timeouts)
│   ├── iterm/                    # iTerm2 tab management via AppleScript
│   ├── mcp/                      # MCP server exposing zen tools
│   ├── notify/                   # macOS notifications
│   ├── prcache/                  # Lightweight PR metadata cache (JSON)
│   ├── reconciler/               # Workqueue-based PR setup + cleanup + session scan
│   ├── review/                   # Shared worktree creation logic (CLI + MCP)
│   ├── session/                  # Claude session detection
│   ├── terminal/                 # Terminal backend abstraction (iterm/ghostty)
│   ├── ui/                       # Terminal formatting
│   └── worktree/                 # Git worktree discovery + management
├── main.go
└── go.mod
```
