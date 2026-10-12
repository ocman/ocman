package testutil

import (
	"os"
	"os/exec"
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

func TestWriteScriptWarmsUpWithoutRunningTheBody(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake")
	marker := filepath.Join(dir, "ran")
	WriteScript(t, path, "#!/bin/sh\ntouch "+marker+"\n")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("warm-up ran the script body: %v", err)
	}
	if out, err := exec.Command(path).CombinedOutput(); err != nil {
		t.Fatalf("run: %v: %s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("script body did not run: %v", err)
	}
}
