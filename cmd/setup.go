package cmd

import (
	"bufio"
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mgreau/zen/internal/agent"
	"github.com/mgreau/zen/internal/config"
	"github.com/mgreau/zen/internal/ui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// EmbeddedCommands holds the embedded slash-command prompt files under
// commands/. Set by main.go before Execute().
var EmbeddedCommands fs.FS = embed.FS{}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Create ~/.zen/config.yaml (interactive, or unattended with flags)",
	Long: `Create ~/.zen/config.yaml and install the agent's slash-command prompts.

With no flags, setup asks for each value on the terminal.

For unattended installs (machine provisioning, dotfiles, CI), pass the config
as flags or as a file and setup runs without prompting:

  zen setup --repo app=octo-sts/app:~/git/repo-octo-sts-app \
            --author mattmoor --author wlynch --terminal tmux

  zen setup --config-file ./zen-config.yaml     # or - to read stdin

A --repo value is [short=]owner/name:base_path; short defaults to name.

Unattended setup is idempotent. When ~/.zen/config.yaml already holds the
same content it is left alone, and when it differs setup fails unless you
pass --force. Prompts that are already installed are kept. The config is
checked with the same rules zen uses when it loads it, so a bad value fails
the install instead of the first zen command after it.`,
	Args: cobra.NoArgs,
	RunE: runSetup,
}

var setupOpts setupOptions

func init() {
	f := setupCmd.Flags()
	f.BoolVar(&setupOpts.nonInteractive, "non-interactive", false, "Never prompt; fail if required values are missing")
	f.StringVar(&setupOpts.configFile, "config-file", "", "Install this config file (- reads stdin) instead of building one from flags")
	f.StringArrayVar(&setupOpts.repos, "repo", nil, "Repository as [short=]owner/name:base_path (repeatable)")
	f.StringSliceVar(&setupOpts.authors, "author", nil, "GitHub username for PR filtering (repeatable or comma-separated)")
	f.StringVar(&setupOpts.agent, "agent", "", `Coding agent: "claude" (default) or "codex"`)
	f.StringVar(&setupOpts.terminal, "terminal", "", `Terminal: "iterm", "ghostty", "kitty", "macos", or "tmux" (default: tmux inside tmux, else iterm)`)
	f.StringVar(&setupOpts.branchPrefix, "branch-prefix", "", "Prefix for feature branch names (default: git user.name)")
	f.BoolVar(&setupOpts.force, "force", false, "Overwrite an existing config that differs")
	f.BoolVar(&setupOpts.skipPrompts, "skip-prompts", false, "Do not install the agent's slash-command prompts")
	rootCmd.AddCommand(setupCmd)
}

// setupOptions are the flags of zen setup.
type setupOptions struct {
	nonInteractive bool
	configFile     string
	repos          []string
	authors        []string
	agent          string
	terminal       string
	branchPrefix   string
	force          bool
	skipPrompts    bool
}

// hasConfigFlags reports whether any flag that builds the config was given.
func (o setupOptions) hasConfigFlags() bool {
	return len(o.repos) > 0 || len(o.authors) > 0 || o.agent != "" || o.terminal != "" || o.branchPrefix != ""
}

// unattended reports whether setup runs without prompting.
func (o setupOptions) unattended() bool {
	return o.nonInteractive || o.configFile != "" || o.hasConfigFlags()
}

func runSetup(cmd *cobra.Command, args []string) error {
	if setupOpts.unattended() {
		return runUnattendedSetup(setupOpts, cmd.InOrStdin(), cmd.OutOrStdout())
	}
	return runInteractiveSetup(setupOpts)
}

