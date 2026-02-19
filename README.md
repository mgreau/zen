# zen

A worktree orchestrator for AI-assisted PR reviews and feature work with [Claude Code](https://docs.anthropic.com/en/docs/claude-code).

AI-assisted development is changing how engineers work — more PRs to review, more context to juggle across parallel tasks. The old workflow — one IDE, one branch, one PR at a time — doesn't scale. You need many reviews and feature branches open in parallel, each with its own worktree, each with Claude Code ready to assist.

Zen manages this silently: a background daemon watches GitHub, creates worktrees, injects PR context into `CLAUDE.local.md` files (never touching the repo's own `CLAUDE.md`), retries git failures, and cleans up worktrees for merged PRs. For feature work, the same isolation — dedicated worktrees with persistent context. You just open a tab and start working with Claude.

## Table of Contents

- [How It Works](#how-it-works)
- [The Automated Loop](#the-automated-loop)
- [Your Workflow](#your-workflow)
  - [Inbox](#inbox)
  - [Review](#review)
  - [Reviews](#reviews)
- [Feature Work](#feature-work)
- [Dashboard](#dashboard)
  - [Status](#status)
  - [Search](#search)
  - [Agent Sessions](#agent-sessions)
  - [Cleanup](#cleanup)
- [Context Injection](#context-injection)
- [MCP Server](#mcp-server)
- [Configuration](#configuration)
- [Design](#design)
  - [Daemon Architecture](#daemon-architecture)
  - [Source of Truth](#source-of-truth)
  - [Worktree Naming](#worktree-naming)
  - [Source Tree](#source-tree)
- [Prerequisites](#prerequisites)
- [Building](#building)

## How It Works

```
  GitHub ──── zen watch ──── Worktrees Ready ──── You Review ──────── Cleanup
   PRs         (daemon)       (silent prep)       (zen review resume)  (automatic)
```

There are two loops. The **automated loop** runs in the background: a daemon polls GitHub, creates worktrees for new PRs, injects context, and removes worktrees for merged PRs. The **manual loop** is yours: check what needs review, open a worktree in a new iTerm tab with Claude, and do the review.

## The Automated Loop

The background daemon handles worktree lifecycle so you don't have to. It polls GitHub for PRs from configured authors, creates worktrees with context pre-loaded, sends a macOS notification when ready, and cleans up merged PRs after a configurable number of days.

```
  Poll GitHub          Setup                           Cleanup
  ┌──────────┐    ┌────────────────────┐    ┌─────────────────────────┐
  │ New PR   │───→│ git worktree add   │───→│ Merged 5+ days ago?     │
  │ detected │    │ inject CLAUDE.local│    │ git worktree remove     │
  └──────────┘    │ cache PR meta      │    └─────────────────────────┘
                  │ notify (macOS)     │
                  └────────────────────┘
```

Each step is idempotent and retries on failure. The daemon does **not** open iTerm tabs — worktrees are prepared silently, you open reviews when ready with `zen review resume`.

```
zen watch start                  # Start background daemon
zen watch stop                   # Stop daemon
zen watch status                 # Show daemon status + last check
zen watch logs                   # Tail daemon log output
zen watch logs search 42         # Search logs for a PR, worktree, or keyword
```

Logs: `~/.zen/state/watch.log` — automatically rotated at 10MB (previous log kept as `watch.log.1`). Search covers both files.

## Your Workflow

Once the daemon has prepared worktrees, your review flow looks like this:

```
  zen inbox               →  zen review resume 42  →  review in Claude     →  done
  (what needs review)        (open iTerm tab)         (CLAUDE.md ready)

  zen review 42           →  review in Claude     →  done
  (create + open)            (CLAUDE.md ready)
```

### Inbox

```
zen inbox                        # PRs needing review (filtered by configured authors)
zen inbox --all                  # From all authors
zen inbox --path pkg/sts          # PRs touching specific paths
zen inbox --repo other-repo      # Different repo
```

Shows pending PR reviews that don't yet have a local worktree. Also shows your own approved-but-unmerged PRs and PRs touching watched paths.

Example output:

```
───────────────────────────────────────────────────────────────
  Legend  W = Worktree
       * = local worktree exists
       zen review resume <number> to open  |  zen review <number> to create

2 Pending PR Reviews — app
Authors: alice bob charlie dave
═══════════════════════════════════════════════════════════════

  PR      Author                Title                                       Link
  ──────  ────────────────────  ──────────────────────────────────────────  ────────────────────────
  #1042   alice                 api: Add pagination to ListUsers endpoi...  https://github.com/acme/app/pull/1042
  #1038   bob                   fix(auth): Handle expired refresh tokens    https://github.com/acme/app/pull/1038

3 Open PRs touching platform/ and agents/ — app
═══════════════════════════════════════════════════════════════

  W   PR      Author                Title                                       Link
  ──  ──────  ────────────────────  ──────────────────────────────────────────  ────────────────────────
      #1045   eve                   fix(agents/result): handle reasoning ...    https://github.com/acme/app/pull/1045
      #1041   app/dependabot        build(deps): bump the all-others grou...    https://github.com/acme/app/pull/1041
  *   #1035   alice                 Surface a Tool for `format_config`          https://github.com/acme/app/pull/1035

2 Other PRs Requesting Your Review — app
═══════════════════════════════════════════════════════════════

  W   PR      Author                Title                                       Link
  ──  ──────  ────────────────────  ──────────────────────────────────────────  ────────────────────────
      #1039   app/dependabot        build(deps): bump the anchore group a...    https://github.com/acme/app/pull/1039
  *   #1036   alice                 Create a module for the metareconciler.     https://github.com/acme/app/pull/1036
```

### Review

```
zen review 42                    # Create worktree + open iTerm tab (auto-detects repo)
zen review 42 --repo other       # Specify repo explicitly
zen review 42 --no-iterm         # Create worktree only, print command
zen review resume 42             # Open existing worktree in new iTerm tab
zen review resume 42 --list      # List available sessions
zen review resume 42 --session 2 # Resume specific session
```

Manually create a PR review worktree: fetches the PR branch, creates the worktree, injects CLAUDE.md context, and opens an iTerm tab with Claude. When `--repo` is omitted, zen auto-detects the repo by querying GitHub — if the PR number exists in multiple repos, it prefers the one where you're a requested reviewer, or asks you to choose. Use this when the daemon hasn't picked up a PR yet or you want to start immediately. If the worktree already exists, use `zen review resume` instead. If you run `zen review resume` and no worktree exists, it offers to create one.

### Reviews

```
zen reviews                      # PR reviews from past 7 days
zen reviews --days 30            # Past 30 days
```

Lists PR review worktrees with titles from the PR cache and session status.

## Feature Work

Not everything is a PR review. Create and manage feature branch worktrees for your own work:

```
zen work                         # List feature worktrees
zen work new <repo> <branch>     # Create new feature worktree
zen work new app my-feature "initial prompt"   # With Claude prompt
zen work resume <name>           # Resume a feature session in new iTerm tab
zen work delete <name>           # Delete a feature worktree
```

## Dashboard

### Status

```
zen status
zen dashboard                    # Alias for zen status
```

Overview of all active work: worktree counts, PR reviews (with remote state and cleanup ETA), feature work, and daemon state.

Example output:

```
═══════════════════════════════════════════════════════════════
  Zen Status Dashboard
═══════════════════════════════════════════════════════════════

Worktrees
───────────────────────────────────────────────────────────────
  Total: 12  |  PR Reviews: 4  |  Features: 8

PR Reviews
───────────────────────────────────────────────────────────────
  State     PR      Title                                       Path
  ────────  ──────  ──────────────────────────────────────────  ──────────────────────────────
  OPEN      #1042   api: Add pagination to ListUsers endpoi...  ~/git/acme/repo-app/app-pr-1042
  OPEN      #1038   fix(auth): Handle expired refresh tokens    ~/git/acme/repo-app/app-pr-1038
  OPEN      #1035   Surface a Tool for `format_config`          ~/git/acme/repo-app/app-pr-1035
  MERGED    #1019   Migrate reconciler to workqueue pattern     ~/git/acme/repo-app/app-pr-1019
'zen review resume <number>' to open  |  'zen inbox' for new PRs

Feature Work
───────────────────────────────────────────────────────────────
  Name                                        Age    Path
  ──────────────────────────────────────────  ─────  ──────────────────────────────
  app-add-oidc-claims                         11d    ~/git/acme/repo-app/app-add-oidc-claims
  app-platform-workshop                       7d     ~/git/acme/repo-app/app-platform-workshop
  app-fix-model-schema                        7d     ~/git/acme/repo-app/app-fix-model-schema
  app-reduce-toolcall-duplication             2d     ~/git/acme/repo-app/app-reduce-toolcall-duplication
  app-skills-refactoring                      0d     ~/git/acme/repo-app/app-skills-refactoring
  ... and 3 more
'zen work resume <name>' to continue  |  'zen work new <repo> <branch>' to start

Watch Daemon
───────────────────────────────────────────────────────────────
  Status: Running (PID: 94998)
'zen watch start/stop' to control  |  'zen watch logs' for logs
```

### Search

```
zen search 42                    # By PR number
zen search oidc                  # By branch/name
zen search --type pr <term>      # Filter: pr, feature
```

Searches across worktrees. Shows active Claude session indicator.

### Agent Sessions

```
zen agent status                 # Claude sessions across all worktrees
zen agent status --running       # Only running sessions
zen agent status --full          # Full token usage scan (slower)
```

Shows session ID, model, token usage, and last activity for each worktree.

### Cleanup

```
zen cleanup                      # Find stale worktrees
zen cleanup --days 14            # Custom age threshold
zen cleanup --delete             # Interactive deletion
```

Finds worktrees for merged/closed PRs or inactive branches. The watch daemon handles merged PR cleanup automatically (5+ days after merge), but this command is useful for manual cleanup and inactive feature branches.

## Context Injection

The daemon writes a `CLAUDE.local.md` file into each PR worktree with the PR title, author, changed files, and review instructions. This keeps the repo's own `CLAUDE.md` untouched so there's no risk of accidental commits. To refresh it manually:

```
zen context inject <path> --pr 42 --repo app
```

## MCP Server

```
zen mcp serve
```

Starts a Model Context Protocol server on stdio, exposing zen tools for Claude sessions to call directly:
- `zen_inbox` — fetch pending PR reviews
- `zen_worktree_list` — list worktrees
- `zen_pr_details` / `zen_pr_files` — PR metadata
- `zen_agent_status` — session info
- `zen_config_repos` — configured repositories

To register with Claude Code:

```
claude mcp add --scope user zen -- zen mcp serve
```

This lets Claude call zen tools directly during sessions (e.g. list worktrees, check inbox, fetch PR details).

### Other Commands

```
zen version                      # Show version and commit SHA
zen setup                        # Interactive first-time setup
```

### Global Flags

```
--json      JSON output (all commands)
--debug     Debug logging
```

## Configuration

Config file: `~/.zen/config.yaml`

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

watch:
  dispatch_interval: "10s"      # How often to process queued work
  cleanup_interval: "1h"        # How often to scan for merged PRs
  cleanup_after_days: 5          # Days after merge before removing worktree
  concurrency: 2                 # Parallel worktree setups
  max_retries: 5                 # Max retry attempts for git failures
```

Each repo key (e.g. `app`) is a short name you choose — it doesn't have to match the GitHub repo name. It's used for worktree naming (`app-pr-42`), queue keys (`app:42`), and display. The `full_name` is the actual `owner/repo` used for GitHub API calls. If two orgs have a repo with the same name, just pick different keys:

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

The daemon re-reads `config.yaml` on every poll tick. Changes to `poll_interval`, `authors`, `repos`, and other settings take effect without restarting.

### State Files

All state lives in `~/.zen/state/`:

| File | Purpose |
|------|---------|
| `watch.pid` | Daemon PID |
| `watch.log` | Daemon logs |
| `last_check.json` | Timestamp of last GitHub poll |
| `pr_cache.json` | PR titles/authors for display |

## Design

### Daemon Architecture

Under the hood, the daemon uses [driftlessaf](https://github.com/driftlessaf) workqueues with two reconcilers — one for setup, one for cleanup:

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

### Source of Truth

**Git worktrees are the single source of truth.** All inventory — which PRs have local worktrees, which feature branches exist, worktree paths and types — is derived from `git worktree list` via `worktree.ListAll()`. There is no external database or registry to drift out of sync.

PR metadata (titles, authors) is cached in a lightweight JSON file (`~/.zen/state/pr_cache.json`) written by the daemon during setup. This cache is purely for display — if it's missing or stale, commands still work (they just show PR numbers instead of titles).

### Worktree Naming

| Type | Pattern | Example |
|------|---------|---------|
| PR review | `<repo>-pr-<number>` | `app-pr-42` |
| Feature | `<repo>-<branch>` | `app-add-oidc-claims` |

### Source Tree

```
zen
├── cmd/                          # CLI commands (cobra)
├── commands/                     # Claude Code commands (embedded in binary)
├── internal/
│   ├── config/                   # YAML config (~/.zen/config.yaml)
│   ├── context/                  # CLAUDE.md generation for PR reviews
│   ├── github/                   # GitHub API (GraphQL + REST)
│   ├── iterm/                    # iTerm2 tab management via AppleScript
│   ├── mcp/                      # MCP server exposing zen tools
│   ├── notify/                   # macOS notifications
│   ├── prcache/                  # Lightweight PR metadata cache (JSON)
│   ├── reconciler/               # Workqueue-based PR setup + cleanup
│   ├── session/                  # Claude session detection
│   ├── ui/                       # Terminal formatting
│   └── worktree/                 # Git worktree discovery + management
├── main.go
└── go.mod
```

## Getting Started

After building, run the interactive setup to create your config:

```
zen setup
```

This walks you through configuring your repositories, GitHub usernames for PR filtering, and watch daemon settings. The config is written to `~/.zen/config.yaml`.

## Prerequisites

| Requirement | Why |
|-------------|-----|
| **macOS** | iTerm2 tab management and notifications use AppleScript |
| **Git** | Worktree creation, fetching PR branches, cleanup |
| **[GitHub CLI](https://cli.github.com/) (`gh`)** | Authentication and GitHub API access — must be logged in (`gh auth login`) |
| **[iTerm2](https://iterm2.com/)** | Opens review/work sessions in new tabs (use `--no-iterm` to skip) |
| **[Claude Code](https://docs.anthropic.com/en/docs/claude-code) (`claude`)** | AI-assisted PR reviews and coding sessions |
| **Go 1.24+** | Building from source |

## Building

```
make build                       # Includes version + commit SHA via ldflags
```

Or manually:

```
go build -o zen .
```

Check your build:

```
zen version                      # Shows version and commit SHA
```

## Testing

```
make test
```

## Why "zen"?

I was watching *The Last Dance* when naming this tool. Phil Jackson — the "Zen Master" — and his coaching philosophy resonated: orchestrate the system, trust the players, stay calm while everything moves around you. That's what this tool does: silently prepares worktrees, injects context, cleans up after itself, and lets you focus on the actual review when you're ready.
