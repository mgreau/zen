package cmd

import (
	"testing"

	ghpkg "github.com/mgreau/zen/internal/github"
)

func reviewFrom(number int, login string) ghpkg.ReviewRequest {
	return ghpkg.ReviewRequest{
		Number: number,
		Author: ghpkg.AuthorInfo{Login: login},
	}
}

func TestReviewsForInbox_IgnoresAuthorsAllowlist(t *testing.T) {
	// Regression test for the bug fixed in this change: explicit review
	// requests (from GetReviewRequests) must survive regardless of the
	// authors config, even when none of the PR authors are configured.
	reviews := []ghpkg.ReviewRequest{
		reviewFrom(101, "billlevine"),
		reviewFrom(102, "someone-else"),
	}

	got := reviewsForInbox(reviews)

	if len(got) != len(reviews) {
		t.Fatalf("reviewsForInbox() dropped explicit review requests: got %d, want %d", len(got), len(reviews))
	}
	for i, pr := range got {
		if pr.Number != reviews[i].Number {
			t.Errorf("reviewsForInbox()[%d].Number = %d, want %d", i, pr.Number, reviews[i].Number)
		}
	}
}

func TestReviewsForInbox_Empty(t *testing.T) {
	if got := reviewsForInbox(nil); len(got) != 0 {
		t.Fatalf("reviewsForInbox(nil) = %v, want empty", got)
	}
}

func TestFilterByAuthors(t *testing.T) {
	prs := []ghpkg.ReviewRequest{
		reviewFrom(1, "alice"),
		reviewFrom(2, "bob"),
		reviewFrom(3, "alice"),
	}

	tests := []struct {
		name    string
		authors []string
		want    []int // expected PR numbers, in order
	}{
		{
			name:    "no authors configured returns everything",
			authors: nil,
			want:    []int{1, 2, 3},
		},
		{
			name:    "single matching author",
			authors: []string{"alice"},
			want:    []int{1, 3},
		},
		{
			name:    "multiple authors, one has no matches",
			authors: []string{"bob", "carol"},
			want:    []int{2},
		},
		{
			name:    "no matching authors returns nothing",
			authors: []string{"carol"},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterByAuthors(prs, tt.authors)
			if len(got) != len(tt.want) {
				t.Fatalf("filterByAuthors() = %d results, want %d", len(got), len(tt.want))
			}
			for i, pr := range got {
				if pr.Number != tt.want[i] {
					t.Errorf("filterByAuthors()[%d].Number = %d, want %d", i, pr.Number, tt.want[i])
				}
			}
		})
	}
}