// runUnattendedSetup writes the config described by opts without prompting.
func runUnattendedSetup(opts setupOptions, stdin io.Reader, out io.Writer) error {
	data, err := unattendedConfigData(opts, stdin)
	if err != nil {
		return err
	}

	if err := validateSetupConfig(data); err != nil {
		return err
	}
	parsed, err := config.ParseStrict(data)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	configPath := config.Path()
	wrote, err := writeConfig(configPath, data, opts.force)
	if err != nil {
		return err
	}
	if wrote {
		fmt.Fprintf(out, "Config written to %s\n", configPath)
	} else {
		fmt.Fprintf(out, "Config unchanged: %s\n", configPath)
	}
	if len(parsed.Authors) == 0 {
		fmt.Fprintln(out, "Note: no authors configured; the daemon will not create review worktrees on its own")
	}

	if opts.skipPrompts {
		return nil
	}
	ag := parsed.NewAgent("")
	installed, err := installPrompts(ag)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Installed %d new %s prompt(s) in %s\n", installed, ag.Kind(), ag.PromptsDir())
	return nil
}

// validateSetupConfig is the check every config setup writes must pass: a
// strict parse (unknown keys and extra documents fail, agent and terminal are
// checked as Load checks them) plus the repo rules in Config.ValidateRepos.
func validateSetupConfig(data []byte) error {
	parsed, err := config.ParseStrict(data)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	if err := parsed.ValidateRepos(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	return nil
}

// unattendedConfigData returns the config YAML to install: the --config-file
// contents as given (comments kept), or a config built from the flags.
func unattendedConfigData(opts setupOptions, stdin io.Reader) ([]byte, error) {
	if opts.configFile != "" {
		if opts.hasConfigFlags() {
			return nil, errors.New("--config-file cannot be combined with --repo, --author, --agent, --terminal, or --branch-prefix")
		}
		if opts.configFile == "-" {
			data, err := io.ReadAll(stdin)
			if err != nil {
				return nil, fmt.Errorf("reading config from stdin: %w", err)
			}
			return data, nil
		}
		data, err := os.ReadFile(opts.configFile)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		return data, nil
	}

	if len(opts.repos) == 0 {
		return nil, errors.New("unattended setup needs at least one --repo, or a --config-file")
	}
	repos := make(map[string]config.RepoConfig, len(opts.repos))
	for _, spec := range opts.repos {
		short, repo, err := parseRepoFlag(spec)
		if err != nil {
			return nil, err
		}
		if _, dup := repos[short]; dup {
			return nil, fmt.Errorf("--repo %q: short name %q given twice", spec, short)
		}
		repos[short] = repo
	}

	var authors []string
	for _, a := range opts.authors {
		if a = strings.TrimSpace(a); a != "" {
			authors = append(authors, a)
		}
	}

	agentKind := strings.ToLower(strings.TrimSpace(opts.agent))
	if agentKind == "" {
		agentKind = "claude"
	}

	cfg := newSetupConfig(repos, authors, agentKind, strings.ToLower(strings.TrimSpace(opts.terminal)))
	cfg.BranchPrefix = strings.TrimSpace(opts.branchPrefix)
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return nil, fmt.Errorf("marshalling config: %w", err)
	}
	return data, nil
}

// parseRepoFlag parses a --repo value, [short=]owner/name:base_path. The short
// name defaults to the repository name.
func parseRepoFlag(spec string) (string, config.RepoConfig, error) {
	rest := spec
	short := ""
	// A short name cannot contain "/", so an "=" after the owner/ belongs to
	// the base path rather than separating a short name.
	if before, after, ok := strings.Cut(spec, "="); ok && !strings.Contains(before, "/") {
		short, rest = strings.TrimSpace(before), after
		if short == "" {
			return "", config.RepoConfig{}, fmt.Errorf("--repo %q: empty short name before =", spec)
		}
	}
	fullName, basePath, ok := strings.Cut(rest, ":")
	fullName, basePath = strings.TrimSpace(fullName), strings.TrimSpace(basePath)
	if !ok || basePath == "" {
		return "", config.RepoConfig{}, fmt.Errorf("--repo %q: want [short=]owner/name:base_path", spec)
	}
	_, name, ok := strings.Cut(fullName, "/")
	if !ok || name == "" {
		return "", config.RepoConfig{}, fmt.Errorf("--repo %q: %q is not owner/name", spec, fullName)
	}
	if short == "" {
		short = name
	}
	return short, config.RepoConfig{FullName: fullName, BasePath: basePath}, nil
}

