package coordmcp

import (
	"context"
	"encoding/json"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mgreau/zen/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{
		Repos: map[string]config.RepoConfig{
			"mono":  {FullName: "chainguard-dev/mono", BasePath: "/tmp/test-repo-mono"},
			"infra": {FullName: "chainguard-dev/infra", BasePath: "/tmp/test-repo-infra"},
		},
	}
}

func makeRequest(args map[string]any) mcpgo.CallToolRequest {
	return mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Arguments: args,
		},
	}
}

func TestHandleConfigRepos(t *testing.T) {
	srv := New(testConfig())
	ctx := context.Background()

	result, err := srv.handleConfigRepos(ctx, makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %v", result.Content)
	}

	// Parse the JSON result
	text := mcpgo.GetTextFromContent(result.Content[0])
	var repos []repoEntry
	if err := json.Unmarshal([]byte(text), &repos); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}

	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}

	// Should be sorted by short_name
	if repos[0].ShortName != "infra" {
		t.Errorf("expected first repo to be 'infra', got %q", repos[0].ShortName)
	}
	if repos[1].ShortName != "mono" {
		t.Errorf("expected second repo to be 'mono', got %q", repos[1].ShortName)
	}
	if repos[1].FullName != "chainguard-dev/mono" {
		t.Errorf("expected full_name 'chainguard-dev/mono', got %q", repos[1].FullName)
	}
}

func TestHandleConfigReposEmpty(t *testing.T) {
	cfg := &config.Config{
		Repos: map[string]config.RepoConfig{},
	}
	srv := New(cfg)
	ctx := context.Background()

	result, err := srv.handleConfigRepos(ctx, makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error")
	}

	text := mcpgo.GetTextFromContent(result.Content[0])
	if text != "[]" {
		t.Errorf("expected empty JSON array '[]', got %q", text)
	}
}

func TestHandlePRDetailsMissingParams(t *testing.T) {
	srv := New(testConfig())
	ctx := context.Background()

	// Missing both required params
	result, err := srv.handlePRDetails(ctx, makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for missing params")
	}

	// Missing pr_number
	result, err = srv.handlePRDetails(ctx, makeRequest(map[string]any{"repo": "mono"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for missing pr_number")
	}
}

func TestHandlePRFilesMissingParams(t *testing.T) {
	srv := New(testConfig())
	ctx := context.Background()

	// Missing both required params
	result, err := srv.handlePRFiles(ctx, makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for missing params")
	}

	// Missing pr_number
	result, err = srv.handlePRFiles(ctx, makeRequest(map[string]any{"repo": "mono"}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected tool error for missing pr_number")
	}
}

func TestHandleAgentStatusNoSessions(t *testing.T) {
	// Use paths that definitely don't have worktrees
	cfg := &config.Config{
		Repos: map[string]config.RepoConfig{
			"fake": {FullName: "test/fake", BasePath: "/tmp/nonexistent-zen-test"},
		},
	}
	srv := New(cfg)
	ctx := context.Background()

	result, err := srv.handleAgentStatus(ctx, makeRequest(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error")
	}

	text := mcpgo.GetTextFromContent(result.Content[0])
	if text != "[]" {
		t.Errorf("expected empty JSON array '[]', got %q", text)
	}
}

func TestJsonResult(t *testing.T) {
	type testData struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}

	result, err := jsonResult(testData{Name: "test", Count: 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := mcpgo.GetTextFromContent(result.Content[0])

	var parsed testData
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if parsed.Name != "test" || parsed.Count != 42 {
		t.Errorf("unexpected parsed result: %+v", parsed)
	}
}

func TestJsonResultNilSlice(t *testing.T) {
	// Ensure nil slices produce "null" not crash
	var data []string
	result, err := jsonResult(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := mcpgo.GetTextFromContent(result.Content[0])
	if text != "null" {
		t.Errorf("expected 'null' for nil slice, got %q", text)
	}
}
