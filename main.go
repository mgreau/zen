package main

import (
	"embed"
	"fmt"
	"os"

	"github.com/mgreau/zen/cmd"
)

//go:embed commands/*
var embeddedCommands embed.FS

func main() {
	cmd.EmbeddedCommands = embeddedCommands

	if err := cmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
