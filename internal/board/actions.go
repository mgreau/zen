package board

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

// launchTimeout bounds a single `zen review` launch triggered from the
// board — enough time to create a worktree and open a terminal tab.
const launchTimeout = 30 * time.Second

// openURL opens url in the default browser.
func openURL(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("opening URLs not supported on %s", runtime.GOOS)
	}
	return cmd.Start()
}

// launchReview shells out to `zen review <number> --repo <repo>`, reusing
// the exact worktree-creation and terminal-tab-opening flow a user gets by
// running that command directly. The board keeps running; the new agent
// session opens in its own tab.
func launchReview(repoShort string, prNumber int) error {
	bin, err := os.Executable()
	if err != nil {
		bin = "zen"
	}
	ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "review", strconv.Itoa(prNumber), "--repo", repoShort)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("zen review timed out after %s", launchTimeout)
		}
		msg := string(out)
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("zen review failed: %s", msg)
	}
	return nil
}
