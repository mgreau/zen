# Session migration between agents

How zen carries a coding session from Claude Code to Codex and back. Read this if you're debugging a migration, curious why the two directions look nothing alike, or updating the code after an agent release changed a format.

The user-facing behaviour is one prompt: when `zen review resume` / `zen work resume` finds no session for the selected agent but the *other* agent has one for the same worktree, zen offers to migrate the most recent session and resume it. `--migrate` skips the prompt. Everything below is what happens after "Y".

All format details on this page were verified against **Claude Code 2.1.200** and **Codex CLI 0.142.0**. Both session stores are undocumented internals of their CLIs — treat every claim here as pinned to those versions.

## The core asymmetry

The two directions are implemented completely differently, on purpose:

| | Claude → Codex | Codex → Claude |
|--|----------------|----------------|
| Converter | **Codex's own importer**, driven over `codex app-server` JSON-RPC | **zen's translator** (`internal/migrate`) |
| Who writes the target files | Codex | zen |
| Format knowledge zen maintains | none (only the RPC protocol) | full Claude session record shape |

Codex ships a first-class importer for Claude Code sessions (the `/import` TUI command); imported sessions become native, resumable Codex threads, and Codex tracks them in a content-addressed ledger. zen drives that machinery instead of reimplementing it — zen never writes into `~/.codex`.

Claude Code has no import machinery at all. But its resume path will happily load a session file it didn't write, as long as the file lands in the right directory with the right record shape — so zen synthesizes one.

## Trigger flow

`maybeMigrateSession` (`cmd/resume.go`) runs only when the selected agent's `FindSessions(worktree)` is empty. It builds the *other* agent via `cfg.NewAgent`, takes its newest session for the same worktree path, prompts (unless `--migrate`), and dispatches by direction. After migration it re-runs `FindSessions` so the normal resume flow gets real file metadata, then resumes with the target agent's `ResumeCommand` — the migrated session is opened exactly like a native one.

The worktree path is the join key in both directions. Claude keys sessions by an encoding of the cwd; Codex records the cwd inside each rollout. No new zen state is introduced.

## Direction 1: Claude → Codex

Implemented in `internal/migrate/codex_import.go` as two tiers.

### Tier 1: the import ledger

Codex records every imported session in `~/.codex/external_agent_session_imports.json`:

```json
{"records": [{
  "source_path": "/Users/me/.claude/projects/-Users-me-git-app-pr-42/9ff7….jsonl",
  "content_sha256": "c4920af0…",
  "imported_thread_id": "019ef4ee-19f9-7be2-95a1-4e7d5d39599c",
  "imported_at": 1782225706,
  "source_modified_at": 1782223315974000000
}]}
```

`lookupImportedThread` hashes the Claude session file and matches on `content_sha256` — **not** on `source_path`. Two consequences, both intentional: a session re-discovered under a moved path still matches, and a session that grew since its last import (the user kept working in Claude) does *not* match, so it re-imports and Codex sees the newer turns. On a hit, zen skips straight to `codex resume <imported_thread_id>`.

zen reads this ledger but never writes it; it belongs to Codex.

### Tier 2: driving the importer over app-server

On a ledger miss, `runCodexImport` spawns `codex app-server` and speaks newline-delimited JSON-RPC over stdio (`appServerClient`):

```
→ {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"zen","version":"1.0"}}}
→ {"jsonrpc":"2.0","method":"initialized"}
→ {"jsonrpc":"2.0","id":2,"method":"externalAgentConfig/detect","params":{"cwds":[],"includeHome":true}}
← items: [... {"itemType":"SESSIONS","details":{"sessions":[{"cwd":…,"path":…,"title":…}, …]}} ...]
→ {"jsonrpc":"2.0","id":3,"method":"externalAgentConfig/import","params":{"migrationItems":[<SESSIONS item narrowed to one session>]}}
← {"importId":"…"}
← notification externalAgentConfig/import/progress   (ignored)
← notification externalAgentConfig/import/completed  {"importId":…, "itemTypeResults":[…]}
```

Two protocol details cost real debugging time; don't rediscover them:

