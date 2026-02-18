package ui

import (
	"fmt"
	"strings"
)

// Banner prints a bordered title section.
func Banner(title string) {
	line := BoldText(CyanText("═══════════════════════════════════════════════════════════════"))
	fmt.Println()
	fmt.Println(line)
	fmt.Println(BoldText(CyanText("  " + title)))
	fmt.Println(line)
	fmt.Println()
}

// SectionHeader prints a bold section header with a separator line.
func SectionHeader(title string) {
	fmt.Println(BoldText(title))
	fmt.Println("───────────────────────────────────────────────────────────────")
}

// Separator prints a horizontal rule.
func Separator() {
	fmt.Println("───────────────────────────────────────────────────────────────")
}

// Truncate shortens a string to max length with ellipsis.
func Truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// ShortenHome replaces the home directory prefix with ~.
func ShortenHome(path, home string) string {
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

// PrintKeyValue prints a key-value pair with dim key.
func PrintKeyValue(key, value string) {
	fmt.Printf("  %s: %s\n", key, value)
}

// Hint prints a dim hint line.
func Hint(msg string) {
	fmt.Println(DimText(msg))
}

// FormatDuration formats seconds into a human-readable duration.
func FormatDuration(seconds int) string {
	switch {
	case seconds < 60:
		return fmt.Sprintf("%ds", seconds)
	case seconds < 3600:
		return fmt.Sprintf("%dm", seconds/60)
	case seconds < 86400:
		return fmt.Sprintf("%dh", seconds/3600)
	default:
		return fmt.Sprintf("%dd", seconds/86400)
	}
}

// FormatSize formats bytes into human-readable size.
func FormatSize(bytes int64) string {
	switch {
	case bytes > 1048576:
		return fmt.Sprintf("%dMB", bytes/1048576)
	case bytes > 1024:
		return fmt.Sprintf("%dKB", bytes/1024)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}
