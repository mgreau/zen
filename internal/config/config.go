package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
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
	CleanupAfterMerge   string `yaml:"cleanup_after_merge"`   // default "1h"
	Concurrency         int    `yaml:"concurrency"`           // default 2
	MaxRetries          int    `yaml:"max_retries"`           // default 5
	DigestInterval      string `yaml:"digest_interval"`       // "" = disabled, e.g. "2h"

	// Deprecated: merged-review cleanup is gated by CleanupAfterMerge. Still
	// parsed so existing config files load; the daemon no longer reads it.
	CleanupAfterDays int `yaml:"cleanup_after_days,omitempty"`
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

// CleanupAfterMergeDuration returns how long after a PR merges (GitHub's
// mergedAt) the daemon waits before removing its review worktree, defaulting
// to 1 hour. Zero removes it on the next cleanup scan. An unparsable or
// negative value falls back to the default.
func (w WatchConfig) CleanupAfterMergeDuration() time.Duration {
	if w.CleanupAfterMerge != "" {
		if d, err := time.ParseDuration(w.CleanupAfterMerge); err == nil && d >= 0 {
			return d
		}
	}
	return 1 * time.Hour
}

// GetCleanupAfterDays returns CleanupAfterDays with a default of 5.
//
// Deprecated: merged-review cleanup uses CleanupAfterMergeDuration.
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

// Dir is the zen config and state directory. ZEN_HOME overrides the default
// ~/.zen so a test harness (or a second install) does not share last_check.json
// or config.yaml with a day-to-day daemon.
func Dir() string {
	if d := strings.TrimSpace(os.Getenv("ZEN_HOME")); d != "" {
		return d
	}
	return filepath.Join(os.Getenv("HOME"), ".zen")
}

func zenHome() string {
	return Dir()
}

// Load reads the YAML config from ~/.zen/config.yaml.
// Returns an error if the config file does not exist or is invalid.
func Load() (*Config, error) {
	yamlPath := Path()
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, fmt.Errorf("config file not found: %s\nRun 'zen setup' to create it", yamlPath)
	}

	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", yamlPath, err)
	}
	return cfg, nil
}

// Path is the config file zen reads: config.yaml under Dir.
func Path() string {
	return filepath.Join(zenHome(), "config.yaml")
}

// Parse decodes config YAML, applies defaults for optional fields, validates
// the agent and terminal, and expands ~ in base paths. Load uses it for the
// config file; zen setup uses it to check a config before writing one.
func Parse(data []byte) (*Config, error) {
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return finish(cfg)
}

// ParseStrict is Parse, except that a key zen does not know (a typo such as
// "authers") is an error instead of being ignored. zen setup uses it for a
// config it installs; Load stays lenient so the daemon's live reload keeps
// working while a config is being edited.
func ParseStrict(data []byte) (*Config, error) {
	cfg := &Config{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	err := dec.Decode(cfg)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if err == nil {
		// Load reads only the first document, so a second one would be
		// installed without ever being checked or used.
		var extra yaml.Node
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			if err != nil {
				return nil, err
			}
			return nil, errors.New("config holds more than one YAML document (---); zen reads only the first")
		}
	}
	return finish(cfg)
}

// finish applies defaults, validates the agent and terminal, and expands
// paths on a decoded config.
func finish(cfg *Config) (*Config, error) {
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
		cfg.Terminal = defaultTerminal()
	}
	switch cfg.Terminal {
	case "iterm", "ghostty", "kitty", "macos", "tmux":
	default:
		return nil, fmt.Errorf("invalid terminal type %q: must be \"iterm\", \"ghostty\", \"kitty\", \"macos\", or \"tmux\"", cfg.Terminal)
	}
	if cfg.Repos == nil {
		cfg.Repos = make(map[string]RepoConfig)
	}

	cfg.expandPaths()
	return cfg, nil
}

// repoShortName is what a repo's short name may look like. It names worktree
// directories (<short>-pr-N), so it cannot hold a path separator or space or
// start like a flag.
var repoShortName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidateRepos reports a config zen cannot work with: no repos, a short name
// that cannot name a worktree directory, or a repo without an owner/name
// full_name or a base_path. Load does not enforce this
// (a hand-edited config may be mid-change and the daemon reloads it live);
// zen setup does, so a provisioned machine starts with a usable config.
func (c *Config) ValidateRepos() error {
	if len(c.Repos) == 0 {
		return fmt.Errorf("no repos configured")
	}
	for _, name := range c.sortedRepoNames() {
		if err := ValidateRepoShortName(name); err != nil {
			return err
		}
		repo := c.Repos[name]
		if err := ValidateRepoFullName(repo.FullName); err != nil {
			return fmt.Errorf("repo %q: %w", name, err)
		}
		if strings.TrimSpace(repo.BasePath) == "" {
			return fmt.Errorf("repo %q: base_path is required", name)
		}
	}
	return nil
}

// ValidateRepoShortName reports a repo short name that cannot name a worktree
// directory.
func ValidateRepoShortName(name string) error {
	if !repoShortName.MatchString(name) {
		return fmt.Errorf("repo short name %q must be letters, digits, '.', '_' or '-', starting with a letter or digit", name)
	}
	return nil
}

// ValidateRepoFullName reports a full_name that is not owner/name.
func ValidateRepoFullName(full string) error {
	owner, rest, ok := strings.Cut(full, "/")
	if !ok || owner == "" || rest == "" || strings.Contains(rest, "/") {
		return fmt.Errorf("full_name %q must be owner/name", full)
	}
	return nil
}

func (c *Config) sortedRepoNames() []string {
	names := c.RepoNames()
	sort.Strings(names)
	return names
}

// GetTerminal returns the configured terminal type.
func (c *Config) GetTerminal() string {
	return c.Terminal
}

// defaultTerminal is the terminal used when the config names none: tmux when
// zen runs inside a tmux session, such as on a workstation reached over ssh
// with no terminal application to drive, and iTerm otherwise, as before.
func defaultTerminal() string {
	if os.Getenv("TMUX") != "" {
		return "tmux"
	}
	return "iterm"
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

// RepoFullNames lists every configured repository as owner/repo, sorted so a
// poll queries them in a stable order. Repos with no full_name are skipped:
// they cannot be scoped in a GitHub search.
func (c *Config) RepoFullNames() []string {
	full := make([]string, 0, len(c.Repos))
	for _, repo := range c.Repos {
		if repo.FullName == "" {
			continue
		}
		full = append(full, repo.FullName)
	}
	sort.Strings(full)
	return full
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

// ConfiguredRepo returns the config short name for a GitHub owner/repo.
// Unlike RepoShortName, it does not fall back to the last path component, so
// an unconfigured repo is not mistaken for a configured one that happens to
// share a name.
func (c *Config) ConfiguredRepo(full string) (string, bool) {
	for name, repo := range c.Repos {
		if repo.FullName == full {
			return name, true
		}
	}
	return "", false
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
