package reconciler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chainguard.dev/driftlessaf/workqueue"
	"github.com/mgreau/zen/internal/config"
	slackpkg "github.com/mgreau/zen/internal/slack"
)

// fakeSlackAPI is a test double for slackAPI — no HTTP involved.
type fakeSlackAPI struct {
	repliesFn     func(ctx context.Context, channel, ts string) ([]slackpkg.Message, error)
	permalinkFn   func(ctx context.Context, channel, ts string) (string, error)
	postMessageFn func(ctx context.Context, channel, text string) error
	hasReactionFn func(ctx context.Context, channel, ts, name string) (bool, error)
}

func (f *fakeSlackAPI) HasReaction(ctx context.Context, channel, ts, name string) (bool, error) {
	if f.hasReactionFn == nil {
		return false, nil
	}
	return f.hasReactionFn(ctx, channel, ts, name)
}

func (f *fakeSlackAPI) ConversationsReplies(ctx context.Context, channel, ts string) ([]slackpkg.Message, error) {
	if f.repliesFn == nil {
		return nil, nil
	}
	return f.repliesFn(ctx, channel, ts)
}

func (f *fakeSlackAPI) Permalink(ctx context.Context, channel, ts string) (string, error) {
	if f.permalinkFn == nil {
		return "", nil
	}
	return f.permalinkFn(ctx, channel, ts)
}

func (f *fakeSlackAPI) PostMessage(ctx context.Context, channel, text string) error {
	if f.postMessageFn == nil {
		return nil
	}
	return f.postMessageFn(ctx, channel, text)
}

func TestMakeSlackKey(t *testing.T) {
	got := MakeSlackKey("C0123456789", "1700000000.123456")
	want := "C0123456789:1700000000.123456"
	if got != want {
		t.Errorf("MakeSlackKey() = %q, want %q", got, want)
	}
}

func TestParseSlackKey(t *testing.T) {
	tests := []struct {
		key         string
		wantChannel string
		wantTS      string
		wantErr     bool
	}{
		{"C0123456789:1700000000.123456", "C0123456789", "1700000000.123456", false},
		{"invalid", "", "", true},
		{"", "", "", true},
		{":1700000000.123456", "", "", true},
		{"C0123456789:", "", "", true},
	}
	for _, tt := range tests {
		channel, ts, err := ParseSlackKey(tt.key)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParseSlackKey(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if channel != tt.wantChannel || ts != tt.wantTS {
			t.Errorf("ParseSlackKey(%q) = (%q, %q), want (%q, %q)", tt.key, channel, ts, tt.wantChannel, tt.wantTS)
		}
	}
}

func TestSlackSlug(t *testing.T) {
	if got := slackSlug("1700000000.123456"); got != "1700000000" {
		t.Errorf("slackSlug() = %q, want %q", got, "1700000000")
	}
	if got := slackSlug("noseparator"); got != "noseparator" {
		t.Errorf("slackSlug() = %q, want %q", got, "noseparator")
	}
}

func TestRenderSlackPrompt(t *testing.T) {
	messages := []slackpkg.Message{
		{User: "U1", Text: "we should fix the flaky test", Ts: "1.1"},
		{User: "U2", Text: "  ", Ts: "1.2"}, // blank after trim, should be skipped
		{User: "U3", Text: "agreed, I'll flag it", Ts: "1.3"},
	}
	got := renderSlackPrompt("https://example.slack.com/archives/C1/p1", messages)

	if !contains(got, "https://example.slack.com/archives/C1/p1") {
		t.Errorf("prompt missing permalink: %q", got)
	}
	if !contains(got, "we should fix the flaky test") || !contains(got, "agreed, I'll flag it") {
		t.Errorf("prompt missing message text: %q", got)
	}
	if contains(got, "U2:   ") {
		t.Errorf("prompt should skip blank messages: %q", got)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestSlackOrigins_RoundTrip(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	origin := SlackOrigin{
		ChannelID:    "C1",
		MessageTS:    "1700000000.123456",
		Permalink:    "https://example.slack.com/archives/C1/p1",
		WorktreePath: "/tmp/mono-slack-1700000000",
		WorktreeName: "mono-slack-1700000000",
	}
	if err := RecordSlackOrigin(origin); err != nil {
		t.Fatalf("RecordSlackOrigin() error: %v", err)
	}

	got := LoadSlackOrigins()
	if len(got) != 1 {
		t.Fatalf("LoadSlackOrigins() = %v, want 1 entry", got)
	}
	if got[origin.WorktreePath] != origin {
		t.Errorf("LoadSlackOrigins()[%q] = %+v, want %+v", origin.WorktreePath, got[origin.WorktreePath], origin)
	}
}

func TestLoadSlackOrigins_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	got := LoadSlackOrigins()
	if len(got) != 0 {
		t.Errorf("LoadSlackOrigins() with no file = %v, want empty map", got)
	}
}

func TestSlackReconcile_InvalidKey(t *testing.T) {
	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"mono": {FullName: "chainguard-dev/mono", BasePath: "/tmp/test"},
	}}
	rec := NewSlackReconciler(cfg, &fakeSlackAPI{}, "U1")

	err := rec.Reconcile(context.Background(), "badkey", workqueue.Options{})
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
	if workqueue.GetNonRetriableDetails(err) == nil {
		t.Error("expected NonRetriableError for invalid key format")
	}
}

