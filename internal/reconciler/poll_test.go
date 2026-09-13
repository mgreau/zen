package reconciler

import "testing"

func TestDecidePoll(t *testing.T) {
	shaA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	tests := []struct {
		name   string
		exists bool
		author bool
		head   string
		oid    string
		want   PollAction
	}{
		{"create for author", false, true, "", shaA, PollCreate},
		{"skip create for other author", false, false, "", shaA, PollSkip},
		{"up to date", true, true, shaA, shaA, PollSkip},
		{"refresh when SHA moved", true, true, shaA, shaB, PollRefresh},
		{"refresh even if author not in list", true, false, shaA, shaB, PollRefresh},
		{"draft→ready with new SHA is refresh not create", true, true, shaA, shaB, PollRefresh},
		{"empty oid does not thrash", true, true, shaA, "", PollSkip},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecidePoll(tt.exists, tt.author, tt.head, tt.oid)
			if got != tt.want {
				t.Fatalf("DecidePoll = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestShouldSyncWorktree(t *testing.T) {
	tests := []struct {
		name         string
		ignoreDrafts bool
		draft        bool
		closed       bool
		want         bool
	}{
		{"open ready", false, false, false, true},
		{"open draft shown", false, true, false, true},
		{"open draft silenced", true, true, false, false},
		{"ignore_drafts but ready", true, false, false, true},
		{"closed", false, false, true, false},
		{"closed draft silenced", true, true, true, false},
		{"merged treated closed", true, false, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldSyncWorktree(tt.ignoreDrafts, tt.draft, tt.closed)
			if got != tt.want {
				t.Fatalf("ShouldSyncWorktree = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPRStateClosed(t *testing.T) {
	for _, s := range []string{"closed", "CLOSED", "merged", "MERGED"} {
		if !PRStateClosed(s) {
			t.Errorf("PRStateClosed(%q) = false", s)
		}
	}
	if PRStateClosed("open") || PRStateClosed("") {
		t.Fatal("open/empty should not be closed")
	}
}

func TestDecideDaemon_authorMutations(t *testing.T) {
	shaA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	base := PollInput{
		InReviewSearch: true,
		WorktreeExists: true,
		AuthorInList:   true,
		WorktreeHEAD:   shaA,
		HeadOID:        shaB,
	}

	tests := []struct {
		name string
		mod  func(*PollInput)
		want PollAction
	}{
		{
			name: "linear push still in search → refresh",
			mod:  func(*PollInput) {},
			want: PollRefresh,
		},
		{
			name: "force-push still in search → refresh (git reset on clean tree)",
			mod:  func(*PollInput) {},
			want: PollRefresh,
		},
		{
			name: "closed: dropped from search, worktree left stale",
			mod: func(in *PollInput) {
				in.InReviewSearch = false
				in.Closed = true
			},
			want: PollSkip,
		},
		{
			name: "closed but still queued (race) → skip, do not fetch",
			mod: func(in *PollInput) {
				in.Closed = true
			},
			want: PollSkip,
		},
		{
			name: "draft + ignore_drafts: dropped from search, no update",
			mod: func(in *PollInput) {
				in.InReviewSearch = false
				in.IgnoreDrafts = true
				in.IsDraft = true
			},
			want: PollSkip,
		},
		{
			name: "draft + ignore_drafts race (queued then drafted) → skip",
			mod: func(in *PollInput) {
				in.IgnoreDrafts = true
				in.IsDraft = true
			},
			want: PollSkip,
		},
		{
			name: "draft with ignore_drafts off: still refresh",
			mod: func(in *PollInput) {
				in.IgnoreDrafts = false
				in.IsDraft = true
			},
			want: PollRefresh,
		},
		{
			name: "undraft after hidden draft: back in search, SHA moved → refresh not create",
			mod: func(in *PollInput) {
				in.IgnoreDrafts = true
				in.IsDraft = false
				in.WorktreeExists = true
			},
			want: PollRefresh,
		},
		{
			name: "retitle only (same SHA) → skip",
			mod: func(in *PollInput) {
				in.HeadOID = shaA
			},
			want: PollSkip,
		},
		{
			name: "GitHub omitted head OID → skip, do not crash",
			mod: func(in *PollInput) {
				in.HeadOID = ""
			},
			want: PollSkip,
		},
		{
			name: "closed: do not create even if author is in list",
			mod: func(in *PollInput) {
				in.WorktreeExists = false
				in.Closed = true
			},
			want: PollSkip,
		},
		{
			name: "silenced draft: do not create even if author is in list",
			mod: func(in *PollInput) {
				in.WorktreeExists = false
				in.IgnoreDrafts = true
				in.IsDraft = true
			},
			want: PollSkip,
		},
		{
			name: "open→draft + ignore_drafts + SHA moved → skip (do not reset)",
			mod: func(in *PollInput) {
				in.IgnoreDrafts = true
				in.IsDraft = true
			},
			want: PollSkip,
		},
		{
			name: "dropped from search with no flags (closed/draft lag) → skip",
			mod: func(in *PollInput) {
				in.InReviewSearch = false
			},
			want: PollSkip,
		},
		{
			name: "draft→ready, no worktree → create",
			mod: func(in *PollInput) {
				in.IgnoreDrafts = true
				in.IsDraft = false
				in.WorktreeExists = false
			},
			want: PollCreate,
		},
		{
			name: "reopened (back in search, SHA moved) → refresh",
			mod: func(in *PollInput) {
				in.Closed = false
				in.InReviewSearch = true
			},
			want: PollRefresh,
		},
		{
			name: "converted to draft, ignore_drafts off, SHA moved → refresh",
			mod: func(in *PollInput) {
				in.IgnoreDrafts = false
				in.IsDraft = true
			},
			want: PollRefresh,
		},
		{
			name: "head branch renamed (SHA same) → skip",
			mod: func(in *PollInput) {
				in.HeadOID = shaA
			},
			want: PollSkip,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			tt.mod(&in)
			got := DecideDaemon(in)
			if got != tt.want {
				t.Fatalf("DecideDaemon = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestShouldNotifyNew(t *testing.T) {
	tests := []struct {
		name     string
		notified bool
		exists   bool
		want     bool
	}{
		{"first sighting, no worktree (any author)", false, false, true},
		{"already notified, no worktree", true, false, false},
		{"first sighting, worktree already exists", false, true, false},
		{"legacy absorb + worktree", true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ShouldNotifyNew(tt.notified, tt.exists)
			if got != tt.want {
				t.Fatalf("ShouldNotifyNew(%v, %v) = %v, want %v", tt.notified, tt.exists, got, tt.want)
			}
		})
	}
}
