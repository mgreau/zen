package cmd

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/ui"
	wt "github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

var (
	whoamiPeriod  string
	whoamiRepo    string
	whoamiMerged  bool
)

var whoamiCmd = &cobra.Command{
	Use:   "who-am-i",
	Short: "Summary of your work across worktrees",
	Long: `Shows a summary of work done for a given time period.

Includes merged PRs (deployed to main), in-progress feature branches,
PR reviews, and Claude session activity. Defaults to the last 7 days.`,
	RunE: runWhoami,
}

func init() {
	whoamiCmd.Flags().StringVarP(&whoamiPeriod, "period", "p", "7d", "Time period (e.g., 1d, 7d, 30d)")
	whoamiCmd.Flags().StringVarP(&whoamiRepo, "repo", "r", "", "Filter by repo (short name)")
	whoamiCmd.Flags().BoolVar(&whoamiMerged, "merged", false, "Show only merged & deployed PRs")
	rootCmd.AddCommand(whoamiCmd)
}

// mergedEntry represents a commit merged to origin/main.
type mergedEntry struct {
	Repo     string `json:"repo"`
	Hash     string `json:"hash"`
	Subject  string `json:"subject"`
	Body     string `json:"body,omitempty"`
	PRNumber string `json:"pr_number,omitempty"`
	Date     string `json:"date"`
}

// whoamiEntry holds per-worktree activity data.
type whoamiEntry struct {
	Name       string `json:"name"`
	Repo       string `json:"repo"`
	Type       string `json:"type"`
	Branch     string `json:"branch"`
	Commits    int    `json:"commits"`
	LastCommit string `json:"last_commit"`
	HasSession bool   `json:"has_session"`
	SessionAge string `json:"session_age,omitempty"`
	PRNumber   int    `json:"pr_number,omitempty"`
}

// whoamiSummary holds the overall summary.
type whoamiSummary struct {
	Period       string        `json:"period"`
	Since        string        `json:"since"`
	Repos        []string      `json:"repos"`
	Merged       []mergedEntry `json:"merged"`
	TotalMerged  int           `json:"total_merged"`
	InProgress   []whoamiEntry `json:"in_progress"`
	PRReviews    []whoamiEntry `json:"pr_reviews"`
	TotalCommits int           `json:"total_in_progress_commits"`
}

