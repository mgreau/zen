package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgreau/zen/internal/session"
)

func TestRelocateClaudeSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := filepath.Join(home, "git", "app")
	worktree := filepath.Join(home, "git", "app-feature")
	for _, d := range []string{src, filepath.Join(worktree, "internal")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oldID := "11111111-1111-4111-8111-111111111111"
	srcProj := session.ProjectDirPath(src)
	if err := os.MkdirAll(filepath.Join(srcProj, oldID, "subagents"), 0o700); err != nil {
		t.Fatal(err)
	}

	lines := []string{
		`{"type":"mode","mode":"normal","sessionId":"` + oldID + `"}`,
		`{"type":"user","sessionId":"` + oldID + `","cwd":"` + src + `","gitBranch":"main","message":{"role":"user","content":"look at <main.go> & fix"},"uuid":"u1"}`,
		`{"type":"assistant","sessionId":"` + oldID + `","cwd":"` + src + `/internal","message":{"role":"assistant","content":[]},"uuid":"a1"}`,
		`{"type":"user","sessionId":"` + oldID + `","cwd":"` + src + `-other","uuid":"u2"}`,
		`{"type":"api-request","session_id":"` + oldID + `"}`,
		`{"type":"ai-title","aiTitle":"Investigate flaky tests","sessionId":"` + oldID + `"}`,
		`not json at all`,
		`{"type":"last-prompt","lastPr`, // partial: the session is still being written
	}
	srcFile := filepath.Join(srcProj, oldID+".jsonl")
	if err := os.WriteFile(srcFile, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := `{"type":"user","sessionId":"` + oldID + `","cwd":"` + src + `","isSidechain":true}` + "\n"
	if err := os.WriteFile(filepath.Join(srcProj, oldID, "subagents", "agent-1.jsonl"), []byte(sub), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcProj, oldID, "subagents", "agent-1.meta.json"), []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}

	info, err := ReadClaudeSessionInfo(srcFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Cwd != src || info.Title != "Investigate flaky tests" {
		t.Errorf("info = %+v", info)
	}

	newID, err := RelocateClaudeSession(srcFile, src, worktree, true, "tp/feature")
	if err != nil {
		t.Fatal(err)
	}
	if newID == oldID {
		t.Fatal("relocated session kept the old id")
	}

	// The copy is discoverable from the worktree, as Claude would find it.
	sessions, _ := session.FindSessions(worktree)
	if len(sessions) != 1 || sessions[0].ID != newID {
		t.Fatalf("FindSessions(worktree) = %+v, want only %s", sessions, newID)
	}
	got, err := os.ReadFile(sessions[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	gotLines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(gotLines) != len(lines)-1 {
		t.Fatalf("got %d lines, want %d (partial line dropped):\n%s", len(gotLines), len(lines)-1, out)
	}
	if strings.Contains(out, oldID) {
		t.Errorf("old session id survived:\n%s", out)
	}
	for _, want := range []string{
		`"cwd":"` + worktree + `"`,
		`"cwd":"` + worktree + `/internal"`,
		`"cwd":"` + src + `-other"`, // a sibling directory is not under src
		`"session_id":"` + newID + `"`,
		`look at <main.go> & fix`, // no HTML escaping
		`"gitBranch":"tp/feature"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("relocated session missing %s:\n%s", want, out)
		}
	}
	if gotLines[6] != "not json at all" {
		t.Errorf("unparsable complete line not kept verbatim: %q", gotLines[6])
	}

	// The original is untouched.
	if orig, _ := os.ReadFile(srcFile); string(orig) != strings.Join(lines, "\n") {
		t.Error("source session file was modified")
	}

	// Sidecar files follow, transcripts rewritten.
	newSub, err := os.ReadFile(filepath.Join(session.ProjectDirPath(worktree), newID, "subagents", "agent-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(newSub), newID) || !strings.Contains(string(newSub), `"cwd":"`+worktree+`"`) {
		t.Errorf("subagent transcript not rewritten: %s", newSub)
	}
	if meta, err := os.ReadFile(filepath.Join(session.ProjectDirPath(worktree), newID, "subagents", "agent-1.meta.json")); err != nil || string(meta) != `{"x":1}` {
		t.Errorf("meta.json = %q, %v", meta, err)
	}

	// The session can be found by id from anywhere.
	if p, err := FindClaudeSessionByID(oldID); err != nil || p != srcFile {
		t.Errorf("FindClaudeSessionByID(old) = %q, %v", p, err)
	}
	if _, err := FindClaudeSessionByID("../../etc/passwd"); err == nil {
		t.Error("FindClaudeSessionByID accepted a non-uuid")
	}
}

func TestRelocateClaudeSessionPathMatching(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	real := filepath.Join(home, "real", "app")
	worktree := filepath.Join(home, "app-feature")
	for _, d := range []string{filepath.Join(real, "pkg"), filepath.Join(worktree, "pkg")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(filepath.Join(home, "real"), link); err != nil {
		t.Fatal(err)
	}
	// Claude recorded the symlinked spelling; git reports the resolved one.
	viaLink := filepath.Join(link, "app")
	id := "22222222-2222-4222-8222-222222222222"
	write := func(t *testing.T) string {
		t.Helper()
		proj := session.ProjectDirPath(viaLink)
		if err := os.MkdirAll(proj, 0o700); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(proj, id+".jsonl")
		body := `{"type":"user","sessionId":"` + id + `","cwd":"` + viaLink + `"}` + "\n" +
			`{"type":"user","sessionId":"` + id + `","cwd":"` + viaLink + `/pkg"}` + "\n"
		if err := os.WriteFile(f, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return f
	}

	for _, tc := range []struct {
		name    string
		subtree bool
		wantPkg string
	}{
		{"same repo moves the subtree", true, worktree + "/pkg"},
		{"other directory moves only itself", false, viaLink + "/pkg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newID, err := RelocateClaudeSession(write(t), real, worktree, tc.subtree, "")
			if err != nil {
				t.Fatal(err)
			}
			out, err := os.ReadFile(filepath.Join(session.ProjectDirPath(worktree), newID+".jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{`"cwd":"` + worktree + `"`, `"cwd":"` + tc.wantPkg + `"`} {
				if !strings.Contains(string(out), want) {
					t.Errorf("missing %s in:\n%s", want, out)
				}
			}
		})
	}
}

func TestRelocateClaudeSessionMissingSubdirFallsBackToRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := filepath.Join(home, "app")
	worktree := filepath.Join(home, "app-feature") // has no newpkg/
	for _, d := range []string{filepath.Join(src, "newpkg"), worktree} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	id := "33333333-3333-4333-8333-333333333333"
	proj := session.ProjectDirPath(src)
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(proj, id+".jsonl")
	if err := os.WriteFile(f, []byte(`{"type":"user","sessionId":"`+id+`","cwd":"`+src+`/newpkg"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	newID, err := RelocateClaudeSession(f, src, worktree, true, "")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(filepath.Join(session.ProjectDirPath(worktree), newID+".jsonl"))
	if !strings.Contains(string(out), `"cwd":"`+worktree+`"`) {
		t.Errorf("cwd not mapped to the worktree root:\n%s", out)
	}
}
