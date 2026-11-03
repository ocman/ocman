package composerattachments

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSweepComposerAttachments covers the unbounded-growth fix: nothing
// ever deleted composer uploads, so real user content (screenshots,
// PDFs, logs) accumulated forever in a directory macOS does not purge.
func TestSweepComposerAttachments(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "projhash", "s1")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(sessionDir, "1-fresh.png")
	stale := filepath.Join(sessionDir, "2-stale.png")
	for _, p := range []string{fresh, stale} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * TTL)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	removed := Sweep(root, TTL)
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("recent attachment was deleted: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale attachment survived the sweep: %v", err)
	}

	// Emptied directories are cleaned up too, so the tree does not grow
	// one dead folder per session forever.
	if err := os.Chtimes(fresh, old, old); err != nil {
		t.Fatal(err)
	}
	Sweep(root, TTL)
	if _, err := os.Stat(sessionDir); !os.IsNotExist(err) {
		t.Errorf("empty session directory survived the sweep: %v", err)
	}

	// A missing root is not an error.
	if got := Sweep(filepath.Join(root, "nope"), TTL); got != 0 {
		t.Errorf("sweep of a missing root = %d, want 0", got)
	}
}

// The sweeper removes expired uploads on its first run, at startup, and
// stops with its context.
func TestSweeperRunsImmediatelyAndStops(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "project", "session", "expired")
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * TTL)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	s := NewSweeper()
	if s.Name() == "" || s.interval != 24*time.Hour {
		t.Fatalf("sweeper = %q every %v", s.Name(), s.interval)
	}
	s.Routes(nil)
	s.root, s.interval = func() string { return root }, time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(file); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expired upload survived the startup sweep")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done

	// A cancelled context skips the sweep.
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	s.Run(ctx)
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("cancelled sweeper removed upload: %v", err)
	}
}
