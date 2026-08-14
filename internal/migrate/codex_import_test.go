package migrate

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLookupImportedThread(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)

	sessionPath := filepath.Join(t.TempDir(), "abc.jsonl")
	content := []byte(`{"type":"user","message":{"role":"user","content":"hi"}}` + "\n")
	if err := os.WriteFile(sessionPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	// sha256 of content, precomputed the same way the importer records it.
	sum := sha256Hex(t, sessionPath)

	ledger := fmt.Sprintf(`{"records":[
		{"source_path":"/old/place/abc.jsonl","content_sha256":"%s","imported_thread_id":"thread-42","imported_at":1782225706,"source_modified_at":1782223315974000000},
		{"source_path":"/x/y.jsonl","content_sha256":"other","imported_thread_id":"thread-99","imported_at":1,"source_modified_at":2}
	]}`, sum)
	if err := os.WriteFile(filepath.Join(codexHome, "external_agent_session_imports.json"), []byte(ledger), 0o644); err != nil {
		t.Fatal(err)
	}

	// Content match wins even though the recorded source_path differs.
	id, err := lookupImportedThread(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if id != "thread-42" {
		t.Errorf("thread id = %q, want thread-42", id)
	}

	// A session modified since import no longer matches: it must re-import.
	if err := os.WriteFile(sessionPath, append(content, []byte("{}\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err = lookupImportedThread(sessionPath)
	if err != nil {
		t.Fatal(err)
	}
	if id != "" {
		t.Errorf("modified session matched thread %q, want no match", id)
	}
}

func TestLookupImportedThreadNoLedger(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	sessionPath := filepath.Join(t.TempDir(), "abc.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := lookupImportedThread(sessionPath)
	if err != nil || id != "" {
		t.Errorf("missing ledger should be (\"\", nil), got (%q, %v)", id, err)
	}
}

func sha256Hex(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// fakeAppServer scripts Codex's side of the import conversation over pipes.
// It mirrors the protocol observed on codex-cli 0.142.0: JSONL-framed
// JSON-RPC, initialize/initialized handshake, detect -> import -> completed.
func fakeAppServer(t *testing.T, r io.Reader, w io.Writer, sessionPath, cwd string, failImport bool) {
	t.Helper()
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	respond := func(v any) {
		raw, _ := json.Marshal(v)
		w.Write(append(raw, '\n'))
	}
	for sc.Scan() {
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			respond(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"userAgent": "codex/0.142.0"}})
		case "initialized":
			// notification, no response
		case "externalAgentConfig/detect":
			respond(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{
				"items": []any{
					map[string]any{"itemType": "AGENTS_MD", "description": "AGENTS.md"},
					map[string]any{
						"itemType":    "SESSIONS",
						"description": "Recent chat sessions",
						"cwd":         cwd,
						"details": map[string]any{
							"sessions": []any{
								map[string]any{"cwd": cwd, "path": sessionPath, "title": "Fix the bug"},
								map[string]any{"cwd": cwd, "path": "/other/session.jsonl", "title": "Other"},
							},
						},
					},
				},
			}})
		case "externalAgentConfig/import":
			// The import must be narrowed to exactly the requested session.
			var p struct {
				MigrationItems []struct {
					ItemType string `json:"itemType"`
					Details  struct {
						Sessions []sessionMigration `json:"sessions"`
					} `json:"details"`
				} `json:"migrationItems"`
			}
			if err := json.Unmarshal(req.Params, &p); err != nil ||
				len(p.MigrationItems) != 1 ||
				p.MigrationItems[0].ItemType != "SESSIONS" ||
				len(p.MigrationItems[0].Details.Sessions) != 1 ||
				p.MigrationItems[0].Details.Sessions[0].Path != sessionPath {
				respond(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -1, "message": "unexpected import params"}})
				continue
			}
			respond(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"importId": "imp-1"}})
			respond(map[string]any{"jsonrpc": "2.0", "method": "externalAgentConfig/import/progress", "params": map[string]any{"importId": "imp-1"}})
			sessions := map[string]any{"itemType": "SESSIONS", "failures": []any{}, "successes": []any{map[string]any{"itemType": "SESSIONS"}}}
			if failImport {
				sessions = map[string]any{"itemType": "SESSIONS", "failures": []any{map[string]any{"error": "boom"}}, "successes": []any{}}
			}
			respond(map[string]any{"jsonrpc": "2.0", "method": "externalAgentConfig/import/completed", "params": map[string]any{
				"importId": "imp-1", "itemTypeResults": []any{sessions},
			}})
		}
	}
}

func TestAppServerImportSession(t *testing.T) {
	cwd := t.TempDir()
	sessionDir := t.TempDir()
	sessionPath := filepath.Join(sessionDir, "s.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The client resolves symlinks before matching; hand the fake server the
	// resolved forms so they compare equal.
	resolvedPath, _ := filepath.EvalSymlinks(sessionPath)
	resolvedCwd, _ := filepath.EvalSymlinks(cwd)

	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	go func() {
		fakeAppServer(t, serverIn, serverOut, resolvedPath, resolvedCwd, false)
		serverOut.Close()
	}()

	c := newAppServerClient(clientOut, clientIn)
	if err := c.importSession(sessionPath); err != nil {
		t.Fatalf("importSession: %v", err)
	}
}

func TestAppServerImportSessionFailure(t *testing.T) {
	cwd := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolvedPath, _ := filepath.EvalSymlinks(sessionPath)
	resolvedCwd, _ := filepath.EvalSymlinks(cwd)

	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	go func() {
		fakeAppServer(t, serverIn, serverOut, resolvedPath, resolvedCwd, true)
		serverOut.Close()
	}()

	c := newAppServerClient(clientOut, clientIn)
	err := c.importSession(sessionPath)
	if err == nil {
		t.Fatal("expected failure from failed import")
	}
}

func TestAppServerImportSessionNotDetected(t *testing.T) {
	cwd := t.TempDir()
	sessionPath := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolvedCwd, _ := filepath.EvalSymlinks(cwd)

	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	go func() {
		// Server only knows about a different session path.
		fakeAppServer(t, serverIn, serverOut, "/nowhere/else.jsonl", resolvedCwd, false)
		serverOut.Close()
	}()

	c := newAppServerClient(clientOut, clientIn)
	err := c.importSession(sessionPath)
	if err == nil {
		t.Fatal("expected error when codex does not detect the session")
	}
}
