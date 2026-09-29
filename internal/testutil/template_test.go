package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTemplateCopyBuildsOnceAndCopiesIndependently(t *testing.T) {
	builds := 0
	build := func(path string) error {
		builds++
		return os.WriteFile(path, []byte("schema"), 0o600)
	}
	first := TemplateCopy(t, "copy-test.db", filepath.Join(t.TempDir(), "a.db"), build)
	if err := os.WriteFile(first, []byte("mutated"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := TemplateCopy(t, "copy-test.db", filepath.Join(t.TempDir(), "b.db"), build)
	if first == second {
		t.Fatalf("copies share a path: %s", first)
	}
	data, err := os.ReadFile(second)
	if err != nil || string(data) != "schema" {
		t.Fatalf("second copy = %q, %v; want the pristine template", data, err)
	}
	if builds != 1 {
		t.Fatalf("build ran %d times, want 1", builds)
	}
}
