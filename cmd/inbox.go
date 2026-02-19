package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	ghpkg "github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/ui"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
)

var inboxCmd = &cobra.Command{
	Use:   "inbox",
	Short: "Pending PR reviews not yet started locally",
	RunE:  runInbox,
}

var (
	inboxRepo       string
	inboxAuthors    string
	inboxAll        bool
	inboxPathFilter string
	inboxLimit      int
)

func init() {
	inboxCmd.Flags().StringVarP(&inboxRepo, "repo", "r", "", "Repository to check (default: all)")
	inboxCmd.Flags().StringVarP(&inboxAuthors, "authors", "a", "", "Override authors list")
	inboxCmd.Flags().BoolVar(&inboxAll, "all", false, "Show from all authors")
	inboxCmd.Flags().StringVarP(&inboxPathFilter, "path", "p", "", "List PRs touching files under DIR")
	inboxCmd.Flags().IntVar(&inboxLimit, "limit", 100, "Max PRs to scan when using --path")
	rootCmd.AddCommand(inboxCmd)
}

// InboxPR holds a pending PR for display/JSON output.
type InboxPR struct {
	Number       int    `json:"number"`
	Title        string `json:"title"`
	Author       string `json:"author"`
	URL          string `json:"url,omitempty"`
	MatchedPaths string `json:"matched_paths,omitempty"`
	MatchedCount int    `json:"matched_count,omitempty"`
}

func runInbox(_ *cobra.Command, _ []string) error {
	repos := []string{inboxRepo}
	if inboxRepo == "" {
		repos = cfg.RepoNames()
	}

	authors := cfg.Authors
	if inboxAuthors != "" {
		authors = strings.Fields(inboxAuthors)
	}
	if inboxAll {
		authors = nil
	}

	// Cache current user once for all repos.
	ctx := context.Background()
	currentUser, _ := ghpkg.GetCurrentUser(ctx)

	hasResults := false
	for _, repo := range repos {
		found, err := runInboxForRepo(repo, authors, currentUser)
		if err != nil {
			return err
		}
		if found {
			hasResults = true
		}
	}

	if hasResults && !jsonFlag {
		printWorktreeLegend()
	}

	if !hasResults {
		if jsonFlag {
			fmt.Println("[]")
		} else {
			fmt.Println()
			fmt.Println(ui.BoldText("No PRs found"))
			if inboxPathFilter != "" {
				repoLabel := strings.Join(repos, ", ")
				ui.Hint(fmt.Sprintf("Path: %s in %s", inboxPathFilter, repoLabel))
			}
			if !inboxAll && len(authors) > 0 {
				ui.Hint(fmt.Sprintf("Authors: %s", strings.Join(authors, " ")))
				ui.Hint("Use --all to check all authors")
			}
			fmt.Println()
		}
	}

	return nil
}

func runInboxForRepo(repo string, authors []string, currentUser string) (bool, error) {
	ctx := context.Background()
	fullRepo := cfg.RepoFullName(repo)
	localPRs := getLocalPRNumbers(repo)
	hasResults := false

	if inboxPathFilter != "" {
		prs, err := fetchPRsByPath(ctx, fullRepo, inboxPathFilter, authors)
		if err != nil {
			return false, err
		}
		pending := filterLocalPRs(prs, localPRs)
		if len(prs) > 0 {
			hasResults = true
			displayPathResults(pending, len(prs), repo)
		}
	} else {
		// Fetch review requests and approved PRs concurrently.
		var reviews []ghpkg.ReviewRequest
		var approved []ghpkg.ApprovedPR
		var reviewsErr, approvedErr error

		g, gctx := errgroup.WithContext(ctx)
		g.Go(func() error {
			reviews, reviewsErr = ghpkg.GetReviewRequests(gctx, fullRepo)
			return nil
		})
		g.Go(func() error {
			approved, approvedErr = ghpkg.GetApprovedUnmerged(gctx, fullRepo)
			return nil
		})
		_ = g.Wait()

		if reviewsErr != nil {
			return false, fmt.Errorf("fetching review requests for %s: %w", repo, reviewsErr)
		}

		filtered := filterByAuthors(reviews, authors)
		pending := filterLocalPRsFromReviews(filtered, localPRs)

		if len(filtered) > 0 {
			hasResults = true
			displayReviewResults(pending, len(filtered), repo)
		}

		if approvedErr == nil && len(approved) > 0 {
			hasResults = true
			displayApprovedUnmerged(approved)
		}

		if len(cfg.WatchPaths) > 0 {
			watched, others, err := fetchOpenPRs(ctx, fullRepo, currentUser)
			if err == nil {
				if len(watched) > 0 {
					hasResults = true
					displayWatchedPRs(watched, localPRs, repo)
				}
				// Only show "other" PRs where the user is a requested reviewer
				reviewPRs := make(map[int]bool, len(reviews))
				for _, r := range reviews {
					reviewPRs[r.Number] = true
				}
				var reviewOthers []InboxPR
				for _, pr := range others {
					if reviewPRs[pr.Number] {
						reviewOthers = append(reviewOthers, pr)
					}
				}
				if len(reviewOthers) > 0 {
					hasResults = true
					displayOtherPRs(reviewOthers, localPRs, repo)
				}
			}
		}
	}

	return hasResults, nil
}

