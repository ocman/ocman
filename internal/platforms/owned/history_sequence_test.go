package owned

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type sequencePlatform struct {
	*historyPlatform
	lookups []string
}

func (p *sequencePlatform) HistorySequence(_ context.Context, id string) (int64, bool, error) {
	p.lookups = append(p.lookups, id)
	seq, ok := p.sequence[id]
	return seq, ok, nil
}

func TestHistorySequenceResolvesOnlyTheRequestedSession(t *testing.T) {
	a, p := historyFixture(t)
	row, err := a.historyIdentity(t.Context(), "hidden-child")
	if err != nil {
		t.Fatal(err)
	}
	// Without a single-session reader the native listing is filtered.
	if seq, ok, err := a.HistorySequence(t.Context(), row.ID); err != nil || !ok || seq != 7 {
		t.Fatalf("listed sequence = %d %v %v", seq, ok, err)
	}
	single := &sequencePlatform{historyPlatform: p}
	a.native = single
	if seq, ok, err := a.HistorySequence(t.Context(), row.ID); err != nil || !ok || seq != 7 || len(single.lookups) != 1 || single.lookups[0] != "hidden-child" {
		t.Fatalf("direct sequence = %d %v %v lookups=%v", seq, ok, err, single.lookups)
	}
	p.sequence = map[string]int64{}
	if _, ok, err := a.HistorySequence(t.Context(), row.ID); err != nil || ok {
		t.Fatalf("session without events = %v %v", ok, err)
	}
	unknown := "ocm_" + strings.Repeat("0", 8) + "-0000-4000-8000-" + strings.Repeat("0", 12)
	if _, ok, err := a.HistorySequence(t.Context(), unknown); err != nil || ok {
		t.Fatalf("unknown session = %v %v", ok, err)
	}
	if _, _, err := a.HistorySequence(t.Context(), "hidden-child"); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("native reference bypassed boundary: %v", err)
	}
	pending, _, err := a.store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "pending", PayloadHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.HistorySequence(t.Context(), pending.ID); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("unbound reservation = %v", err)
	}
	if _, err := a.store.Attach(t.Context(), pending.ID, a.binding("configuring")); err != nil {
		t.Fatal(err)
	}
	var deferred *platforms.HistoryDeferred
	if _, _, err := a.HistorySequence(t.Context(), pending.ID); !errors.As(err, &deferred) {
		t.Fatalf("configuring session = %v, want deferred", err)
	}
	p.source = "another-source"
	if _, _, err := a.HistorySequence(t.Context(), row.ID); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("changed source = %v", err)
	}
	a.native = p.nativePlatform
	if _, _, err := a.HistorySequence(t.Context(), row.ID); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("non-feed platform = %v", err)
	}
}
