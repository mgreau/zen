package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mgreau/zen/internal/session"
)

// codexAgent drives OpenAI's Codex CLI.
//
// Unlike Claude (which stores sessions under a per-project directory derived
// from the worktree path), Codex stores every session as a rollout file under
// ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl keyed only by date.
// To attribute sessions to a worktree we read each rollout's session_meta line
// and match its recorded cwd. File→cwd results are cached for the process
// lifetime (a rollout's cwd never changes) so repeated daemon scans stay cheap.
type codexAgent struct {
	bin string

	cwdCache sync.Map // rollout file path -> cwd string
}

func (a *codexAgent) Kind() Kind  { return Codex }
func (a *codexAgent) Bin() string { return a.bin }

func (a *codexAgent) StartCommand(prompt, model string) string {
	cmd := a.bin
	if model != "" {
		cmd += fmt.Sprintf(" -m %s", model)
	}
	if prompt != "" {
		cmd += fmt.Sprintf(" %q", prompt)
	}
	return cmd
}

// ResumeCommand resumes a recorded session by UUID. The model is restored from
// the session itself, so it is not re-specified here.
func (a *codexAgent) ResumeCommand(sessionID, _ string) string {
	return fmt.Sprintf("%s resume %s", a.bin, sessionID)
}

// ContextFile is AGENTS.md, the project-context file Codex reads automatically.
func (a *codexAgent) ContextFile() string { return "AGENTS.md" }

const (
	codexSideContextFile = ".zen/PR_CONTEXT.md"
	// codexSentinel marks that zen has injected PR context, regardless of which
	// file received it. It makes daemon idempotency reliable even when the repo
	// ships its own AGENTS.md (which would otherwise look "already present").
	codexSentinel = ".zen/.pr_context_injected"
)

// InjectContext writes AGENTS.md when the worktree has none. If the repo
// already ships its own AGENTS.md, zen never clobbers it — the context goes to
// .zen/PR_CONTEXT.md instead, which ReviewPrompt then points the agent at.
// The written path and the .zen/ marker dir are added to the worktree's git
// exclude so nothing shows up as a pending change.
func (a *codexAgent) InjectContext(worktreePath, rendered string) (string, error) {
	ref := a.ContextFile()
	if _, err := os.Stat(filepath.Join(worktreePath, ref)); err == nil {
		ref = codexSideContextFile
	}

	outPath := filepath.Join(worktreePath, ref)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return "", fmt.Errorf("creating dir for %s: %w", ref, err)
	}
	if err := os.WriteFile(outPath, []byte(rendered), 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", outPath, err)
	}

	// Drop the idempotency sentinel (best-effort).
	sentinel := filepath.Join(worktreePath, codexSentinel)
	if err := os.MkdirAll(filepath.Dir(sentinel), 0o755); err == nil {
		_ = os.WriteFile(sentinel, nil, 0o644)
	}

	addToGitExclude(worktreePath, ref)
	addToGitExclude(worktreePath, ".zen/")
	return ref, nil
}

// ContextPresent relies on the sentinel rather than the context file, so a
// repo that ships its own AGENTS.md is not mistaken for already-injected.
func (a *codexAgent) ContextPresent(worktreePath string) bool {
	_, err := os.Stat(filepath.Join(worktreePath, codexSentinel))
	return err == nil
}

// ReviewPrompt points Codex at whichever context file was injected. The
// side-file is not auto-loaded by Codex, so it must be named explicitly; even
// for AGENTS.md naming it keeps the instruction unambiguous.
func (a *codexAgent) ReviewPrompt(worktreePath string) string {
	ref := a.ContextFile()
	if _, err := os.Stat(filepath.Join(worktreePath, codexSideContextFile)); err == nil {
		ref = codexSideContextFile
	}
	return fmt.Sprintf("Review this pull request. The PR details and review checklist are in %s — read the changed files it lists, then give your review.", ref)
}

func (a *codexAgent) PromptsDir() string {
	return filepath.Join(codexHome(), "prompts")
}

func (a *codexAgent) EnsurePrompt(name string, content []byte) (bool, error) {
	return ensurePromptFile(a.PromptsDir(), name, content)
}

// ---- session discovery -----------------------------------------------------

func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	return filepath.Join(os.Getenv("HOME"), ".codex")
}

func sessionsRoot() string {
	return filepath.Join(codexHome(), "sessions")
}

