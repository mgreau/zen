package migrate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// ClaudeToCodex makes a Claude Code session resumable in Codex and returns the
// Codex thread id to pass to `codex resume`.
//
// Codex owns this direction: its importer converts Claude sessions into native
// rollout threads and records them in an import ledger. zen first checks the
// ledger (the session may already be imported — imports are content-addressed
// by sha256), and otherwise drives the importer over the `codex app-server`
// JSON-RPC API. zen never writes to ~/.codex itself.
//
// Known importer limits (Codex 0.142.0): only the ~50 most recent sessions
// from the last 30 days are detected, and sessions whose recorded cwd no
// longer exists on disk are skipped.
func ClaudeToCodex(ctx context.Context, claudeSessionPath, codexBin string) (string, error) {
	if id, err := lookupImportedThread(claudeSessionPath); err == nil && id != "" {
		return id, nil
	}

	if err := runCodexImport(ctx, claudeSessionPath, codexBin); err != nil {
		return "", err
	}

	id, err := lookupImportedThread(claudeSessionPath)
	if err != nil {
		return "", fmt.Errorf("import completed but reading the Codex import ledger failed: %w", err)
	}
	if id == "" {
		return "", fmt.Errorf("import completed but the session is missing from the Codex import ledger (%s)", importLedgerPath())
	}
	return id, nil
}

// ---- import ledger -----------------------------------------------------

func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return h
	}
	return filepath.Join(os.Getenv("HOME"), ".codex")
}

func importLedgerPath() string {
	return filepath.Join(codexHome(), "external_agent_session_imports.json")
}

// lookupImportedThread returns the Codex thread id a Claude session was
// imported as, or "" when it was never imported. Matching is by content
// sha256, so a session re-discovered under a moved path still matches;
// a session modified since import (e.g. resumed in Claude) does not, and is
// imported again so Codex sees the newer turns.
func lookupImportedThread(claudeSessionPath string) (string, error) {
	data, err := os.ReadFile(importLedgerPath())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var ledger struct {
		Records []struct {
			ContentSHA256    string `json:"content_sha256"`
			ImportedThreadID string `json:"imported_thread_id"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &ledger); err != nil {
		return "", fmt.Errorf("parsing %s: %w", importLedgerPath(), err)
	}

	f, err := os.Open(claudeSessionPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	sum := fmt.Sprintf("%x", h.Sum(nil))

	for _, r := range ledger.Records {
		if r.ContentSHA256 == sum {
			return r.ImportedThreadID, nil
		}
	}
	return "", nil
}

// ---- app-server driven import -------------------------------------------

// migrationItem mirrors ExternalAgentConfigMigrationItem from the app-server
// protocol schema (codex app-server generate-json-schema).
type migrationItem struct {
	ItemType    string          `json:"itemType"`
	Description string          `json:"description"`
	Cwd         *string         `json:"cwd,omitempty"`
	Details     json.RawMessage `json:"details,omitempty"`
}

type sessionMigration struct {
	Cwd   string  `json:"cwd"`
	Path  string  `json:"path"`
	Title *string `json:"title,omitempty"`
}

// runCodexImport asks Codex to import exactly one Claude session: detect the
// migratable items, narrow the SESSIONS item to the one session, import it,
// and wait for the completed notification.
func runCodexImport(ctx context.Context, claudeSessionPath, codexBin string) error {
	cmd := exec.CommandContext(ctx, codexBin, "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting %s app-server: %w", codexBin, err)
	}
	// The server exits when stdin closes; Wait reaps it (or the ctx kills it).
	defer func() {
		stdin.Close()
		_ = cmd.Wait()
	}()

	c := newAppServerClient(stdin, stdout)
	return c.importSession(claudeSessionPath)
}

// appServerClient speaks Codex's newline-delimited JSON-RPC over stdio.
type appServerClient struct {
	w      io.Writer
	sc     *bufio.Scanner
	nextID int
}

func newAppServerClient(w io.Writer, r io.Reader) *appServerClient {
	sc := bufio.NewScanner(r)
	// Detect responses enumerate whole sessions lists; allow large payloads.
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	return &appServerClient{w: w, sc: sc}
}

type rpcEnvelope struct {
	ID     *int            `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *appServerClient) send(msg map[string]any) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = c.w.Write(append(raw, '\n'))
	return err
}

// call sends a request and reads until its response arrives. Server-initiated
// requests and notifications encountered in between are skipped: nothing in
// the import flow notifies before its triggering call has been answered.
func (c *appServerClient) call(method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := c.nextID
	if err := c.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for c.sc.Scan() {
		var env rpcEnvelope
		if json.Unmarshal(c.sc.Bytes(), &env) != nil {
			continue
		}
		if env.ID == nil || *env.ID != id || env.Method != "" {
			continue
		}
		if env.Error != nil {
			return nil, fmt.Errorf("%s: %s (code %d)", method, env.Error.Message, env.Error.Code)
		}
		return env.Result, nil
	}
	if err := c.sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: reading app-server output: %w", method, err)
	}
	return nil, fmt.Errorf("%s: app-server closed before responding", method)
}