// newSetupConfig is the config setup writes, with the daemon defaults spelled
// out so they are visible to edit. An empty terminal is left unset so zen picks
// one when it runs (tmux inside a tmux session, iTerm otherwise).
func newSetupConfig(repos map[string]config.RepoConfig, authors []string, agentKind, terminal string) config.Config {
	return config.Config{
		Repos:        repos,
		Authors:      authors,
		PollInterval: "5m",
		Agent:        agentKind,
		ClaudeBin:    "claude",
		CodexBin:     "codex",
		Terminal:     terminal,
		Watch: config.WatchConfig{
			DispatchInterval:  "10s",
			CleanupInterval:   "1h",
			CleanupAfterMerge: "1h",
			Concurrency:       2,
			MaxRetries:        5,
		},
	}
}

// writeConfig writes data to path. An existing file with the same content is
// left alone (wrote is false); one with different content is replaced only
// when force is set. The write goes through a temporary file and a rename so
// a running daemon, which reloads config on every poll, never reads half a
// file. A symlinked config (stow, chezmoi, home-manager) is written through
// to its target, even one that does not exist yet, so the link survives, and
// a replaced file keeps its mode.
func writeConfig(path string, data []byte, force bool) (wrote bool, err error) {
	target, err := resolveSymlinks(path)
	if err != nil {
		return false, fmt.Errorf("resolving %s: %w", path, err)
	}
	mode := fs.FileMode(0o644)
	existing, err := os.ReadFile(target)
	switch {
	case err == nil:
		if bytes.Equal(existing, data) {
			return false, nil
		}
		if !force {
			return false, fmt.Errorf("%s already exists with different content; pass --force to overwrite it", path)
		}
		if info, err := os.Stat(target); err == nil {
			mode = info.Mode().Perm()
		}
	case !errors.Is(err, fs.ErrNotExist):
		return false, fmt.Errorf("reading existing config: %w", err)
	}

	if err := replaceFile(target, data, mode); err != nil {
		if target != path {
			return false, fmt.Errorf("%s is a symlink to %s, which cannot be replaced (if another tool manages it, change its source instead): %w", path, target, err)
		}
		return false, err
	}
	return true, nil
}

// resolveSymlinks follows path through any chain of symlinks to the file it
// names. Unlike filepath.EvalSymlinks it succeeds when the final target does
// not exist yet (a dotfiles link created before its source), returning where
// that target would be.
func resolveSymlinks(path string) (string, error) {
	p := path
	for range 40 {
		info, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return p, nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			return p, nil
		}
		link, err := os.Readlink(p)
		if err != nil {
			return "", err
		}
		if !filepath.IsAbs(link) {
			// Resolve the link's own directory first so ".." in a relative
			// link means what it means to the kernel.
			dir, err := filepath.EvalSymlinks(filepath.Dir(p))
			if err != nil {
				return "", err
			}
			link = filepath.Join(dir, link)
		}
		p = link
	}
	return "", fmt.Errorf("too many levels of symbolic links")
}

// replaceFile atomically replaces path with data: it writes a temporary file
// in the same directory and renames it over path.
func replaceFile(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("writing config: %w", err)
	}
	// Flush before the rename, or a crash right after provisioning can leave
	// an empty config.yaml on some filesystems.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("writing config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	// Persist the rename too. Best effort: not every platform can sync a
	// directory, and the file itself is already on disk.
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// errStdinClosed is returned when interactive setup runs out of input, such
// as when a provisioning script runs zen setup without a terminal.
var errStdinClosed = errors.New("stdin closed before setup finished; for an unattended install pass --repo or --config-file (see zen setup --help)")

