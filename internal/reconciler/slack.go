package reconciler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chainguard.dev/driftlessaf/workqueue"
	"github.com/mgreau/zen/internal/config"
	ghpkg "github.com/mgreau/zen/internal/github"
	slackpkg "github.com/mgreau/zen/internal/slack"
	"github.com/mgreau/zen/internal/terminal"
	wt "github.com/mgreau/zen/internal/worktree"
)

// SlackOrigin records which worktree a Slack-flagged thread produced, so the
// session scanner can DM completion back to the thread it came from, and so
// completion (a merged PR from Branch, or the done-emoji on the original
// message) can be detected to stop further DMs.
type SlackOrigin struct {
	ChannelID    string `json:"channel_id"`
	MessageTS    string `json:"message_ts"`
	Permalink    string `json:"permalink"`
	WorktreePath string `json:"worktree_path"`
	WorktreeName string `json:"worktree_name"`
	Repo         string `json:"repo"`
	Branch       string `json:"branch"`
}

func slackOriginsPath() string {
	return filepath.Join(config.StateDir(), "slack_origins.json")
}

var slackOriginsMu sync.Mutex

// LoadSlackOrigins reads the persisted worktree->Slack-thread map. Returns an
// empty map (never nil) if the file doesn't exist or can't be parsed.
func LoadSlackOrigins() map[string]SlackOrigin {
	origins := make(map[string]SlackOrigin)
	data, err := os.ReadFile(slackOriginsPath())
	if err != nil {
		return origins
	}
	if err := json.Unmarshal(data, &origins); err != nil {
		return make(map[string]SlackOrigin)
	}
	return origins
}

