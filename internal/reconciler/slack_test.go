package reconciler

import (
	"context"
	"path/filepath"
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
