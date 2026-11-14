package claudeprovider

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Stale-file inspection can run after creation but before flock. A live
// waiter's inode must survive that interleaving and remain discoverable.
func TestSlotWaiterPublicationSurvivesStaleScan(t *testing.T) {
	dir := t.TempDir()
	w, err := prepareSlotWaiter(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer dropWaiter(w)
	if othersWaiting(dir) {
		t.Fatal("unpublished waiter counted as demand")
	}
	if _, err := os.Stat(w.path); err != nil {
		t.Fatalf("stale scan removed private waiter: %v", err)
	}
	if err := w.publish(); err != nil {
		t.Fatal(err)
	}
	if !othersWaiting(dir) {
		t.Fatal("live slot waiter disappeared during publication")
	}
	if _, err := os.Stat(w.path); err != nil {
		t.Fatalf("published waiter: %v", err)
	}
	dropWaiter(w)
	if othersWaiting(dir) {
		t.Fatal("released waiter still counted as demand")
	}
	if paths, _ := filepath.Glob(filepath.Join(dir, "*")); len(paths) != 0 {
		t.Fatalf("waiter files leaked: %v", paths)
	}
}

func TestSlotWaiterPublicationErrors(t *testing.T) {
	for _, stage := range []string{"create", "lock", "rename"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			if stage == "create" {
				if _, err := prepareSlotWaiter(filepath.Join(dir, "missing")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("create error = %v", err)
				}
				return
			}
			w, err := prepareSlotWaiter(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer dropWaiter(w)
			if stage == "lock" {
				if err := w.file.Close(); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Remove(w.path); err != nil {
				t.Fatal(err)
			}
			if err := w.publish(); err == nil {
				t.Fatal("publication failure was ignored")
			}
		})
	}
}

func TestSlotWaitCreationFailureReturnsWithLockHeld(t *testing.T) {
	l := &live{opts: Options{SlotDir: filepath.Join(t.TempDir(), "missing")}}
	var w *slotWaiter
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.waitSlotLocked(t.Context(), &w); err == nil {
		t.Fatal("waiter creation failure was ignored")
	}
	if l.mu.TryLock() {
		l.mu.Unlock()
		t.Fatal("failed publication released the caller's lock")
	}
	if w != nil {
		t.Fatal("failed waiter was installed")
	}
}
