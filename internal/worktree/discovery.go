package worktree

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/ui"
)

// Type classifies a worktree.
type Type string

const (
	TypePRReview Type = "pr-review"
	TypeFeature  Type = "feature"
)

// Worktree represents a discovered git worktree.
type Worktree struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Branch   string `json:"branch"`
	Type     Type   `json:"type"`
	PRNumber int    `json:"pr_number,omitempty"`
	Repo     string `json:"repo"`
}

var prPattern = regexp.MustCompile(`-pr-(\d+)$`)

// Classify determines if a worktree name represents a PR review or feature work.
func Classify(name string) (Type, int) {
	m := prPattern.FindStringSubmatch(name)
	if m != nil {
		var pr int
		fmt.Sscanf(m[1], "%d", &pr)
		return TypePRReview, pr
	}
	return TypeFeature, 0
}

// ParseRepoFromName extracts the repo short name from a worktree directory name.
// e.g., "mono-pr-1234" -> "mono"
func ParseRepoFromName(name string) string {
	idx := strings.Index(name, "-")
	if idx < 0 {
		return name
	}
	return name[:idx]
}

// ParseBranchFromName extracts the branch suffix from a worktree directory name.
// e.g., "mono-feature-branch" -> "feature-branch"
func ParseBranchFromName(name string) string {
	idx := strings.Index(name, "-")
	if idx < 0 || idx+1 >= len(name) {
		return ""
	}
	return name[idx+1:]
}

// ListForRepo lists all worktrees for a given repository using `git worktree list`.
func ListForRepo(cfg *config.Config, repo string) ([]Worktree, error) {
	basePath := cfg.RepoBasePath(repo)
	if basePath == "" {
		return nil, nil
	}

	originPath := filepath.Join(basePath, repo)
	if _, err := os.Stat(filepath.Join(originPath, ".git")); os.IsNotExist(err) {
		return nil, nil
	}

	// Clean stale locks before git operations
	CleanStaleLocks(cfg, repo)

	cmd := exec.Command("git", "worktree", "list")
	cmd.Dir = originPath
	out, err := cmd.Output()
	if err != nil {
		ui.LogDebug(fmt.Sprintf("git worktree list failed for %s: %v", repo, err))
		return nil, nil
	}

	var worktrees []Worktree
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 1 {
			continue
		}
		path := parts[0]

		// Skip the main worktree
		if path == originPath {
			continue
		}

		// Extract branch from [branch] notation
		branch := ""
		if idx := strings.Index(line, "["); idx >= 0 {
			if end := strings.Index(line[idx:], "]"); end >= 0 {
				branch = line[idx+1 : idx+end]
			}
		}

		name := filepath.Base(path)
		wtype, pr := Classify(name)

		wt := Worktree{
			Path:   path,
			Name:   name,
			Branch: branch,
			Type:   wtype,
			Repo:   repo,
		}
		if pr > 0 {
			wt.PRNumber = pr
		}
		worktrees = append(worktrees, wt)
	}

	return worktrees, nil
}

// ListAll lists worktrees across all configured repositories.
func ListAll(cfg *config.Config) ([]Worktree, error) {
	var all []Worktree
	for _, repo := range cfg.RepoNames() {
		wts, err := ListForRepo(cfg, repo)
		if err != nil {
			ui.LogDebug(fmt.Sprintf("error listing worktrees for %s: %v", repo, err))
			continue
		}
		all = append(all, wts...)
	}
	return all, nil
}

// Stats holds worktree statistics.
type Stats struct {
	Total     int            `json:"total"`
	PRReviews int            `json:"pr_reviews"`
	Features  int            `json:"features"`
	ByRepo    map[string]int `json:"by_repo"`
}

// GetStats computes statistics across all worktrees.
func GetStats(cfg *config.Config) (*Stats, error) {
	wts, err := ListAll(cfg)
	if err != nil {
		return nil, err
	}

	stats := &Stats{
		Total:  len(wts),
		ByRepo: make(map[string]int),
	}
	for _, wt := range wts {
		switch wt.Type {
		case TypePRReview:
			stats.PRReviews++
		case TypeFeature:
			stats.Features++
		}
		stats.ByRepo[wt.Repo]++
	}
	return stats, nil
}
