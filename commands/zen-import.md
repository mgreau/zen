---
description: Split a sub-task out of this session into its own zen work item (own worktree, branch and terminal), started from a scoped hand-off
argument-hint: "[branch] [repo] [what to split out]"
allowed-tools: Bash(zen work new:*), Read(~/.zen/config.yaml)
---

Split one focused piece of the work in this conversation into its own zen work item. zen creates a worktree and branch from origin/main and starts a new agent session there. That session gets a hand-off you write, not this conversation. This session keeps going with everything else. Arguments from the user: "$ARGUMENTS"

1. **What to split out.** Use the arguments if they describe the sub-task; otherwise take it from the conversation: the workstream the user just asked to hand off. If more than one piece of work could be meant, ask which one.
2. **Repo.** zen takes the repo the new work changes, which need not be the directory this session runs in. Use a repo named in the arguments. Otherwise work it out from the sub-task (which code it changes) and, failing that, from this session's directory. The configured repos are the `repos` in `~/.zen/config.yaml`. If none clearly fits, ask; never pick one silently.
3. **Branch.** Use the branch from the arguments if there is one. Otherwise choose a short kebab-case name (one path segment: no `/`, no spaces) for the sub-task. Do not add a user prefix; zen applies the configured `branch_prefix`.
4. **Write the hand-off** to `~/.zen/handoffs/<repo>-<branch>.md`. The new session has none of this conversation, so the hand-off must stand on its own, and it covers the sub-task only. Leave out the rest of this session's work unless the sub-task depends on it. Use this shape, dropping sections that would be empty:
   - `# Handoff: <sub-task> (as of <date and time>)`, then one paragraph: who it's for, what the goal is, and that **Confirmed** means seen in a file, command output or source, and **Inferred** means reasoned but not checked.
   - **Where things live:** repos, paths, docs, tickets, branches and PRs, with links or exact paths.
   - **What's known:** confirmed facts and decisions already made with the user, then inferences, each labelled.
   - **What's left, in order:** concrete next steps, with commands where you know them.
   - **Ground rules:** what the user asked for or ruled out, what still needs their approval, and what belongs to this session or others, which the new session must leave alone.
   The worktree starts from origin/main. If the sub-task depends on uncommitted or unpushed work here, say so in the hand-off and tell the user. Never put secrets or tokens in the hand-off.
5. **Show it before launching.** Give the user the repo, the branch, the hand-off's path and the hand-off itself, and ask whether to go ahead. Make any changes they ask for. Do not run zen until they agree.
6. Run:

   ```
   zen work new --context-file ~/.zen/handoffs/<repo>-<branch>.md <repo> <branch>
   ```

   If zen says the repo is unknown, show the configured repos from its message and ask which one to use. If the worktree already exists, ask for another branch name, and rename the hand-off file to match.
7. When it succeeds, tell the user, in two or three lines, the worktree path, the branch, and that the new session opened in a new terminal tab or window. A Claude session in a new folder may first ask whether to trust it; that prompt is the user's to answer. Then carry on with this session's work. Leave the split-out sub-task to the new session unless the user asks otherwise.