func getLocalPRNumbers(repo string) map[int]bool {
	wts, _ := worktree.ListForRepo(cfg, repo)
	m := make(map[int]bool)
	for _, wt := range wts {
		if wt.Type == worktree.TypePRReview && wt.PRNumber > 0 {
			m[wt.PRNumber] = true
		}
	}
	return m
}

func filterByAuthors(prs []ghpkg.ReviewRequest, authors []string) []ghpkg.ReviewRequest {
	if len(authors) == 0 {
		return prs
	}
	authorSet := make(map[string]bool)
	for _, a := range authors {
		authorSet[a] = true
	}
	var filtered []ghpkg.ReviewRequest
	for _, pr := range prs {
		if authorSet[pr.Author.Login] {
			filtered = append(filtered, pr)
		}
	}
	return filtered
}

func filterLocalPRsFromReviews(prs []ghpkg.ReviewRequest, local map[int]bool) []ghpkg.ReviewRequest {
	var pending []ghpkg.ReviewRequest
	for _, pr := range prs {
		if !local[pr.Number] {
			pending = append(pending, pr)
		}
	}
	return pending
}

func filterLocalPRs(prs []InboxPR, local map[int]bool) []InboxPR {
	var pending []InboxPR
	for _, pr := range prs {
		if !local[pr.Number] {
			pending = append(pending, pr)
		}
	}
	return pending
}

func fetchPRsByPath(ctx context.Context, fullRepo, pathPrefix string, authors []string) ([]InboxPR, error) {
	pathPrefix = strings.TrimSuffix(pathPrefix, "/")

	prs, err := ghpkg.ListOpenPRs(ctx, fullRepo, inboxLimit)
	if err != nil {
		return nil, err
	}

	if len(authors) > 0 {
		prs = filterByAuthors(prs, authors)
	}

	ghClient, err := ghpkg.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	if !jsonFlag {
		fmt.Fprintf(os.Stderr, "  Scanning %d PRs in %s for %s/...", len(prs), fullRepo, pathPrefix)
	}

	type prResult struct {
		entry   InboxPR
		matched bool
	}
	slots := make([]prResult, len(prs))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)
	for i, pr := range prs {
		g.Go(func() error {
			files, err := ghClient.GetPRFiles(gctx, fullRepo, pr.Number)
			if err != nil {
				return nil
			}
			count := 0
			for _, f := range files {
				if strings.HasPrefix(f, pathPrefix+"/") {
					count++
				}
			}
			if count > 0 {
				slots[i] = prResult{
					entry: InboxPR{
						Number:       pr.Number,
						Title:        pr.Title,
						Author:       pr.Author.Login,
						URL:          pr.URL,
						MatchedCount: count,
					},
					matched: true,
				}
			}
			return nil
		})
	}
	_ = g.Wait()

	if !jsonFlag {
		fmt.Fprintf(os.Stderr, "\r%-60s\r", "")
	}

	var results []InboxPR
	for _, s := range slots {
		if s.matched {
			results = append(results, s.entry)
		}
	}
	return results, nil
}

