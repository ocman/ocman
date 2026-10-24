package claudeprovider

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/platformtest"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type rejectionHistoryPlatform struct {
	*provider.Adapter
	platformtest.NoBrowsing
	source string
}

func (p *rejectionHistoryPlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: string(p.ID()), SourceKey: p.source}, nil
}

func TestRejectedClaudeForkAllowsLaterExternalDiscovery(t *testing.T) {
	h := startLive(t, "", nil)
	parent := h.create(t)
	db := statetest.Open(t, sessioncatalog.Schema)
	catalogue, err := sessioncatalog.New(db)
	if err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: "owner", Platform: string(h.adapter.ID()), SourceKey: "claude-test"}
	if _, err := catalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: parent, Directory: h.dir}}); err != nil {
		t.Fatal(err)
	}
	// The catalogue-backed service needs only the physical source metadata;
	// native operations still use the real socket adapter and Claude fixture.
	p := &rejectionHistoryPlatform{Adapter: h.adapter, source: source.SourceKey}
	registry := platforms.NewRegistry()
	registry.Register(p)
	svc := sessionsvc.New(registry, sessionsvc.Hooks{})
	svc.WithSessionCatalogue(catalogue, source.OwnerID)
	_, err = svc.Fork(t.Context(), string(p.ID()), platforms.ForkSessionRequest{SessionID: parent, MessageID: "unsupported-message"})
	if !errors.Is(err, platforms.ErrCreationRejected) {
		t.Fatalf("missing definitive rejection: %v", err)
	}
	result, err := catalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "later-external", Directory: h.dir}})
	if err != nil || result.Created != 1 || result.Deferred != 0 {
		t.Fatalf("rejected Claude fork blocked discovery: %+v %v", result, err)
	}
}
