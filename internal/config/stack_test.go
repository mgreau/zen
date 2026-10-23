package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mgreau/zen/internal/agent"
)

// stackFixture writes executable launchers named after their config source so
// a resolved Launcher shows which candidate won.
func stackFixture(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	for _, name := range []string{"flag", "repo-review", "repo", "top-review", "top"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestResolveStackPrecedence(t *testing.T) {
	dir := stackFixture(t)
	at := func(name string) string { return filepath.Join(dir, name) }

	type keys struct{ flag, repoReview, repo, topReview, top bool }
	all := keys{true, true, true, true, true}

	tests := []struct {
		name         string
		mode         StackMode
		set          keys
		wantLauncher string // "" means no stack
		wantSource   string
	}{
		{"review: everything set, flag wins", ReviewStack, all, "flag", "--stack"},
		{"review: repo review_stack beats repo stack", ReviewStack, keys{repoReview: true, repo: true, topReview: true, top: true}, "repo-review", "repos.app.review_stack"},
		{"review: repo stack beats top review_stack", ReviewStack, keys{repo: true, topReview: true, top: true}, "repo", "repos.app.stack"},
		{"review: top review_stack beats top stack", ReviewStack, keys{topReview: true, top: true}, "top-review", "review_stack"},
		{"review: top stack alone", ReviewStack, keys{top: true}, "top", "stack"},
		{"review: flag beats repo review_stack only", ReviewStack, keys{flag: true, repoReview: true}, "flag", "--stack"},
		{"review: repo review_stack alone", ReviewStack, keys{repoReview: true}, "repo-review", "repos.app.review_stack"},
		{"review: top review_stack alone", ReviewStack, keys{topReview: true}, "top-review", "review_stack"},
		{"review: repo stack beats top stack", ReviewStack, keys{repo: true, top: true}, "repo", "repos.app.stack"},
		{"review: nothing set", ReviewStack, keys{}, "", ""},

		{"work: everything set, flag wins", WorkStack, all, "flag", "--stack"},
		{"work: repo stack beats top stack, review keys ignored", WorkStack, keys{repoReview: true, repo: true, topReview: true, top: true}, "repo", "repos.app.stack"},
		{"work: top stack when only review keys and top stack", WorkStack, keys{repoReview: true, topReview: true, top: true}, "top", "stack"},
		{"work: repo stack alone", WorkStack, keys{repo: true}, "repo", "repos.app.stack"},
		{"work: top stack alone", WorkStack, keys{top: true}, "top", "stack"},
		{"work: flag beats top stack", WorkStack, keys{flag: true, top: true}, "flag", "--stack"},
		{"work: review keys alone resolve nothing", WorkStack, keys{repoReview: true, topReview: true}, "", ""},
		{"work: nothing set", WorkStack, keys{}, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pick := func(on bool, name string) string {
				if on {
					return at(name)
				}
				return ""
			}
			c := &Config{
				Stack:       pick(tt.set.top, "top"),
				ReviewStack: pick(tt.set.topReview, "top-review"),
				Repos: map[string]RepoConfig{"app": {
					Stack:       pick(tt.set.repo, "repo"),
					ReviewStack: pick(tt.set.repoReview, "repo-review"),
				}},
			}
			ref, err := c.ResolveStack(tt.mode, "app", pick(tt.set.flag, "flag"))
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantLauncher == "" {
				if ref != nil {
					t.Fatalf("ResolveStack() = %+v, want nil", ref)
				}
				return
			}
			if ref == nil {
				t.Fatalf("ResolveStack() = nil, want %s", tt.wantLauncher)
			}
			if ref.Launcher != at(tt.wantLauncher) || ref.Source != tt.wantSource {
				t.Errorf("ResolveStack() = launcher %q source %q, want %q from %s", ref.Launcher, ref.Source, at(tt.wantLauncher), tt.wantSource)
			}
		})
	}
}

func TestResolveStackUnknownRepoUsesTopLevel(t *testing.T) {
	dir := stackFixture(t)
	c := &Config{Stack: filepath.Join(dir, "top")}
	ref, err := c.ResolveStack(WorkStack, "nope", "")
	if err != nil || ref == nil || ref.Source != "stack" {
		t.Fatalf("ResolveStack() = %+v, %v; want the top-level stack", ref, err)
	}
}

func TestResolveStackWhitespaceValueIsUnset(t *testing.T) {
	c := &Config{Stack: "   "}
	if ref, err := c.ResolveStack(WorkStack, "app", ""); err != nil || ref != nil {
		t.Fatalf("ResolveStack() = %+v, %v; want nil, nil", ref, err)
	}
}

func TestResolveStackForms(t *testing.T) {
	dir := stackFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "stacks"), 0o755); err != nil {
		t.Fatal(err)
	}
	tilde := filepath.Join(home, "stacks", "mine")
	if err := os.WriteFile(tilde, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	tests := []struct {
		name, value, want string
	}{
		{"absolute path", filepath.Join(dir, "top"), filepath.Join(dir, "top")},
		{"tilde path", "~/stacks/mine", tilde},
		{"bare name on PATH", "repo-review", filepath.Join(dir, "repo-review")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := (&Config{}).ResolveStack(WorkStack, "app", tt.value)
			if err != nil {
				t.Fatal(err)
			}
			if ref.Launcher != tt.want || ref.Value != tt.value {
				t.Errorf("got launcher %q value %q, want %q %q", ref.Launcher, ref.Value, tt.want, tt.value)
			}
		})
	}
}

