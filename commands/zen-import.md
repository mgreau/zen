---
description: Turn this session into a zen work item (own worktree, branch and terminal tab)
argument-hint: "[branch] [repo] [--carry-changes]"
allowed-tools: Bash(zen work import:*), Bash(git status:*)
---

Move this conversation into its own zen feature worktree, where it will be resumed in a new terminal tab. Arguments from the user: "$ARGUMENTS"

1. **Branch.** Arguments come as `[branch] [repo]`: the first word that is not a flag is the branch, a second one is the repo. Use that branch name if there is one. Otherwise choose a short kebab-case name (one path segment: no `/`, no spaces) describing the work this conversation is heading toward. Do not add a user prefix; zen applies the configured `branch_prefix`.
2. **Repo.** Use the repo from the arguments if there is one (zen takes it before the branch). Otherwise leave it out: zen infers it from the directory this session runs in.
3. **Uncommitted changes.** Pass `--carry-changes` if the arguments include it. If they don't, run `git status --porcelain`; when it shows changes, ask the user whether to copy them into the new worktree before going on. The current checkout is never modified either way.
4. Run:

   ```
   zen work import --yes --session ${CLAUDE_SESSION_ID} [--carry-changes] [<repo>] <branch>
   ```

   If zen cannot infer the repo, show its message (it lists the configured repos) and ask which one to use.
5. When it succeeds, tell the user, in one or two lines, the worktree path and branch, and that the conversation continues in the new tab, so they can close this one. Do not keep working on the task here.