// runInteractiveSetup asks for each value on the terminal. --force skips the
// overwrite question and --skip-prompts skips installing prompts.
func runInteractiveSetup(opts setupOptions) error {
	scanner := bufio.NewScanner(os.Stdin)

	configPath := config.Path()

	fmt.Println()
	fmt.Println(ui.BoldText("Zen Setup"))
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println()
	fmt.Println("Prerequisites:")
	fmt.Println("  gh auth login         — authenticate GitHub CLI")
	fmt.Println("  a terminal emulator   — iTerm2, Ghostty, kitty, macOS Terminal, or tmux")
	fmt.Println("  claude or codex CLI   — the coding agent zen launches")
	fmt.Println()

	// Check for existing config
	if _, err := os.Stat(configPath); err == nil && !opts.force {
		fmt.Printf("Config already exists: %s\n", configPath)
		fmt.Print("Overwrite? [y/N]: ")
		answer, err := readLine(scanner)
		if err != nil {
			return err
		}
		if strings.ToLower(answer) != "y" {
			fmt.Println("Setup cancelled.")
			return nil
		}
		fmt.Println()
	}

	// Collect repos
	repoMap := make(map[string]config.RepoConfig)
	for {
		fmt.Println(ui.BoldText(fmt.Sprintf("Repository %d", len(repoMap)+1)))
		fmt.Println("───────────────────────────────────────────────────────────────")

		shortName, err := promptValid(scanner, "Short name (e.g. apko)", "apko", config.ValidateRepoShortName)
		if err != nil {
			return err
		}
		fullName, err := promptValid(scanner, "GitHub full name (e.g. chainguard-dev/apko)", "", config.ValidateRepoFullName)
		if err != nil {
			return err
		}
		basePath, err := promptRequired(scanner, "Base path for worktrees (e.g. ~/git/repo-apko)")
		if err != nil {
			return err
		}
		repoMap[shortName] = config.RepoConfig{FullName: fullName, BasePath: basePath}
		fmt.Println()

		fmt.Print("Add another repo? [y/N]: ")
		answer, err := readLine(scanner)
		if err != nil {
			return err
		}
		fmt.Println()
		if strings.ToLower(answer) != "y" {
			break
		}
	}

	// Collect authors
	fmt.Println("───────────────────────────────────────────────────────────────")
	authors, err := promptRequired(scanner, "GitHub username(s) for PR filtering (comma-separated)")
	if err != nil {
		return err
	}
	fmt.Println()

	// Choose the coding agent
	agentChoice, err := prompt(scanner, "Coding agent (claude or codex)", "claude")
	if err != nil {
		return err
	}
	agentChoice = strings.ToLower(agentChoice)
	if agentChoice != "claude" && agentChoice != "codex" {
		fmt.Printf("  Unknown agent %q, defaulting to claude\n", agentChoice)
		agentChoice = "claude"
	}
	fmt.Println()

	terminalChoice, err := prompt(scanner, "Terminal (iterm, ghostty, kitty, macos, or tmux)", "iterm")
	if err != nil {
		return err
	}
	terminalChoice = strings.ToLower(terminalChoice)
	switch terminalChoice {
	case "iterm", "ghostty", "kitty", "macos", "tmux":
	default:
		fmt.Printf("  Unknown terminal %q, defaulting to iterm\n", terminalChoice)
		terminalChoice = "iterm"
	}
	fmt.Println()

	authorList := strings.Split(authors, ",")
	for i, a := range authorList {
		authorList[i] = strings.TrimSpace(a)
	}

	cfg := newSetupConfig(repoMap, authorList, agentChoice, terminalChoice)
	data, err := yaml.Marshal(&cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	// Answers are checked as they are given; this applies the same final check
	// as an unattended install so the two can never write different things.
	if err := validateSetupConfig(data); err != nil {
		return err
	}
	// The user already confirmed overwriting an existing config above.
	if _, err := writeConfig(configPath, data, true); err != nil {
		return err
	}

	fmt.Println(ui.GreenText("✓ Config written to " + configPath))
	fmt.Println()

	// Install slash-command prompts for the chosen agent
	installedCount := 0
	if !opts.skipPrompts {
		ag := agent.New(agent.Kind(agentChoice), "")
		if installedCount, err = installAgentPrompts(scanner, ag); err != nil {
			return err
		}
	}

	fmt.Println("Next steps:")
	fmt.Println("  zen status          — see dashboard")
	fmt.Println("  zen watch start     — start background daemon")
	fmt.Println("  zen inbox           — check pending PR reviews")
	if installedCount > 0 {
		if agentChoice == "codex" {
			// Codex expands custom prompts only inside its TUI composer.
			fmt.Println("  codex, then type /review-pr — review a PR")
		} else {
			fmt.Println("  claude /review-pr   — review a PR")
		}
	}
	fmt.Println()

	return nil
}

// readLine reads one trimmed line, failing with errStdinClosed at end of input
// instead of returning "" forever.
func readLine(scanner *bufio.Scanner) (string, error) {
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("reading stdin: %w", err)
		}
		fmt.Println()
		return "", errStdinClosed
	}
	return strings.TrimSpace(scanner.Text()), nil
}

