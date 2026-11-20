package agent

import "strings"

// shellQuote wraps s in single quotes for safe interpolation into a shell
// command line, escaping embedded single quotes. Unlike %q (double quotes),
// this also neutralises $, backticks, and backslashes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ShellQuote is shellQuote for callers outside the package that append
// arguments to a command built by an Agent.
func ShellQuote(s string) string { return shellQuote(s) }