// fetchOpenPRs splits recent open PRs into two groups: those touching watched
// paths and all others. The current user's PRs are excluded from both.
func fetchOpenPRs(ctx context.Context, fullRepo string, currentUser string) ([]InboxPR, []InboxPR, error) {
	prs, err := ghpkg.ListOpenPRs(ctx, fullRepo, 30)
	if err != nil {
		return nil, nil, err
	}

	ghClient, err := ghpkg.NewClient(ctx)
	if err != nil {
		return nil, nil, err
	}

	// Filter out current user's PRs before scanning.
	var candidates []ghpkg.ReviewRequest
	for _, pr := range prs {
		if currentUser != "" && pr.Author.Login == currentUser {
			continue
		}
		candidates = append(candidates, pr)
	}

	if !jsonFlag {
		fmt.Fprintf(os.Stderr, "  %s", ui.DimText(fmt.Sprintf("Scanning %d open PRs...", len(candidates))))
	}

	type prResult struct {
		entry   InboxPR
		watched bool
		ok      bool
	}
	slots := make([]prResult, len(candidates))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(5)
	for i, pr := range candidates {
		g.Go(func() error {
			files, err := ghClient.GetPRFiles(gctx, fullRepo, pr.Number)
			if err != nil {
				return nil
			}

			seen := make(map[string]bool)
			for _, f := range files {
				for _, wp := range cfg.WatchPaths {
					if (strings.HasPrefix(f, wp+"/") || strings.HasPrefix(f, wp)) && !seen[wp] {
						seen[wp] = true
					}
				}
			}

			entry := InboxPR{
				Number: pr.Number,
				Title:  pr.Title,
				Author: pr.Author.Login,
				URL:    pr.URL,
			}

			if len(seen) > 0 {
				var paths []string
				for p := range seen {
					paths = append(paths, p)
				}
				entry.MatchedPaths = strings.Join(paths, ", ")
				slots[i] = prResult{entry: entry, watched: true, ok: true}
			} else {
				slots[i] = prResult{entry: entry, watched: false, ok: true}
			}
			return nil
		})
	}
	_ = g.Wait()

	if !jsonFlag {
		fmt.Fprintf(os.Stderr, "\r%-60s\r", "")
	}

	var watched, others []InboxPR
	for _, s := range slots {
		if !s.ok {
			continue
		}
		if s.watched {
			watched = append(watched, s.entry)
		} else {
			others = append(others, s.entry)
		}
	}
	return watched, others, nil
}

func displayReviewResults(pending []ghpkg.ReviewRequest, total int, repo string) {
	if jsonFlag {
		var prs []InboxPR
		for _, pr := range pending {
			prs = append(prs, InboxPR{
				Number: pr.Number,
				Title:  pr.Title,
				Author: pr.Author.Login,
				URL:    pr.URL,
			})
		}
		printJSON(prs)
		return
	}

	fmt.Println()
	if inboxAll {
		fmt.Printf("%s %s\n", ui.BoldText("Pending PR Reviews — "+ui.YellowText(repo)), ui.DimText("(all authors)"))
	} else {
		fmt.Println(ui.BoldText("Pending PR Reviews — " + ui.YellowText(repo)))
		ui.Hint(fmt.Sprintf("Authors: %s", strings.Join(cfg.Authors, " ")))
	}
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	if len(pending) == 0 {
		fmt.Println("All review requests already have local worktrees.")
		fmt.Println()
		ui.Hint(fmt.Sprintf("Total PRs matched: %d (all have worktrees)", total))
		fmt.Println()
		return
	}

	fmt.Printf("  %-6s  %-20s  %-42s  %s\n", "PR", "Author", "Title", "Link")
	fmt.Printf("  %-6s  %-20s  %-42s  %s\n", "──────", "────────────────────", "──────────────────────────────────────────", "────────────────────────")

	for _, pr := range pending {
		shortTitle := ui.Truncate(pr.Title, 40)
		fmt.Printf("  %s  %-20s  %-42s  %s\n",
			ui.CyanText(fmt.Sprintf("#%-5d", pr.Number)),
			pr.Author.Login,
			shortTitle,
			ui.DimText(pr.URL))
	}

	fmt.Println()
	ui.Separator()
	fmt.Printf("%s PRs without local worktree (%d total matched)\n",
		ui.BoldText(fmt.Sprintf("%d", len(pending))), total)
	fmt.Println()
}

func displayPathResults(pending []InboxPR, total int, repo string) {
	if jsonFlag {
		printJSON(pending)
		return
	}

	fmt.Println()
	fmt.Printf("%s\n", ui.BoldText(fmt.Sprintf("Open PRs touching %s — %s", ui.CyanText(inboxPathFilter), ui.YellowText(repo))))
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	if len(pending) == 0 {
		fmt.Printf("No open PRs touching %s without a local worktree.\n", inboxPathFilter)
		fmt.Println()
		return
	}

	fmt.Printf("  %-6s  %-20s  %-42s  %-10s  %s\n", "PR", "Author", "Title", "Files", "Link")
	fmt.Printf("  %-6s  %-20s  %-42s  %-10s  %s\n", "──────", "────────────────────", "──────────────────────────────────────────", "──────────", "────────────────────────")

	for _, pr := range pending {
		shortTitle := ui.Truncate(pr.Title, 40)
		files := ""
		if pr.MatchedCount > 0 {
			files = fmt.Sprintf("%d file(s)", pr.MatchedCount)
		}
		fmt.Printf("  %s  %-20s  %-42s  %-10s  %s\n",
			ui.CyanText(fmt.Sprintf("#%-5d", pr.Number)),
			pr.Author,
			shortTitle,
			ui.DimText(files),
			ui.DimText(pr.URL))
	}

	fmt.Println()
	ui.Separator()
	fmt.Printf("%s PRs without local worktree (%d total matched)\n",
		ui.BoldText(fmt.Sprintf("%d", len(pending))), total)
	fmt.Println()
}

