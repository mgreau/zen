package iterm

import (
	"strings"
	"testing"
)

func TestRandomColor(t *testing.T) {
	color := RandomColor()

	// Should contain the iTerm escape sequences
	if !strings.Contains(color, "bg;red;brightness") {
		t.Errorf("RandomColor() = %q, missing red escape", color)
	}
	if !strings.Contains(color, "bg;green;brightness") {
		t.Errorf("RandomColor() = %q, missing green escape", color)
	}
	if !strings.Contains(color, "bg;blue;brightness") {
		t.Errorf("RandomColor() = %q, missing blue escape", color)
	}
}

func TestPaletteNotEmpty(t *testing.T) {
	if len(palette) == 0 {
		t.Error("palette should not be empty")
	}

	for i, c := range palette {
		for j := 0; j < 3; j++ {
			if c[j] < 0 || c[j] > 255 {
				t.Errorf("palette[%d][%d] = %d, want 0-255", i, j, c[j])
			}
		}
	}
}
