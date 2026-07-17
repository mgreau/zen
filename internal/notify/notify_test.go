package notify

import (
	"reflect"
	"testing"
)

func TestLinuxNotifyArgs(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		message  string
		subtitle string
		want     []string
	}{
		{
			"title and message",
			"PR Merged", "PR #42: fix things", "",
			[]string{"--app-name=zen", "PR Merged", "PR #42: fix things"},
		},
		{
			"subtitle appended to body",
			"New PR Review Request", "PR #7: add feature", "by alice in app",
			[]string{"--app-name=zen", "New PR Review Request", "PR #7: add feature\nby alice in app"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := linuxNotifyArgs(tt.title, tt.message, tt.subtitle)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("linuxNotifyArgs(%q, %q, %q) = %v, want %v", tt.title, tt.message, tt.subtitle, got, tt.want)
			}
		})
	}
}
