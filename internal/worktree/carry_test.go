package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// carryFixture is a repo with one commit and a linked worktree "dst" on a
// fresh branch from it.
func carryFixture(t *testing.T) (src, dst string) {
	t.Helper()
	base := t.TempDir()
	src = filepath.Join(base, "repo")
	initRepo(t, src)
	dst = filepath.Join(base, "repo-feature")
	git(t, src, "worktree", "add", "-b", "feature", dst)
	return src, dst
}

func TestCarryChanges(t *testing.T) {
	src, dst := carryFixture(t)
	writeFile(t, filepath.Join(src, "file.txt"), "A\nB\n")
	writeFile(t, filepath.Join(src, "staged.txt"), "staged\n")
	git(t, src, "add", "staged.txt")
	writeFile(t, filepath.Join(src, "notes", "new.md"), "untracked\n")
	writeFile(t, filepath.Join(src, ".gitignore"), "*.log\n")
	writeFile(t, filepath.Join(src, "debug.log"), "ignored\n")
	srcStatus := git(t, src, "status", "--porcelain")

	changes, err := FindLocalChanges(src)
	if err != nil {
		t.Fatal(err)
	}
	if !changes.Tracked || len(changes.Untracked) != 2 {
		t.Fatalf("FindLocalChanges = %+v, want tracked + 2 untracked (.gitignore, notes/new.md)", changes)
	}

	res, err := CarryChanges(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Patched || len(res.Conflicts) > 0 {
		t.Errorf("result = %+v, want clean patch", res)
	}
	if got := readFile(t, filepath.Join(dst, "file.txt")); got != "A\nB\n" {
		t.Errorf("file.txt = %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "staged.txt")); got != "staged\n" {
		t.Errorf("staged.txt = %q", got)
	}
	if got := readFile(t, filepath.Join(dst, "notes", "new.md")); got != "untracked\n" {
		t.Errorf("notes/new.md = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dst, "debug.log")); !os.IsNotExist(err) {
		t.Errorf("ignored debug.log was copied")
	}
	if staged := git(t, dst, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("carried changes left staged: %q", staged)
	}
	// staged.txt was a new file in the patch: it stays intent-to-add, so
	// commit -a still includes it.
	if added := git(t, dst, "diff", "--name-only", "--diff-filter=A"); added != "staged.txt" {
		t.Errorf("intent-to-add files = %q, want staged.txt", added)
	}
	if modified := git(t, dst, "diff", "--name-only", "--diff-filter=M"); modified != "file.txt" {
		t.Errorf("modified files = %q, want file.txt", modified)
	}
	if got := git(t, src, "status", "--porcelain"); got != srcStatus {
		t.Errorf("source checkout changed:\nbefore %q\nafter  %q", srcStatus, got)
	}
}

func TestCarryChangesSkipsExistingUntracked(t *testing.T) {
	src, dst := carryFixture(t)
	writeFile(t, filepath.Join(src, "new.txt"), "from source\n")
	writeFile(t, filepath.Join(dst, "new.txt"), "already here\n")

	res, err := CarryChanges(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if res.Patched || len(res.Copied) != 0 || len(res.Skipped) != 1 {
		t.Errorf("result = %+v, want new.txt skipped only", res)
	}
	if got := readFile(t, filepath.Join(dst, "new.txt")); got != "already here\n" {
		t.Errorf("existing file overwritten: %q", got)
	}
}

func TestCarryChangesConflict(t *testing.T) {
	src, dst := carryFixture(t)
	// The destination branch moved on in the same lines the source edits.
	writeFile(t, filepath.Join(dst, "file.txt"), "C\n")
	git(t, dst, "commit", "-am", "C")
	writeFile(t, filepath.Join(src, "file.txt"), "B\n")

	res, err := CarryChanges(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0] != "file.txt" {
		t.Fatalf("result = %+v, want file.txt conflicted", res)
	}
	if got := readFile(t, filepath.Join(dst, "file.txt")); !strings.Contains(got, "<<<<<<<") {
		t.Errorf("file.txt has no conflict markers: %q", got)
	}
}

func TestCarryChangesRejectsOtherRepo(t *testing.T) {
	src, _ := carryFixture(t)
	other := filepath.Join(t.TempDir(), "other")
	initRepo(t, other)
	writeFile(t, filepath.Join(src, "file.txt"), "B\n")

	if _, err := CarryChanges(src, other); err == nil || !strings.Contains(err.Error(), "different repositories") {
		t.Fatalf("err = %v, want different repositories", err)
	}
}

func TestCarryChangesIgnoresDiffConfig(t *testing.T) {
	src, dst := carryFixture(t)
	git(t, src, "config", "diff.noprefix", "true")
	git(t, src, "config", "color.diff", "always")
	git(t, src, "config", "diff.external", "false")
	writeFile(t, filepath.Join(src, "file.txt"), "A\nB\n")

	res, err := CarryChanges(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Patched {
		t.Fatalf("result = %+v, want patched", res)
	}
	if got := readFile(t, filepath.Join(dst, "file.txt")); got != "A\nB\n" {
		t.Errorf("file.txt = %q", got)
	}
}

func TestCarryChangesCopiesUntrackedWhenAFileIsRefused(t *testing.T) {
	src, dst := carryFixture(t)
	// The patch adds new.txt, which the destination already has on disk, so
	// git apply refuses it outright (no conflict to resolve).
	writeFile(t, filepath.Join(src, "new.txt"), "from source\n")
	git(t, src, "add", "new.txt")
	writeFile(t, filepath.Join(dst, "new.txt"), "in the way\n")
	writeFile(t, filepath.Join(src, "untracked.txt"), "keep me\n")

	res, err := CarryChanges(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.NotApplied) != 1 || res.NotApplied[0] != "new.txt" {
		t.Fatalf("NotApplied = %v, want [new.txt]", res.NotApplied)
	}
	if got := readFile(t, filepath.Join(dst, "untracked.txt")); got != "keep me\n" {
		t.Errorf("untracked.txt = %q, want copied despite the patch failure", got)
	}
	if got := readFile(t, filepath.Join(dst, "new.txt")); got != "in the way\n" {
		t.Errorf("existing new.txt overwritten: %q", got)
	}
}

func TestCarryChangesConflictUnstagesCleanFiles(t *testing.T) {
	src, dst := carryFixture(t)
	writeFile(t, filepath.Join(src, "clean.txt"), "1\n")
	git(t, src, "add", "clean.txt")
	git(t, src, "commit", "-m", "clean")
	git(t, dst, "merge", "-q", "--ff-only", "main")
	writeFile(t, filepath.Join(dst, "file.txt"), "C\n")
	git(t, dst, "commit", "-am", "C")
	writeFile(t, filepath.Join(src, "file.txt"), "B\n")
	writeFile(t, filepath.Join(src, "clean.txt"), "2\n")

	res, err := CarryChanges(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0] != "file.txt" {
		t.Fatalf("conflicts = %v, want [file.txt]", res.Conflicts)
	}
	if unmerged := git(t, dst, "diff", "--name-only", "--diff-filter=U"); unmerged != "file.txt" {
		t.Errorf("unmerged = %q, want file.txt", unmerged)
	}
	if staged := git(t, dst, "diff", "--cached", "--name-only", "--diff-filter=ACDMRT"); staged != "" {
		t.Errorf("staged = %q, want clean.txt left unstaged", staged)
	}
	if unstaged := git(t, dst, "diff", "--name-only", "--diff-filter=M"); !strings.Contains(unstaged, "clean.txt") {
		t.Errorf("unstaged = %q, want clean.txt", unstaged)
	}
}

func TestCarryChangesSkipsNestedRepo(t *testing.T) {
	src, dst := carryFixture(t)
	initRepo(t, filepath.Join(src, "vendor", "nested"))

	res, err := CarryChanges(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Copied) != 0 || len(res.Skipped) != 1 {
		t.Errorf("result = %+v, want the nested repo skipped, not copied", res)
	}
}

func TestCarryChangesRetargetsAbsoluteSymlinks(t *testing.T) {
	src, dst := carryFixture(t)
	if err := os.Symlink(filepath.Join(src, "file.txt"), filepath.Join(src, "inside.lnk")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(outside, filepath.Join(src, "outside.lnk")); err != nil {
		t.Fatal(err)
	}

	if _, err := CarryChanges(src, dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Readlink(filepath.Join(dst, "inside.lnk")); got != filepath.Join(dst, "file.txt") {
		t.Errorf("inside.lnk -> %q, want it retargeted into the new worktree", got)
	}
	if got, _ := os.Readlink(filepath.Join(dst, "outside.lnk")); got != outside {
		t.Errorf("outside.lnk -> %q, want %q unchanged", got, outside)
	}
}

func TestCarryChangesConflictSurvivesGlobLikeNames(t *testing.T) {
	src, dst := carryFixture(t)
	// dst moves on so that src's edit to file.txt conflicts.
	writeFile(t, filepath.Join(dst, "file.txt"), "C\n")
	git(t, dst, "commit", "-am", "C")
	writeFile(t, filepath.Join(src, "file.txt"), "B\n")
	// A new file whose name, read as a pathspec, would match file.txt.
	writeFile(t, filepath.Join(src, "f*.txt"), "glob\n")
	git(t, src, "add", "--", ":(literal)f*.txt")

	res, err := CarryChanges(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Conflicts) != 1 {
		t.Fatalf("conflicts = %v, want [file.txt]", res.Conflicts)
	}
	if unmerged := git(t, dst, "diff", "--name-only", "--diff-filter=U"); unmerged != "file.txt" {
		t.Errorf("unmerged = %q: the reset of f*.txt resolved file.txt", unmerged)
	}
}

func TestCarryChangesAppliesWhatItCanPastLocalCommits(t *testing.T) {
	src, dst := carryFixture(t)
	// src has a commit dst lacks, adding a file that then gets edited.
	writeFile(t, filepath.Join(src, "pkg", "new.go"), "package pkg\n")
	git(t, src, "add", "pkg/new.go")
	git(t, src, "commit", "-m", "add pkg")
	writeFile(t, filepath.Join(src, "pkg", "new.go"), "package pkg\n\nfunc F() {}\n")
	writeFile(t, filepath.Join(src, "file.txt"), "A\nB\n")

	res, err := CarryChanges(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Patched || len(res.NotApplied) != 1 || res.NotApplied[0] != "pkg/new.go" {
		t.Fatalf("result = %+v, want file.txt applied and pkg/new.go not applied", res)
	}
	if got := readFile(t, filepath.Join(dst, "file.txt")); got != "A\nB\n" {
		t.Errorf("file.txt = %q, want the edit carried despite pkg/new.go", got)
	}
}

func TestCarryChangesStaysInsideWorktree(t *testing.T) {
	src, dst := carryFixture(t)
	outside := t.TempDir()
	// The new branch tracks vendor as a symlink out of the checkout, while
	// the old checkout has an untracked vendor/ directory.
	if err := os.Symlink(outside, filepath.Join(dst, "vendor")); err != nil {
		t.Fatal(err)
	}
	git(t, dst, "add", "vendor")
	git(t, dst, "commit", "-m", "vendor link")
	writeFile(t, filepath.Join(src, "vendor", "new.go"), "package vendor\n")

	res, err := CarryChanges(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.go")); err == nil {
		t.Fatal("copy followed the symlink out of the worktree")
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "vendor/new.go" {
		t.Errorf("Skipped = %v, want [vendor/new.go]", res.Skipped)
	}
}
