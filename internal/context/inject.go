package context

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/mgreau/zen/internal/github"
	"github.com/mgreau/zen/internal/ui"
)

// PRContext holds all data needed to render the CLAUDE.md template.
type PRContext struct {
	Number      int
	Title       string
	Author      string
	URL         string
	HeadBranch  string
	BaseBranch  string
	IsFork      bool
	Body        string
	ChangedFiles []string
}

const claudeMDTemplate = `# PR Review: #{{.Number}} — {{.Title}}

## PR Info

| Field | Value |
|-------|-------|
| **PR** | [#{{.Number}}]({{.URL}}) |
| **Author** | {{.Author}} |
| **Branch** | ` + "`{{.HeadBranch}}`" + ` → ` + "`{{.BaseBranch}}`" + ` |
{{- if .IsFork}}
| **Fork** | Yes |
{{- end}}

## Description

{{if .Body}}{{.Body}}{{else}}_No description provided._{{end}}

## Changed Files

{{range .ChangedFiles}}- ` + "`{{.}}`" + `
{{end}}
## Review Instructions

You are reviewing PR #{{.Number}}. Focus on:

1. **Correctness** — Does the code do what the PR description says?
2. **Security** — Any injection, auth bypass, or data exposure risks?
3. **Tests** — Are changes adequately tested?
4. **Style** — Does it follow existing patterns in the codebase?

Start by reading the changed files listed above, then provide your review.
`

var tmpl = template.Must(template.New("claude-md").Parse(claudeMDTemplate))

// InjectPRContext fetches PR metadata from GitHub and writes a CLAUDE.md
// file in the given worktree directory.
func InjectPRContext(ctx context.Context, worktreePath string, fullRepo string, prNumber int) error {
	client, err := github.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("creating GitHub client: %w", err)
	}

	details, err := client.GetPRDetails(ctx, fullRepo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching PR details: %w", err)
	}

	files, err := client.GetPRFiles(ctx, fullRepo, prNumber)
	if err != nil {
		return fmt.Errorf("fetching PR files: %w", err)
	}

	prCtx := PRContext{
		Number:       details.Number,
		Title:        details.Title,
		Author:       details.Author,
		URL:          details.URL,
		HeadBranch:   details.HeadRefName,
		BaseBranch:   details.BaseRefName,
		IsFork:       details.IsFork,
		Body:         details.Body,
		ChangedFiles: files,
	}

	return WriteClaudeMD(worktreePath, prCtx)
}

// WriteClaudeMD renders the template and writes PR review context to the
// worktree. Always writes to CLAUDE.local.md so the repo's own CLAUDE.md
// is never modified — no risk of accidental commits.
func WriteClaudeMD(dir string, prCtx PRContext) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, prCtx); err != nil {
		return fmt.Errorf("rendering template: %w", err)
	}

	outPath := filepath.Join(dir, "CLAUDE.local.md")
	if err := os.WriteFile(outPath, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", outPath, err)
	}

	ui.LogDebug(fmt.Sprintf("Wrote PR context to %s", outPath))
	return nil
}

// RenderClaudeMD renders the template to a string (useful for testing/preview).
func RenderClaudeMD(prCtx PRContext) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, prCtx); err != nil {
		return "", fmt.Errorf("rendering template: %w", err)
	}
	return buf.String(), nil
}
