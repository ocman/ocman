package server

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestAnalyticsIdentityUsesPhysicalHistorySourceNotActiveNativeCollision(t *testing.T) {
	s, _, ownedID := identityHTTPFixture(t)
	store := s.historyAnalytics.Store
	for _, source := range []string{"source", "inactive"} {
		if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: source, OwnerID: "owner", Platform: "test", SourceKey: source}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: source, SessionID: "native"}, Title: source, CreatedAt: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	active, err := s.historySessionPublicID(t.Context(), "source", "native")
	if err != nil || active != ownedID {
		t.Fatalf("active history identity: %s %v", active, err)
	}
	inactive, err := s.historySessionPublicID(t.Context(), "inactive", "native")
	if err != nil || inactive == "" || inactive == ownedID {
		t.Fatalf("colliding source was rebound: %s %v", inactive, err)
	}
	again, err := s.historySessionPublicID(t.Context(), "inactive", "native")
	if err != nil || again != inactive {
		t.Fatalf("retained history identity changed: %s %v", again, err)
	}
	if _, err := s.sessions.LookupSessionIdentity(t.Context(), "test", platforms.SessionIdentityRequest{ID: inactive}); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("retained identity acquired active execution authority: %v", err)
	}
}
