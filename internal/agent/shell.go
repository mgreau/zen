package agent

import "strings"

// shellQuote wraps s in single quotes for safe interpolation into a shell
// command line, escaping embedded single quotes. Unlike %q (double quotes),
// this also neutralises $, backticks, and backslashes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// quotedPrompt is prompt as one shell word, or "" when there is no prompt.
func quotedPrompt(prompt string) string {
	if prompt == "" {
		return ""
	}
	return shellQuote(prompt)
}

// promptFromFile is a shell word that expands to the contents of the file at
// path when the command runs.
func promptFromFile(path string) string {
	return `"$(cat ` + shellQuote(path) + `)"`
}