func TestSlackReconcile_MissingDefaultRepo(t *testing.T) {
	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"mono": {FullName: "chainguard-dev/mono", BasePath: "/tmp/test"},
	}}
	rec := NewSlackReconciler(cfg, &fakeSlackAPI{}, "U1")

	err := rec.Reconcile(context.Background(), "C1:1700000000.1", workqueue.Options{})
	if err == nil {
		t.Fatal("expected error when slack.default_repo is unset")
	}
	if workqueue.GetNonRetriableDetails(err) == nil {
		t.Error("expected NonRetriableError for missing default repo")
	}
}

func TestSlackReconcile_UnknownRepo(t *testing.T) {
	cfg := &config.Config{
		Repos: map[string]config.RepoConfig{
			"mono": {FullName: "chainguard-dev/mono", BasePath: "/tmp/test"},
		},
		Slack: config.SlackConfig{DefaultRepo: "nonexistent"},
	}
	rec := NewSlackReconciler(cfg, &fakeSlackAPI{}, "U1")

	err := rec.Reconcile(context.Background(), "C1:1700000000.1", workqueue.Options{})
	if err == nil {
		t.Fatal("expected error for unknown repo")
	}
	if workqueue.GetNonRetriableDetails(err) == nil {
		t.Error("expected NonRetriableError for unknown repo")
	}
}

func TestSlackReconcile_BadStackFailsBeforeCreatingWorktree(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{
		Repos: map[string]config.RepoConfig{
			"mono": {FullName: "chainguard-dev/mono", BasePath: base, Stack: filepath.Join(base, "missing-stack")},
		},
		Slack: config.SlackConfig{DefaultRepo: "mono"},
	}
	rec := NewSlackReconciler(cfg, &fakeSlackAPI{}, "U1")

	err := rec.Reconcile(context.Background(), "C1:1700000000.1", workqueue.Options{})
	if err == nil || !strings.Contains(err.Error(), "repos.mono.stack") {
		t.Fatalf("Reconcile() error = %v, want one naming repos.mono.stack", err)
	}
	if workqueue.GetNonRetriableDetails(err) != nil {
		t.Error("a bad stack should be retriable: a config fix is hot-reloaded")
	}
	if entries, _ := os.ReadDir(base); len(entries) != 0 {
		t.Errorf("worktree directory created despite bad stack: %v", entries)
	}
}

