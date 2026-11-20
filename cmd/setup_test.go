package cmd

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mgreau/zen/internal/config"
)

// setupTestHome points HOME and ZEN_HOME at temp dirs and embeds one prompt.
func setupTestHome(t *testing.T) (home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZEN_HOME", filepath.Join(home, ".zen"))
	t.Setenv("CODEX_HOME", "")
	t.Setenv("TMUX", "")

	saved := EmbeddedCommands
	t.Cleanup(func() { EmbeddedCommands = saved })
	EmbeddedCommands = fstest.MapFS{
		"commands/review-pr.md": {Data: []byte("review prompt\n")},
	}
	return home
}

func TestParseRepoFlag(t *testing.T) {
	tests := []struct {
		spec      string
		short     string
		full      string
		base      string
		wantError bool
	}{
		{spec: "octo-sts/app:~/git/app", short: "app", full: "octo-sts/app", base: "~/git/app"},
		{spec: "sts=octo-sts/app:~/git/app", short: "sts", full: "octo-sts/app", base: "~/git/app"},
		{spec: "octo-sts/app:/srv/a=b", short: "app", full: "octo-sts/app", base: "/srv/a=b"},
		{spec: "x=octo-sts/app:/srv/c:d", short: "x", full: "octo-sts/app", base: "/srv/c:d"},
		{spec: "octo-sts/app", wantError: true},
		{spec: "octo-sts/app:", wantError: true},
		{spec: "app:~/git/app", wantError: true},
		{spec: "=octo-sts/app:~/git/app", wantError: true},
		{spec: "octo-sts/:~/git/app", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			short, repo, err := parseRepoFlag(tt.spec)
			if (err != nil) != tt.wantError {
				t.Fatalf("parseRepoFlag(%q) error = %v, want error %t", tt.spec, err, tt.wantError)
			}
			if tt.wantError {
				return
			}
			if short != tt.short || repo.FullName != tt.full || repo.BasePath != tt.base {
				t.Errorf("parseRepoFlag(%q) = %q, %+v; want %q, {%s %s}", tt.spec, short, repo, tt.short, tt.full, tt.base)
			}
		})
	}
}

func TestUnattendedSetupFromFlags(t *testing.T) {
	home := setupTestHome(t)
	opts := setupOptions{
		repos:    []string{"app=octo-sts/app:~/git/repo-app", "wolfi-dev/os:~/git/repo-os"},
		authors:  []string{"alice", " bob "},
		agent:    "codex",
		terminal: "tmux",
	}

	var out bytes.Buffer
	if err := runUnattendedSetup(opts, nil, &out); err != nil {
		t.Fatalf("runUnattendedSetup: %v", err)
	}
	if !strings.Contains(out.String(), "Config written") {
		t.Errorf("output = %q, want Config written", out.String())
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load after setup: %v", err)
	}
	if got := cfg.RepoFullName("app"); got != "octo-sts/app" {
		t.Errorf("repo app = %q", got)
	}
	if got := cfg.RepoFullName("os"); got != "wolfi-dev/os" {
		t.Errorf("repo os = %q", got)
	}
	if got := cfg.RepoBasePath("app"); got != filepath.Join(os.Getenv("HOME"), "git/repo-app") {
		t.Errorf("base path app = %q, want ~ expanded", got)
	}
	if strings.Join(cfg.Authors, ",") != "alice,bob" {
		t.Errorf("authors = %v", cfg.Authors)
	}
	if cfg.Agent != "codex" || cfg.Terminal != "tmux" {
		t.Errorf("agent, terminal = %q, %q", cfg.Agent, cfg.Terminal)
	}
	promptPath := filepath.Join(home, ".codex", "prompts", "review-pr.md")
	if got, err := os.ReadFile(promptPath); err != nil || string(got) != "review prompt\n" {
		t.Errorf("codex prompt = %q, %v; want it installed", got, err)
	}
	if !strings.Contains(out.String(), "Installed 1 new codex prompt") {
		t.Errorf("output = %q, want one prompt installed", out.String())
	}

	// Running the same install again is a no-op.
	out.Reset()
	if err := runUnattendedSetup(opts, nil, &out); err != nil {
		t.Fatalf("second runUnattendedSetup: %v", err)
	}
	if !strings.Contains(out.String(), "Config unchanged") || !strings.Contains(out.String(), "Installed 0 new") {
		t.Errorf("second output = %q, want config unchanged and no prompts installed", out.String())
	}
}

