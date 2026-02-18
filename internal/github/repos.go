package github

import "github.com/mgreau/zen/internal/config"

// RepoFullName maps short repo name to full GitHub path using config.
func RepoFullName(cfg *config.Config, short string) string {
	return cfg.RepoFullName(short)
}

// RepoShortName maps full GitHub path to short repo name using config.
func RepoShortName(cfg *config.Config, full string) string {
	return cfg.RepoShortName(full)
}