func displayApprovedUnmerged(prs []ghpkg.ApprovedPR) {
	if jsonFlag {
		printJSON(prs)
		return
	}

	fmt.Println()
	fmt.Println(ui.BoldText("Your PRs — Approved, Ready to Merge"))
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	fmt.Printf("  %-6s  %-50s  %s\n", "PR", "Title", "Link")
	fmt.Printf("  %-6s  %-50s  %s\n", "──────", "──────────────────────────────────────────────────", "────────────────────────")

	for _, pr := range prs {
		shortTitle := ui.Truncate(pr.Title, 48)
		fmt.Printf("  %s  %-50s  %s\n",
			ui.GreenText(fmt.Sprintf("#%-5d", pr.Number)),
			shortTitle,
			ui.DimText(pr.URL))
	}

	fmt.Println()
	ui.Separator()
	fmt.Printf("%s PR(s) approved and ready to merge\n",
		ui.BoldText(fmt.Sprintf("%d", len(prs))))
	fmt.Println()
}

func displayWatchedPRs(prs []InboxPR, localPRs map[int]bool, repo string) {
	if jsonFlag {
		printJSON(prs)
		return
	}

	fmt.Println()
	watchPathsStr := strings.Join(cfg.WatchPaths, "/ and ") + "/"
	fmt.Printf("%s\n", ui.BoldText(fmt.Sprintf("Open PRs touching %s — %s", ui.CyanText(watchPathsStr), ui.YellowText(repo))))
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	printPRTable(prs, localPRs)

	fmt.Println()
	ui.Separator()
	fmt.Printf("%s open PR(s)\n", ui.BoldText(fmt.Sprintf("%d", len(prs))))
	fmt.Println()
}

func displayOtherPRs(prs []InboxPR, localPRs map[int]bool, repo string) {
	if jsonFlag {
		printJSON(prs)
		return
	}

	fmt.Println()
	fmt.Println(ui.BoldText(fmt.Sprintf("Other PRs Requesting Your Review — %s", ui.YellowText(repo))))
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()

	printPRTable(prs, localPRs)

	fmt.Println()
	ui.Separator()
	fmt.Printf("%s open PR(s)\n", ui.BoldText(fmt.Sprintf("%d", len(prs))))
	fmt.Println()
}

// printPRTable renders a PR table with a W (worktree) column.
func printPRTable(prs []InboxPR, localPRs map[int]bool) {
	fmt.Printf("  %-2s  %-6s  %-20s  %-42s  %s\n", "W", "PR", "Author", "Title", "Link")
	fmt.Printf("  %-2s  %-6s  %-20s  %-42s  %s\n", "──", "──────", "────────────────────", "──────────────────────────────────────────", "────────────────────────")

	for _, pr := range prs {
		shortTitle := ui.Truncate(pr.Title, 40)
		wCol := "  "
		if localPRs[pr.Number] {
			wCol = ui.GreenText("* ")
		}
		fmt.Printf("  %s  %s  %-20s  %-42s  %s\n",
			wCol,
			ui.CyanText(fmt.Sprintf("#%-5d", pr.Number)),
			pr.Author,
			shortTitle,
			ui.DimText(pr.URL))
	}
}

// printWorktreeLegend prints a legend explaining the W column and worktree indicators.
func printWorktreeLegend() {
	fmt.Println(ui.DimText("───────────────────────────────────────────────────────────────"))
	fmt.Printf("  %s  W = Worktree\n", ui.BoldText("Legend"))
	fmt.Printf("       %s = local worktree exists\n", ui.GreenText("*"))
	fmt.Printf("       %s to open  |  %s to create\n",
		ui.DimText("zen review resume <number>"),
		ui.DimText("zen review <number>"))
	fmt.Println()
}