// notify sends a JSON-RPC notification (no response expected).
func (c *appServerClient) notify(method string) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method})
}

// waitNotification reads until a notification for method satisfies pred.
func (c *appServerClient) waitNotification(method string, pred func(json.RawMessage) bool) (json.RawMessage, error) {
	for c.sc.Scan() {
		var env rpcEnvelope
		if json.Unmarshal(c.sc.Bytes(), &env) != nil {
			continue
		}
		if env.ID == nil && env.Method == method && pred(env.Params) {
			return env.Params, nil
		}
	}
	if err := c.sc.Err(); err != nil {
		return nil, fmt.Errorf("waiting for %s: %w", method, err)
	}
	return nil, fmt.Errorf("app-server closed before sending %s", method)
}

func (c *appServerClient) importSession(claudeSessionPath string) error {
	if _, err := c.call("initialize", map[string]any{
		"clientInfo": map[string]any{"name": "zen", "version": "1.0"},
	}); err != nil {
		return err
	}
	if err := c.notify("initialized"); err != nil {
		return err
	}

	// SESSIONS is a home-scoped migration item: it is only returned with
	// includeHome and enumerates recent Claude sessions across all cwds
	// (each entry carries its own cwd/path). Repo-scoped detection returns
	// no sessions at all.
	detectRes, err := c.call("externalAgentConfig/detect", map[string]any{
		"cwds":        []string{},
		"includeHome": true,
	})
	if err != nil {
		return err
	}

	item, err := singleSessionItem(detectRes, claudeSessionPath)
	if err != nil {
		return err
	}

	importRes, err := c.call("externalAgentConfig/import", map[string]any{
		"migrationItems": []migrationItem{*item},
	})
	if err != nil {
		return err
	}
	var imp struct {
		ImportID string `json:"importId"`
	}
	if err := json.Unmarshal(importRes, &imp); err != nil || imp.ImportID == "" {
		return fmt.Errorf("externalAgentConfig/import returned no importId: %s", importRes)
	}

	completed, err := c.waitNotification("externalAgentConfig/import/completed", func(params json.RawMessage) bool {
		var p struct {
			ImportID string `json:"importId"`
		}
		return json.Unmarshal(params, &p) == nil && p.ImportID == imp.ImportID
	})
	if err != nil {
		return err
	}

	var result struct {
		ItemTypeResults []struct {
			ItemType string `json:"itemType"`
			Failures []struct {
				Error *string `json:"error"`
			} `json:"failures"`
			Successes []json.RawMessage `json:"successes"`
		} `json:"itemTypeResults"`
	}
	if err := json.Unmarshal(completed, &result); err != nil {
		return fmt.Errorf("parsing import completion: %w", err)
	}
	for _, r := range result.ItemTypeResults {
		if r.ItemType != "SESSIONS" {
			continue
		}
		if len(r.Failures) > 0 {
			msg := "unknown error"
			if r.Failures[0].Error != nil {
				msg = *r.Failures[0].Error
			}
			return fmt.Errorf("codex failed to import the session: %s", msg)
		}
		if len(r.Successes) > 0 {
			return nil
		}
	}
	return fmt.Errorf("codex reported no imported session")
}

// singleSessionItem extracts the SESSIONS migration item from a detect
// response and narrows its session list to the one matching path.
func singleSessionItem(detectRes json.RawMessage, claudeSessionPath string) (*migrationItem, error) {
	var detect struct {
		Items []struct {
			ItemType    string  `json:"itemType"`
			Description string  `json:"description"`
			Cwd         *string `json:"cwd"`
			Details     struct {
				Sessions []sessionMigration `json:"sessions"`
			} `json:"details"`
		} `json:"items"`
	}
	if err := json.Unmarshal(detectRes, &detect); err != nil {
		return nil, fmt.Errorf("parsing detect response: %w", err)
	}

	want := claudeSessionPath
	if resolved, err := filepath.EvalSymlinks(claudeSessionPath); err == nil {
		want = resolved
	}
	for _, it := range detect.Items {
		if it.ItemType != "SESSIONS" {
			continue
		}
		for _, s := range it.Details.Sessions {
			got := s.Path
			if resolved, err := filepath.EvalSymlinks(s.Path); err == nil {
				got = resolved
			}
			if got != want {
				continue
			}
			details, err := json.Marshal(map[string]any{"sessions": []sessionMigration{s}})
			if err != nil {
				return nil, err
			}
			return &migrationItem{
				ItemType:    it.ItemType,
				Description: it.Description,
				Cwd:         it.Cwd,
				Details:     details,
			}, nil
		}
	}
	return nil, fmt.Errorf("codex did not detect the Claude session %s (only the ~50 most recent sessions from the last 30 days, with a cwd that still exists, are importable)", claudeSessionPath)
}
