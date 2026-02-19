package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version and build info",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("zen %s (commit: %s)\n", Version, Commit)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
