package migrate

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/mgreau/zen/internal/session"
)

// ClaudeSessionInfo is what zen reads from a Claude Code session file to
// present and relocate it.
type ClaudeSessionInfo struct {
	// Cwd is the directory the session ran in (the first record's cwd).
	Cwd string
	// Title is the session's name: a /rename title when set, else Claude's
	// generated title. Empty when the session has neither yet.
	Title string
}

// ReadClaudeSessionInfo scans a Claude session file for its working directory
// and title.
func ReadClaudeSessionInfo(path string) (ClaudeSessionInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return ClaudeSessionInfo{}, err
	}
	defer f.Close()

	var info ClaudeSessionInfo
	var aiTitle, customTitle string
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var rec struct {
				Type        string `json:"type"`
				Cwd         string `json:"cwd"`
				AITitle     string `json:"aiTitle"`
				CustomTitle string `json:"customTitle"`
			}
			if json.Unmarshal(line, &rec) == nil {
				if info.Cwd == "" && rec.Cwd != "" {
					info.Cwd = rec.Cwd
				}
				if rec.AITitle != "" {
					aiTitle = rec.AITitle
				}
				if rec.CustomTitle != "" {
					customTitle = rec.CustomTitle
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return info, err
		}
	}
	info.Title = customTitle
	if info.Title == "" {
		info.Title = aiTitle
	}
	return info, nil
}

// RelocateClaudeSession copies a Claude Code session so that it can be resumed
// from worktreePath, and returns the new session id.
//
// Claude finds sessions by cwd (~/.claude/projects/<munged cwd>/<id>.jsonl),
// so the file is copied into worktreePath's project directory. The copy gets
// a fresh session id: the original stays where it was, untouched and
// resumable, and the two never share an id (the original may still be open
// and appending to its own file). Each record's sessionId is rewritten to
// the new id. Each cwd equal to fromDir becomes worktreePath; with subtree
// set (fromDir is a checkout of the same repository), a cwd under fromDir
// moves to the same place under worktreePath too. Paths match whether or not
// symlinks in them are resolved. Message content is copied verbatim.
//
// The session's sidecar directory (subagent transcripts, persisted tool
// results) is copied too, with the same rewrite applied to its .jsonl files.
// State Claude keeps elsewhere by session id is not: /rewind checkpoints
// (~/.claude/file-history) point at files in the old checkout, so the copy
// starts without them.
// A trailing partial line, left when the source session is still being
// written, is dropped. A copy taken from inside the live session (/zen-import)
// ends on the pending tool call with no result; Claude Code repairs that on
// resume (verified on 2.1.329), so it is copied as is.
//
// branch, when set, replaces the gitBranch of every record whose cwd moved,
// so Claude's session list shows the new branch.
func RelocateClaudeSession(srcPath, fromDir, worktreePath string, subtree bool, branch string) (string, error) {
	resolved, err := filepath.EvalSymlinks(worktreePath)
	if err != nil {
		return "", fmt.Errorf("resolving worktree path (must exist for Claude to find the session): %w", err)
	}
	worktreePath = resolved
	oldID := strings.TrimSuffix(filepath.Base(srcPath), ".jsonl")
	newID := uuid.NewString()
	rw := recordRewriter{oldID: oldID, newID: newID, toDir: worktreePath, subtree: subtree, branch: branch, resolved: map[string]string{}}
	if fromDir != "" {
		rw.fromDirs = append(rw.fromDirs, filepath.Clean(fromDir))
		if r, err := filepath.EvalSymlinks(fromDir); err == nil && r != filepath.Clean(fromDir) {
			rw.fromDirs = append(rw.fromDirs, r)
		}
	}

	projDir := session.ProjectDirPath(worktreePath)
	if err := os.MkdirAll(projDir, 0o700); err != nil {
		return "", fmt.Errorf("creating Claude project directory: %w", err)
	}

	dst := filepath.Join(projDir, newID+".jsonl")
	if err := rw.copyFile(srcPath, dst); err != nil {
		return "", err
	}

	sidecar := filepath.Join(filepath.Dir(srcPath), oldID)
	if st, err := os.Stat(sidecar); err == nil && st.IsDir() {
		if err := rw.copyTree(sidecar, filepath.Join(projDir, newID)); err != nil {
			_ = os.Remove(dst)
			_ = os.RemoveAll(filepath.Join(projDir, newID))
			return "", fmt.Errorf("copying session sidecar files: %w", err)
		}
	}
	return newID, nil
}

// recordRewriter moves Claude session records from one session id and
// directory to another.
type recordRewriter struct {
	oldID, newID string
	fromDirs     []string // fromDir as given and symlink-resolved
	toDir        string
	subtree      bool
	branch       string
	resolved     map[string]string // cache of EvalSymlinks per record cwd
}

