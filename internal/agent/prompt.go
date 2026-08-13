package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

// ensurePromptFile installs a slash-command prompt named <name>.md into dir if
// it is not already present. It reports whether a file was written.
func ensurePromptFile(dir, name string, content []byte) (bool, error) {
	dst := filepath.Join(dir, name+".md")
	if _, err := os.Stat(dst); err == nil {
		return false, nil // already installed
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.WriteFile(dst, content, 0o644); err != nil {
		return false, fmt.Errorf("writing %s: %w", dst, err)
	}
	return true, nil
}
