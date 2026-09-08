package cmd

import (
	"fmt"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/gitrepo"
	"github.com/mgreau/zen/internal/ui"
	"github.com/spf13/cobra"
)

var repoCmd = &cobra.Command{
	Use:   "repo",
	Short: "Manage repositories in ~/.zen/config.yaml",
}

var repoAddCmd = &cobra.Command{
	Use:   "add [path]",
	Short: "Register a local clone with zen",
	Long: `Register a local clone in ~/.zen/config.yaml so zen can create worktrees for it.

Everything is inferred from the clone: the short name is the clone's directory
name, the base path (where worktrees are created) is its parent directory, and
the GitHub full name comes from the upstream remote, falling back to origin.

Run it with no arguments from inside the clone, or pass the clone's path.
Adding the same repo twice is a no-op, and the watch daemon picks up the new
repo on its next poll.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRepoAdd,
}

func init() {
	repoCmd.AddCommand(repoAddCmd)
	rootCmd.AddCommand(repoCmd)
}

func runRepoAdd(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}

	info, err := gitrepo.Detect(dir)
	if err != nil {
		return err
	}

	added, err := config.AddRepo(info.Short, config.RepoConfig{
		FullName: info.FullName,
		BasePath: info.BasePath,
	})
	if err != nil {
		return err
	}
	if !added {
		ui.LogInfo(fmt.Sprintf("%s is already registered as %q", info.FullName, info.Short))
		return nil
	}

	home := homeDir()
	ui.LogSuccess(fmt.Sprintf("Registered %q → %s (from %s remote)", info.Short, info.FullName, info.Remote))
	fmt.Printf("  Worktrees will be created in %s\n", ui.ShortenHome(info.BasePath, home))
	return nil
}
