package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the complete zen configuration.
type Config struct {
	Repos        map[string]RepoConfig `yaml:"repos"`
	WatchPaths   []string              `yaml:"watch_paths"`
	Authors      []string              `yaml:"authors"`
	PollInterval string                `yaml:"poll_interval"`
	ClaudeBin    string                `yaml:"claude_bin"`
	Terminal     string                `yaml:"terminal"` // "iterm" or "ghostty"
	BranchPrefix string                `yaml:"branch_prefix"`
	Watch        WatchConfig           `yaml:"watch"`
}

// WatchConfig holds configuration for the watch daemon's workqueue behavior.
type WatchConfig struct {
	DispatchInterval    string `yaml:"dispatch_interval"`     // default "10s"
	CleanupInterval     string `yaml:"cleanup_interval"`      // default "1h"
	SessionScanInterval string `yaml:"session_scan_interval"` // default "10s"
	CleanupAfterDays    int    `yaml:"cleanup_after_days"`    // default 5
	Concurrency         int    `yaml:"concurrency"`           // default 2
	MaxRetries          int    `yaml:"max_retries"`           // default 5
	DigestInterval      string `yaml:"digest_interval"`       // "" = disabled, e.g. "2h"
}

// DispatchIntervalDuration returns the dispatch interval as a time.Duration,
// falling back to the default of 10 seconds.
func (w WatchConfig) DispatchIntervalDuration() time.Duration {
	if w.DispatchInterval != "" {
		if d, err := time.ParseDuration(w.DispatchInterval); err == nil {
			return d
		}
	}
	return 10 * time.Second
}

// CleanupIntervalDuration returns the cleanup interval as a time.Duration,
// falling back to the default of 1 hour.
func (w WatchConfig) CleanupIntervalDuration() time.Duration {
	if w.CleanupInterval != "" {
		if d, err := time.ParseDuration(w.CleanupInterval); err == nil {
			return d
		}
	}
	return 1 * time.Hour
}

// GetCleanupAfterDays returns CleanupAfterDays with a default of 5.
func (w WatchConfig) GetCleanupAfterDays() int {
	if w.CleanupAfterDays > 0 {
		return w.CleanupAfterDays
	}
	return 5
}

// GetConcurrency returns the concurrency limit with a default of 2.
func (w WatchConfig) GetConcurrency() int {
	if w.Concurrency > 0 {
		return w.Concurrency
	}
	return 2
}

// GetMaxRetries returns the max retries with a default of 5.
func (w WatchConfig) GetMaxRetries() int {
	if w.MaxRetries > 0 {
		return w.MaxRetries
	}
	return 5
}

// DigestIntervalDuration returns the digest interval duration and whether it is enabled.
// An empty DigestInterval string disables the digest (returns 0, false).
func (w WatchConfig) DigestIntervalDuration() (time.Duration, bool) {
	if w.DigestInterval == "" {
		return 0, false
	}
	d, err := time.ParseDuration(w.DigestInterval)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

// SessionScanIntervalDuration returns the session scan interval as a time.Duration,
// falling back to the default of 10 seconds.
func (w WatchConfig) SessionScanIntervalDuration() time.Duration {
	if w.SessionScanInterval != "" {
		if d, err := time.ParseDuration(w.SessionScanInterval); err == nil {
			return d
		}
	}
	return 10 * time.Second
}

// RepoConfig holds per-repository configuration.
type RepoConfig struct {
	FullName string `yaml:"full_name"`
	BasePath string `yaml:"base_path"`
}

// zenHome returns the path to ~/.zen.
func zenHome() string {
	return filepath.Join(os.Getenv("HOME"), ".zen")
}

// Load reads the YAML config from ~/.zen/config.yaml.
// Returns an error if the config file does not exist or is invalid.
func Load() (*Config, error) {
	yamlPath := filepath.Join(zenHome(), "config.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("config file not found: %s\nRun 'zen setup' to create it", yamlPath)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", yamlPath, err)
	}

	// Apply defaults for optional fields
	if cfg.PollInterval == "" {
		cfg.PollInterval = "5m"
	}
	if cfg.ClaudeBin == "" {
		cfg.ClaudeBin = "claude"
	}
	if cfg.Terminal == "" {
		cfg.Terminal = "iterm" // default to iTerm for backward compatibility
	}
	if cfg.Terminal != "iterm" && cfg.Terminal != "ghostty" {
		return nil, fmt.Errorf("invalid terminal type %q: must be \"iterm\" or \"ghostty\"", cfg.Terminal)
	}
	if cfg.Repos == nil {
		cfg.Repos = make(map[string]RepoConfig)
	}

	cfg.expandPaths()
	return cfg, nil
}

// GetTerminal returns the configured terminal type.
func (c *Config) GetTerminal() string {
	return c.Terminal
}

// GetBranchPrefix returns the prefix for feature branch names.
// Falls back to git config user.name (with spaces replaced by hyphens), then empty string.
func (c *Config) GetBranchPrefix() string {
	if c.BranchPrefix != "" {
		return c.BranchPrefix
	}
	// Try git config user.name; replace spaces so the prefix is branch-safe.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "config", "user.name").Output()
	if err == nil {
		name := strings.ReplaceAll(strings.TrimSpace(string(out)), " ", "-")
		if name != "" {
			return name
		}
	}
	return ""
}

// expandPaths replaces ~ with $HOME in base paths.
func (c *Config) expandPaths() {
	home := os.Getenv("HOME")
	for name, repo := range c.Repos {
		if strings.HasPrefix(repo.BasePath, "~/") {
			repo.BasePath = filepath.Join(home, repo.BasePath[2:])
			c.Repos[name] = repo
		}
	}
}

// RepoNames returns all configured short repo names.
func (c *Config) RepoNames() []string {
	names := make([]string, 0, len(c.Repos))
	for name := range c.Repos {
		names = append(names, name)
	}
	return names
}

// RepoFullName maps a short name to full GitHub owner/repo.
func (c *Config) RepoFullName(short string) string {
	if repo, ok := c.Repos[short]; ok {
		return repo.FullName
	}
	return short
}

// RepoShortName maps a full GitHub owner/repo to short name.
func (c *Config) RepoShortName(full string) string {
	for name, repo := range c.Repos {
		if repo.FullName == full {
			return name
		}
	}
	// Fallback: return last path component
	parts := strings.Split(full, "/")
	return parts[len(parts)-1]
}

// RepoBasePath returns the local base path for a repo (the parent dir
// that contains the main clone directory).
func (c *Config) RepoBasePath(short string) string {
	if repo, ok := c.Repos[short]; ok {
		return repo.BasePath
	}
	return ""
}

// AllBasePaths returns all configured repo base paths.
func (c *Config) AllBasePaths() []string {
	paths := make([]string, 0, len(c.Repos))
	for _, repo := range c.Repos {
		paths = append(paths, repo.BasePath)
	}
	return paths
}

// IsAuthor returns true if the given login is in the authors list.
func (c *Config) IsAuthor(login string) bool {
	for _, a := range c.Authors {
		if a == login {
			return true
		}
	}
	return false
}

// StateDir returns the path to the zen state directory.
func StateDir() string {
	return filepath.Join(zenHome(), "state")
}

// EnsureDirs creates required zen directories.
func EnsureDirs() error {
	dirs := []string{
		StateDir(),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}
