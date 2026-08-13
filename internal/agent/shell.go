package agent

import "strings"

// shellQuote wraps s in single quotes for safe interpolation into a shell
// command line, escaping embedded single quotes. Unlike %q (double quotes),
// this also neutralises $, backticks, and backslashes.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
