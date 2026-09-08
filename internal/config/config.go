package config

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mgreau/zen/internal/agent"
	"gopkg.in/yaml.v3"
)

// Config holds the complete zen configuration.
type Config struct {
	Repos        map[string]RepoConfig `yaml:"repos"`
	WatchPaths   []string              `yaml:"watch_paths"`
	Authors      []string              `yaml:"authors"`
	PollInterval string                `yaml:"poll_interval"`
	Agent        string                `yaml:"agent"` // "claude" (default) or "codex"
	ClaudeBin    string                `yaml:"claude_bin"`
	CodexBin     string                `yaml:"codex_bin"`
	Terminal     string                `yaml:"terminal"` // "iterm", "ghostty", "kitty", or "macos"
	BranchPrefix string                `yaml:"branch_prefix"`
	IgnoreDrafts bool                  `yaml:"ignore_drafts"`
	Watch        WatchConfig           `yaml:"watch"`
	Slack        SlackConfig           `yaml:"slack"`
}

// SlackConfig holds configuration for the Slack task watcher: polling for the
// user's own emoji reaction on a message, then spinning up a feature worktree
// seeded with the thread as context. Disabled unless Enabled is explicitly
// set to true. The Slack token itself is never stored here — it's read from
// the ZEN_SLACK_TOKEN environment variable at daemon startup.
type SlackConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Emoji        string `yaml:"emoji"`         // reaction name (no colons) that flags a task, default "claudecode"
	DefaultRepo  string `yaml:"default_repo"`  // short repo name (from repos:) worktrees are created in
	PollInterval string `yaml:"poll_interval"` // default "5m"
	AckReaction  string `yaml:"ack_reaction"`  // reaction added on pickup, default "eyes"
	DoneEmoji    string `yaml:"done_emoji"`    // reaction (on the original message) or a merged PR from the worktree's branch stops further completion DMs, default "done_check"
}

// GetEmoji returns the configured reaction name, defaulting to "claudecode".
func (s SlackConfig) GetEmoji() string {
	if s.Emoji != "" {
		return s.Emoji
	}
	return "claudecode"
}

// GetAckReaction returns the reaction added on pickup, defaulting to "eyes".
func (s SlackConfig) GetAckReaction() string {
	if s.AckReaction != "" {
		return s.AckReaction
	}
	return "eyes"
}

// GetDoneEmoji returns the reaction name that marks a task done, defaulting
// to "done_check".
func (s SlackConfig) GetDoneEmoji() string {
	if s.DoneEmoji != "" {
		return s.DoneEmoji
	}
	return "done_check"
}

// PollIntervalDuration returns the Slack poll interval, defaulting to 5 minutes.
func (s SlackConfig) PollIntervalDuration() time.Duration {
	if s.PollInterval != "" {
		if d, err := time.ParseDuration(s.PollInterval); err == nil {
			return d
		}
	}
	return 5 * time.Minute
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
	if cfg.CodexBin == "" {
		cfg.CodexBin = "codex"
	}
	if cfg.Agent == "" {
		cfg.Agent = "claude" // default for backward compatibility
	}
	if cfg.Agent != "claude" && cfg.Agent != "codex" {
		return nil, fmt.Errorf("invalid agent %q: must be \"claude\" or \"codex\"", cfg.Agent)
	}
	if cfg.Terminal == "" {
		cfg.Terminal = "iterm" // default to iTerm for backward compatibility
	}
	if cfg.Terminal != "iterm" && cfg.Terminal != "ghostty" && cfg.Terminal != "kitty" && cfg.Terminal != "macos" {
		return nil, fmt.Errorf("invalid terminal type %q: must be \"iterm\", \"ghostty\", \"kitty\", or \"macos\"", cfg.Terminal)
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

// AgentKind returns the effective agent kind. A non-empty override (e.g. from
// a --agent flag) wins over the configured default.
func (c *Config) AgentKind(override string) string {
	if override != "" {
		return override
	}
	if c.Agent != "" {
		return c.Agent
	}
	return "claude"
}

// NewAgent builds the configured agent, honouring an optional override kind.
func (c *Config) NewAgent(override string) agent.Agent {
	kind := c.AgentKind(override)
	bin := c.ClaudeBin
	if kind == "codex" {
		bin = c.CodexBin
	}
	return agent.New(agent.Kind(kind), bin)
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

// AddRepo registers short → rc in ~/.zen/config.yaml, editing the YAML
// document in place so comments and formatting the user added survive.
// It reports whether the file was modified: false means an identical entry
// already exists. A short name or full name that is already configured with
// different values is an error rather than a silent overwrite.
func AddRepo(short string, rc RepoConfig) (bool, error) {
	yamlPath := filepath.Join(zenHome(), "config.yaml")
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return false, fmt.Errorf("config file not found: %s\nRun 'zen setup' to create it", yamlPath)
	}

	existing := &Config{}
	if err := yaml.Unmarshal(data, existing); err != nil {
		return false, fmt.Errorf("parsing %s: %w", yamlPath, err)
	}

	rc.BasePath = collapseHome(rc.BasePath)
	if prev, ok := existing.Repos[short]; ok {
		if prev.FullName == rc.FullName && expandHome(prev.BasePath) == expandHome(rc.BasePath) {
			return false, nil
		}
		return false, fmt.Errorf("repo %q is already configured (full_name: %s, base_path: %s) — edit %s to change it",
			short, prev.FullName, prev.BasePath, yamlPath)
	}
	for name, prev := range existing.Repos {
		if prev.FullName == rc.FullName {
			return false, fmt.Errorf("%s is already configured as %q (base_path: %s) — a repo can only be registered once",
				rc.FullName, name, prev.BasePath)
		}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return false, fmt.Errorf("parsing %s: %w", yamlPath, err)
	}
	if len(doc.Content) == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	repos := findOrCreateMapping(doc.Content[0], "repos")
	repos.Content = append(repos.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: short},
		&yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "full_name"},
			{Kind: yaml.ScalarNode, Value: rc.FullName},
			{Kind: yaml.ScalarNode, Value: "base_path"},
			{Kind: yaml.ScalarNode, Value: rc.BasePath},
		}},
	)

	var buf strings.Builder
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return false, fmt.Errorf("marshalling config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return false, fmt.Errorf("marshalling config: %w", err)
	}

	// Write via a temp file + rename so an interrupted write can never
	// truncate the config, keeping the file's original permissions.
	fi, err := os.Stat(yamlPath)
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", yamlPath, err)
	}
	tmpPath := yamlPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(buf.String()), fi.Mode().Perm()); err != nil {
		return false, fmt.Errorf("writing %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, yamlPath); err != nil {
		os.Remove(tmpPath)
		return false, fmt.Errorf("replacing %s: %w", yamlPath, err)
	}
	return true, nil
}

// findOrCreateMapping returns the value node for key in the mapping root,
// creating an empty mapping (or converting a null `key:` entry) as needed.
func findOrCreateMapping(root *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == key {
			val := root.Content[i+1]
			if val.Kind != yaml.MappingNode {
				*val = yaml.Node{Kind: yaml.MappingNode}
			}
			return val
		}
	}
	val := &yaml.Node{Kind: yaml.MappingNode}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, val)
	return val
}

// collapseHome rewrites an absolute path under $HOME to the ~/ form, which
// is how base paths are conventionally written in the config file.
func collapseHome(p string) string {
	home := os.Getenv("HOME")
	if home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// expandHome is the inverse of collapseHome, for comparing paths that may
// be written in either form.
func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(os.Getenv("HOME"), p[2:])
	}
	return p
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
