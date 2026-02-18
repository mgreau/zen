package reconciler

import (
	"fmt"
	"strconv"
	"strings"
)

// MakePRKey creates a workqueue key for a PR in the format "repo:number".
func MakePRKey(repo string, prNumber int) string {
	return fmt.Sprintf("%s:%d", repo, prNumber)
}

// ParsePRKey parses a workqueue key back into repo and PR number.
func ParsePRKey(key string) (repo string, number int, err error) {
	parts := strings.SplitN(key, ":", 2)
	if len(parts) != 2 {
		return "", 0, fmt.Errorf("invalid PR key %q: expected format repo:number", key)
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("invalid PR key %q: bad number: %w", key, err)
	}
	return parts[0], n, nil
}