func TestSlackReconcile_BadStackDMsOncePerError(t *testing.T) {
	base := t.TempDir()
	t.Setenv("PATH", base)
	cfg := &config.Config{
		Repos: map[string]config.RepoConfig{
			"mono": {FullName: "chainguard-dev/mono", BasePath: base, Stack: "app-work"},
		},
		Slack: config.SlackConfig{DefaultRepo: "mono"},
	}
	var dms []struct{ channel, text string }
	api := &fakeSlackAPI{
		permalinkFn: func(_ context.Context, _, _ string) (string, error) {
			return "https://slack.example/archives/C1/p1", nil
		},
		postMessageFn: func(_ context.Context, channel, text string) error {
			dms = append(dms, struct{ channel, text string }{channel, text})
			return nil
		},
	}
	rec := NewSlackReconciler(cfg, api, "U1")
	reconcile := func(key string) {
		t.Helper()
		if err := rec.Reconcile(context.Background(), key, workqueue.Options{}); err == nil {
			t.Fatal("Reconcile() succeeded with a stack that is not on PATH")
		}
	}

	// The dispatcher retries a failing key; the user hears about it once.
	reconcile("C1:1700000000.1")
	reconcile("C1:1700000000.1")
	if len(dms) != 1 {
		t.Fatalf("sent %d DMs for one failing task, want 1", len(dms))
	}
	if dms[0].channel != "U1" {
		t.Errorf("DM sent to %q, want the user U1", dms[0].channel)
	}
	for _, want := range []string{"`app-work`", "`repos.mono.stack`", "not found on PATH", "absolute path", "https://slack.example/archives/C1/p1"} {
		if !strings.Contains(dms[0].text, want) {
			t.Errorf("DM %q does not mention %q", dms[0].text, want)
		}
	}

	// A different error for the same task is new information.
	rec.SetConfig(&config.Config{
		Repos: map[string]config.RepoConfig{
			"mono": {FullName: "chainguard-dev/mono", BasePath: base, Stack: filepath.Join(base, "missing")},
		},
		Slack: config.SlackConfig{DefaultRepo: "mono"},
	})
	reconcile("C1:1700000000.1")
	if len(dms) != 2 || !strings.Contains(dms[1].text, "does not exist") {
		t.Fatalf("after the error changed, DMs = %d (last %q), want a second one naming the new error", len(dms), dms[len(dms)-1].text)
	}

	// Another task failing the same way gets its own DM.
	reconcile("C1:1700000001.1")
	if len(dms) != 3 {
		t.Errorf("sent %d DMs, want a third for a second task", len(dms))
	}
}

func TestSlackReconcile_BadStackDMFailureStillReturnsError(t *testing.T) {
	base := t.TempDir()
	cfg := &config.Config{
		Repos: map[string]config.RepoConfig{
			"mono": {FullName: "chainguard-dev/mono", BasePath: base, Stack: filepath.Join(base, "missing")},
		},
		Slack: config.SlackConfig{DefaultRepo: "mono"},
	}
	api := &fakeSlackAPI{
		permalinkFn: func(_ context.Context, _, _ string) (string, error) {
			return "", errors.New("permalink unavailable")
		},
		postMessageFn: func(_ context.Context, _, _ string) error {
			return errors.New("slack down")
		},
	}
	err := NewSlackReconciler(cfg, api, "U1").Reconcile(context.Background(), "C1:1700000000.1", workqueue.Options{})
	if err == nil || !strings.Contains(err.Error(), "resolving stack") {
		t.Fatalf("Reconcile() error = %v, want the stack error", err)
	}
}

func TestNotifyReady_NoOriginIsNoop(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	called := false
	fake := &fakeSlackAPI{postMessageFn: func(ctx context.Context, channel, text string) error {
		called = true
		return nil
	}}
	cfg := &config.Config{}
	rec := NewSlackReconciler(cfg, fake, "U1")
	// No origin recorded for this path — must not post anything.
	rec.NotifyReady(filepath.Join(tmpDir, "unknown-worktree"), "unknown-worktree", "zen work resume unknown-worktree")

	if called {
		t.Error("NotifyReady() posted a message for a worktree with no recorded Slack origin")
	}
}

