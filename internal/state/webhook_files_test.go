package state

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWebhookFilesAreWrittenAndPruned(t *testing.T) {
	d := openTestDB(t)
	path, err := d.WriteWebhookFile("inbox", "../../escape", WebhookBodyFile, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(d.dataDir, webhookFileDir) {
		t.Fatalf("body written outside the store: %s", path)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "hello" {
		t.Fatalf("body = %q, %v", data, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("body mode = %v, %v", info, err)
	}
	// Rewriting the same delivery replaces it in place.
	if again, err := d.WriteWebhookFile("inbox", "../../escape", WebhookBodyFile, []byte("again")); err != nil || again != path {
		t.Fatalf("rewrite = %q, %v", again, err)
	}
	headers, err := d.WriteWebhookFile("inbox", "../../escape", WebhookHeadersFile, []byte("{}"))
	if err != nil || headers != strings.TrimSuffix(path, ".body")+".headers" {
		t.Fatalf("headers = %q, %v", headers, err)
	}
	if _, err := d.WriteWebhookFile("inbox", "d", "../x", nil); err == nil {
		t.Fatal("wrote an unknown file kind")
	}
	keep, err := d.WriteWebhookFile("inbox", "new", WebhookBodyFile, []byte("new"))
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

// Every inbox poller calls cleanup every 15s; the directory is swept at most
// once per interval, and a sweep walks past one read batch.
func TestWebhookFilesSweepIsThrottledAndBatched(t *testing.T) {
	d := openTestDB(t)
	old := time.Now().Add(-48 * time.Hour)
	var paths []string
	for i := range webhookFilesGCBatch + 44 {
		path, err := d.WriteWebhookFile("inbox", fmt.Sprint(i), WebhookBodyFile, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	cutoff := time.Now().Add(-24 * time.Hour).UnixMilli()
	if err := d.CleanupWebhookHistory(t.Context(), cutoff); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expired file kept past a batch: %s", path)
		}
	}
	late, err := d.WriteWebhookFile("inbox", "late", WebhookBodyFile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(late, old, old); err != nil {
		t.Fatal(err)
	}
	// Other pollers within the interval must not rescan.
	for range 3 {
		if err := d.CleanupWebhookHistory(t.Context(), cutoff); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(late); err != nil {
		t.Fatalf("swept again within the interval: %v", err)
	}
	d.webhookFilesGCAt.Store(time.Now().Add(-webhookFilesGCInterval).UnixMilli())
	if err := d.CleanupWebhookHistory(t.Context(), cutoff); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(late); !os.IsNotExist(err) {
		t.Fatalf("not swept after the interval: %v", err)
	}
}

func TestWebhookFilesNeedDataDir(t *testing.T) {
	d := openTestDB(t)
	d.dataDir = ""
	if _, err := d.WriteWebhookFile("inbox", "d", WebhookBodyFile, nil); err == nil {
		t.Fatal("wrote a body without a data directory")
	}
	if err := d.cleanupWebhookFiles(1); err != nil {
		t.Fatal(err)
	}
}
