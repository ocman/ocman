package conversationingest

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func TestFirstImportCallbackOnceAfterComplete(t *testing.T) {
	f := fakeSource("fresh")
	m, store := testImporter(t, f)
	calls := 0
	m.deps.OnInitialImport = func(ctx context.Context, adapter string, sessions []statev2.Session) error {
		calls++
		if adapter != "fresh" || len(sessions) != 1 {
			t.Fatalf("callback %q: %+v", adapter, sessions)
		}
		if _, err := store.ReadConversation(ctx, sessions[0].Key, 10, 0); err != nil {
			t.Fatal("callback before commit", err)
		}
		return nil
	}
	w := m.workers[0]
	f.snapshotErr = errors.New("incomplete")
	if err := w.sweepChecked(t.Context()); err == nil {
		t.Fatal("expected failed import")
	}
	if calls != 0 || m.InitialImportReady("fresh") {
		t.Fatal("failed import completed")
	}
	// The pending marker survives a restart with partially committed history.
	f.snapshotErr = nil
	m = NewModule(m.deps)
	w = m.workers[0]
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !m.InitialImportReady("fresh") {
		t.Fatal("successful import did not complete")
	}
	w.readiness(false)
	if !m.InitialImportReady("fresh") {
		t.Fatal("later refresh paused inactivity maintenance")
	}
	m = NewModule(m.deps)
	if err := m.workers[0].sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("cleanup repeated on restart")
	}
}

func TestInitialImportDeferredInventoryAcrossWorkerModes(t *testing.T) {
	for _, mode := range []string{"poll", "journal", "feed"} {
		t.Run(mode, func(t *testing.T) {
			f := newFakeFeed()
			w, _ := feedImporter(t, f)
			calls := 0
			w.module.deps.OnInitialImport = func(context.Context, string, []statev2.Session) error {
				calls++
				return nil
			}
			f.inventoryErr = &platforms.HistoryDeferred{Count: 1, Err: errors.New("not yet discovered")}
			run := func() {
				if mode == "feed" {
					w.drainFeed(t.Context())
				} else {
					w.feed = nil
					w.journaled = mode == "journal"
					_ = w.sweepChecked(t.Context())
				}
			}
			run()
			if calls != 0 {
				t.Fatal("deferred inventory triggered cleanup")
			}
			f.inventoryErr = nil
			run()
			if calls != 1 {
				t.Fatalf("calls=%d, want one", calls)
			}
			run()
			if calls != 1 {
				t.Fatal("cleanup repeated")
			}
		})
	}
}

func TestFirstImportCallbackRetry(t *testing.T) {
	m, _ := testImporter(t, fakeSource("fresh"))
	calls := 0
	m.deps.OnInitialImport = func(context.Context, string, []statev2.Session) error {
		calls++
		if calls == 1 {
			return errors.New("state unavailable")
		}
		return nil
	}
	w := m.workers[0]
	if err := w.sweepChecked(t.Context()); err == nil {
		t.Fatal("callback error ignored")
	}
	if m.InitialImportReady("fresh") {
		t.Fatal("ready before cleanup succeeded")
	}
	// An explicit targeted refresh must still finish a full inventory before
	// retrying the initial cleanup.
	w.dirty["session"] = true
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !m.InitialImportReady("fresh") {
		t.Fatalf("calls=%d", calls)
	}
}

func TestFirstImportSkipsExistingHistory(t *testing.T) {
	for _, bookkeeping := range []bool{true, false} {
		t.Run(map[bool]string{true: "bookkeeping", false: "content only"}[bookkeeping], func(t *testing.T) {
			m, store := testImporter(t, fakeSource("existing"))
			w := m.workers[0]
			if err := w.sweepChecked(t.Context()); err != nil {
				t.Fatal(err)
			}
			// Model an upgrade from before initial-import markers existed.
			saved, err := store.SourceState(t.Context(), w.instance.ID)
			if err != nil {
				t.Fatal(err)
			}
			for key := range saved {
				if key == initialImportKey || !bookkeeping {
					if err := store.SetSourceState(t.Context(), w.instance.ID, key, ""); err != nil {
						t.Fatal(err)
					}
				}
			}
			m.deps.OnInitialImport = func(context.Context, string, []statev2.Session) error {
				t.Fatal("existing history cleaned up")
				return nil
			}
			m = NewModule(m.deps)
			if err := m.workers[0].sweepChecked(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
