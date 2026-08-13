package context

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"github.com/mgreau/zen/internal/github"
)

// PRContext holds all data needed to render the PR-review context template.
type PRContext struct {
	Number       int
	Title        string
	Author       string
	URL          string
	HeadBranch   string
	BaseBranch   string
	IsFork       bool
	Body         string
	ChangedFiles []string
}

const prContextTemplate = `# PR Review: #{{.Number}} — {{.Title}}

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

var tmpl = template.Must(template.New("pr-context").Parse(prContextTemplate))

// RenderPRContext fetches PR metadata from GitHub and renders the review
// context to a markdown string. The agent layer is responsible for writing it
// to the appropriate per-agent context file (CLAUDE.local.md, AGENTS.md, ...).
func RenderPRContext(ctx context.Context, fullRepo string, prNumber int) (string, error) {
	client, err := github.NewClient(ctx)
	if err != nil {
		return "", fmt.Errorf("creating GitHub client: %w", err)
	}

	details, err := client.GetPRDetails(ctx, fullRepo, prNumber)
	if err != nil {
		return "", fmt.Errorf("fetching PR details: %w", err)
	}

	files, err := client.GetPRFiles(ctx, fullRepo, prNumber)
	if err != nil {
		return "", fmt.Errorf("fetching PR files: %w", err)
	}

	return Render(PRContext{
		Number:       details.Number,
		Title:        details.Title,
		Author:       details.Author,
		URL:          details.URL,
		HeadBranch:   details.HeadRefName,
		BaseBranch:   details.BaseRefName,
		IsFork:       details.IsFork,
		Body:         details.Body,
		ChangedFiles: files,
	})
}

// Render renders the PR-review context template to a markdown string.
func Render(prCtx PRContext) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, prCtx); err != nil {
		return "", fmt.Errorf("rendering template: %w", err)
	}
	return buf.String(), nil
}
