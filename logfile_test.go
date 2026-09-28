package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"
)

func TestResolveLogPath(t *testing.T) {
	tests := []struct {
		name, flag, goos, xdg, want string
	}{
		{"darwin default", "", "darwin", "/x", "/h/Library/Logs/ocman/ocman.log"},
		{"linux xdg", "", "linux", "/x", "/x/ocman/ocman.log"},
		{"linux no xdg", "", "linux", "", "/h/.local/state/ocman/ocman.log"},
		{"dash disables", "-", "linux", "", ""},
		{"off disables", "off", "darwin", "", ""},
		{"explicit", "/tmp/o.log", "linux", "", "/tmp/o.log"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveLogPath(tt.flag, tt.goos, "/h", tt.xdg); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenLogFileSizeCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "ocman.log")
	f, err := openLogFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v", fi.Mode().Perm())
	}

	// Small file: kept and appended to.
	if err := os.WriteFile(path, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ = openLogFile(path)
	_ = f.Close()
	if b, _ := os.ReadFile(path); string(b) != "keep\n" {
		t.Fatalf("small file changed: %q", b)
	}

	// Oversized file: moved to .1, fresh file opened.
	if err := os.Truncate(path, logFileMaxBytes+1); err != nil {
		t.Fatal(err)
	}
	f, _ = openLogFile(path)
	_ = f.Close()
	if fi, err := os.Stat(path + ".1"); err != nil || fi.Size() != logFileMaxBytes+1 {
		t.Fatalf("rotated file missing: %v", err)
	}
	if fi, _ := os.Stat(path); fi.Size() != 0 {
		t.Fatalf("new file not empty: %d", fi.Size())
	}
}

func TestSetupLogFileWritesPlainLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ocman.log")
	saved := log.StandardLogger().ReplaceHooks(make(log.LevelHooks))
	defer log.StandardLogger().ReplaceHooks(saved)

	if got := setupLogFile(path); got != path {
		t.Fatalf("got %q", got)
	}
	log.Info("hello file")
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "hello file") || strings.Contains(string(b), "\x1b[") {
		t.Fatalf("unexpected log content: %q", b)
	}
	if got := setupLogFile(""); got != "" {
		t.Fatalf("disabled returned %q", got)
	}
	if got := setupLogFile(filepath.Join(path, "nested.log")); got != "" {
		t.Fatalf("unopenable path returned %q", got)
	}
}
