package conversationingest

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
)

type confirmingHistory struct {
	*fakeHistory
	confirmed func(context.Context, string, *db.HistorySnapshot) error
}

func (h *confirmingHistory) HistoryCommitted(ctx context.Context, id string, s *db.HistorySnapshot) error {
	return h.confirmed(ctx, id, s)
}

func TestHistoryConfirmationFollowsDurableCommitAndRetries(t *testing.T) {
	f := fakeSource("receipt")
	m, store := testImporter(t, f)
	w := m.workers[0]
	repair(t, w, nil)
	count := 0
	cause := errors.New("confirmation unavailable")
	h := &confirmingHistory{fakeHistory: f, confirmed: func(ctx context.Context, id string, s *db.HistorySnapshot) error {
		count++
		text, _ := importText(t, store, sourceKey("receipt"))
		if text != "changed" || id != "session" {
			t.Fatal("confirmation preceded durable content")
		}
		return cause
	}}
	w.source.Reader = h
	f.snapshots["session"] = historySnapshot("changed")
	f.snapshots["session"].Subagent = true
	f.snapshots["session"].SourceCursor = "cursor"
	if _, err := w.importSession(t.Context(), f.entries[0]); !errors.Is(err, cause) {
		t.Fatalf("confirmation failure: %v", err)
	}
	cause = nil
	f.snapshots["session"] = &db.HistorySnapshot{Session: db.Session{ID: "session"}, Subagent: true, Partial: true, SourceCursor: "cursor", Version: "v2"}
	if _, err := w.importSession(t.Context(), f.entries[0]); err != nil || count != 2 {
		t.Fatalf("unchanged durable content did not retry confirmation: count=%d err=%v", count, err)
	}
	f.snapshots["session"] = historySnapshot("not committed")
	f.afterSnapshot = func() { _ = store.Close() }
	if _, err := w.importSession(t.Context(), f.entries[0]); err == nil || count != 2 {
		t.Fatalf("failed commit confirmed source: count=%d err=%v", count, err)
	}
}