func TestUnattendedSetupExistingConfig(t *testing.T) {
	setupTestHome(t)
	path := config.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("# hand edited\nrepos:\n  app:\n    full_name: octo-sts/app\n    base_path: /srv/app\n")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatal(err)
	}

	opts := setupOptions{repos: []string{"octo-sts/app:/srv/other"}, skipPrompts: true}
	err := runUnattendedSetup(opts, nil, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("error = %v, want a --force hint", err)
	}
	if got, _ := os.ReadFile(path); !bytes.Equal(got, original) {
		t.Fatalf("config changed without --force:\n%s", got)
	}

	opts.force = true
	if err := runUnattendedSetup(opts, nil, &bytes.Buffer{}); err != nil {
		t.Fatalf("with --force: %v", err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RepoBasePath("app"); got != "/srv/other" {
		t.Errorf("base path after --force = %q", got)
	}
}

func TestUnattendedSetupConfigFile(t *testing.T) {
	setupTestHome(t)
	data := "# provisioned\nrepos:\n  app:\n    full_name: octo-sts/app\n    base_path: ~/git/app\nauthors: [alice]\n"

	// From stdin, kept byte for byte so comments survive.
	opts := setupOptions{configFile: "-", skipPrompts: true}
	if err := runUnattendedSetup(opts, strings.NewReader(data), &bytes.Buffer{}); err != nil {
		t.Fatalf("config from stdin: %v", err)
	}
	got, err := os.ReadFile(config.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != data {
		t.Errorf("written config = %q, want input unchanged", got)
	}

	// From a file with the same content: unchanged.
	src := filepath.Join(t.TempDir(), "zen.yaml")
	if err := os.WriteFile(src, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	opts.configFile = src
	if err := runUnattendedSetup(opts, nil, &out); err != nil {
		t.Fatalf("config from file: %v", err)
	}
	if !strings.Contains(out.String(), "Config unchanged") {
		t.Errorf("output = %q, want Config unchanged", out.String())
	}
}

func TestUnattendedSetupRejectsBadInput(t *testing.T) {
	tests := []struct {
		name  string
		opts  setupOptions
		stdin string
		want  string
	}{
		{name: "no repos", opts: setupOptions{nonInteractive: true}, want: "--repo"},
		{name: "bad agent", opts: setupOptions{repos: []string{"o/r:/p"}, agent: "gpt"}, want: "invalid agent"},
		{name: "bad terminal", opts: setupOptions{repos: []string{"o/r:/p"}, terminal: "xterm"}, want: "invalid terminal"},
		{name: "duplicate short", opts: setupOptions{repos: []string{"o/r:/p", "x/r:/q"}}, want: "given twice"},
		{name: "file plus flags", opts: setupOptions{configFile: "-", repos: []string{"o/r:/p"}}, want: "cannot be combined"},
		{name: "file not yaml", opts: setupOptions{configFile: "-"}, stdin: "repos: [", want: "invalid config"},
		{name: "file without repos", opts: setupOptions{configFile: "-"}, stdin: "authors: [a]\n", want: "no repos"},
		{name: "file bad full_name", opts: setupOptions{configFile: "-"}, stdin: "repos:\n  a:\n    full_name: app\n    base_path: /p\n", want: "owner/name"},
		{name: "file missing base_path", opts: setupOptions{configFile: "-"}, stdin: "repos:\n  a:\n    full_name: o/app\n", want: "base_path"},
		{name: "file unknown key", opts: setupOptions{configFile: "-"}, stdin: "repos:\n  a:\n    full_name: o/a\n    base_path: /p\nauthers: [x]\n", want: "authers"},
		{name: "file unknown nested key", opts: setupOptions{configFile: "-"}, stdin: "repos:\n  a:\n    full_name: o/a\n    base_path: /p\nwatch:\n  concurency: 4\n", want: "concurency"},
		{name: "file short name with space", opts: setupOptions{configFile: "-"}, stdin: "repos:\n  my app:\n    full_name: o/a\n    base_path: /p\n", want: "short name"},
		{name: "file short name traversal", opts: setupOptions{configFile: "-"}, stdin: "repos:\n  ../x:\n    full_name: o/a\n    base_path: /p\n", want: "short name"},
		{name: "flag short name like a flag", opts: setupOptions{repos: []string{"-x=o/a:/p"}}, want: "short name"},
		{name: "file two documents", opts: setupOptions{configFile: "-"}, stdin: "repos:\n  a:\n    full_name: o/a\n    base_path: /p\n---\nauthers: [x]\n", want: "more than one YAML document"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTestHome(t)
			tt.opts.skipPrompts = true
			err := runUnattendedSetup(tt.opts, strings.NewReader(tt.stdin), &bytes.Buffer{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tt.want)
			}
			if _, statErr := os.Stat(config.Path()); !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("config was written despite the error")
			}
		})
	}
}

func TestSetupUnattendedDetection(t *testing.T) {
	if (setupOptions{}).unattended() {
		t.Error("no flags should run interactively")
	}
	for _, o := range []setupOptions{
		{nonInteractive: true},
		{configFile: "x"},
		{repos: []string{"o/r:/p"}},
		{authors: []string{"a"}},
		{terminal: "tmux"},
	} {
		if !o.unattended() {
			t.Errorf("%+v should run unattended", o)
		}
	}
}

func TestPromptFailsAtEndOfInput(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader(""))
	if _, err := promptRequired(scanner, "GitHub full name"); !errors.Is(err, errStdinClosed) {
		t.Fatalf("promptRequired at EOF = %v, want errStdinClosed", err)
	}

	scanner = bufio.NewScanner(strings.NewReader("\n\nvalue\n"))
	got, err := promptRequired(scanner, "label")
	if err != nil || got != "value" {
		t.Fatalf("promptRequired = %q, %v; want value", got, err)
	}
}

