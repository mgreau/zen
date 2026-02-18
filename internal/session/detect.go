package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session represents a Claude Code session file.
type Session struct {
	ID       string `json:"id"`
	Modified int64  `json:"modified_epoch"`
	ModHuman string `json:"modified"`
	Size     int64  `json:"size"`
	SizeStr  string `json:"size_str"`
}

// FindSessions finds Claude sessions for a worktree path by scanning
// ~/.claude/projects/<encoded-path>/*.jsonl files.
func FindSessions(worktreePath string) ([]Session, error) {
	projectDirName := pathToClaudeProject(worktreePath)
	claudeDir := filepath.Join(os.Getenv("HOME"), ".claude", "projects", projectDirName)

	entries, err := os.ReadDir(claudeDir)
	if err != nil {
		return nil, nil // no sessions found
	}

	var sessions []Session
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}

		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		sessions = append(sessions, Session{
			ID:       id,
			Modified: info.ModTime().Unix(),
			ModHuman: info.ModTime().Format("2006-01-02 15:04"),
			Size:     info.Size(),
			SizeStr:  formatSize(info.Size()),
		})
	}

	// Sort by modification time, newest first
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].Modified > sessions[j].Modified
	})

	return sessions, nil
}

// HasActiveSession checks if a worktree has any Claude session files.
// This is a lightweight check - it doesn't verify if the session is running.
func HasActiveSession(worktreePath string) bool {
	sessions, _ := FindSessions(worktreePath)
	return len(sessions) > 0
}

// pathToClaudeProject converts a worktree path to the Claude projects directory name.
// /Users/maxime.greau/git/cgr/repo-mono/mono-pr-123
// -> -Users-maxime-greau-git-cgr-repo-mono-mono-pr-123
func pathToClaudeProject(path string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(path)
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

// TokenUsage holds aggregated token counts from a session.
type TokenUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// SessionDetail extends Session with parsed token usage and process status.
type SessionDetail struct {
	Session
	Model      string     `json:"model"`
	Tokens     TokenUsage `json:"tokens"`
	Running    bool       `json:"running"`
	LastActive time.Time  `json:"last_active"`
	AgeStr     string     `json:"age_str"`
}

// jsonLine is the minimal structure we parse from session .jsonl files.
type jsonLine struct {
	Message *jsonMessage `json:"message,omitempty"`
}

type jsonMessage struct {
	Model string    `json:"model,omitempty"`
	Usage *jsonUsage `json:"usage,omitempty"`
}

type jsonUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
}

// tailSize is the number of bytes to read from the end of a session file
// for fast (non-full) parsing.
const tailSize = 64 * 1024

// SessionFilePath returns the full filesystem path for a session .jsonl file.
func SessionFilePath(worktreePath, sessionID string) string {
	projectDirName := pathToClaudeProject(worktreePath)
	return filepath.Join(os.Getenv("HOME"), ".claude", "projects", projectDirName, sessionID+".jsonl")
}

// IsProcessRunning checks if a Claude process is running for the given session ID
// by looking for a process whose command line contains the session ID.
func IsProcessRunning(sessionID string) bool {
	cmd := exec.Command("pgrep", "-f", sessionID)
	err := cmd.Run()
	return err == nil
}

// ParseSessionDetailTail reads the last tailSize bytes of a session file
// and extracts the model and most recent token usage. This is fast but may
// not capture all token usage from long sessions.
func ParseSessionDetailTail(path string) (model string, tokens TokenUsage, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", TokenUsage{}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", TokenUsage{}, err
	}

	offset := int64(0)
	if info.Size() > tailSize {
		offset = info.Size() - tailSize
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", TokenUsage{}, err
	}

	// If we seeked into the middle of the file, skip the first partial line
	reader := bufio.NewReader(f)
	if offset > 0 {
		reader.ReadString('\n') // discard partial line
	}

	return parseLines(reader)
}

// ParseSessionDetailFull reads the entire session file and sums up all
// token usage. Slower but accurate for total counts.
func ParseSessionDetailFull(path string) (model string, tokens TokenUsage, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", TokenUsage{}, err
	}
	defer f.Close()

	return parseLines(bufio.NewReader(f))
}

// parseLines scans lines from a reader, extracting model and summing token usage.
func parseLines(reader *bufio.Reader) (string, TokenUsage, error) {
	var model string
	var tokens TokenUsage

	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			var jl jsonLine
			if json.Unmarshal([]byte(line), &jl) == nil && jl.Message != nil {
				if jl.Message.Model != "" {
					model = jl.Message.Model
				}
				if jl.Message.Usage != nil {
					tokens.InputTokens += jl.Message.Usage.InputTokens
					tokens.OutputTokens += jl.Message.Usage.OutputTokens
					tokens.CacheCreationInputTokens += jl.Message.Usage.CacheCreationInputTokens
					tokens.CacheReadInputTokens += jl.Message.Usage.CacheReadInputTokens
				}
			}
		}
		if err != nil {
			break
		}
	}

	return model, tokens, nil
}

// FormatTokenCount formats a token count in human-readable form (e.g. 1.2K, 3.5M).
func FormatTokenCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// ShortenModel shortens a Claude model identifier.
// "claude-opus-4-6" -> "opus-4-6", "claude-sonnet-4-5-20250929" -> "sonnet-4-5"
func ShortenModel(model string) string {
	m := strings.TrimPrefix(model, "claude-")
	// Remove date suffixes like -20250929
	if idx := strings.LastIndex(m, "-20"); idx > 0 {
		m = m[:idx]
	}
	return m
}

// FormatAge returns a human-readable relative time string.
func FormatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
