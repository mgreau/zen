// Package gitrepo inspects a local git clone and describes it in the terms
// zen's config uses: a short name, a GitHub full name, and a base path.
package gitrepo

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Info describes a local clone in the shape of a config repo entry.
type Info struct {
	Short    string // clone directory name, used as the config key
	FullName string // GitHub owner/repo
	BasePath string // parent directory of the clone (where worktrees go)
	Remote   string // remote the full name came from ("upstream" or "origin")
}

// Detect inspects the git repository containing dir and infers the config
// entry zen would need for it. The full name is taken from the upstream
// remote when one exists (for forks, PRs live on the upstream repo),
// falling back to origin.
func Detect(dir string) (Info, error) {
	root, err := gitOutput(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Info{}, fmt.Errorf("%s is not inside a git repository", dir)
	}

	// A linked worktree's directory name and parent say nothing about the
	// main clone, so registering from one would produce a bogus entry.
	gitDir, _ := gitOutput(root, "rev-parse", "--git-dir")
	commonDir, _ := gitOutput(root, "rev-parse", "--git-common-dir")
	if gitDir != "" && commonDir != "" && absPath(root, gitDir) != absPath(root, commonDir) {
		return Info{}, fmt.Errorf("%s is a linked worktree — run this from the main clone", root)
	}

	remote := "upstream"
	remoteURL, err := gitOutput(root, "remote", "get-url", "upstream")
	if err != nil {
		remote = "origin"
		remoteURL, err = gitOutput(root, "remote", "get-url", "origin")
		if err != nil {
			return Info{}, fmt.Errorf("no upstream or origin remote in %s", root)
		}
	}

	fullName, err := ParseRemote(remoteURL)
	if err != nil {
		return Info{}, fmt.Errorf("remote %s: %w", remote, err)
	}

	return Info{
		Short:    filepath.Base(root),
		FullName: fullName,
		BasePath: filepath.Dir(root),
		Remote:   remote,
	}, nil
}

// scpLikeRe matches scp-style remotes such as git@github.com:owner/repo.git.
var scpLikeRe = regexp.MustCompile(`^(?:[\w.-]+@)?[\w.-]+:(.+)$`)

// ParseRemote extracts the "owner/repo" full name from a git remote URL.
// It accepts https://, ssh://, git:// and scp-style (git@host:owner/repo)
// forms, with or without a trailing .git.
func ParseRemote(remoteURL string) (string, error) {
	remoteURL = strings.TrimSpace(remoteURL)

	var repoPath string
	if strings.Contains(remoteURL, "://") {
		u, err := url.Parse(remoteURL)
		if err != nil || u.Host == "" {
			return "", fmt.Errorf("cannot parse remote URL %q", remoteURL)
		}
		repoPath = u.Path
	} else if m := scpLikeRe.FindStringSubmatch(remoteURL); m != nil {
		repoPath = m[1]
	} else {
		return "", fmt.Errorf("cannot parse remote URL %q", remoteURL)
	}

	repoPath = strings.TrimSuffix(strings.Trim(repoPath, "/"), ".git")
	parts := strings.Split(repoPath, "/")
	if len(parts) < 2 || parts[len(parts)-1] == "" || parts[len(parts)-2] == "" {
		return "", fmt.Errorf("remote URL %q has no owner/repo path", remoteURL)
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1], nil
}

// gitOutput runs git in dir and returns its trimmed stdout.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// absPath resolves p against base when p is relative.
func absPath(base, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Join(base, p)
}
