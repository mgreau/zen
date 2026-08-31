package board

import "testing"

func TestTruncateBodyLines(t *testing.T) {
	tests := []struct {
		name string
		body string
		max  int
		want []string
	}{
		{"empty body", "", 20, nil},
		{"fewer lines than max", "line1\nline2", 20, []string{"line1", "line2"}},
		{"exactly max", "a\nb\nc", 3, []string{"a", "b", "c"}},
		{"more than max truncates", "a\nb\nc\nd", 3, []string{"a", "b", "c"}},
		{"CRLF normalized", "a\r\nb\r\nc", 20, []string{"a", "b", "c"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateBodyLines(tt.body, tt.max)
			if len(got) != len(tt.want) {
				t.Fatalf("truncateBodyLines(%q, %d) = %v, want %v", tt.body, tt.max, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
