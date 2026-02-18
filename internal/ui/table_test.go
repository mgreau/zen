package ui

import "testing"

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello..."},
		{"abcdefghij", 6, "abc..."},
		{"ab", 3, "ab"},
		{"abcdef", 3, "abc"},
		{"a", 1, "a"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := Truncate(tt.input, tt.max)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
			}
		})
	}
}

func TestShortenHome(t *testing.T) {
	tests := []struct {
		path string
		home string
		want string
	}{
		{"/Users/alice/git/repo", "/Users/alice", "~/git/repo"},
		{"/opt/other/path", "/Users/alice", "/opt/other/path"},
		{"/Users/alice", "/Users/alice", "~"},
		{"", "/Users/alice", ""},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ShortenHome(tt.path, tt.home)
			if got != tt.want {
				t.Errorf("ShortenHome(%q, %q) = %q, want %q", tt.path, tt.home, got, tt.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		seconds int
		want    string
	}{
		{0, "0s"},
		{30, "30s"},
		{59, "59s"},
		{60, "1m"},
		{300, "5m"},
		{3599, "59m"},
		{3600, "1h"},
		{7200, "2h"},
		{86399, "23h"},
		{86400, "1d"},
		{172800, "2d"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatDuration(tt.seconds)
			if got != tt.want {
				t.Errorf("FormatDuration(%d) = %q, want %q", tt.seconds, got, tt.want)
			}
		})
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{500, "500B"},
		{1024, "1024B"},
		{1025, "1KB"},
		{1048576, "1024KB"}, // exactly 1MB boundary (not > 1048576, so falls to KB)
		{1048577, "1MB"},
		{10485760, "10MB"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := FormatSize(tt.bytes)
			if got != tt.want {
				t.Errorf("FormatSize(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}
