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
| Slack task | `<repo>-slack-<slug>` | `<branch_prefix>/slack-<slug>` | `app-slack-1712... ` |

The git branch for feature worktrees uses `branch_prefix` from config (falling back to `git config user.name`, then no prefix). The worktree directory name itself is always `<repo>-<branch>` regardless of prefix.

## Worktree placement

`worktree_layout` decides where a **new** worktree goes: `sibling` (the default) puts it beside the clone at `<base_path>/<name>`, `nested` puts it inside at `<base_path>/<repo>/_worktrees/<name>`. See [configuration.md](configuration.md#worktree-layout) for the user-facing description.

The `_` prefix on `_worktrees` is load-bearing. The Go tool skips directories beginning with `_` or `.`, so `go build ./...` in a Go repo does not descend into checked-out worktrees. Renaming it would break `./...` for every Go repo zen manages.

**Reads come from git; only writes consult the layout.** `worktree.ListAll()` already derives inventory from `git worktree list`, so it never had a layout assumption. Path *construction* did — six call sites built `<base_path>/<name>` directly. They now go through `worktree.Resolve()`, which looks the worktree up in git first and falls back to the configured layout only when nothing is registered.

That asymmetry is what makes the setting safe to change. Deriving the path from the layout alone would strand every worktree created under the previous setting, silently: `reconciler.CleanupReconciler` would `os.Stat` a path that does not exist and take its "already removed" branch, reporting success without removing anything, while `SetupReconciler` would find nothing and run `git worktree add` for a branch git already has checked out elsewhere — rejected on every poll, through the full retry backoff. Resolving through git instead means both layouts work at once, in both directions, so existing worktrees keep working and drain on their own rather than needing migration.

`Resolve` returns the configured spelling of a path whenever it names the same location as the registration (compared through `filepath.EvalSymlinks`, since git reports resolved paths and `base_path` may not be one). Callers use the worktree path as an agent-session key — Claude encodes it into a directory name under `~/.claude/projects/` — so handing back a different string for an unchanged worktree would orphan its history.

**Keeping git quiet.** A nested worktree would otherwise show as untracked in the main checkout. `worktree.EnsureNestedExcluded` runs after a successful `git worktree add` and delegates to `gitignore.EnsureExcluded`, which prefers to do nothing: if `git check-ignore` says the path is already ignored — by a user-level ignore, a committed `.gitignore`, or an earlier call — it returns `AlreadyIgnored` and no repository is modified. Only otherwise does it append to that clone's `.git/info/exclude` and log a note pointing at the user-level file.

That ordering matters because `_worktrees/` describes how the user works, not anything about a given project, so the user-level ignore is the right scope for it; a single line in `~/.config/git/ignore` (which git reads with no `core.excludesFile` setting) puts every repo in the `AlreadyIgnored` state at once. The per-clone fallback exists so `nested` works without setup. The committed `.gitignore` is never touched in either case — zen manages repos the user may not own.

Exclusion is checked with `git check-ignore` rather than by reading `.gitignore`, because a negated pattern or a `core.excludesFile` entry both change the answer invisibly. `Failed` — still not ignored after the attempt — is the only outcome that warns, and the only one a user has to fix by hand.

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
    │  │ if context present? skip   │    │     │         Error Handling           │
    │  │ fetch PR details + files   │    │     │                                  │
    │  │ render + inject context    │    │     │  Invalid key    ──→ SKIP (permanent)
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
├── commands/                     # Slash-command prompts (embedded in binary)
├── internal/
│   ├── agent/                    # Agent abstraction (Claude Code / Codex)
│   ├── board/                    # `zen board` bubbletea TUI: live PR status view
│   ├── config/                   # YAML config (~/.zen/config.yaml)
│   ├── context/                  # PR-review context rendering
│   ├── ghostty/                  # Ghostty tab/window management via AppleScript
│   ├── github/                   # GitHub API (GraphQL + REST, 30s call timeouts)
│   ├── iterm/                    # iTerm2 tab management via AppleScript
│   ├── kitty/                    # kitty window management via kitty CLI (Linux + macOS)
│   ├── macos/                    # Terminal.app tab/window management via AppleScript
│   ├── mcp/                      # MCP server exposing zen tools
│   ├── notify/                   # Desktop notifications (osascript on macOS, notify-send on Linux)
│   ├── prcache/                  # Lightweight PR metadata cache (JSON)
│   ├── reconciler/               # Workqueue-based PR setup + cleanup + session scan + Slack task watcher
│   ├── review/                   # Shared worktree creation logic (CLI + MCP)
│   ├── session/                  # Shared session types + Claude session detection
│   ├── slack/                    # Minimal Slack Web API client for the task watcher
│   ├── terminal/                 # Terminal backend abstraction (iterm/ghostty/kitty/macos)
│   ├── ui/                       # Terminal formatting
│   └── worktree/                 # Git worktree discovery + management
├── main.go
└── go.mod
```

## Agent abstraction

zen launches a coding agent in each worktree. Originally hardwired to Claude Code, the agent-specific behaviour now lives behind the `agent.Agent` interface (`internal/agent`), with `claude` and `codex` implementations selected by `agent:` in config or a `--agent` flag.

The interface owns the six places the two agents differ: the launch and resume shell commands (binary, `--model` vs `-m`, `--resume <id>` vs `resume <uuid>`), the per-worktree context file, the slash-command prompts directory, and session discovery plus token parsing. The `context` package renders the PR-review markdown; the agent decides where to write it. The `terminal` layer is agent-agnostic — it just runs a command string the agent builds.

Session discovery differs most: Claude keys session files by an encoded worktree path (`~/.claude/projects/<encoded>/*.jsonl`), while Codex stores rollout files by date (`~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`) and records the worktree as a `cwd` field inside each file. The Codex implementation walks the sessions tree, matches `cwd`, and caches file→cwd results for the daemon's repeated scans. Codex token totals are cumulative in `total_token_usage`, so the latest event wins rather than summing.