func TestNotifyReady_WritesToRecordedOrigin(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	worktreePath := filepath.Join(tmpDir, "mono-slack-1700000000")
	if err := RecordSlackOrigin(SlackOrigin{
		ChannelID:    "C1",
		MessageTS:    "1700000000.1",
		Permalink:    "https://example.slack.com/archives/C1/p1",
		WorktreePath: worktreePath,
		WorktreeName: "mono-slack-1700000000",
	}); err != nil {
		t.Fatal(err)
	}

	var postedChannel, postedText string
	fake := &fakeSlackAPI{postMessageFn: func(ctx context.Context, channel, text string) error {
		postedChannel, postedText = channel, text
		return nil
	}}

	cfg := &config.Config{}
	rec := NewSlackReconciler(cfg, fake, "U1")
	rec.NotifyReady(worktreePath, "mono-slack-1700000000", "zen work resume mono-slack-1700000000")

	if postedChannel != "U1" {
		t.Errorf("posted channel = %q, want %q", postedChannel, "U1")
	}
	if !contains(postedText, "mono-slack-1700000000") {
		t.Errorf("posted text missing worktree name: %q", postedText)
	}
	if !contains(postedText, "https://example.slack.com/archives/C1/p1") {
		t.Errorf("posted text missing permalink: %q", postedText)
	}
}

func TestNotifyReady_DoneEmojiSkipsAndClearsOrigin(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	worktreePath := filepath.Join(tmpDir, "mono-slack-1700000000")
	if err := RecordSlackOrigin(SlackOrigin{
		ChannelID:    "C1",
		MessageTS:    "1700000000.1",
		WorktreePath: worktreePath,
		WorktreeName: "mono-slack-1700000000",
	}); err != nil {
		t.Fatal(err)
	}

	posted := false
	fake := &fakeSlackAPI{
		postMessageFn: func(ctx context.Context, channel, text string) error {
			posted = true
			return nil
		},
		hasReactionFn: func(ctx context.Context, channel, ts, name string) (bool, error) {
			return name == "done_check", nil
		},
	}

	cfg := &config.Config{}
	rec := NewSlackReconciler(cfg, fake, "U1")
	rec.NotifyReady(worktreePath, "mono-slack-1700000000", "zen work resume mono-slack-1700000000")

	if posted {
		t.Error("NotifyReady() sent a DM for a task already marked done via the done-emoji")
	}
	if _, ok := LoadSlackOrigins()[worktreePath]; ok {
		t.Error("NotifyReady() should clear the origin once the task is done")
	}
}

func TestPruneDone_RemovesDoneEmojiOrigin(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	donePath := filepath.Join(tmpDir, "mono-slack-done")
	pendingPath := filepath.Join(tmpDir, "mono-slack-pending")
	if err := RecordSlackOrigin(SlackOrigin{ChannelID: "C1", MessageTS: "1.1", WorktreePath: donePath, WorktreeName: "mono-slack-done"}); err != nil {
		t.Fatal(err)
	}
	if err := RecordSlackOrigin(SlackOrigin{ChannelID: "C2", MessageTS: "2.2", WorktreePath: pendingPath, WorktreeName: "mono-slack-pending"}); err != nil {
		t.Fatal(err)
	}

	fake := &fakeSlackAPI{
		hasReactionFn: func(ctx context.Context, channel, ts, name string) (bool, error) {
			return channel == "C1", nil // only the "done" origin has the reaction
		},
	}

	cfg := &config.Config{}
	rec := NewSlackReconciler(cfg, fake, "U1")
	if err := rec.PruneDone(context.Background()); err != nil {
		t.Fatalf("PruneDone() error: %v", err)
	}

	origins := LoadSlackOrigins()
	if _, ok := origins[donePath]; ok {
		t.Error("PruneDone() should have removed the done origin")
	}
	if _, ok := origins[pendingPath]; !ok {
		t.Error("PruneDone() should have kept the still-pending origin")
	}
}

func TestPruneDone_NoDoneOriginsIsNoop(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	if err := config.EnsureDirs(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(tmpDir, "mono-slack-pending")
	if err := RecordSlackOrigin(SlackOrigin{ChannelID: "C1", MessageTS: "1.1", WorktreePath: path, WorktreeName: "mono-slack-pending"}); err != nil {
		t.Fatal(err)
	}

	fake := &fakeSlackAPI{} // hasReactionFn nil -> always false
	cfg := &config.Config{}
	rec := NewSlackReconciler(cfg, fake, "U1")
	if err := rec.PruneDone(context.Background()); err != nil {
		t.Fatalf("PruneDone() error: %v", err)
	}

	if _, ok := LoadSlackOrigins()[path]; !ok {
		t.Error("PruneDone() should not remove an origin with no completion signal")
	}
}
