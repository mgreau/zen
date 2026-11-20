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
	"time"

	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/prcache"
	"github.com/mgreau/zen/internal/tmux"
	"github.com/mgreau/zen/internal/worktree"
	"github.com/mgreau/zen/internal/zenbin"
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
window. It never fails: tmux discards its output, so problems are appended to
~/.zen/state/tmux-hook.log.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// tmux runs this in the background with its output discarded, since
		// it would show in the new session's pane: problems go to a log.
		if err := runHookSessionCreated(args[0]); err != nil {
			logHookError("session-created", err)
		}
		return nil
	},
}

func init() {
	hookCmd.AddCommand(hookNotifyCmd)
	hookCmd.AddCommand(hookSessionCreatedCmd)
	rootCmd.AddCommand(hookCmd)
}

// maxHookLog caps tmux-hook.log: a hook that fails for every new session,
// as with a broken config.yaml, must not grow it forever.
const maxHookLog = 64 * 1024

// logHookError appends a hook's error to ~/.zen/state/tmux-hook.log, for
// hooks whose output nobody sees, starting the file afresh once it passes
// maxHookLog. Best effort.
func logHookError(hook string, err error) {
	path := filepath.Join(config.StateDir(), "tmux-hook.log")
	if os.MkdirAll(filepath.Dir(path), 0o755) != nil {
		return
	}
	flags := os.O_APPEND | os.O_CREATE | os.O_WRONLY
	if info, serr := os.Stat(path); serr == nil && info.Size() > maxHookLog {
		flags |= os.O_TRUNC
	}
	f, ferr := os.OpenFile(path, flags, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s zen hook %s: %v\n", time.Now().Format(time.RFC3339), hook, err)
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
	bin, err := zenbin.Path()
	if err != nil {
		return "zen"
	}
	// zenbin.Path prefers the zen on PATH: when that is what it returned,
	// typing "zen" runs the same one.
	if onPath, err := exec.LookPath("zen"); err == nil {
		if abs, err := filepath.Abs(onPath); err == nil && abs == bin {
			return "zen"
		}
	}
	return "'" + strings.ReplaceAll(bin, "'", `'\''`) + "'"
}

// hookInput is the part of a Claude Code hook's input zen reads.
type hookInput struct {
	Event                string `json:"hook_event_name"`
	Cwd                  string `json:"cwd"`
	Message              string `json:"message"`
	NotificationType     string `json:"notification_type"`
	LastAssistantMessage string `json:"last_assistant_message"`
	Error                string `json:"error"`
	StopHookActive       bool   `json:"stop_hook_active"`
}

func runHookNotify(stdin io.Reader) error {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return nil // not in tmux: nowhere to send it
	}
	var in hookInput
	// Read it all: cutting it short would leave invalid JSON. Only the start
	// of the text is shown (see tmux.Clean).
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
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
// lists it: "stop" for the end of a turn, "stop_failure" for a turn that
// ended on an error, else Claude's notification type.
func hookEvent(in hookInput) string {
	switch in.Event {
	case "Stop":
		return "stop"
	case "StopFailure":
		return "stop_failure"
	}
	if in.NotificationType == "" {
		// Claude versions that sent no type sent two kinds: the idle notice
		// after a turn, and requests (permissions, questions). Tell them
		// apart by the idle notice's text.
		if strings.Contains(strings.ToLower(in.Message), "waiting for your input") {
			return "idle_prompt"
		}
		return "permission_prompt"
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
	case "StopFailure":
		body = "Turn failed"
		if e := strings.TrimSpace(in.Error); e != "" {
			body += ": " + e
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
