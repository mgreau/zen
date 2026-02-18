package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mgreau/zen/internal/session"
	"github.com/mgreau/zen/internal/ui"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search <term>",
	Short: "Search across PR reviews and worktrees",
	Args:  cobra.ExactArgs(1),
	RunE:  runSearch,
}

var searchType string

func init() {
	searchCmd.Flags().StringVarP(&searchType, "type", "t", "all", "Filter by type: all, pr, feature")
	rootCmd.AddCommand(searchCmd)
}

// SearchResult holds a search result.
type SearchResult struct {
	Name       string `json:"name"`
	Path       string `json:"path,omitempty"`
	Type       string `json:"type"`
	PRNumber   int    `json:"pr_number,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Repo       string `json:"repo,omitempty"`
	HasSession bool   `json:"has_session"`
}

func runSearch(cmd *cobra.Command, args []string) error {
	term := args[0]
	termLower := strings.ToLower(term)

	results := searchWorktrees(termLower)

	if jsonFlag {
		printJSON(results)
		return nil
	}

	// Human-readable output
	fmt.Println()
	fmt.Println(ui.BoldText(fmt.Sprintf("Search Results for %q", term)))
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	if len(results) == 0 {
		fmt.Println("No results found.")
		fmt.Println()
		return nil
	}

	// Group by type
	var prResults, featureResults, otherResults []SearchResult
	for _, r := range results {
		switch {
		case r.Type == "pr-review":
			prResults = append(prResults, r)
		case r.Type == "feature":
			featureResults = append(featureResults, r)
		default:
			otherResults = append(otherResults, r)
		}
	}

	home := os.Getenv("HOME")

	if len(prResults) > 0 {
		ui.SectionHeader(fmt.Sprintf("PR Reviews (%d)", len(prResults)))
		for _, r := range prResults {
			sessionInd := ""
			if r.HasSession {
				sessionInd = " " + ui.GreenText("●")
			}
			if r.PRNumber > 0 {
				fmt.Printf("  %s%s\n", ui.CyanText(fmt.Sprintf("PR #%d", r.PRNumber)), sessionInd)
			} else {
				fmt.Printf("  %s%s\n", ui.CyanText(r.Name), sessionInd)
			}
			if r.Path != "" {
				fmt.Printf("    %s\n", ui.DimText(ui.ShortenHome(r.Path, home)))
			}
			if r.Repo != "" {
				fmt.Printf("    %s\n", ui.DimText("Repo: "+r.Repo))
			}
		}
		fmt.Println()
	}

	if len(featureResults) > 0 {
		ui.SectionHeader(fmt.Sprintf("Feature Work (%d)", len(featureResults)))
		for _, r := range featureResults {
			sessionInd := ""
			if r.HasSession {
				sessionInd = " " + ui.GreenText("●")
			}
			fmt.Printf("  %s%s\n", ui.CyanText(r.Name), sessionInd)
			if r.Path != "" {
				fmt.Printf("    %s\n", ui.DimText(ui.ShortenHome(r.Path, home)))
			}
			if r.Repo != "" && r.Branch != "" {
				fmt.Printf("    %s\n", ui.DimText(r.Repo+" @ "+r.Branch))
			} else if r.Repo != "" {
				fmt.Printf("    %s\n", ui.DimText("Repo: "+r.Repo))
			}
		}
		fmt.Println()
	}

	if len(otherResults) > 0 {
		ui.SectionHeader(fmt.Sprintf("Other (%d)", len(otherResults)))
		for _, r := range otherResults {
			fmt.Printf("  %s\n", ui.CyanText(r.Name))
			if r.Path != "" {
				fmt.Printf("    %s\n", ui.DimText(ui.ShortenHome(r.Path, home)))
			}
		}
		fmt.Println()
	}

	ui.Separator()
	fmt.Printf("Found: %s results\n", ui.BoldText(fmt.Sprintf("%d", len(results))))
	ui.Hint("● = Active Claude session")
	fmt.Println()
	return nil
}

func searchWorktrees(termLower string) []SearchResult {
	wts, err := worktree.ListAll(cfg)
	if err != nil {
		return nil
	}

	var results []SearchResult
	for _, wt := range wts {
		if searchType == "pr" && wt.Type != worktree.TypePRReview {
			continue
		}
		if searchType == "feature" && wt.Type != worktree.TypeFeature {
			continue
		}

		matched := false
		nameLower := strings.ToLower(wt.Name)
		pathLower := strings.ToLower(wt.Path)
		branchLower := strings.ToLower(wt.Branch)
		repoLower := strings.ToLower(wt.Repo)

		if wt.PRNumber > 0 && strings.Contains(fmt.Sprintf("%d", wt.PRNumber), termLower) {
			matched = true
		}
		if strings.Contains(nameLower, termLower) {
			matched = true
		}
		if strings.Contains(pathLower, termLower) {
			matched = true
		}
		if wt.Branch != "" && strings.Contains(branchLower, termLower) {
			matched = true
		}
		if strings.Contains(repoLower, termLower) {
			matched = true
		}

		if matched {
			results = append(results, SearchResult{
				Name:       wt.Name,
				Path:       wt.Path,
				Type:       string(wt.Type),
				PRNumber:   wt.PRNumber,
				Branch:     wt.Branch,
				Repo:       wt.Repo,
				HasSession: session.HasActiveSession(wt.Path),
			})
		}
	}
	return results
}