// prompt asks for input with a default value shown in brackets.
func prompt(scanner *bufio.Scanner, label, defaultVal string) (string, error) {
	fmt.Printf("%s [%s]: ", label, defaultVal)
	val, err := readLine(scanner)
	if err != nil {
		return "", err
	}
	if val == "" {
		return defaultVal, nil
	}
	return val, nil
}

// promptRequired asks for input and repeats until a non-empty value is given.
func promptRequired(scanner *bufio.Scanner, label string) (string, error) {
	return promptValid(scanner, label, "", nil)
}

// promptValid asks for input until validate accepts it, printing why it did
// not. An empty answer takes defaultVal; with no default, an answer is
// required. A nil validate accepts any non-empty answer.
func promptValid(scanner *bufio.Scanner, label, defaultVal string, validate func(string) error) (string, error) {
	for {
		if defaultVal != "" {
			fmt.Printf("%s [%s]: ", label, defaultVal)
		} else {
			fmt.Printf("%s: ", label)
		}
		val, err := readLine(scanner)
		if err != nil {
			return "", err
		}
		if val == "" {
			val = defaultVal
		}
		if val == "" {
			fmt.Println("  (required)")
			continue
		}
		if validate != nil {
			if err := validate(val); err != nil {
				fmt.Printf("  %v\n", err)
				continue
			}
		}
		return val, nil
	}
}

// embeddedPromptNames lists the slash-command prompts built into zen, named
// without extension.
func embeddedPromptNames() []string {
	entries, err := fs.ReadDir(EmbeddedCommands, "commands")
	if err != nil {
		// No embedded prompts (shouldn't happen with a proper build)
		return nil
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())))
		}
	}
	return names
}

// installPrompts installs every embedded slash-command prompt that the agent
// does not already have, and returns how many it wrote.
func installPrompts(ag agent.Agent) (int, error) {
	entries, err := fs.ReadDir(EmbeddedCommands, "commands")
	if err != nil {
		return 0, nil
	}
	installed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		srcData, err := fs.ReadFile(EmbeddedCommands, "commands/"+e.Name())
		if err != nil {
			return installed, fmt.Errorf("reading embedded %s: %w", e.Name(), err)
		}
		name := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
		wrote, err := ag.EnsurePrompt(name, srcData)
		if err != nil {
			return installed, err
		}
		if wrote {
			installed++
		}
	}
	return installed, nil
}

// installAgentPrompts asks the user, then installs the embedded slash-command
// prompts into the chosen agent's prompts directory.
func installAgentPrompts(scanner *bufio.Scanner, ag agent.Agent) (int, error) {
	names := embeddedPromptNames()
	if len(names) == 0 {
		return 0, nil
	}

	targetDir := ag.PromptsDir()
	fmt.Printf("Install %s slash-command prompts?\n", ag.Kind())
	fmt.Printf("  Prompts: %s\n", strings.Join(names, ", "))
	fmt.Printf("  Target:  %s\n", targetDir)
	fmt.Print("Install? [Y/n]: ")
	answer, err := readLine(scanner)
	if err != nil {
		return 0, err
	}
	answer = strings.ToLower(answer)
	if answer == "n" || answer == "no" {
		fmt.Println()
		return 0, nil
	}

	installed, err := installPrompts(ag)
	if err != nil {
		return installed, err
	}
	fmt.Println(ui.GreenText(fmt.Sprintf("✓ Installed %d prompt(s) to %s", installed, targetDir)))
	fmt.Println()
	return installed, nil
}
