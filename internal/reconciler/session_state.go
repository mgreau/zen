package reconciler

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mgreau/zen/internal/config"
)

// SessionState holds the cached state of a single Claude session.
type SessionState struct {
	WorktreePath string `json:"worktree_path"`
	WorktreeName string `json:"worktree_name"`
	SessionID    string `json:"session_id"`
	Status       string `json:"status"` // "running", "waiting", "stopped"
	Model        string `json:"model"`
	Size         string `json:"size"`
	InputTokens  string `json:"input_tokens"`
	OutputTokens string `json:"output_tokens"`
	LastModified int64  `json:"last_modified_epoch"`
	UpdatedAt    int64  `json:"updated_at"`
}

// SessionSnapshot is the top-level structure written to sessions.json.
type SessionSnapshot struct {
	Timestamp string         `json:"timestamp"`
	Sessions  []SessionState `json:"sessions"`
}

// sessionSnapshotPath returns the path to ~/.zen/state/sessions.json.
func sessionSnapshotPath() string {
	return filepath.Join(config.StateDir(), "sessions.json")
}

// WriteSessionSnapshot writes session states to ~/.zen/state/sessions.json.
func WriteSessionSnapshot(states []SessionState) error {
	snapshot := SessionSnapshot{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Sessions:  states,
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(sessionSnapshotPath(), data, 0o644)
}

// ReadSessionSnapshot reads the cached session snapshot from disk.
// Returns nil if the file doesn't exist or can't be parsed.
func ReadSessionSnapshot() (*SessionSnapshot, error) {
	data, err := os.ReadFile(sessionSnapshotPath())
	if err != nil {
		return nil, err
	}
	var snapshot SessionSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// IsSnapshotFresh returns true if the snapshot was updated within maxAge.
func IsSnapshotFresh(snapshot *SessionSnapshot, maxAge time.Duration) bool {
	if snapshot == nil {
		return false
	}
	ts, err := time.Parse(time.RFC3339, snapshot.Timestamp)
	if err != nil {
		return false
	}
	return time.Since(ts) < maxAge
}

// SnapshotMatchesConfig returns true if the snapshot contains sessions
// under at least one of the given base paths, or if both are empty.
// This prevents using a stale cache generated with a different config.
func SnapshotMatchesConfig(snapshot *SessionSnapshot, basePaths []string) bool {
	if snapshot == nil || len(snapshot.Sessions) == 0 {
		// Empty snapshot is valid for any config
		return true
	}
	if len(basePaths) == 0 {
		return false
	}
	for _, s := range snapshot.Sessions {
		for _, bp := range basePaths {
			if strings.HasPrefix(s.WorktreePath, bp) {
				return true
			}
		}
	}
	return false
}
