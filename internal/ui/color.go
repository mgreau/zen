package ui

import (
	"fmt"
	"os"
)

// ANSI color codes
const (
	Red    = "\033[0;31m"
	Green  = "\033[0;32m"
	Yellow = "\033[1;33m"
	Blue   = "\033[0;34m"
	Cyan   = "\033[0;36m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Reset  = "\033[0m"
)

var colorsEnabled = true

func init() {
	// Disable colors if not a terminal or NO_COLOR is set
	if os.Getenv("NO_COLOR") != "" {
		colorsEnabled = false
	}
}

// SetColorsEnabled controls whether ANSI codes are emitted.
func SetColorsEnabled(enabled bool) {
	colorsEnabled = enabled
}

func wrap(code, s string) string {
	if !colorsEnabled {
		return s
	}
	return code + s + Reset
}

func RedText(s string) string    { return wrap(Red, s) }
func GreenText(s string) string  { return wrap(Green, s) }
func YellowText(s string) string { return wrap(Yellow, s) }
func BlueText(s string) string   { return wrap(Blue, s) }
func CyanText(s string) string   { return wrap(Cyan, s) }
func BoldText(s string) string   { return wrap(Bold, s) }
func DimText(s string) string    { return wrap(Dim, s) }

func LogInfo(msg string)    { fmt.Fprintf(os.Stderr, "%s %s\n", BlueText("[INFO]"), msg) }
func LogSuccess(msg string) { fmt.Fprintf(os.Stderr, "%s %s\n", GreenText("[OK]"), msg) }
func LogWarn(msg string)    { fmt.Fprintf(os.Stderr, "%s %s\n", YellowText("[WARN]"), msg) }
func LogError(msg string)   { fmt.Fprintf(os.Stderr, "%s %s\n", RedText("[ERROR]"), msg) }

var DebugEnabled bool

func LogDebug(msg string) {
	if DebugEnabled {
		fmt.Fprintf(os.Stderr, "%s %s\n", DimText("[DEBUG]"), msg)
	}
}
