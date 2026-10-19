package claudeprovider

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestHistoryInventoryIncludesCompletedChildren(t *testing.T) {
	config := fixture(t)
	index := openSynced(t, config)
	history := &HistoryReader{index: index}
	entries, err := history.Inventory(t.Context())
	if err != nil || len(entries) != 3 {
		t.Fatalf("complete inventory: %+v %v", entries, err)
	}
	snapshot, err := history.Snapshot(t.Context(), mainID)
	if err != nil || len(snapshot.Messages) != 4 || len(snapshot.Parts) == 0 {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	child, err := history.Snapshot(t.Context(), childID)
	if err != nil || child.Session.ParentID != mainID || len(child.Messages) == 0 {
		t.Fatalf("child: %+v %v", child, err)
	}
	if _, err := history.Snapshot(t.Context(), "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	entry, err := index.get(t.Context(), mainID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(entry.Path, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Snapshot(t.Context(), mainID); !errors.Is(err, db.ErrHistoryChanged) {
		t.Fatalf("accepted stale transcript offsets: %v", err)
	}
	if err := index.Sync(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Inventory(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Snapshot(t.Context(), mainID); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(config, "projects"), filepath.Join(config, "offline")); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Inventory(t.Context()); err == nil {
		t.Fatal("missing root became an authoritative empty inventory")
	}
}

func TestHistoryScansUninitializedInventoryAndCancelsBlockedReads(t *testing.T) {
	index, err := OpenIndex(filepath.Join(t.TempDir(), "index.db"), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	history := &HistoryReader{index: index}
	if entries, err := history.Inventory(t.Context()); err != nil || len(entries) != 3 {
		t.Fatalf("uninitialized index inventory: %+v %v", entries, err)
	}
	index.mu.Lock()
	defer index.mu.Unlock()
	ctx, stop := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer stop()
	if _, err := history.Inventory(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("inventory waited beyond cancellation for index writer: %v", err)
	}
	if _, err := history.Snapshot(ctx, mainID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("snapshot waited beyond cancellation for index writer: %v", err)
	}
	reader := historyContextReader{ctx: ctx, reader: strings.NewReader("must not read")}
	if n, err := reader.Read(make([]byte, 10)); n != 0 || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled transcript read: %d %v", n, err)
	}
}

func TestHistoryFailsClosedForInvalidTranscriptAndCancellation(t *testing.T) {
	index := openSynced(t, fixture(t))
	history := &HistoryReader{index: index}
	entry, err := index.get(t.Context(), mainID)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the indexed size/mtime while introducing invalid JSON. The new
	// history reader must not inherit the renderer's best-effort line skipping.
	raw, err := os.ReadFile(entry.Path)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '!'
	if err := os.WriteFile(entry.Path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(0, entry.Mtime)
	if err := os.Chtimes(entry.Path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := history.Snapshot(t.Context(), mainID); err == nil {
		t.Fatal("invalid transcript silently dropped history")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := history.Inventory(ctx); err == nil {
		t.Fatal("read cancelled inventory")
	}
	if _, err := history.Snapshot(ctx, mainID); err == nil {
		t.Fatal("read cancelled snapshot")
	}
	index.Close()
	if _, err := history.Inventory(t.Context()); err == nil {
		t.Fatal("read closed inventory")
	}
	if _, err := history.Snapshot(t.Context(), mainID); err == nil {
		t.Fatal("read closed snapshot")
	}
}
