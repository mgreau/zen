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
     │ GitHub GraphQL │ │  notify  │ │  setupQueue   │     │ cleanupQueue  │
     │ GetReview      │ │          │ │               │     │               │
     │ Requests()     │ │          │ │  "app:42" │     │  "app:42" │
     └────────┬───────┘ └──────────┘ │  "app:87" │     │  "app:35" │
              │                      └───────┬───────┘     └───────┬───────┘
              │  new review request,         │                     │
              │  or HEAD ≠ GitHub SHA        │                     │
              │  on an existing worktree     │                     │
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
    │  │ missing: fetch pull/N/head │    │    │  │ if missing? skip            │  │
    │  │   into pr-N, worktree add   │    │    │  │ git worktree remove --force │  │
    │  │ exists: fetch into          │    │    │  └─────────────────────────────┘  │
    │  │   origin/pr-N, ff-only      │    │    │         │                         │
    │  │ skip: dirty or live agent    │    │    │         v on error: RETRY         │
    │  │ rewritten: skip (CLI       │    │    └───────────────────────────────────┘
    │  │   prompts before reset)     │    │
    │  └─────────────────────────────┘    │
    │         │                           │
    │         v on error: RETRY           │
    │                                      │
    │  Step 2: ensureContextInjected      │
    │  ┌─────────────────────────────┐    │     ┌──────────────────────────────────┐
    │  │ rewrite when HEAD moved or  │    │     │         Error Handling           │
    │  │   first create              │    │     │                                  │
    │  │ fetch PR details + files    │    │     │  Invalid key    ──→ SKIP (permanent)
    │  │ render + inject context     │    │     │  Unknown repo   ──→ SKIP (permanent)
    │  └─────────────────────────────┘    │     │  Closed / silenced draft /       │
    │         │                           │     │    missing pull ref ──→ SKIP     │
    │         v on error: LOG, CONTINUE   │     │  Git failure    ──→ RETRY         │
    │                                      │     │                     30s → 60s →   │
    │  Step 3: cachePRMeta                │     │                     ... → 10m cap │
    │  ┌─────────────────────────────┐    │     │                     max 5 attempts│
    │  │ prcache.Set(repo, pr,       │    │     │  Context/cache  ──→ LOG, CONTINUE │
    │  │   title, author)            │    │     │  Dirty / live agent /         │
    │  └─────────────────────────────┘    │     │    rewritten head ──→ skip     │
    │                                      │     └──────────────────────────────────┘
    │  ✓ notify.WorktreeReady() on create │
    │  ✓ notify.PRUpdated() on SHA move  │
    │                                      │
    └──────────────────────────────────────┘
```

Each step is **idempotent** — safe to re-run if interrupted.

**Which PRs a poll sees.** The daemon runs the review-request search once per configured repository, scoped with `repo:`, and follows GitHub's pagination to the end of each. One unscoped search would not do: its page is filled on GitHub's terms, so review requests in repositories zen knows nothing about can crowd out a configured repository's PR, which then gets neither a worktree nor a refresh. Per-repository queries also stay well inside GitHub's 256-character search limit that a combined `repo:a repo:b …` query would eventually cross.

**Create vs refresh.** Skip-if-exists is the wrong idempotency for worktrees. Each poll compares GitHub `headRefOid` to worktree `HEAD` and queues the same setup key when they differ. Linear updates are `git merge --ff-only` onto `refs/remotes/origin/pr-N` (git refuses to fetch into the local `pr-N` branch while it is checked out). Refresh does not require the author to be in `authors:` — `zen review` can create those worktrees and the daemon still keeps them current. Create (no worktree yet) still requires the author to be in `authors:`.

**When zen will not move a worktree.** Tracked local edits, a live agent, a closed PR, and a draft hidden by `ignore_drafts` are left alone. Untracked context (`CLAUDE.local.md`, `.zen/`) does not count as dirty. An agent is live if a session UUID is on a process argv, or if `claude` / `codex` (or a node/python wrapper) has cwd in the worktree — that covers a first-pass `claude /review-pr` that has no UUID on argv yet. A live REST check at reconcile covers the race where a PR is queued while ready then closed or converted to draft. A missing `pull/N/head` is skip, not a git retry. `zen review` still opens a draft the user asked for.

**Rewritten history.** A worktree that cannot be fast-forwarded needs `git reset --hard`, and nothing resets it unattended. The daemon and MCP never do. `zen review` and `zen review resume` prompt `[y/N]` (default no) **only when stdin is a terminal**: a piped or redirected run declines, so `yes | zen review <n>` cannot answer for you. `--json` never resets. Untracked files stay; the previous tip stays in the reflog.

Two shapes reach that prompt. History **diverged** — git refuses the fast-forward outright. Or GitHub's head is an **ancestor** of the worktree: a force-push backward, or a commit made locally in the checkout. `git merge --ff-only` reports "Already up to date" for the second case without moving anything, so zen re-reads `HEAD` after the merge and treats "did not reach the target" the same as a refused fast-forward. Only a `HEAD` that actually landed on the fetched head counts as an update, which is what keeps context rewrites and “PR #N updated” notifications from repeating every poll.

**Errors.** Git failures retry with exponential backoff (30s..10m, max 5 attempts). Context injection and PR cache writes are non-blocking — failures are logged but do not prevent the worktree from being created.

**Notifications.** A first sighting with no local worktree sends “New PR Review Request”, whether or not the author is in `authors:`. `WorktreeReady` fires after create; `PRUpdated` fires after a successful fast-forward. PRs already in the inbox when you upgrade are not re-announced (see [configuration.md](configuration.md#upgrading)).

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
