package worktree

import (
	"os/exec"
	"strings"
	"time"
)

// LastActivity returns the date of the last commit in the worktree.
func LastActivity(path string) (time.Time, error) {
	cmd := exec.Command("git", "log", "-1", "--format=%ci")
	cmd.Dir = path
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, err
	}

	dateStr := strings.TrimSpace(string(out))
	if dateStr == "" {
		return time.Time{}, nil
	}

	// git log --format=%ci produces: "2024-01-15 14:30:00 -0800"
	t, err := time.Parse("2006-01-02 15:04:05 -0700", dateStr)
	if err != nil {
		// Try date-only
		t, err = time.Parse("2006-01-02", dateStr[:10])
	}
	return t, err
}

// AgeDays returns the age of a worktree in days based on its last commit.
func AgeDays(path string) (int, error) {
	last, err := LastActivity(path)
	if err != nil {
		return -1, err
	}
	if last.IsZero() {
		return -1, nil
	}
	days := int(time.Since(last).Hours() / 24)
	return days, nil
}

// AgeHours returns the age of a worktree in hours based on its last commit.
func AgeHours(path string) (int, error) {
	last, err := LastActivity(path)
	if err != nil {
		return -1, err
	}
	if last.IsZero() {
		return -1, nil
	}
	return int(time.Since(last).Hours()), nil
}
