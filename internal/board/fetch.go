package board

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/ui"
	"golang.org/x/sync/errgroup"
)

// fetchTimeout bounds a single board refresh across all configured repos.
const fetchTimeout = 25 * time.Second

// maxConcurrentRepoFetches caps how many repos are queried at once, so a
// large repo list doesn't fan out into an unbounded burst of `gh` processes.
const maxConcurrentRepoFetches = 6

// maxConcurrentFileChecks caps concurrent "which files does this PR touch"
// lookups used to detect watch_paths membership for Needs Your Review.
const maxConcurrentFileChecks = 5

// Review tiers, in display priority order (lower sorts first).
const (
	reviewTierAuthor    = 0 // PR author is in the `authors` config
	reviewTierWatchPath = 1 // PR touches a configured watch_paths entry
	reviewTierOther     = 2 // everything else
)

// MyPRRow is one row of the "My Pull Requests" table.
type MyPRRow struct {
	Status  Status
	Number  int
	Repo    string // short config name
	Title   string
	URL     string
	Age     string
	ageSecs int
	BaseRef string
	HeadRef string
	// Depth is 0 for an independent PR or the root of a dependency chain,
	// and increases by 1 per level for PRs stacked on top of another.
	Depth int
}

// ReviewRow is one row of the "Needs Your Review" table.
type ReviewRow struct {
	Kind    string // github.ReviewKindNew or github.ReviewKindRereview
	Number  int
	Repo    string // short config name
	Title   string
	Author  string
	URL     string
	Age     string
	ageSecs int
	tier    int
}

// ageOf formats how long ago an RFC3339 timestamp was, using the same
// human-readable units as the rest of zen. Returns ("", 0) if createdAt
// can't be parsed.
func ageOf(createdAt string) (string, int) {
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return "", 0
	}
	secs := int(time.Since(t).Seconds())
	if secs < 0 {
		secs = 0
	}
	return ui.FormatDuration(secs), secs
}

// FetchMyPRs fetches the user's own open PRs across all configured repos,
// classifies each into a status bucket, and orders them by dependency chain
// (a PR immediately followed by anything stacked on top of it), with chains
// and independent PRs ranked by status priority then age (oldest first).
func FetchMyPRs(ctx context.Context, cfg *config.Config) ([]MyPRRow, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	repos := cfg.RepoNames()
	rowsByRepo := make([][]MyPRRow, len(repos))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentRepoFetches)
	for i, repo := range repos {
		g.Go(func() error {
			full := cfg.RepoFullName(repo)
			prs, err := github.GetMyOpenPRs(gctx, full)
			if err != nil {
				return fmt.Errorf("fetching your PRs in %s: %w", repo, err)
			}
			rows := make([]MyPRRow, 0, len(prs))
			for _, pr := range prs {
				age, ageSecs := ageOf(pr.CreatedAt)
				rows = append(rows, MyPRRow{
					Status:  ClassifyMyPR(pr),
					Number:  pr.Number,
					Repo:    repo,
					Title:   pr.Title,
					URL:     pr.URL,
					Age:     age,
					ageSecs: ageSecs,
					BaseRef: pr.BaseRefName,
					HeadRef: pr.HeadRefName,
				})
			}
			rowsByRepo[i] = rows
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	var all []MyPRRow
	for _, rows := range rowsByRepo {
		all = append(all, rows...)
	}
	return orderWithDependencies(all), nil
}

// FetchReviewRequests fetches PRs the user has been asked to review across
// all configured repos, unfiltered by the `authors` config — a "don't miss
// anything" dashboard shouldn't hide a review request just because the
// author isn't in the curated inbox list. Sorted into three tiers —
// configured authors, PRs touching a configured watch_paths entry, then
// everyone else — and by age (most recent first) within each tier.
func FetchReviewRequests(ctx context.Context, cfg *config.Config) ([]ReviewRow, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	repos := cfg.RepoNames()
	rowsByRepo := make([][]ReviewRow, len(repos))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentRepoFetches)
	for i, repo := range repos {
		g.Go(func() error {
			full := cfg.RepoFullName(repo)
			reqs, err := github.GetReviewRequests(gctx, full, false)
			if err != nil {
				return fmt.Errorf("fetching review requests in %s: %w", repo, err)
			}
			rows := make([]ReviewRow, 0, len(reqs))
			for _, rr := range reqs {
				age, ageSecs := ageOf(rr.CreatedAt)
				rows = append(rows, ReviewRow{
					Kind:    rr.Kind,
					Number:  rr.Number,
					Repo:    repo,
					Title:   rr.Title,
					Author:  rr.Author.Login,
					URL:     rr.URL,
					Age:     age,
					ageSecs: ageSecs,
				})
			}
			rowsByRepo[i] = rows
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}

	var all []ReviewRow
	for _, rows := range rowsByRepo {
		all = append(all, rows...)
	}

	assignReviewTiers(ctx, cfg, all)

	sort.Slice(all, func(i, j int) bool {
		if all[i].tier != all[j].tier {
			return all[i].tier < all[j].tier
		}
		return all[i].ageSecs < all[j].ageSecs
	})
	return all, nil
}

// assignReviewTiers sets tier on each row in place: reviewTierAuthor when
// the PR's author is in the `authors` config, reviewTierWatchPath when it
// touches a configured watch_paths entry, reviewTierOther otherwise.
//
// Checking watch_paths membership requires fetching each candidate PR's
// changed files — skipped entirely when no watch_paths are configured, and
// only performed for rows not already reviewTierAuthor. A failure fetching
// files or building the GitHub client leaves the affected rows at
// reviewTierOther rather than failing the whole refresh.
func assignReviewTiers(ctx context.Context, cfg *config.Config, rows []ReviewRow) {
	var needsFileCheck []int
	for i := range rows {
		if cfg.IsAuthor(rows[i].Author) {
			rows[i].tier = reviewTierAuthor
			continue
		}
		rows[i].tier = reviewTierOther
		if len(cfg.WatchPaths) > 0 {
			needsFileCheck = append(needsFileCheck, i)
		}
	}
	if len(needsFileCheck) == 0 {
		return
	}

	client, err := github.NewClient(ctx)
	if err != nil {
		return
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentFileChecks)
	for _, i := range needsFileCheck {
		g.Go(func() error {
			full := cfg.RepoFullName(rows[i].Repo)
			files, err := client.GetPRFiles(gctx, full, rows[i].Number)
			if err != nil {
				return nil
			}
			if touchesWatchPaths(files, cfg.WatchPaths) {
				rows[i].tier = reviewTierWatchPath
			}
			return nil
		})
	}
	_ = g.Wait()
}

// touchesWatchPaths reports whether any file touches a configured watch
// path, matching the same prefix rule as `zen inbox`'s watch_paths scan.
func touchesWatchPaths(files, watchPaths []string) bool {
	for _, f := range files {
		for _, wp := range watchPaths {
			if strings.HasPrefix(f, wp+"/") || strings.HasPrefix(f, wp) {
				return true
			}
		}
	}
	return false
}
