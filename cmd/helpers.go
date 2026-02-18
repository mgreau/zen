package cmd

import "os"

// homeDir returns the user's home directory.
func homeDir() string {
	return os.Getenv("HOME")
}