func runWhoami(cmd *cobra.Command, args []string) error {
	since, err := parsePeriod(whoamiPeriod)
	if err != nil {
		return err
	}

	// Determine which repos to scan
	repos := cfg.RepoNames()
	if whoamiRepo != "" {
		if cfg.RepoBasePath(whoamiRepo) == "" {
			return fmt.Errorf("unknown repo %q", whoamiRepo)
		}
		repos = []string{whoamiRepo}
	}

	// --- Merged work (commits on origin/main by the user) ---
	var merged []mergedEntry
	for _, repo := range repos {
		basePath := cfg.RepoBasePath(repo)
		originPath := filepath.Join(basePath, repo)
		entries := mergedCommits(originPath, since, whoamiMerged)
		for i := range entries {
			entries[i].Repo = repo
		}
		merged = append(merged, entries...)
	}

	// If --merged, skip worktree scanning and show only merged work
	if whoamiMerged {
		return renderMergedOnly(merged, repos, since)
	}

	// --- In-progress worktrees ---
	wts, err := wt.ListAll(cfg)
	if err != nil {
		return fmt.Errorf("listing worktrees: %w", err)
	}

	var inProgress, prReviews []whoamiEntry
	repoSet := make(map[string]bool)
	totalBranchCommits := 0

	for _, w := range wts {
		if whoamiRepo != "" && w.Repo != whoamiRepo {
			continue
		}

		commits := countCommits(w.Path, since)
		hasSession := session.HasActiveSession(w.Path)

		if commits == 0 && !hasRecentSession(w.Path, since) {
			continue
		}

		repoSet[w.Repo] = true
		totalBranchCommits += commits

		entry := whoamiEntry{
			Name:     w.Name,
			Repo:     w.Repo,
			Type:     string(w.Type),
			Branch:   w.Branch,
			Commits:  commits,
			PRNumber: w.PRNumber,
		}

		if commits > 0 {
			entry.LastCommit = lastCommitMessage(w.Path)
		}

		if hasSession {
			entry.HasSession = true
			if sessions, _ := session.FindSessions(w.Path); len(sessions) > 0 {
				entry.SessionAge = session.FormatAge(time.Unix(sessions[0].Modified, 0))
			}
		}

		if w.Type == wt.TypePRReview {
			prReviews = append(prReviews, entry)
		} else {
			inProgress = append(inProgress, entry)
		}
	}

	// Sort in-progress by commits desc
	sort.Slice(inProgress, func(i, j int) bool {
		if inProgress[i].Commits != inProgress[j].Commits {
			return inProgress[i].Commits > inProgress[j].Commits
		}
		return inProgress[i].Name < inProgress[j].Name
	})
	sort.Slice(prReviews, func(i, j int) bool {
		if prReviews[i].Commits != prReviews[j].Commits {
			return prReviews[i].Commits > prReviews[j].Commits
		}
		return prReviews[i].Name < prReviews[j].Name
	})

	// Collect all repos that had activity
	for _, m := range merged {
		repoSet[m.Repo] = true
	}
	allRepos := make([]string, 0, len(repoSet))
	for r := range repoSet {
		allRepos = append(allRepos, r)
	}
	sort.Strings(allRepos)

	summary := whoamiSummary{
		Period:       whoamiPeriod,
		Since:        since.Format("2006-01-02"),
		Repos:        allRepos,
		Merged:       merged,
		TotalMerged:  len(merged),
		InProgress:   inProgress,
		PRReviews:    prReviews,
		TotalCommits: totalBranchCommits,
	}

	if jsonFlag {
		if summary.Merged == nil {
			summary.Merged = []mergedEntry{}
		}
		if summary.InProgress == nil {
			summary.InProgress = []whoamiEntry{}
		}
		if summary.PRReviews == nil {
			summary.PRReviews = []whoamiEntry{}
		}
		printJSON(summary)
		return nil
	}

	// --- Human-readable output ---
	fmt.Println()
	ui.SectionHeader("Who Am I")
	fmt.Println()

	repoLabel := strings.Join(allRepos, ", ")
	if repoLabel == "" {
		repoLabel = "(none)"
	}

	fmt.Printf("  Period:  last %s (since %s)\n", whoamiPeriod, since.Format("Jan 2"))
	fmt.Printf("  Repos:   %s\n", repoLabel)
	fmt.Printf("  Merged:  %s to main  |  In-progress: %s branches\n",
		ui.GreenText(fmt.Sprintf("%d PRs", len(merged))),
		ui.CyanText(fmt.Sprintf("%d", len(inProgress))))
	fmt.Println()

	// Merged section
	if len(merged) > 0 {
		fmt.Printf("  %s\n", ui.BoldText("Merged & Deployed"))
		fmt.Println()
		for _, m := range merged {
			prTag := ""
			if m.PRNumber != "" {
				prTag = ui.DimText(fmt.Sprintf("#%s", m.PRNumber))
			}
			subject := ui.Truncate(m.Subject, 65)
			fmt.Printf("    %s  %s  %s\n", ui.GreenText("✓"), subject, prTag)
		}
		fmt.Println()
	}

	// In-progress features
	if len(inProgress) > 0 {
		fmt.Printf("  %s\n", ui.BoldText("In Progress"))
		fmt.Println()
		for _, e := range inProgress {
			commitStr := ui.DimText(commitLabel(e.Commits))
			if e.Commits == 0 {
				commitStr = ui.DimText("session only")
			}
			sessionIcon := ""
			if e.HasSession {
				sessionIcon = ui.GreenText(" ●")
			}

			fmt.Printf("    %-45s  %s%s\n", ui.Truncate(e.Name, 45), commitStr, sessionIcon)
			if e.LastCommit != "" {
				fmt.Printf("    %s\n", ui.DimText("  └ "+ui.Truncate(e.LastCommit, 60)))
			}
		}
		fmt.Println()
	}

	// PR reviews
	if len(prReviews) > 0 {
		fmt.Printf("  %s\n", ui.BoldText("PR Reviews"))
		fmt.Println()
		for _, e := range prReviews {
			commitStr := ui.DimText(commitLabel(e.Commits))
			if e.Commits == 0 {
				commitStr = ui.DimText("session only")
			}
			sessionIcon := ""
			if e.HasSession {
				sessionIcon = ui.GreenText(" ●")
			}

			label := e.Name
			if e.PRNumber > 0 {
				label = fmt.Sprintf("#%d %s", e.PRNumber, e.Branch)
			}
			fmt.Printf("    %-45s  %s%s\n", ui.Truncate(label, 45), commitStr, sessionIcon)
		}
		fmt.Println()
	}

	if len(merged) == 0 && len(inProgress) == 0 && len(prReviews) == 0 {
		fmt.Println("  No activity found in this period.")
		fmt.Println()
	}

	return nil
}

