package github

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWithTimeout_addsDeadlineWhenNone(t *testing.T) {
	ctx, cancel := withTimeout(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > apiTimeout {
		t.Fatalf("expected deadline within %s, got %s remaining", apiTimeout, remaining)
	}
}

func TestWithTimeout_preservesExistingDeadline(t *testing.T) {
	existing := time.Now().Add(5 * time.Second)
	parent, parentCancel := context.WithDeadline(context.Background(), existing)
	defer parentCancel()

	ctx, cancel := withTimeout(parent)
	defer cancel()

	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline to be set")
	}
	if !deadline.Equal(existing) {
		t.Fatalf("expected existing deadline %v, got %v", existing, deadline)
	}
}

func TestGetCurrentUser_timeoutError(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := GetCurrentUser(ctx)
	if err == nil {
		t.Fatal("expected error from expired context")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error message, got: %s", err)
	}
}

func TestGetReviewRequests_timeoutError(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := GetReviewRequests(ctx, "")
	if err == nil {
		t.Fatal("expected error from expired context")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error message, got: %s", err)
	}
}

func TestGetApprovedUnmerged_timeoutError(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := GetApprovedUnmerged(ctx, "")
	if err == nil {
		t.Fatal("expected error from expired context")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error message, got: %s", err)
	}
}

func TestListOpenPRs_timeoutError(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	_, err := ListOpenPRs(ctx, "owner/repo", 10)
	if err == nil {
		t.Fatal("expected error from expired context")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error message, got: %s", err)
	}
}
