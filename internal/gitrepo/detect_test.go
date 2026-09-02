package gitrepo

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseRemote(t *testing.T) {
	tests := []struct {
		url     string
		want    string
		wantErr bool
	}{
		{url: "git@github.com:mgreau/zen.git", want: "mgreau/zen"},
		{url: "git@github.com:mgreau/zen", want: "mgreau/zen"},
		{url: "https://github.com/mgreau/zen.git", want: "mgreau/zen"},
		{url: "https://github.com/mgreau/zen", want: "mgreau/zen"},
		{url: "https://github.com/mgreau/zen/", want: "mgreau/zen"},
		{url: "ssh://git@github.com/mgreau/zen.git", want: "mgreau/zen"},
		{url: "git://github.com/mgreau/zen.git", want: "mgreau/zen"},
		{url: "https://gitlab.com/group/subgroup/repo.git", want: "subgroup/repo"},
		{url: "https://github.com/", wantErr: true},
		{url: "https://github.com/zen", wantErr: true},
		{url: "/local/path/repo.git", wantErr: true},
		{url: "file:///local/repo.git", wantErr: true},
		{url: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got, err := ParseRemote(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseRemote(%q) = %q, want error", tt.url, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRemote(%q): %v", tt.url, err)
			}
			if got != tt.want {
				t.Errorf("ParseRemote(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

// tempDir returns a temp directory with symlinks resolved, since git
// reports symlink-resolved paths (macOS puts TMPDIR behind /private).
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// initRepo creates a git repo at dir with the given remotes (name → url).
func initRepo(t *testing.T, dir string, remotes map[string]string) {
	t.Helper()
	run(t, dir, "init", "-q")
	for name, url := range remotes {
		run(t, dir, "remote", "add", name, url)
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestDetectFromOrigin(t *testing.T) {
	base := tempDir(t)
	clone := filepath.Join(base, "zen")
	if err := exec.Command("mkdir", clone).Run(); err != nil {
		t.Fatal(err)
	}
	initRepo(t, clone, map[string]string{"origin": "git@github.com:mgreau/zen.git"})

	info, err := Detect(clone)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if info.Short != "zen" {
		t.Errorf("Short = %q, want %q", info.Short, "zen")
	}
	if info.FullName != "mgreau/zen" {
		t.Errorf("FullName = %q, want %q", info.FullName, "mgreau/zen")
	}
	if info.BasePath != base {
		t.Errorf("BasePath = %q, want %q", info.BasePath, base)
	}
	if info.Remote != "origin" {
		t.Errorf("Remote = %q, want %q", info.Remote, "origin")
	}
}

func TestDetectPrefersUpstream(t *testing.T) {
	base := tempDir(t)
	clone := filepath.Join(base, "apko")
	if err := exec.Command("mkdir", clone).Run(); err != nil {
		t.Fatal(err)
	}
	initRepo(t, clone, map[string]string{
		"origin":   "git@github.com:imkarrer/apko.git",
		"upstream": "https://github.com/chainguard-dev/apko.git",
	})

	info, err := Detect(clone)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if info.FullName != "chainguard-dev/apko" {
		t.Errorf("FullName = %q, want upstream's %q", info.FullName, "chainguard-dev/apko")
	}
	if info.Remote != "upstream" {
		t.Errorf("Remote = %q, want %q", info.Remote, "upstream")
	}
}

func TestDetectFromSubdirectory(t *testing.T) {
	base := tempDir(t)
	clone := filepath.Join(base, "zen")
	sub := filepath.Join(clone, "internal", "config")
	if err := exec.Command("mkdir", "-p", sub).Run(); err != nil {
		t.Fatal(err)
	}
	initRepo(t, clone, map[string]string{"origin": "https://github.com/mgreau/zen.git"})

	info, err := Detect(sub)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if info.Short != "zen" || info.BasePath != base {
		t.Errorf("got Short=%q BasePath=%q, want zen under %q", info.Short, info.BasePath, base)
	}
}

func TestDetectErrors(t *testing.T) {
	t.Run("not a repo", func(t *testing.T) {
		if _, err := Detect(t.TempDir()); err == nil {
			t.Error("Detect on a non-repo should fail")
		}
	})

	t.Run("no remotes", func(t *testing.T) {
		dir := t.TempDir()
		initRepo(t, dir, nil)
		if _, err := Detect(dir); err == nil {
			t.Error("Detect without remotes should fail")
		}
	})

	t.Run("linked worktree", func(t *testing.T) {
		base := tempDir(t)
		clone := filepath.Join(base, "zen")
		if err := exec.Command("mkdir", clone).Run(); err != nil {
			t.Fatal(err)
		}
		initRepo(t, clone, map[string]string{"origin": "git@github.com:mgreau/zen.git"})
		run(t, clone, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "init")
		wtPath := filepath.Join(base, "zen-pr-1")
		run(t, clone, "worktree", "add", "-q", wtPath)

		if _, err := Detect(wtPath); err == nil {
			t.Error("Detect in a linked worktree should fail")
		}
	})
}

func TestDetectUnparseableRemote(t *testing.T) {
	base := tempDir(t)
	clone := filepath.Join(base, "zen")
	if err := exec.Command("mkdir", clone).Run(); err != nil {
		t.Fatal(err)
	}
	initRepo(t, clone, map[string]string{"origin": "/local/path/repo.git"})

	if _, err := Detect(clone); err == nil {
		t.Error("Detect with an unparseable remote URL should fail")
	}
}
