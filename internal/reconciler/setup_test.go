package reconciler

import (
	"context"
	"testing"

	"chainguard.dev/driftlessaf/workqueue"
	"chainguard.dev/driftlessaf/workqueue/dispatcher"
	"chainguard.dev/driftlessaf/workqueue/inmem"
	"github.com/mgreau/zen/internal/config"
	ghpkg "github.com/mgreau/zen/internal/github"
)

func TestMakePRKey(t *testing.T) {
	tests := []struct {
		repo   string
		number int
		want   string
	}{
		{"mono", 31414, "mono:31414"},
		{"os", 1, "os:1"},
		{"infra-images", 999, "infra-images:999"},
	}
	for _, tt := range tests {
		got := MakePRKey(tt.repo, tt.number)
		if got != tt.want {
			t.Errorf("MakePRKey(%q, %d) = %q, want %q", tt.repo, tt.number, got, tt.want)
		}
	}
}

func TestParsePRKey(t *testing.T) {
	tests := []struct {
		key      string
		wantRepo string
		wantNum  int
		wantErr  bool
	}{
		{"mono:31414", "mono", 31414, false},
		{"os:1", "os", 1, false},
		{"infra-images:999", "infra-images", 999, false},
		{"invalid", "", 0, true},
		{"mono:abc", "", 0, true},
		{"", "", 0, true},
		{":123", "", 123, false},
	}
	for _, tt := range tests {
		repo, num, err := ParsePRKey(tt.key)
		if (err != nil) != tt.wantErr {
			t.Errorf("ParsePRKey(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if repo != tt.wantRepo || num != tt.wantNum {
			t.Errorf("ParsePRKey(%q) = (%q, %d), want (%q, %d)", tt.key, repo, num, tt.wantRepo, tt.wantNum)
		}
	}
}

func TestReconcile_InvalidKey(t *testing.T) {
	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"mono": {FullName: "chainguard-dev/mono", BasePath: "/tmp/test"},
	}}
	rec := NewSetupReconciler(cfg)

	err := rec.Reconcile(context.Background(), "badkey", workqueue.Options{})
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
	if workqueue.GetNonRetriableDetails(err) == nil {
		t.Error("expected NonRetriableError for invalid key format")
	}
}

func TestReconcile_UnknownRepo(t *testing.T) {
	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"mono": {FullName: "chainguard-dev/mono", BasePath: "/tmp/test"},
	}}
	rec := NewSetupReconciler(cfg)

	// Store PR data so we pass the key parse step but fail on unknown repo
	rec.StorePRData("nonexistent:123", ghpkg.ReviewRequest{
		Number: 123,
		Title:  "Test PR",
		Author: ghpkg.AuthorInfo{Login: "testuser"},
	})

	err := rec.Reconcile(context.Background(), "nonexistent:123", workqueue.Options{})
	if err == nil {
		t.Fatal("expected error for unknown repo")
	}
	if workqueue.GetNonRetriableDetails(err) == nil {
		t.Error("expected NonRetriableError for unknown repo")
	}
}

func TestReconcile_MissingPRData(t *testing.T) {
	cfg := &config.Config{Repos: map[string]config.RepoConfig{
		"mono": {FullName: "chainguard-dev/mono", BasePath: "/tmp/test"},
	}}
	rec := NewSetupReconciler(cfg)

	err := rec.Reconcile(context.Background(), "mono:123", workqueue.Options{})
	if err == nil {
		t.Fatal("expected error for missing PR data")
	}
	if workqueue.GetNonRetriableDetails(err) == nil {
		t.Error("expected NonRetriableError for missing PR data")
	}
}

func TestDispatcherIntegration(t *testing.T) {
	queue := inmem.NewWorkQueue(10)
	ctx := context.Background()

	called := false
	callback := func(ctx context.Context, key string, opts workqueue.Options) error {
		if key != "test:1" {
			t.Errorf("unexpected key: %q", key)
		}
		called = true
		return nil
	}

	if err := queue.Queue(ctx, "test:1", workqueue.Options{Priority: 1}); err != nil {
		t.Fatalf("Queue() error: %v", err)
	}

	if err := dispatcher.HandleAsync(ctx, queue, 1, 1, callback, 3)(); err != nil {
		t.Fatalf("HandleAsync() error: %v", err)
	}

	if !called {
		t.Error("callback was not called")
	}
}