func TestResolveStackErrors(t *testing.T) {
	dir := t.TempDir()
	notExec := filepath.Join(dir, "plain")
	if err := os.WriteFile(notExec, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	missing := filepath.Join(dir, "missing")
	tests := []struct {
		name  string
		cfg   *Config
		mode  StackMode
		flag  string
		wants []string
	}{
		{"flag path missing", &Config{}, WorkStack, missing, []string{missing, "--stack", "does not exist"}},
		{"flag path not executable", &Config{}, WorkStack, notExec, []string{notExec, "--stack", "not executable"}},
		{"flag path is a directory", &Config{}, WorkStack, dir, []string{dir, "--stack", "directory"}},
		{"flag bare name not on PATH", &Config{}, WorkStack, "no-such-stack", []string{"no-such-stack", "--stack", "not found on PATH"}},
		{"bare name present but not executable is not on PATH", &Config{}, WorkStack, "plain", []string{"plain", "--stack", "not found on PATH"}},
		{"repo key", &Config{Repos: map[string]RepoConfig{"app": {Stack: missing}}}, WorkStack, "", []string{missing, "repos.app.stack"}},
		{"repo review key", &Config{Repos: map[string]RepoConfig{"app": {ReviewStack: missing}}}, ReviewStack, "", []string{missing, "repos.app.review_stack"}},
		{"top-level key", &Config{Stack: missing}, WorkStack, "", []string{missing, "stack"}},
		{"top-level review key", &Config{ReviewStack: "no-such-stack"}, ReviewStack, "", []string{"no-such-stack", "review_stack"}},
		{"bad flag wins over good config", &Config{Stack: notExec}, WorkStack, missing, []string{"--stack"}},
		{"bad config is not skipped for a lower one", &Config{ReviewStack: missing, Stack: notExec}, ReviewStack, "", []string{"review_stack"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref, err := tt.cfg.ResolveStack(tt.mode, "app", tt.flag)
			if err == nil {
				t.Fatalf("ResolveStack() = %+v, want an error", ref)
			}
			for _, w := range tt.wants {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

func TestStacksDir(t *testing.T) {
	zen := t.TempDir()
	t.Setenv("ZEN_HOME", zen)
	if got, want := StacksDir("/work/app-pr-12"), filepath.Join(zen, "stacks", "app-pr-12"); got != want {
		t.Errorf("StacksDir() = %q, want %q", got, want)
	}
}

func TestStackRefApply(t *testing.T) {
	zen := t.TempDir()
	t.Setenv("ZEN_HOME", zen)
	ag := agent.New(agent.Claude, "claude")

	if got := (*StackRef)(nil).Apply(ag, "/w/app-pr-1"); got.StartCommand("", "") != "claude" {
		t.Errorf("nil StackRef changed the command: %q", got.StartCommand("", ""))
	}

	ref := &StackRef{Launcher: "/s/launch"}
	got := ref.Apply(ag, "/w/app-pr-1")
	want := "AGENT_STACKS_DIR='" + filepath.Join(zen, "stacks", "app-pr-1") + "' '/s/launch'"
	if cmd := got.StartCommand("", ""); cmd != want {
		t.Errorf("StartCommand() = %q, want %q", cmd, want)
	}
	if got.Bin() != "claude" {
		t.Errorf("Bin() = %q, want the real agent", got.Bin())
	}
}

func TestLoadParsesStackKeys(t *testing.T) {
	zen := t.TempDir()
	t.Setenv("ZEN_HOME", zen)
	yaml := `stack: top
review_stack: top-review
repos:
  app:
    full_name: o/app
    base_path: /tmp/app
    stack: repo
    review_stack: repo-review
`
	if err := os.WriteFile(filepath.Join(zen, "config.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	r := c.Repos["app"]
	if c.Stack != "top" || c.ReviewStack != "top-review" || r.Stack != "repo" || r.ReviewStack != "repo-review" {
		t.Errorf("stack keys not parsed: %+v %+v", c, r)
	}
}
