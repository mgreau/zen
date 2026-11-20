package worktree

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocalChanges summarizes the uncommitted work in a checkout.
type LocalChanges struct {
	// Tracked is true when tracked files differ from HEAD (staged or not).
	Tracked bool
	// Untracked lists untracked, non-ignored files relative to the top level.
	Untracked []string
}

// Empty reports whether there is nothing to carry.
func (c LocalChanges) Empty() bool { return !c.Tracked && len(c.Untracked) == 0 }

// TopLevel returns the root of the checkout containing dir.
func TopLevel(dir string) (string, error) {
	out, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// CommonDir returns the absolute, symlink-resolved git common directory for
// the checkout containing dir. Every worktree of a repository shares it, so it
// identifies the repository regardless of which checkout dir is in.
func CommonDir(dir string) (string, error) {
	out, err := gitOutput(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(string(out))
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return p, nil
}

// FindLocalChanges reports the uncommitted work in the checkout rooted at top.
func FindLocalChanges(top string) (LocalChanges, error) {
	var c LocalChanges
	diff := exec.Command("git", "diff", "--quiet", "--ignore-submodules", "HEAD", "--")
	diff.Dir = top
	diff.Env = gitEnv()
	switch err := diff.Run(); {
	case err == nil:
	case isExit(err, 1):
		c.Tracked = true
	default:
		return c, fmt.Errorf("git diff HEAD in %s: %w", top, err)
	}

	out, err := gitOutput(top, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return c, err
	}
	c.Untracked = splitNUL(out)
	return c, nil
}

// CommitsNotIn counts the commits reachable from dir's HEAD but not from
// ref: committed work that a worktree cut from ref would not have.
func CommitsNotIn(dir, ref string) (int, error) {
	out, err := gitOutput(dir, "rev-list", "--count", ref+"..HEAD")
	if err != nil {
		return 0, err
	}
	var n int
	_, err = fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
	return n, err
}

// CarryResult reports what CarryChanges did.
type CarryResult struct {
	// Patched is true when tracked-file changes were applied.
	Patched bool
	// Conflicts lists files the 3-way apply left with conflict markers.
	// ConflictOutput holds git's report.
	Conflicts      []string
	ConflictOutput string
	// Copied and Skipped list untracked files, relative to the top level.
	// A file is skipped when the destination already has a file there, or
	// when it is not a regular file or symlink (such as a nested repo).
	Copied  []string
	Skipped []string
	// Failed lists untracked files whose copy hit an error (permissions,
	// I/O); the errors are returned alongside the result.
	Failed []string
	// NotApplied lists tracked files whose changes could not be applied at
	// all, typically because the file only exists in commits the new branch
	// does not have. NotAppliedOutput holds git's report.
	NotApplied       []string
	NotAppliedOutput string
}

// CarryChanges copies the uncommitted work of the checkout rooted at srcTop
// into the worktree at dst: tracked changes as a patch against HEAD,
// untracked files as copies. Both checkouts must belong to the same
// repository, so the patch's blobs resolve for a 3-way merge when the
// branches have diverged. srcTop is only read, never modified. Submodules
// are not carried.
//
// Applied changes are left unstaged. On a conflict the merge result, markers
// included, is left in place for the user to resolve and CarryResult reports
// it; that is not an error. When the patch fails outright, untracked files
// are still copied and the error is returned alongside the result.
func CarryChanges(srcTop, dst string) (CarryResult, error) {
	var res CarryResult
	srcRepo, err := CommonDir(srcTop)
	if err != nil {
		return res, err
	}
	dstRepo, err := CommonDir(dst)
	if err != nil {
		return res, err
	}
	if srcRepo != dstRepo {
		return res, fmt.Errorf("cannot carry changes: %s and %s are different repositories", srcTop, dst)
	}

	changes, err := FindLocalChanges(srcTop)
	if err != nil {
		return res, err
	}

	var patchErr error
	var copyErrs []error
	if changes.Tracked {
		patchErr = applyTracked(srcTop, dst, &res)
	}

	for _, rel := range changes.Untracked {
		target := filepath.Join(dst, rel)
		if _, err := os.Lstat(target); err == nil {
			res.Skipped = append(res.Skipped, rel)
			continue
		}
		// A parent that is a symlink (tracked on the new branch) could lead
		// the copy outside the worktree; skip anything that would.
		if !insideDir(dst, filepath.Dir(target)) {
			res.Skipped = append(res.Skipped, rel)
			continue
		}
		copied, err := copyPreservingMode(filepath.Join(srcTop, rel), target, srcTop, dst)
		if err != nil {
			// Keep going: one uncopyable file must not strand the rest.
			copyErrs = append(copyErrs, fmt.Errorf("copying %s: %w", rel, err))
			res.Failed = append(res.Failed, rel)
			continue
		}
		if copied {
			res.Copied = append(res.Copied, rel)
		} else {
			res.Skipped = append(res.Skipped, rel)
		}
	}
	return res, errors.Join(append([]error{patchErr}, copyErrs...)...)
}

// carryDiffArgs pin the patch format: user config (diff.noprefix,
// diff.external, color.diff=always, textconv drivers, rename detection) must
// not change what git apply reads, and with renames off every file's change
// stands alone, so files can be applied one at a time.
var carryDiffArgs = []string{"diff", "--no-ext-diff", "--no-color", "--no-textconv", "--no-renames",
	"--src-prefix=a/", "--dst-prefix=b/", "--ignore-submodules", "--binary", "HEAD", "--"}

// applyTracked applies srcTop's tracked changes against HEAD to dst.
func applyTracked(srcTop, dst string, res *CarryResult) error {
	patch, err := gitOutput(srcTop, carryDiffArgs...)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(patch)) == 0 {
		return nil
	}
	out, aerr := applyPatch(dst, patch)
	if aerr != nil && !isExit(aerr, 1) {
		return fmt.Errorf("git apply: %w: %s", aerr, strings.TrimSpace(out))
	}
	// Exit status 1 is either a conflicted 3-way merge or a refused patch;
	// the index tells them apart without parsing git's localized messages.
	unmerged, err := unmergedPaths(dst)
	if err != nil {
		return err
	}
	report := out
	res.Patched = true
	if aerr != nil && len(unmerged) == 0 {
		// git apply checks every file before writing any, so a patch it
		// refused left dst untouched. Apply file by file instead, so one
		// file that cannot apply does not hold back all the others.
		files, err := gitOutput(srcTop, "diff", "--name-only", "-z", "--no-renames", "--ignore-submodules", "HEAD", "--")
		if err != nil {
			return err
		}
		var failed []string
		var reports []string
		applied := 0
		for _, f := range splitNUL(files) {
			one, err := gitOutput(srcTop, append(carryDiffArgs, f)...)
			if err != nil {
				return err
			}
			if len(bytes.TrimSpace(one)) == 0 {
				continue
			}
			o, err := applyPatch(dst, one)
			if err != nil && !isExit(err, 1) {
				return fmt.Errorf("git apply %s: %w: %s", f, err, strings.TrimSpace(o))
			}
			if err != nil {
				failed = append(failed, f)
				reports = append(reports, strings.TrimSpace(o))
				continue
			}
			applied++
		}
		if unmerged, err = unmergedPaths(dst); err != nil {
			return err
		}
		conflicted := map[string]bool{}
		for _, u := range unmerged {
			conflicted[u] = true
		}
		for i, f := range failed {
			if !conflicted[f] {
				res.NotApplied = append(res.NotApplied, f)
				res.NotAppliedOutput += reports[i] + "\n"
			}
		}
		res.NotAppliedOutput = strings.TrimSpace(res.NotAppliedOutput)
		report = strings.Join(reports, "\n")
		res.Patched = applied > 0 || len(unmerged) > 0
	}
	res.Conflicts = unmerged
	if len(res.Conflicts) > 0 {
		res.ConflictOutput = strings.TrimSpace(report)
	}

	// --3way stages what it applies; leave the changes unstaged, as they
	// are in an ordinary edit. New files stay intent-to-add (-N) so that
	// commit -a still picks them up. Conflicted paths keep their unmerged
	// entries so git still reports them as needing resolution. Paths go on
	// stdin: a large carry can exceed the argument-length limit.
	staged, err := gitOutput(dst, "diff", "--cached", "--name-only", "--diff-filter=ACDMRT", "--no-renames", "-z")
	if err != nil {
		return err
	}
	if len(staged) > 0 {
		reset := exec.Command("git", "reset", "-q", "-N", "--pathspec-from-file=-", "--pathspec-file-nul")
		reset.Dir = dst
		// Paths are names, not patterns: a staged "a*.md" must not also
		// reset (and so silently resolve) a conflicted "abc.md".
		reset.Env = gitEnv()
		reset.Stdin = bytes.NewReader(staged)
		if out, err := reset.CombinedOutput(); err != nil {
			return fmt.Errorf("git reset in %s: %w: %s", dst, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func applyPatch(dst string, patch []byte) (string, error) {
	apply := exec.Command("git", "apply", "--3way", "--whitespace=nowarn", "-")
	apply.Dir = dst
	apply.Env = gitEnv()
	apply.Stdin = bytes.NewReader(patch)
	out, err := apply.CombinedOutput()
	return string(out), err
}

func unmergedPaths(dir string) ([]string, error) {
	out, err := gitOutput(dir, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	return splitNUL(out), nil
}

// resolveExisting resolves symlinks in the longest existing prefix of p.
func resolveExisting(p string) string {
	rest := ""
	for dir := filepath.Clean(p); ; {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}

// insideDir reports whether dir, resolved through its nearest existing
// ancestor, lies within root.
func insideDir(root, dir string) bool {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	existing, rest := dir, ""
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return false
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
	real, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootReal, filepath.Join(real, rest))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func splitNUL(b []byte) []string {
	var out []string
	for _, p := range strings.Split(string(b), "\x00") {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// copyPreservingMode copies a regular file or symlink, creating parent
// directories as needed and never overwriting an existing file. It reports
// false, copying nothing, for anything else (such as a nested repository,
// which git ls-files lists as "dir/"). An absolute symlink into fromTop is
// retargeted to the same place under toTop, so the copy does not reach back
// into the old checkout.
func copyPreservingMode(src, dst, fromTop, toTop string) (bool, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return false, err
	}
	isLink := info.Mode()&os.ModeSymlink != 0
	if !isLink && !info.Mode().IsRegular() {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	if isLink {
		link, err := os.Readlink(src)
		if err != nil {
			return false, err
		}
		if filepath.IsAbs(link) {
			// fromTop comes from git, symlink-resolved; the link may name
			// the same place unresolved (/var vs /private/var).
			for _, target := range []string{link, resolveExisting(link)} {
				if rel, err := filepath.Rel(fromTop, target); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					link = filepath.Join(toTop, rel)
					break
				}
			}
		}
		return true, os.Symlink(link, dst)
	}
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return false, err
	}
	return true, out.Close()
}

// gitEnv is the environment for the git commands here. Optional locks are
// off so read-only commands do not refresh the index of the checkout they
// read: the source checkout may belong to a live session running git itself,
// and a refresh would contend for its index.lock. Pathspecs are literal:
// every path passed is a file name, never a pattern.
func gitEnv() []string {
	return append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_LITERAL_PATHSPECS=1")
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func isExit(err error, code int) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee) && ee.ExitCode() == code
}
