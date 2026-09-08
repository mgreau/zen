package cmd

import (
	"io"
	"os"
	"testing"

	"github.com/mgreau/zen/internal/review"
)

// pipeStdin replaces os.Stdin with the read end of a pipe holding input, the
// shape `yes | zen review 20` produces. Returns the reader so a test can check
// what is left unread.
func pipeStdin(t *testing.T, input string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, input); err != nil {
		t.Fatal(err)
	}
	w.Close()

	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		r.Close()
	})
	return r
}

func TestConfirmResetWorktree_pipedYesIsDeclined(t *testing.T) {
	// Regression: `yes | zen review 20 --no-terminal` ran git reset --hard.
	// fmt.Scanln happily read the pipe; only --json was checked. A confirmation
	// that discards commits has to come from a terminal.
	r := pipeStdin(t, "y\ny\ny\n")
	oldJSON := jsonFlag
	jsonFlag = false
	t.Cleanup(func() { jsonFlag = oldJSON })

	if confirmResetWorktree(review.ResetRequest{PRNumber: 20, UniqueCommits: 2}) {
		t.Fatal("piped stdin confirmed a hard reset")
	}

	// The prompt must not have consumed the pipe either: nothing was asked.
	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != "y\ny\ny\n" {
		t.Fatalf("piped input was consumed by a prompt: %q left", rest)
	}
}

func TestConfirmResetWorktree_jsonNeverResets(t *testing.T) {
	pipeStdin(t, "y\n")
	oldJSON := jsonFlag
	jsonFlag = true
	t.Cleanup(func() { jsonFlag = oldJSON })

	if confirmResetWorktree(review.ResetRequest{PRNumber: 20}) {
		t.Fatal("--json confirmed a hard reset")
	}
}

func TestConfirmResetWorktree_bothResetKindsDecline(t *testing.T) {
	pipeStdin(t, "yes\n")
	oldJSON := jsonFlag
	jsonFlag = false
	t.Cleanup(func() { jsonFlag = oldJSON })

	for _, kind := range []review.ResetKind{review.ResetDiverged, review.ResetBehind} {
		if confirmResetWorktree(review.ResetRequest{PRNumber: 20, Kind: kind, UniqueCommits: 1}) {
			t.Fatalf("piped stdin confirmed a %s reset", kind)
		}
	}
}