// rolloutUUID matches the trailing UUID of a rollout filename.
var rolloutUUID = regexp.MustCompile(`([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)

func sessionIDFromFile(name string) string {
	stem := strings.TrimSuffix(name, ".jsonl")
	if m := rolloutUUID.FindString(stem); m != "" {
		return m
	}
	return stem
}

func (a *codexAgent) FindSessions(worktreePath string) ([]session.Session, error) {
	root := sessionsRoot()
	var sessions []session.Session

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		cwd := a.rolloutCwd(path)
		if cwd != worktreePath {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		sessions = append(sessions, session.Session{
			ID:       sessionIDFromFile(d.Name()),
			Path:     path,
			Modified: info.ModTime().Unix(),
			ModHuman: info.ModTime().Format("2006-01-02 15:04"),
			Size:     info.Size(),
			SizeStr:  formatSize(info.Size()),
		})
		return nil
	})
	if err != nil {
		return nil, nil // no sessions directory yet
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Modified > sessions[j].Modified
	})
	return sessions, nil
}

// rolloutCwd returns the recorded cwd for a rollout file, caching the result.
func (a *codexAgent) rolloutCwd(path string) string {
	if v, ok := a.cwdCache.Load(path); ok {
		return v.(string)
	}
	cwd := readRolloutCwd(path)
	a.cwdCache.Store(path, cwd)
	return cwd
}

// codexLine is the outer rollout-line envelope: {"timestamp","type","payload"}.
type codexLine struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

// readRolloutCwd reads the session_meta line and returns its cwd, or "".
func readRolloutCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	// session_meta is the first line, but scan a few in case of ordering changes.
	for i := 0; i < 5 && sc.Scan(); i++ {
		var line codexLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.Type != "session_meta" {
			continue
		}
		var meta struct {
			Cwd string `json:"cwd"`
		}
		if json.Unmarshal(line.Payload, &meta) == nil && meta.Cwd != "" {
			return meta.Cwd
		}
	}
	return ""
}

// ---- token parsing ---------------------------------------------------------

// codexTokenUsage mirrors Codex's TokenUsage. total_token_usage is cumulative,
// so the latest token_count event already holds the running total.
type codexTokenUsage struct {
	InputTokens           int64 `json:"input_tokens"`
	CachedInputTokens     int64 `json:"cached_input_tokens"`
	OutputTokens          int64 `json:"output_tokens"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
}

const codexTailSize = 64 * 1024

func (a *codexAgent) ParseTokensTail(path string) (string, session.TokenUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", session.TokenUsage{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", session.TokenUsage{}, err
	}
	offset := int64(0)
	if info.Size() > codexTailSize {
		offset = info.Size() - codexTailSize
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", session.TokenUsage{}, err
	}
	reader := bufio.NewReader(f)
	if offset > 0 {
		reader.ReadString('\n') // discard partial line
	}
	return parseCodexLines(reader)
}

func (a *codexAgent) ParseTokensFull(path string) (string, session.TokenUsage, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", session.TokenUsage{}, err
	}
	defer f.Close()
	return parseCodexLines(bufio.NewReader(f))
}

// parseCodexLines extracts the latest model (from turn_context) and the latest
// cumulative token usage (from token_count events).
func parseCodexLines(reader *bufio.Reader) (string, session.TokenUsage, error) {
	var model string
	var latest codexTokenUsage
	var seenUsage bool

	for {
		raw, err := reader.ReadString('\n')
		if s := strings.TrimSpace(raw); s != "" {
			var line codexLine
			if json.Unmarshal([]byte(s), &line) == nil {
				switch line.Type {
				case "turn_context":
					var tc struct {
						Model string `json:"model"`
					}
					if json.Unmarshal(line.Payload, &tc) == nil && tc.Model != "" {
						model = tc.Model
					}
				case "event_msg":
					var ev struct {
						Type string `json:"type"`
						Info *struct {
							TotalTokenUsage codexTokenUsage `json:"total_token_usage"`
						} `json:"info"`
					}
					if json.Unmarshal(line.Payload, &ev) == nil && ev.Type == "token_count" && ev.Info != nil {
						latest = ev.Info.TotalTokenUsage
						seenUsage = true
					}
				}
			}
		}
		if err != nil {
			break
		}
	}

	tokens := session.TokenUsage{}
	if seenUsage {
		tokens.InputTokens = latest.InputTokens
		tokens.OutputTokens = latest.OutputTokens
		tokens.CacheReadInputTokens = latest.CachedInputTokens
	}
	return model, tokens, nil
}

// ---- cleanup & misc --------------------------------------------------------

func (a *codexAgent) CleanSessions(worktreePath string) (int, error) {
	sessions, _ := a.FindSessions(worktreePath)
	removed := 0
	for _, s := range sessions {
		if s.Path == "" {
			continue
		}
		if err := os.Remove(s.Path); err != nil {
			return removed, err
		}
		a.cwdCache.Delete(s.Path)
		removed++
	}
	return removed, nil
}

func (a *codexAgent) IsProcessRunning(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	return exec.Command("pgrep", "-f", sessionID).Run() == nil
}

// ShortenModel trims provider prefixes for compact display, e.g.
// "gpt-5-codex" stays "gpt-5-codex"; an "openai/" provider prefix is dropped.
func (a *codexAgent) ShortenModel(model string) string {
	if i := strings.LastIndex(model, "/"); i >= 0 {
		model = model[i+1:]
	}
	return model
}

func formatSize(bytes int64) string {
	switch {
	case bytes > 1048576:
		return fmt.Sprintf("%dMB", bytes/1048576)
	case bytes > 1024:
		return fmt.Sprintf("%dKB", bytes/1024)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

// addToGitExclude appends ref to the worktree's git exclude file so the
// injected context file never appears as an untracked change. Best-effort.
func addToGitExclude(worktreePath, ref string) {
	out, err := runGit(worktreePath, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return
	}
	excludePath := strings.TrimSpace(out)
	if excludePath == "" {
		return
	}
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(worktreePath, excludePath)
	}

	if data, err := os.ReadFile(excludePath); err == nil {
		for _, l := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(l) == ref {
				return // already excluded
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", ref)
}

func runGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}
