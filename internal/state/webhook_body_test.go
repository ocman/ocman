package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWebhookBodyFileIsWrittenAndPruned(t *testing.T) {
	d := openTestDB(t)
	path, err := d.WriteWebhookBody("inbox", "../../escape", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(d.dataDir, webhookBodyDir) {
		t.Fatalf("body written outside the store: %s", path)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "hello" {
		t.Fatalf("body = %q, %v", data, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("body mode = %v, %v", info, err)
	}
	// Rewriting the same delivery replaces it in place.
	if again, err := d.WriteWebhookBody("inbox", "../../escape", []byte("again")); err != nil || again != path {
		t.Fatalf("rewrite = %q, %v", again, err)
	}
	keep, err := d.WriteWebhookBody("inbox", "new", []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := d.CleanupWebhookHistory(t.Context(), time.Now().Add(-24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expired body kept: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("fresh body pruned: %v", err)
	}
}

func TestWebhookBodyNeedsDataDir(t *testing.T) {
	d := openTestDB(t)
	d.dataDir = ""
	if _, err := d.WriteWebhookBody("inbox", "d", nil); err == nil {
		t.Fatal("wrote a body without a data directory")
	}
	if err := d.cleanupWebhookBodies(1); err != nil {
		t.Fatal(err)
	}
}
