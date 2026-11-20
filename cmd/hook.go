package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/prcache"
	"github.com/mgreau/zen/internal/tmux"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/spf13/cobra"
)

// hookCmd groups commands that agents run as hooks. zen passes them to the
// agent itself (see agent.WithTmuxStatus); they are not meant to be run by
// hand, so they are hidden.
var hookCmd = &cobra.Command{
	Use:    "hook",
	Short:  "Commands agents run as hooks",
	Hidden: true,
}

var hookNotifyCmd = &cobra.Command{
	Use:   "notify",
	Short: "Claude Notification/Stop hook: send a desktop notification through tmux",
	Long: `Reads a Claude Code Notification or Stop hook's input on stdin and sends a
desktop notification naming the worktree, through tmux to the terminal you
ssh from. It never fails, so it cannot get in Claude's way: problems are
reported on stderr.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := runHookNotify(os.Stdin); err != nil {
			fmt.Fprintf(os.Stderr, "zen hook notify: %v\n", err)
		}
		return nil
	},
}

var hookSessionCreatedCmd = &cobra.Command{
	Use:   "session-created <session-id>",
	Short: "tmux session-created hook: the first window's command and the Agents window",
	Long: `Run by the session-created hook in zen's tmux settings (zen setup tmux) for
each new session. As config.yaml's tmux section says, it types a command,
"zen inbox" by default, into the session's first window, and opens an Agents
window. It never fails: problems are reported on stderr.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := runHookSessionCreated(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "zen hook session-created: %v\n", err)
		}
		return nil
	},
}

func init() {
	hookCmd.AddCommand(hookNotifyCmd)
	hookCmd.AddCommand(hookSessionCreatedCmd)
	rootCmd.AddCommand(hookCmd)
}

func runHookSessionCreated(session string) error {
	c, err := config.Load()
	if err != nil {
		return err // without a config, zen inbox would only print the error
	}
	first, agents := sessionCommands(c, zenCommand())
	return tmux.SetUpSession(session, first, agents, c.Tmux.GetAgentsWindowDir())
}

// sessionCommands returns the commands a new tmux session gets, as c says:
// one typed into its first window, and one for the Agents window. Empty
// means none.
func sessionCommands(c *config.Config, zen string) (first, agents string) {
	return c.Tmux.FirstWindow(zen), c.Tmux.AgentsWindowCmd(c.NewAgent(""))
}

// zenCommand returns how to run this zen from a shell: "zen" when that is
// what PATH finds, so the typed command reads naturally, else its full path.
func zenCommand() string {
	self, err := os.Executable()
	if err != nil {
		return "zen"
	}
	if onPath, err := exec.LookPath("zen"); err == nil && sameFile(onPath, self) {
		return "zen"
	}
	return "'" + strings.ReplaceAll(self, "'", `'\''`) + "'"
}

func sameFile(a, b string) bool {
	ia, errA := os.Stat(a)
	ib, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ia, ib)
}

// hookInput is the part of a Claude Code hook's input zen reads.
type hookInput struct {
	Event                string `json:"hook_event_name"`
	Cwd                  string `json:"cwd"`
	Message              string `json:"message"`
	NotificationType     string `json:"notification_type"`
	LastAssistantMessage string `json:"last_assistant_message"`
	StopHookActive       bool   `json:"stop_hook_active"`
}

// maxHookInput bounds what zen reads from the hook: the last message of a
// long turn is the largest part, and only its start is shown.
const maxHookInput = 4 << 20

func runHookNotify(stdin io.Reader) error {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return nil // not in tmux: nowhere to send it
	}
	var in hookInput
	if err := json.NewDecoder(io.LimitReader(stdin, maxHookInput)).Decode(&in); err != nil {
		return fmt.Errorf("reading hook input: %w", err)
	}

	// A hook must work even if the config doesn't load: fall back to the
	// defaults, and name the worktree after its directory.
	var n config.NotifyConfig
	c, err := config.Load()
	if err == nil {
		n = c.Notify
	}
	if !n.TerminalEnabled() {
		return nil
	}
	note, ok := hookNotification(in, n.GetTerminalEvents(), worktreeLabel(c, in.Cwd))
	if !ok {
		return nil
	}
	return tmux.Notify(pane, note, n.GetTerminalSequence())
}

// hookEvent names the event a hook input reports, as notify.terminal_events
// lists it: "stop" for the end of a turn, else Claude's notification type.
func hookEvent(in hookInput) string {
	if in.Event == "Stop" {
		return "stop"
	}
	return in.NotificationType
}

// hookNotification builds the notification for a hook input, and reports
// whether there is one to send: the event must be in events and have text.
func hookNotification(in hookInput, events []string, label string) (tmux.Notification, bool) {
	if !slices.Contains(events, hookEvent(in)) {
		return tmux.Notification{}, false
	}
	var body string
	switch in.Event {
	case "Stop":
		if in.StopHookActive {
			// Claude is continuing because a Stop hook told it to: the
			// turn has not really ended.
			return tmux.Notification{}, false
		}
		body = in.LastAssistantMessage
		if strings.TrimSpace(body) == "" {
			body = "Turn finished"
		}
	case "Notification":
		body = in.Message
	}
	if strings.TrimSpace(body) == "" {
		return tmux.Notification{}, false
	}
	return tmux.Notification{Title: "Claude · " + label, Body: body}, true
}

// worktreeLabel names the worktree cwd is in: "repo#N · PR title" for a PR
// review (the title when zen has it cached), the worktree name otherwise,
// and the directory name when cwd is in no worktree zen knows.
func worktreeLabel(c *config.Config, cwd string) string {
	var wts []worktree.Worktree
	if c != nil {
		wts, _ = worktree.ListAll(c)
	}
	wt, ok := worktreeFor(wts, cwd)
	if !ok {
		return filepath.Base(cwd)
	}
	if wt.Type == worktree.TypePRReview && wt.PRNumber > 0 {
		label := fmt.Sprintf("%s#%d", wt.Repo, wt.PRNumber)
		if meta, ok := prcache.Get(wt.Repo, wt.PRNumber); ok && meta.Title != "" {
			label += " · " + meta.Title
		}
		return label
	}
	return wt.Name
}

// worktreeFor returns the worktree that contains dir: the deepest one, in
// case worktrees nest.
func worktreeFor(wts []worktree.Worktree, dir string) (worktree.Worktree, bool) {
	dir = filepath.Clean(dir)
	var best worktree.Worktree
	bestLen := -1
	for _, wt := range wts {
		p := filepath.Clean(wt.Path)
		if (dir == p || strings.HasPrefix(dir, p+string(filepath.Separator))) && len(p) > bestLen {
			best, bestLen = wt, len(p)
		}
	}
	return best, bestLen >= 0
}
