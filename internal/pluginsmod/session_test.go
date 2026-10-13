package pluginsmod

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionDirectoryBoundaries(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(root, "inside")
	if err := os.Mkdir(inside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(inside, filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if dir, ok := sessionDirectory(root, "alias"); !ok || dir != inside {
		t.Fatalf("contained symlink: %q %v", dir, ok)
	}
	for _, test := range [][2]string{{"relative", ""}, {root + "/missing", ""}, {root, "missing"}, {root, "../escape"}} {
		if dir, ok := sessionDirectory(test[0], test[1]); ok {
			t.Fatalf("invalid directory admitted: %q", dir)
		}
	}
}