// copyFile writes src to dst with every record rewritten. It writes to a
// temporary file first so a failure never leaves a half-written session for
// Claude to find.
func (rw recordRewriter) copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), ".zen-relocate-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after the rename

	w := bufio.NewWriter(tmp)
	r := bufio.NewReader(in)
	for {
		line, rerr := r.ReadBytes('\n')
		if rerr != nil && rerr != io.EOF {
			tmp.Close()
			return fmt.Errorf("reading %s: %w", src, rerr)
		}
		complete := bytes.HasSuffix(line, []byte("\n"))
		if body := bytes.TrimSpace(line); len(body) > 0 {
			out, ok := rw.rewrite(body)
			// An unterminated last line that does not parse is a record
			// still being written; anything else is kept, rewritten or not.
			if ok || complete {
				if _, err := w.Write(out); err != nil {
					tmp.Close()
					return err
				}
				if err := w.WriteByte('\n'); err != nil {
					tmp.Close()
					return err
				}
			}
		}
		if rerr == io.EOF {
			break
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

// copyTree copies a session sidecar directory, rewriting .jsonl transcripts
// and copying every other file verbatim.
func (rw recordRewriter) copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o700)
		case !d.Type().IsRegular():
			return nil // skip symlinks, sockets and the like
		case strings.HasSuffix(path, ".jsonl"):
			return rw.copyFile(path, target)
		default:
			return copyRegular(path, target)
		}
	})
}

// rewrite returns the record with its session id and cwd moved, and whether
// the line parsed as a JSON object. Lines that need no change, or do not
// parse, are returned unchanged. A changed record is re-encoded, which sorts
// its keys; Claude Code reads records as JSON objects, so order is not
// significant.
func (rw recordRewriter) rewrite(line []byte) ([]byte, bool) {
	var rec map[string]json.RawMessage
	if err := json.Unmarshal(line, &rec); err != nil {
		return line, false
	}
	changed := false
	for _, key := range []string{"sessionId", "session_id"} {
		if s, ok := rawString(rec[key]); ok && s == rw.oldID {
			rec[key] = mustRawString(rw.newID)
			changed = true
		}
	}
	if s, ok := rawString(rec["cwd"]); ok {
		if moved, ok := rw.moveDir(s); ok {
			if moved != s {
				rec["cwd"] = mustRawString(moved)
				changed = true
			}
			if _, has := rec["gitBranch"]; has && rw.branch != "" {
				rec["gitBranch"] = mustRawString(rw.branch)
				changed = true
			}
		}
	}
	if !changed {
		return line, true
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep <, >, & readable, as Claude writes them
	if err := enc.Encode(rec); err != nil {
		return line, true
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), true
}

// moveDir maps a path equal to fromDir (or, with subtree, under it) to the
// same place under toDir. It tries the path as recorded and symlink-resolved,
// since Claude records the cwd it was started in while git reports resolved
// paths.
func (rw recordRewriter) moveDir(p string) (string, bool) {
	clean := filepath.Clean(p)
	candidates := []string{clean}
	r, ok := rw.resolved[clean]
	if !ok {
		r, _ = filepath.EvalSymlinks(clean)
		rw.resolved[clean] = r
	}
	if r != "" && r != clean {
		candidates = append(candidates, r)
	}
	for _, c := range candidates {
		for _, from := range rw.fromDirs {
			if c == from {
				return rw.toDir, true
			}
			if !rw.subtree {
				continue
			}
			if rest, ok := strings.CutPrefix(c, from+string(filepath.Separator)); ok {
				// A subdirectory the new worktree lacks (untracked, or only
				// in commits not on its branch) falls back to the root.
				moved := filepath.Join(rw.toDir, rest)
				if st, err := os.Stat(moved); err != nil || !st.IsDir() {
					return rw.toDir, true
				}
				return moved, true
			}
		}
	}
	return p, false
}

func rawString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func mustRawString(s string) json.RawMessage {
	b, _ := json.Marshal(s) // marshaling a string cannot fail
	return b
}

func copyRegular(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// ErrSessionNotFound means no Claude session file has the requested id.
var ErrSessionNotFound = errors.New("no Claude session with that id")

// FindClaudeSessionByID locates a session file by id across every Claude
// project directory, so a session can be named from any working directory.
func FindClaudeSessionByID(id string) (string, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return "", fmt.Errorf("invalid session id %q: %w", id, err)
	}
	id = parsed.String()
	root := filepath.Join(os.Getenv("HOME"), ".claude", "projects")
	matches, err := filepath.Glob(filepath.Join(root, "*", id+".jsonl"))
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("%w: %s", ErrSessionNotFound, id)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("session id %s exists in %d project directories: %s", id, len(matches), strings.Join(matches, ", "))
	}
	return matches[0], nil
}
