package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoFullName(t *testing.T) {
	cfg := &Config{
		Repos: map[string]RepoConfig{
			"mono":           {FullName: "chainguard-dev/mono", BasePath: "/tmp/test"},
			"os":             {FullName: "wolfi-dev/os", BasePath: "/tmp/test"},
			"images-private": {FullName: "chainguard-images/images-private", BasePath: "/tmp/test"},
		},
	}
	tests := []struct {
		short string
		want  string
	}{
		{"mono", "chainguard-dev/mono"},
		{"os", "wolfi-dev/os"},
		{"images-private", "chainguard-images/images-private"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.short, func(t *testing.T) {
			got := cfg.RepoFullName(tt.short)
			if got != tt.want {
				t.Errorf("RepoFullName(%q) = %q, want %q", tt.short, got, tt.want)
			}
		})
	}
}

func TestRepoShortName(t *testing.T) {
	cfg := &Config{
		Repos: map[string]RepoConfig{
			"mono":           {FullName: "chainguard-dev/mono", BasePath: "/tmp/test"},
			"os":             {FullName: "wolfi-dev/os", BasePath: "/tmp/test"},
			"images-private": {FullName: "chainguard-images/images-private", BasePath: "/tmp/test"},
		},
	}
	tests := []struct {
		full string
		want string
	}{
		{"chainguard-dev/mono", "mono"},
		{"wolfi-dev/os", "os"},
		{"chainguard-images/images-private", "images-private"},
		{"unknown/repo", "repo"},
	}

	for _, tt := range tests {
		t.Run(tt.full, func(t *testing.T) {
			got := cfg.RepoShortName(tt.full)
			if got != tt.want {
				t.Errorf("RepoShortName(%q) = %q, want %q", tt.full, got, tt.want)
			}
		})
	}
}

func TestIsAuthor(t *testing.T) {
	cfg := &Config{
		Authors: []string{"alice", "bob"},
	}

	if !cfg.IsAuthor("alice") {
		t.Error("IsAuthor(alice) should be true")
	}
	if cfg.IsAuthor("nobody") {
		t.Error("IsAuthor(nobody) should be false")
	}
}

func TestRepoNames(t *testing.T) {
	cfg := &Config{
		Repos: map[string]RepoConfig{
			"a": {FullName: "org/a"},
			"b": {FullName: "org/b"},
		},
	}
	names := cfg.RepoNames()
	if len(names) != 2 {
		t.Errorf("RepoNames() returned %d names, want 2", len(names))
	}
}

func TestLoadYAML(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	zenDir := filepath.Join(tmpDir, ".zen")
	os.MkdirAll(zenDir, 0o755)

	yamlContent := `repos:
  test-repo:
    full_name: org/test-repo
    base_path: ~/git/test
watch_paths:
  - src
authors:
  - testuser
poll_interval: 10m
claude_bin: /usr/local/bin/claude
`
	os.WriteFile(filepath.Join(zenDir, "config.yaml"), []byte(yamlContent), 0o644)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if _, ok := cfg.Repos["test-repo"]; !ok {
		t.Error("config should contain test-repo")
	}

	if cfg.Repos["test-repo"].FullName != "org/test-repo" {
		t.Errorf("test-repo full_name = %q, want %q", cfg.Repos["test-repo"].FullName, "org/test-repo")
	}

	expectedBase := filepath.Join(tmpDir, "git/test")
	if cfg.Repos["test-repo"].BasePath != expectedBase {
		t.Errorf("test-repo base_path = %q, want %q", cfg.Repos["test-repo"].BasePath, expectedBase)
	}

	if len(cfg.WatchPaths) != 1 || cfg.WatchPaths[0] != "src" {
		t.Errorf("WatchPaths = %v, want [src]", cfg.WatchPaths)
	}

	if len(cfg.Authors) != 1 || cfg.Authors[0] != "testuser" {
		t.Errorf("Authors = %v, want [testuser]", cfg.Authors)
	}

	if cfg.PollInterval != "10m" {
		t.Errorf("PollInterval = %q, want %q", cfg.PollInterval, "10m")
	}

	if cfg.ClaudeBin != "/usr/local/bin/claude" {
		t.Errorf("ClaudeBin = %q, want %q", cfg.ClaudeBin, "/usr/local/bin/claude")
	}
}

func TestLoadDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	zenDir := filepath.Join(tmpDir, ".zen")
	os.MkdirAll(zenDir, 0o755)

	// Minimal config — only repos
	yamlContent := `repos:
  mono:
    full_name: chainguard-dev/mono
    base_path: /tmp/mono
`
	os.WriteFile(filepath.Join(zenDir, "config.yaml"), []byte(yamlContent), 0o644)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.PollInterval != "5m" {
		t.Errorf("PollInterval default = %q, want %q", cfg.PollInterval, "5m")
	}
	if cfg.ClaudeBin != "claude" {
		t.Errorf("ClaudeBin default = %q, want %q", cfg.ClaudeBin, "claude")
	}
}

func TestLoadMissingConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	_, err := Load()
	if err == nil {
		t.Fatal("Load() should return error when config file is missing")
	}
}

func TestEnsureDirs(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	err := EnsureDirs()
	if err != nil {
		t.Fatalf("EnsureDirs() error: %v", err)
	}

	stateDir := filepath.Join(tmpDir, ".zen", "state")
	if _, err := os.Stat(stateDir); os.IsNotExist(err) {
		t.Error("state dir was not created")
	}
}

func TestExpandPaths(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfg := &Config{
		Repos: map[string]RepoConfig{
			"test": {FullName: "org/test", BasePath: "~/git/test"},
		},
	}
	cfg.expandPaths()

	expected := filepath.Join(tmpDir, "git/test")
	if cfg.Repos["test"].BasePath != expected {
		t.Errorf("expandPaths: got %q, want %q", cfg.Repos["test"].BasePath, expected)
	}
}

func TestWatchConfigDefaults(t *testing.T) {
	w := WatchConfig{}

	if d := w.DispatchIntervalDuration(); d.String() != "10s" {
		t.Errorf("DispatchIntervalDuration default = %v, want 10s", d)
	}
	if d := w.CleanupIntervalDuration(); d.String() != "1h0m0s" {
		t.Errorf("CleanupIntervalDuration default = %v, want 1h0m0s", d)
	}
	if n := w.GetCleanupAfterDays(); n != 5 {
		t.Errorf("GetCleanupAfterDays default = %d, want 5", n)
	}
	if n := w.GetConcurrency(); n != 2 {
		t.Errorf("GetConcurrency default = %d, want 2", n)
	}
	if n := w.GetMaxRetries(); n != 5 {
		t.Errorf("GetMaxRetries default = %d, want 5", n)
	}
}

func TestWatchConfigCustom(t *testing.T) {
	w := WatchConfig{
		DispatchInterval: "30s",
		CleanupInterval:  "2h",
		CleanupAfterDays: 10,
		Concurrency:      4,
		MaxRetries:       3,
	}

	if d := w.DispatchIntervalDuration(); d.String() != "30s" {
		t.Errorf("DispatchIntervalDuration = %v, want 30s", d)
	}
	if d := w.CleanupIntervalDuration(); d.String() != "2h0m0s" {
		t.Errorf("CleanupIntervalDuration = %v, want 2h0m0s", d)
	}
	if n := w.GetCleanupAfterDays(); n != 10 {
		t.Errorf("GetCleanupAfterDays = %d, want 10", n)
	}
	if n := w.GetConcurrency(); n != 4 {
		t.Errorf("GetConcurrency = %d, want 4", n)
	}
	if n := w.GetMaxRetries(); n != 3 {
		t.Errorf("GetMaxRetries = %d, want 3", n)
	}
}