// renderMergedOnly shows a detailed view of only merged PRs.
func renderMergedOnly(merged []mergedEntry, repos []string, since time.Time) error {
	if jsonFlag {
		if merged == nil {
			merged = []mergedEntry{}
		}
		printJSON(merged)
		return nil
	}

	fmt.Println()
	ui.SectionHeader("Merged & Deployed")
	fmt.Println()

	repoLabel := strings.Join(repos, ", ")
	fmt.Printf("  Period:  last %s (since %s)\n", whoamiPeriod, since.Format("Jan 2"))
	fmt.Printf("  Repos:   %s\n", repoLabel)
	fmt.Printf("  Total:   %s\n", ui.GreenText(fmt.Sprintf("%d PRs", len(merged))))
	fmt.Println()

	if len(merged) == 0 {
		fmt.Println("  No merged PRs found in this period.")
		fmt.Println()
		return nil
	}

	for i, m := range merged {
		prTag := ""
		if m.PRNumber != "" {
			prTag = ui.DimText(fmt.Sprintf(" #%s", m.PRNumber))
		}
		dateTag := ""
		if m.Date != "" {
			dateTag = ui.DimText(fmt.Sprintf("  %s", m.Date))
		}
		fmt.Printf("  %s  %s%s%s\n", ui.GreenText("✓"), m.Subject, prTag, dateTag)

		if m.Body != "" {
			// Show body lines indented, as a summary
			for _, line := range strings.Split(m.Body, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				// Skip common noise lines
				if isNoiseBodyLine(line) {
					continue
				}
				fmt.Printf("     %s\n", ui.DimText(ui.Truncate(line, 75)))
			}
		}

		if i < len(merged)-1 {
			fmt.Println()
		}
	}

	fmt.Println()
	return nil
}

// isNoiseBodyLine returns true for lines that don't add value to the summary.
func isNoiseBodyLine(line string) bool {
	lower := strings.ToLower(line)
	noisePatterns := []string{
		"co-authored-by:",
		"signed-off-by:",
		"<!--",
		"-->",
		"**full changelog**",
	}
	for _, p := range noisePatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// parsePeriod converts a period string like "7d" to a time.Time.
func parsePeriod(period string) (time.Time, error) {
	now := time.Now()
	if len(period) < 2 {
		return time.Time{}, fmt.Errorf("invalid period %q (use e.g., 1d, 7d, 30d)", period)
	}

	unit := period[len(period)-1]
	valStr := period[:len(period)-1]

	var val int
	if _, err := fmt.Sscanf(valStr, "%d", &val); err != nil || val <= 0 {
		return time.Time{}, fmt.Errorf("invalid period %q (use e.g., 1d, 7d, 30d)", period)
	}

	switch unit {
	case 'd':
		return now.AddDate(0, 0, -val), nil
	case 'w':
		return now.AddDate(0, 0, -val*7), nil
	case 'm':
		return now.AddDate(0, -val, 0), nil
	default:
		return time.Time{}, fmt.Errorf("invalid period unit %q (use d, w, or m)", string(unit))
	}
}

// prNumberRe extracts PR numbers from commit subjects like "feat: something (#1234)"
var prNumberRe = regexp.MustCompile(`\(#(\d+)\)\s*$`)

// mergedCommits returns commits by the current git user on origin/main since the given time.
// When withBody is true, it also fetches the commit body for each entry.
func mergedCommits(originPath string, since time.Time, withBody bool) []mergedEntry {
	// Get the git user name for author filtering
	authorCmd := exec.Command("git", "config", "user.name")
	authorCmd.Dir = originPath
	authorOut, err := authorCmd.Output()
	if err != nil {
		return nil
	}
	author := strings.TrimSpace(string(authorOut))

	sinceStr := since.Format("2006-01-02")
	cmd := exec.Command("git", "log",
		"--format=%h\t%s\t%ad",
		"--date=short",
		"--since="+sinceStr,
		"--author="+author,
		"origin/main",
	)
	cmd.Dir = originPath
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var entries []mergedEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}

		e := mergedEntry{
			Hash:    parts[0],
			Subject: parts[1],
		}
		if len(parts) == 3 {
			e.Date = parts[2]
		}

		// Extract PR number from subject
		if m := prNumberRe.FindStringSubmatch(e.Subject); len(m) == 2 {
			e.PRNumber = m[1]
			e.Subject = strings.TrimSpace(prNumberRe.ReplaceAllString(e.Subject, ""))
		}

		// Fetch commit body for summary
		if withBody {
			e.Body = commitBody(originPath, e.Hash)
		}

		entries = append(entries, e)
	}

	return entries
}

// commitBody returns the body (message without subject) of a commit.
func commitBody(repoPath, hash string) string {
	cmd := exec.Command("git", "log", "-1", "--format=%b", hash)
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// countCommits counts commits on the branch (not on origin/main) since the given time.
func countCommits(worktreePath string, since time.Time) int {
	sinceStr := since.Format("2006-01-02")
	cmd := exec.Command("git", "rev-list", "--count", "--since="+sinceStr, "origin/main..HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	var count int
	fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &count)
	return count
}

// lastCommitMessage returns the subject line of the most recent branch-only commit.
func lastCommitMessage(worktreePath string) string {
	cmd := exec.Command("git", "log", "-1", "--format=%s", "origin/main..HEAD")
	cmd.Dir = worktreePath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// commitLabel returns a human-readable commit count string.
func commitLabel(n int) string {
	if n == 1 {
		return "1 commit"
	}
	return fmt.Sprintf("%d commits", n)
}

// hasRecentSession returns true if the worktree has a session modified since the given time.
func hasRecentSession(worktreePath string, since time.Time) bool {
	sessions, _ := session.FindSessions(worktreePath)
	if len(sessions) == 0 {
		return false
	}
	return time.Unix(sessions[0].Modified, 0).After(since)
}