// SaveSlackOrigins persists the worktree->Slack-thread map, keyed by worktree path.
func SaveSlackOrigins(origins map[string]SlackOrigin) error {
	data, err := json.MarshalIndent(origins, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(slackOriginsPath(), data, 0o644)
}

// RecordSlackOrigin adds a single origin to the persisted map. Serialized
// with slackOriginsMu since SlackReconciler.Reconcile may run concurrently.
func RecordSlackOrigin(o SlackOrigin) error {
	slackOriginsMu.Lock()
	defer slackOriginsMu.Unlock()
	origins := LoadSlackOrigins()
	origins[o.WorktreePath] = o
	return SaveSlackOrigins(origins)
}

// clearSlackOrigin removes a single worktree's recorded origin, e.g. once
// its task is done and no further DMs should be sent for it.
func clearSlackOrigin(worktreePath string) error {
	slackOriginsMu.Lock()
	defer slackOriginsMu.Unlock()
	origins := LoadSlackOrigins()
	delete(origins, worktreePath)
	return SaveSlackOrigins(origins)
}

// SlackReadyHook is invoked by ScanSessions when a worktree that originated
// from a Slack-flagged thread transitions to "waiting". Set by the watch
// daemon when Slack polling is enabled; nil otherwise, in which case
// ScanSessions skips the Slack DM entirely.
var SlackReadyHook func(worktreePath, worktreeName, resumeCmd string)

// slackAPI is the subset of *slackpkg.Client that SlackReconciler needs,
// narrowed so tests can substitute a fake instead of making real HTTP calls.
type slackAPI interface {
	ConversationsReplies(ctx context.Context, channel, ts string) ([]slackpkg.Message, error)
	Permalink(ctx context.Context, channel, ts string) (string, error)
	PostMessage(ctx context.Context, channel, text string) error
	HasReaction(ctx context.Context, channel, ts, name string) (bool, error)
}

// SlackReconciler creates a feature worktree for a Slack thread the user
// flagged with the configured emoji, seeds it with the thread as an initial
// agent prompt, and launches the agent immediately in a terminal tab —
// unlike SetupReconciler, which only prepares PR-review worktrees silently
// for later manual resume. A Slack reaction is a deliberate, comparatively
// rare signal (versus the high-volume PR-review firehose), so starting the
// agent right away matches the intent of reacting in the first place.
type SlackReconciler struct {
	cfg        *config.Config
	client     slackAPI
	selfUserID string
}

// NewSlackReconciler creates a new SlackReconciler.
func NewSlackReconciler(cfg *config.Config, client slackAPI, selfUserID string) *SlackReconciler {
	return &SlackReconciler{cfg: cfg, client: client, selfUserID: selfUserID}
}

// SetConfig updates the config used by this reconciler.
func (r *SlackReconciler) SetConfig(cfg *config.Config) {
	r.cfg = cfg
}

// Reconcile processes a single Slack key ("channelID:messageTS").
func (r *SlackReconciler) Reconcile(ctx context.Context, key string, _ workqueue.Options) error {
	channelID, messageTS, err := ParseSlackKey(key)
	if err != nil {
		return workqueue.NonRetriableError(err, "invalid key format")
	}

	repo := r.cfg.Slack.DefaultRepo
	if repo == "" {
		return workqueue.NonRetriableError(
			fmt.Errorf("slack.default_repo is not configured"),
			"missing default repo",
		)
	}
	basePath := r.cfg.RepoBasePath(repo)
	if basePath == "" {
		return workqueue.NonRetriableError(
			fmt.Errorf("unknown repo %q", repo),
			"repo not configured",
		)
	}

	messages, err := r.client.ConversationsReplies(ctx, channelID, messageTS)
	if err != nil {
		return fmt.Errorf("fetching thread: %w", err)
	}
	permalink, err := r.client.Permalink(ctx, channelID, messageTS)
	if err != nil {
		logf("Warning: failed to fetch permalink for %s: %v", key, err)
	}

	slug := slackSlug(messageTS)
	worktreeName := fmt.Sprintf("%s-slack-%s", repo, slug)
	worktreePath := filepath.Join(basePath, worktreeName)
	originPath := filepath.Join(basePath, repo)

	branchSuffix := fmt.Sprintf("slack-%s", slug)
	gitBranch := branchSuffix
	if prefix := r.cfg.GetBranchPrefix(); prefix != "" {
		gitBranch = fmt.Sprintf("%s/%s", prefix, branchSuffix)
	}

	if _, err := os.Stat(worktreePath); err != nil {
		if err := wt.CreateFromMain(originPath, worktreePath, worktreeName, gitBranch); err != nil {
			return fmt.Errorf("creating worktree: %w", err)
		}
	}

	ag := r.cfg.NewAgent("")
	launchCmd := ag.StartCommand(renderSlackPrompt(permalink, messages), "")
	term, err := terminal.NewTerminal(r.cfg.GetTerminal())
	if err != nil {
		return fmt.Errorf("resolving terminal: %w", err)
	}
	if err := term.OpenTab(worktreePath, launchCmd); err != nil {
		return fmt.Errorf("opening %s tab: %w", term.Name(), err)
	}

	if err := RecordSlackOrigin(SlackOrigin{
		ChannelID:    channelID,
		MessageTS:    messageTS,
		Permalink:    permalink,
		WorktreePath: worktreePath,
		WorktreeName: worktreeName,
		Repo:         repo,
		Branch:       gitBranch,
	}); err != nil {
		logf("Warning: failed to record Slack origin for %s: %v", key, err)
	}

	logf("Slack task started for %s (worktree: %s)", key, worktreePath)
	return nil
}

// NotifyReady sends a Slack DM back to the user when a Slack-originated
// worktree's session goes idle. A no-op if worktreePath has no recorded
// Slack origin (e.g. a plain `zen work new` worktree). Also a no-op — and
// clears the origin so no future transition re-fires it — if the task is
// already done (see isDone): this catches a task that finished between the
// last PruneDone sweep and this transition.
func (r *SlackReconciler) NotifyReady(worktreePath, worktreeName, resumeCmd string) {
	origin, ok := LoadSlackOrigins()[worktreePath]
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if done, reason := r.isDone(ctx, origin); done {
		logf("Slack task done (%s) before notify: %s — no DM sent", reason, worktreeName)
		if err := clearSlackOrigin(worktreePath); err != nil {
			logf("Warning: failed to clear origin for %s: %v", worktreeName, err)
		}
		return
	}

	msg := fmt.Sprintf(":white_check_mark: *%s* is ready for review.\n\nResume: `%s`", worktreeName, resumeCmd)
	if origin.Permalink != "" {
		msg += fmt.Sprintf("\nThread: %s", origin.Permalink)
	}

	if err := r.client.PostMessage(ctx, r.selfUserID, msg); err != nil {
		logf("Warning: failed to send Slack DM for %s: %v", worktreeName, err)
	}
}

// PruneDone checks every tracked Slack origin for a completion signal — the
// configured done-emoji reacted onto the original message, or a merged PR
// from the worktree's branch — and removes any that are done, so
// NotifyReady stops sending further DMs for them. Intended to run on a
// ticker (e.g. alongside the daemon's cleanup scan).
func (r *SlackReconciler) PruneDone(ctx context.Context) error {
	origins := LoadSlackOrigins()
	if len(origins) == 0 {
		return nil
	}

	var toClear []string
	for path, o := range origins {
		if done, reason := r.isDone(ctx, o); done {
			logf("Slack task done (%s): %s — no further DMs", reason, o.WorktreeName)
			toClear = append(toClear, path)
		}
	}
	if len(toClear) == 0 {
		return nil
	}

	slackOriginsMu.Lock()
	defer slackOriginsMu.Unlock()
	origins = LoadSlackOrigins()
	for _, path := range toClear {
		delete(origins, path)
	}
	return SaveSlackOrigins(origins)
}

// isDone reports whether a Slack-originated task is complete: the
// configured done-emoji was reacted onto the original message, or a PR from
// o.Branch has been merged. Best-effort — a lookup failure is treated as
// "not done yet", not an error, so a transient Slack/GitHub API issue never
// blocks a legitimate completion DM.
func (r *SlackReconciler) isDone(ctx context.Context, o SlackOrigin) (bool, string) {
	if emoji := r.cfg.Slack.GetDoneEmoji(); emoji != "" {
		if has, err := r.client.HasReaction(ctx, o.ChannelID, o.MessageTS, emoji); err == nil && has {
			return true, "done-emoji"
		}
	}
	if o.Repo == "" || o.Branch == "" {
		return false, ""
	}
	ghClient, err := ghpkg.NewClient(ctx)
	if err != nil {
		return false, ""
	}
	fullRepo := r.cfg.RepoFullName(o.Repo)
	if state, _, err := ghClient.GetPRStateByBranch(ctx, fullRepo, o.Branch); err == nil && state == "MERGED" {
		return true, "PR merged"
	}
	return false, ""
}

// slackSlug derives a filesystem/branch-safe slug from a Slack message
// timestamp (e.g. "1700000000.123456" -> "1700000000").
func slackSlug(messageTS string) string {
	if i := strings.Index(messageTS, "."); i >= 0 {
		return messageTS[:i]
	}
	return messageTS
}

// renderSlackPrompt builds the initial agent prompt from a Slack thread:
// an instruction header followed by the thread transcript. The agent reads
// this verbatim, so all "figuring out what to do" happens in the launched
// session, not in the daemon.
func renderSlackPrompt(permalink string, messages []slackpkg.Message) string {
	var b strings.Builder
	b.WriteString("You were flagged for this task via a Slack reaction. Read the discussion below, figure out what needs to be done, and do it.\n\n")
	if permalink != "" {
		fmt.Fprintf(&b, "Thread: %s\n\n", permalink)
	}
	b.WriteString("---\n\n")
	for _, m := range messages {
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n\n", m.User, text)
	}
	return b.String()
}
