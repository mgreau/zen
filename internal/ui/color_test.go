package ui

import "testing"

func TestRedText(t *testing.T) {
	SetColorsEnabled(true)
	got := RedText("error")
	want := "\033[0;31merror\033[0m"
	if got != want {
		t.Errorf("RedText() = %q, want %q", got, want)
	}
}

func TestColorsDisabled(t *testing.T) {
	SetColorsEnabled(false)
	defer SetColorsEnabled(true)

	got := RedText("error")
	if got != "error" {
		t.Errorf("with colors disabled, RedText() = %q, want %q", got, "error")
	}

	got = BoldText("bold")
	if got != "bold" {
		t.Errorf("with colors disabled, BoldText() = %q, want %q", got, "bold")
	}
}

func TestAllColorFunctions(t *testing.T) {
	SetColorsEnabled(true)
	tests := []struct {
		name string
		fn   func(string) string
		code string
	}{
		{"Red", RedText, Red},
		{"Green", GreenText, Green},
		{"Yellow", YellowText, Yellow},
		{"Blue", BlueText, Blue},
		{"Cyan", CyanText, Cyan},
		{"Bold", BoldText, Bold},
		{"Dim", DimText, Dim},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn("test")
			want := tt.code + "test" + Reset
			if got != want {
				t.Errorf("%s() = %q, want %q", tt.name, got, want)
			}
		})
	}
}
