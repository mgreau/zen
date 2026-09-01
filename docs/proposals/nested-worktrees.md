# In-repo worktrees

**Status:** implemented in this PR, opt-in behind `worktree_layout`, default
unchanged (`sibling`). Flipping the default is a separate release. User-facing
description in [configuration.md](../configuration.md#worktree-layout); the
internals are in [architecture.md](../architecture.md#worktree-placement).

## What

Let zen create worktrees inside the repository, at
`<clone>/_worktrees/<name>`, instead of beside it at `<base_path>/<name>`.
The directory is kept out of `git status` via the clone's
`.git/info/exclude`, not via a committed `.gitignore`.

Selected per repo, or globally, by a new config key:

```yaml
worktree_layout: nested        # "sibling" (default) | "nested"

repos:
  app:
    full_name: octo-sts/app
    base_path: ~/git/repo-octo-sts-app
    worktree_layout: sibling   # optional per-repo override
```

## Why

The sibling layout spreads one repo's worktrees across its parent directory.
A `~/src` holding eight repos and seven review worktrees interleaves them in
one alphabetical list with nothing but a name prefix to say which is which,
and a worktree whose branch has been deleted lingers there indefinitely with
no owner to notice it.

Nested placement makes ownership structural rather than lexical. The
worktrees for `app` are in `app/_worktrees/`, they are obviously zen's, and
removing the clone removes them with it. The parent directory goes back to
holding only repositories.

It also narrows `base_path`. Today that key means two things: where the
clone lives, and which directory zen is entitled to write sibling
directories into. Under `nested` it means only the first, so pointing zen at
a repo inside a directory you do not otherwise control stops being a
question.

## What this actually changes

zen derives worktree inventory from git, not from the filesystem layout:
`worktree.ListForRepo` shells out to `git worktree list`
(`internal/worktree/discovery.go:82`) and parses its output. Reads are
already layout-agnostic. Creation is centralised too —
`worktree.CreateFromMain` and `worktree.CreateFromPR`
(`internal/worktree/create.go`) are the only callers of `git worktree add`.

What is *not* centralised is path computation. Six call sites build a
worktree path by string concatenation and assume the sibling layout:

| Site | Purpose |
|------|---------|
| `cmd/review.go:90` | existence check before opening a review |
| `cmd/work.go:152` | feature worktree create |
| `internal/review/review.go:58` | PR review create / refresh |
| `internal/reconciler/setup.go:84` | daemon create |
| `internal/reconciler/cleanup.go:49` | daemon remove |
| `internal/reconciler/slack.go:155` | Slack thread worktree create |

`originPath := filepath.Join(basePath, repo)` is unaffected everywhere it
appears — the clone does not move, only the worktrees do.

### The failure this creates, and the seam that prevents it

Changing those six sites to build a nested path is not sufficient, and doing
only that is actively harmful. An existing sibling worktree stays registered
with git, so `git worktree list` keeps returning it and `zen status` keeps
showing it — while every writer now computes a path that does not exist.

Two concrete breakages follow:

- `internal/reconciler/cleanup.go:49` computes the nested path, `os.Stat`
  reports it missing, and the function returns `nil` on the "already
  removed" branch. The sibling worktree is never cleaned up and the queue
  reports success.
- `internal/reconciler/setup.go:84` computes the nested path, finds nothing,
  and calls `git worktree add` for branch `pr-42` — which git refuses,
  because `pr-42` is already checked out at the sibling. The key retries
  through the full backoff schedule and lands in the dead-letter state, on
  every poll, forever.

So the load-bearing part of this change is not the config key. It is a
single resolution seam:

```go
// Resolve returns the on-disk path for a worktree. An existing worktree is
// located through git, wherever it happens to live; the configured layout
// decides only where a new one will be created.
func Resolve(cfg *config.Config, repo, name string) string
```

`Resolve` consults the registered worktrees first and falls back to the
configured layout only when nothing is registered. Every one of the six
sites goes through it. Mixed layouts then work by construction, in both
directions, which is what makes the config key safe and the change
reversible.

Two details it has to get right. Registrations whose directory is gone
(git lists them as `prunable`) are skipped, or a stale entry from the old
layout would pin a new worktree to it. And paths are compared through
`filepath.EvalSymlinks`, because git reports fully resolved paths while zen
builds its own from `base_path` as the user wrote it — a symlink anywhere
above the repo would otherwise make `Resolve` fail to match a worktree that
is in fact the one it is looking for, or mistake the main worktree for a
linked one. When a registration names the same location as the configured
path, the configured spelling is what comes back: callers use this string as
an agent-session key, so returning git's spelling for an unchanged worktree
would orphan its history.

## Verified behaviour

Measured on git 2.54.0 and Go 1.25.13, because two of these cut against the
intuition that motivated the sibling layout in the first place.

**`git clean -xfd` does not destroy nested worktrees.** git treats a linked
worktree as a repository and skips it:

```
$ git clean -xfd --dry-run
Would skip repository _worktrees/feature
```

Removal requires `-xff` — the double force. The blast-radius argument
against nesting is therefore about `rm -rf` and `git clean -ffd`
specifically, not about the everyday clean.

**Go tooling ignores `_`-prefixed directories.** `go build`, `go vet`, and
`go list` skip directories beginning with `_` or `.`. A file containing
`this is not valid go at all !!!` placed in `_worktrees/copy/broken.go` does
not affect `go build ./...`; the same file in `worktrees/copy/broken.go`
fails the build. The underscore is load-bearing for a Go repository, not
decoration — the directory must be named `_worktrees`, and a future rename
to something without the prefix would break `./...` for every consumer.

**`git check-ignore` is the only correct exclusion test.** A negated pattern
in `.gitignore` (`!_worktrees`), or a `core.excludesFile` entry, both change
the answer in ways that reading `.gitignore` cannot detect. `git
check-ignore -q _worktrees/` asks git.

Recursion into nested worktrees remains a genuine cost for tools that do not
consult git's ignore rules — `find`, plain `grep`, file watchers such as
`fswatch` and `entr`, and Docker build contexts, which use `.dockerignore`
and would otherwise copy every worktree into the image. `rg` respects
`.gitignore` and is unaffected. Nix flake builds take only tracked files and
are unaffected.

## Keeping git quiet

An untracked `_worktrees/` shows up in `git status` in the main checkout.
Three mechanisms could suppress it, and the choice matters because zen
operates on repositories the user does not own.

`.gitignore` is wrong: it requires a commit to someone else's project.
A global `core.excludesFile` is wrong: it is the user's, not zen's, and
changing it reaches beyond the repos zen manages.

A **user-level ignore** is the best fit, and was underweighted in the first
draft of this proposal. `~/.config/git/ignore` — which git reads with no
`core.excludesFile` setting at all — covers every repo with one line, and
leaves zen writing into none of them. `_worktrees/` describes how the user
works rather than anything about a given project, so the scope of the
mechanism should match: user-level for `_worktrees/`, per-repo for `.zen/`
(which really is content injected into one specific worktree). Its costs are
real but small: it applies to every repo the user ever clones, and it does
not travel to other machines — irrelevant here, since worktrees are local
anyway. zen never writes it automatically; that file belongs to the user and
is shared with every tool they run.

`.git/info/exclude` is the right *fallback*. It is per-clone, needs no commit,
and zen already used it — `addToGitExclude` in `internal/agent/codex.go` added
`.zen/` there, with a policy comment stating that only zen-owned names
belong in a file shared by every worktree of the repo. `_worktrees/` is such
a name.

The approach is therefore to **prefer doing nothing, then fix, then warn**:
`EnsureExcluded` returns `AlreadyIgnored` without touching the repository
when `git check-ignore` already says yes — which is what a user-level ignore
produces everywhere — writes to `info/exclude` only otherwise, and warns
only when the path is still not ignored afterwards — an unwritable exclude file, or a
negation pattern overriding it — because that is the only case the user has
to resolve by hand.

That helper lives in a new leaf package, `internal/gitignore`, rather than
in `internal/worktree` as first sketched. `internal/agent` needs it too for
`.zen/`, and `internal/worktree` imports `internal/config`, which imports
`internal/agent` — putting it there would have made an import cycle. The
call site is `worktree.EnsureNestedExcluded`, which derives nesting by
comparing the worktree path to the clone path rather than by reading config,
so it stays correct whatever decided the layout, and runs *after* a
successful `git worktree add`: excluding a directory for a create that was
never going to succeed just produces a spurious warning.

One honest cost: writing `_worktrees/` to `info/exclude` in a repo you do
not own means a contributor's legitimate `_worktrees` directory would be
hidden from `git status` in that clone. The underscore prefix makes the
collision unlikely, and it is the same trade the existing `.zen/` exclusion
already accepts, but it is a real reach into another project's repository.

## Naming

Worktree directory names do not change. A PR review worktree in `app`
remains `app-pr-42`, at `<clone>/_worktrees/app-pr-42`.

The repo prefix is redundant once the worktree lives inside the repo, and
dropping it is tempting. It is deliberately out of scope: `Classify`'s
`-pr-(\d+)$` pattern (`internal/worktree/discovery.go:34`),
`ParseRepoFromName`, and `ParseBranchFromName` all key on that shape, as do
the Slack (`<repo>-slack-<slug>`) and feature (`<repo>-<branch>`) forms.
Layout and naming are separable changes and should not be debugged together.

## Migration: none

Existing sibling worktrees are not moved, and no `zen worktree migrate`
command is proposed.

Moving a worktree silently destroys its agent session. Claude keys session
files by an encoded worktree path — `~/.claude/projects/<encoded-path>/`,
via `pathToClaudeProject` (`internal/session/detect.go:84`) — so a `git
worktree move` orphans the history that `zen review resume` and `zen work
resume` depend on, with no error at the time it happens. Making migration
safe means relocating the encoded project directory in lockstep, and doing
it for Claude, Codex (which records `cwd` inside each rollout file), and
Aider (`.zen/aider.chat.history.md`) separately. That is a second feature,
not a step in this one.

Because `Resolve` locates existing worktrees through git, sibling worktrees
keep working indefinitely with `worktree_layout: nested` set. PR review
worktrees are removed automatically `cleanup_after_days` after merge, and
feature worktrees drain through `zen cleanup`. The old layout empties itself.

## Delivery

Shipped together in this PR, since the seam is most of it and the config key
is inert without it:

1. **The seam.** `worktree.Resolve` / `NewPath` / `OriginPath`
   (`internal/worktree/resolve.go`), with all six sites routed through it.
   The test that matters is `TestResolveFindsSiblingUnderNestedLayout`: with
   `worktree_layout: nested` configured and a sibling worktree on disk,
   `Resolve` still returns the sibling. Its mirror,
   `TestResolveFindsNestedUnderSiblingLayout`, covers rolling the setting
   back.
2. **Config and exclusion.** `Config.WorktreeLayout` plus the per-repo
   `RepoConfig.WorktreeLayout` override, both validated at load;
   `internal/gitignore` with `check-ignore` verification and the
   failure-only warning.
3. **One creation path.** `worktree.CreateFromPR` gained a context, a
   per-command timeout, and an optional logger so `internal/review` could
   stop duplicating the fetch/add/checkout sequence inline. That removed a
   third `EnsureNestedExcluded` call site: exclusion now happens in the two
   creators and nowhere else, so a future creation path cannot silently skip
   it. It also made the most-used creation path testable without a GitHub
   client, which it previously was not.
4. **Documentation.** Placement and a Slack row in
   [architecture.md](../architecture.md);
   [configuration.md](../configuration.md) documents the key and the
   narrowed meaning of `base_path`.

Still to come, deliberately separate:

5. **Default flip** to `nested`, after dogfooding on zen itself. A one-line
   change whose risk is entirely carried by step 1.

## Coverage

`make cover` reports coverage of the lines a branch adds and fails below 75%;
CI runs the same gate on every pull request. Whole-project coverage would let
a well-covered core hide an untested new feature, which is the case worth
catching. This change lands at 90.4% of its new statements.

The residue is deliberate: the single `Resolve` call line in each of
`cmd/review.go`, `cmd/work.go`, `internal/reconciler/setup.go`,
`internal/reconciler/slack.go`, and `internal/review/review.go`, plus the
write-error branches in `internal/gitignore` that need an unwritable
filesystem to reach. The `Resolve` lines are covered functionally instead, by
[zen-tests](https://github.com/imkarrer/zen-tests) `scripts/worktree-layout.sh`,
which drives the real binary through both layouts and the transition.

## Notes

- The layout is a per-repo property in config but not a property of the
  repository itself. Two machines can disagree about where a given repo's
  worktrees live, and that is fine — git holds the truth on each one.
- `worktree_layout` is deliberately an enum, not a path template. A template
  invites `../` escapes, cross-repo collisions when two repos resolve to the
  same directory, and a validation surface with no corresponding benefit.
- Nested paths are longer, which lengthens the encoded Claude project
  directory name. No limit is close: the encoding is flat and macOS allows
  255 bytes per path component.
