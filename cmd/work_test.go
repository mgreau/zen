package cmd

import "testing"

func TestValidateWorkBranch(t *testing.T) {
	tests := []struct {
		branch    string
		wantError bool
	}{
		{"feature", false},
		{"DEV-123-feature", false},
		{"", true},
		{"-feature", true},
		{"mgreau/feature", true},
		{"nested/feature", true},
		{"two words", true},
		{" feature", true},
		{"tab\tname", true},
	}
	for _, test := range tests {
		t.Run(test.branch, func(t *testing.T) {
			err := validateWorkBranch(test.branch)
			if (err != nil) != test.wantError {
				t.Fatalf("validateWorkBranch(%q) error = %v, want error %t", test.branch, err, test.wantError)
			}
		})
	}
}
