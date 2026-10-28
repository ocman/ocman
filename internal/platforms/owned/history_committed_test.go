package owned

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/db"
)

type confirmingPlatform struct {
	*historyPlatform
	id       string
	snapshot *db.HistorySnapshot
}

func (p *confirmingPlatform) HistoryCommitted(_ context.Context, id string, s *db.HistorySnapshot) error {
	p.id, p.snapshot = id, s
	return nil
}

func TestHistoryConfirmationUsesOwnerNativeIdentity(t *testing.T) {
	a, p := historyFixture(t)
	observer := &confirmingPlatform{historyPlatform: p}
	a.native = observer
	entries, err := a.HistoryInventory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	s, err := a.HistorySnapshot(t.Context(), entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.HistoryCommitted(t.Context(), entries[0].ID, s); err != nil || observer.id != "hidden-child" || observer.snapshot != s {
		t.Fatalf("wrong confirmation identity: %q %v", observer.id, err)
	}
	if err := a.HistoryCommitted(t.Context(), "native", s); err == nil {
		t.Fatal("native reference bypassed owner boundary")
	}
	id := ingest(t, a, p.snapshot)
	if err := a.HistoryCommitted(t.Context(), id, &db.HistorySnapshot{Subagent: true, Partial: true}); err != nil {
		t.Fatal(err)
	}
	if len(observer.snapshot.Messages) != len(p.snapshot.Messages) || len(observer.snapshot.Parts) != len(p.snapshot.Parts) {
		t.Fatal("empty replay did not confirm retained durable content")
	}
}
