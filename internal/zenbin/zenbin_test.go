package zenbin

import (
	"os"
	"path/filepath"
	"testing"
)

// Path prefers the zen on PATH, a stable path, over the one the
// running binary resolves to.
func TestPath(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "zen")
	if err := os.Symlink(self, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if got, err := Path(); err != nil || got != link {
		t.Errorf("Path() with zen on PATH = %q, %v; want %q", got, err, link)
	}
	t.Setenv("PATH", t.TempDir())
	if got, err := Path(); err != nil || got != self {
		t.Errorf("Path() without zen on PATH = %q, %v; want %q", got, err, self)
	}
}
