package claudecode

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Resolve the physical existing ancestor even before Claude creates its config
// directory. The resulting identity stays stable across first-run initialization.
func canonicalHistoryConfig(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for ancestor := abs; ; ancestor = filepath.Dir(ancestor) {
		physical, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			relative, err := filepath.Rel(ancestor, abs)
			return filepath.Join(physical, relative), err
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if _, statErr := os.Lstat(ancestor); statErr == nil {
			return "", err
		}
		if parent := filepath.Dir(ancestor); parent == ancestor {
			return "", err
		}
	}
}