- **SESSIONS is a home-scoped item.** `detect` with `includeHome: false` and a `cwds` list returns *zero* sessions — repo-scoped detection covers AGENTS.md/config/skills, not chats. zen requests `includeHome: true` and filters the returned list down to the one session by exact (symlink-resolved) file path (`singleSessionItem`).
- **Detection is bounded.** Codex enumerates roughly the 50 most recent Claude sessions from the last 30 days, and skips sessions whose recorded cwd no longer exists on disk. Older sessions are simply not importable through this API; zen surfaces that in the error message.

After the `completed` notification reports a SESSIONS success, zen re-reads the ledger to get the new `imported_thread_id`. The schemas for these methods are dumpable with `codex app-server generate-json-schema --out <dir>` — the source of truth when a Codex update changes the protocol.

### What Codex produces

The importer writes a real rollout file under `~/.codex/sessions/YYYY/MM/DD/` (with `originator` set from the RPC client name — zen's imports are attributable) and registers the thread in Codex's state DB. Message text is carried over verbatim; Claude tool calls are flattened into `[external_agent_tool_call: …]` text blocks rather than native function calls, and thinking blocks are dropped entirely. The thread ends with an `<EXTERNAL SESSION IMPORTED>` sentinel and an *estimated* token count so Codex can auto-compact before the first real turn.

One resume caveat: the `codex resume` picker filters by cwd, and zen always resumes by explicit thread id (`codex resume <uuid>`), which works from anywhere.

## Direction 2: Codex → Claude

Implemented as a two-stage pipeline: `codex_read.go` parses the rollout into a neutral transcript, `claude_write.go` renders that transcript as a Claude session file. The neutral IR (`transcript.go`) exists so neither side needs to know the other's format.

### Reading the rollout

A rollout line is `{"timestamp","type","payload"}`. What each type becomes:

| Rollout record | Handling |
|---|---|
| `session_meta` | Source thread id, cwd, git branch → transcript metadata |
| `response_item` / `message`, role `user` or `assistant` | Text message — unless it's Codex-injected context (see below) |
| `response_item` / `message`, role `developer`/`system` | Dropped (Codex runtime scaffolding) |
| `response_item` / `function_call` | Tool call (name + parsed JSON arguments + `call_id`) |
| `response_item` / `function_call_output` | Attached to its call by `call_id` |
| `response_item` / `reasoning` | Dropped — the content is encrypted, only OpenAI can read it |
| `event_msg`, `turn_context` | Dropped — UI replay and per-turn config, duplicates of the above |

Codex injects non-conversation content as user-role messages: sandbox rules (`<permissions instructions>`), AGENTS.md content (`# AGENTS.md instructions` on 0.142), environment context. `codexInjectedPrefixes` filters these; when a Codex update changes the wrappers, this list is the thing to extend.

The reader guarantees an invariant the writer depends on: **every tool call is immediately followed by its result** in the transcript. Outputs are collected by `call_id` in a first pass; a call whose output was never recorded gets a synthesized placeholder. This is what keeps the generated Claude history free of dangling `tool_use` blocks, which the Anthropic API rejects.

### Writing the Claude session

Claude Code discovers sessions at `~/.claude/projects/<munged-cwd>/<session-id>.jsonl`, where the munge is: take the **symlink-resolved** worktree path and replace every non-alphanumeric character with `-`. Both halves of that rule matter — on macOS, `/tmp/foo` must become `-private-tmp-foo`, and `my_branch` becomes `my-branch`. There is no fallback scan: a file in the wrong directory yields "No conversation found with session ID". (Fixing zen's `pathToClaudeProject` to match this rule was a side effect of this feature; the old `/`-and-`.`-only replacement also made zen blind to existing sessions for worktrees with `_` in the path.)

Each line is one record. The shape zen writes, per record:

```json
{
  "parentUuid": "<previous record's uuid, null on the first>",
  "isSidechain": false,
  "userType": "external",
  "cwd": "/resolved/worktree/path",
  "sessionId": "<the filename stem>",
  "version": "2.1.200",
  "gitBranch": "<from the rollout>",
  "type": "user" | "assistant",
  "message": { … },
  "uuid": "<fresh uuidv4>",
  "timestamp": "<carried from the rollout record>"
}
```

Records form a linked list via `parentUuid`; `sessionId` must equal the filename stem on every line.

**The one load-bearing field that is not obvious: assistant messages must carry `model`.** Claude Code reads the session's model from the transcript when resuming; if no assistant message has one, `claude --resume` hangs indefinitely — no error, no output. This was found by bisection: a file identical to a working one except for the missing `model` hangs; adding `model` alone unblocks it. `assistantMessage` therefore stamps every assistant record with `claudeMigrationModel` (plus `id`/`type`/`stop_reason` to mirror the native shape). The value only serves as the resumed session's default-model hint.

Content mapping:

| Transcript message | Claude record |
|---|---|
| User text | `{"type":"user","message":{"role":"user","content":"…"}}` |
| Assistant text | assistant record with a `text` content block |
| Tool call, shell family (`exec_command`, `shell`, `local_shell`) | assistant record with a native `tool_use` block, `name: "Bash"`, input `{"command": …}` |
| Tool call, anything else | assistant *text* block wrapped in `[migrated_tool_call: <name>]…[/migrated_tool_call]` — Claude has no equivalent tool, and an unknown tool name in native history is more confusing than a marked textual trace (this mirrors the flattening Codex's own importer does in the other direction) |
| Tool result | user record with a `tool_result` block paired by `tool_use_id` (native calls) or a `[migrated_tool_result]` text (flattened calls); individual outputs truncated at 32 KB |

The file opens with a preamble user message naming the source Codex thread and warning that tool calls were translated — the receiving model reads a conversation shaped by another agent's system prompt and tools, and the preamble measurably reduces its confusion about that history.

### Size cap

Claude's resume fast path discards everything before the last compact boundary once a session file exceeds 5,242,880 bytes. zen caps synthesized files at 4 MiB (`capBodies`): the newest records win, the preamble always survives, a truncation note marks the cut, and a `tool_use`/`tool_result` pair is never split (a leading orphan result is dropped). Capping happens on the pre-envelope record bodies, so the `parentUuid` chain is built over exactly what gets written.

## What survives, what doesn't

| | Claude → Codex | Codex → Claude |
|--|---|---|
| Message text | verbatim | verbatim |
| Tool calls | flattened to marked text | shell → native `Bash` history; others → marked text |
| Reasoning / thinking | dropped (Claude thinking blocks carry signatures that can't be replayed) | dropped (encrypted, unreadable) |
| Sidechains / subagent transcripts, images, file-history snapshots | dropped | n/a (not present in rollouts) |
| Session identity | new Codex thread id (ledger maps back to the source) | new Claude session id (preamble names the source thread) |

Migration is a context hand-off, not a bit-perfect transcript. The receiving agent occasionally comments on the foreign shape of its own history — expected, and harmless with the preamble in place.

## Failure modes

- **Codex can't detect the session** (too old, beyond the recency cap, cwd deleted): the Claude→Codex error says so; the fallback is starting fresh (decline the prompt) or resuming in Claude and letting the newer file re-import later.
- **Codex import reports failures**: surfaced verbatim from the `completed` notification.
- **app-server never responds**: the whole import runs under a 2-minute context; killing the process unblocks the reader.
- **Unknown record types** in either format are skipped, not fatal — a format drift degrades fidelity before it breaks migration.
- **Resume mutates the file**: resuming a migrated Claude session appends to it in place, which also changes its sha256 — so a later Claude→Codex migration of that session correctly re-imports rather than reusing a stale thread.

## Verifying after an agent update

Unit tests cover the translator and a scripted fake app-server (`internal/migrate/*_test.go`), but the real formats can only be validated against the real CLIs. The recipe that was used to validate this feature end-to-end, reusable as a manual check:

1. Pick a real rollout with `function_call` records from `~/.codex/sessions`, run it through `migrate.CodexToClaude` targeting a scratch dir, and confirm `claude -p --resume <id> "…"` (run from the scratch dir) answers with awareness of the history. A hang here most likely means a new required field — bisect against a session file Claude itself wrote.
2. Point `CODEX_HOME` at an empty dir and run `migrate.ClaudeToCodex` on the file from step 1; confirm a rollout appears under `$CODEX_HOME/sessions` and a second call returns the same thread id via the ledger.
3. `codex app-server generate-json-schema` diffs cleanly against the shapes in `codex_import.go`.