func TestUnattendedSetupThroughSymlink(t *testing.T) {
	home := setupTestHome(t)
	// A dotfiles manager keeps the real file elsewhere and links it in.
	target := filepath.Join(home, "dotfiles", "zen.yaml")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	old := "repos:\n  app:\n    full_name: octo-sts/app\n    base_path: /srv/app\n"
	if err := os.WriteFile(target, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	link := config.Path()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// Same content through the link: unchanged.
	var out bytes.Buffer
	opts := setupOptions{configFile: "-", skipPrompts: true}
	if err := runUnattendedSetup(opts, strings.NewReader(old), &out); err != nil {
		t.Fatalf("same content: %v", err)
	}
	if !strings.Contains(out.String(), "Config unchanged") {
		t.Errorf("output = %q, want Config unchanged", out.String())
	}

	// New content with --force: the link stays and its target is updated.
	updated := "repos:\n  app:\n    full_name: octo-sts/app\n    base_path: /srv/new\n"
	opts.force = true
	if err := runUnattendedSetup(opts, strings.NewReader(updated), &bytes.Buffer{}); err != nil {
		t.Fatalf("--force through symlink: %v", err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is no longer a symlink", link)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != updated {
		t.Errorf("target = %q, want updated content", got)
	}
	if st, err := os.Stat(target); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("target mode = %v, %v; want 0600 kept", st.Mode().Perm(), err)
	}
}

func TestUnattendedSetupLeadingDocumentMarker(t *testing.T) {
	setupTestHome(t)
	// A single document that starts with --- is still one document.
	data := "---\nrepos:\n  a:\n    full_name: o/a\n    base_path: /p\n"
	opts := setupOptions{configFile: "-", skipPrompts: true}
	if err := runUnattendedSetup(opts, strings.NewReader(data), &bytes.Buffer{}); err != nil {
		t.Fatalf("leading ---: %v", err)
	}
}

func TestUnattendedSetupDanglingSymlink(t *testing.T) {
	for _, relative := range []bool{false, true} {
		name := "absolute"
		if relative {
			name = "relative"
		}
		t.Run(name, func(t *testing.T) {
			home := setupTestHome(t)
			// The dotfiles link exists, but its source has not been created.
			target := filepath.Join(home, "dotfiles", "zen", "config.yaml")
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				t.Fatal(err)
			}
			link := config.Path()
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			dest := target
			if relative {
				dest = filepath.Join("..", "dotfiles", "zen", "config.yaml")
			}
			if err := os.Symlink(dest, link); err != nil {
				t.Fatal(err)
			}

			opts := setupOptions{repos: []string{"o/a:/p"}, skipPrompts: true}
			if err := runUnattendedSetup(opts, nil, &bytes.Buffer{}); err != nil {
				t.Fatalf("setup through dangling symlink: %v", err)
			}
			info, err := os.Lstat(link)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("%s was replaced by a regular file", link)
			}
			if _, err := os.Stat(target); err != nil {
				t.Fatalf("symlink target was not written: %v", err)
			}
			if _, err := config.Load(); err != nil {
				t.Fatalf("config.Load through symlink: %v", err)
			}
		})
	}
}

func TestResolveSymlinksLoop(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSymlinks(a); err == nil {
		t.Fatal("resolveSymlinks on a loop succeeded, want an error")
	}
}

func TestPromptValidReasksOnBadAnswer(t *testing.T) {
	scanner := bufio.NewScanner(strings.NewReader("apko\nchainguard-dev/apko\n"))
	got, err := promptValid(scanner, "GitHub full name", "", config.ValidateRepoFullName)
	if err != nil || got != "chainguard-dev/apko" {
		t.Fatalf("promptValid = %q, %v; want the second, valid answer", got, err)
	}

	scanner = bufio.NewScanner(strings.NewReader("my repo\n\n"))
	got, err = promptValid(scanner, "Short name", "apko", config.ValidateRepoShortName)
	if err != nil || got != "apko" {
		t.Fatalf("promptValid = %q, %v; want the default after a bad answer", got, err)
	}

	scanner = bufio.NewScanner(strings.NewReader("../x\n"))
	if _, err := promptValid(scanner, "Short name", "apko", config.ValidateRepoShortName); !errors.Is(err, errStdinClosed) {
		t.Fatalf("promptValid with only bad answers = %v, want errStdinClosed", err)
	}
}
