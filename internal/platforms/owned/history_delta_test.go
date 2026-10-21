package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

type deltaPlatform struct {
	*historyPlatform
	cursor string
	err    error
}

func (p *deltaPlatform) HistoryDelta(ctx context.Context, id, cursor string) (*db.HistorySnapshot, error) {
	p.cursor = cursor
	if p.err != nil {
		return nil, p.err
	}
	return p.HistorySnapshot(ctx, id)
}

func TestHistoryDeltaPreservesCanonicalBoundaryAndCursor(t *testing.T) {
	a, native := historyFixture(t)
	entries, err := a.HistoryInventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	id := entries[0].ID
	if _, err := a.HistoryDelta(t.Context(), id, "cursor"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("unsupported source: %v", err)
	}
	p := &deltaPlatform{historyPlatform: native}
	a.native = p
	p.snapshot.Partial, p.snapshot.MessageOffset, p.snapshot.SourceCursor = true, 7, "next"
	snapshot, err := a.HistoryDelta(t.Context(), id, "previous")
	if err != nil || snapshot.Session.ID != id || snapshot.Messages[0].SessionID != id || snapshot.Parts[0].SessionID != id || !snapshot.Partial || snapshot.MessageOffset != 7 || snapshot.SourceCursor != "next" || p.cursor != "previous" || p.requestedID != "hidden-child" {
		t.Fatalf("delta boundary: %+v %v", snapshot, err)
	}
	if _, err := a.HistoryDelta(t.Context(), "hidden-child", ""); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("native ID bypass: %v", err)
	}
	p.err = errors.New("source read failed")
	if _, err := a.HistoryDelta(t.Context(), id, ""); !errors.Is(err, p.err) {
		t.Fatalf("source error swallowed: %v", err)
	}
	p.err = nil
	p.source = "replaced"
	if _, err := a.HistoryDelta(t.Context(), id, ""); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("source change bypass: %v", err)
	}
}
