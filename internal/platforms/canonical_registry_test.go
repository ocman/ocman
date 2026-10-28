package platforms

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms/identity"
)

type canonicalPlatform struct{ *fakePlatform }

func (*canonicalPlatform) CanonicalSessionIDs() bool { return true }

func TestCanonicalRegistryRejectsNativeAdaptersAndCacheKeys(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakePlatform{id: "raw"})
	if err := r.EnableCanonicalSessions(); err == nil || r.CanonicalSessionIDs() {
		t.Fatal("registry enabled a mixed identity domain")
	}
	r.Unregister("raw")
	p := &canonicalPlatform{fakePlatform: &fakePlatform{id: "canonical", available: true, sessions: []db.Session{{ID: "native"}}}}
	r.Register(p)
	r.RememberSessions(p.ID(), []db.Session{{ID: "native"}})
	if err := r.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	if !r.CanonicalSessionIDs() {
		t.Fatal("canonical mode was not retained")
	}
	r.Register(&fakePlatform{id: "raw"})
	if _, ok := r.Get("raw"); ok {
		t.Fatal("native adapter reentered canonical registry")
	}
	r.Register(&fakePlatform{id: p.ID()})
	if current, _ := r.Get(p.ID()); current != p {
		t.Fatal("native adapter replaced canonical execution boundary")
	}
	for _, id := range []string{"native", identity.PendingReference()} {
		r.RememberSessions(p.ID(), []db.Session{{ID: id}})
		if owner, ok := r.PlatformForSession(t.Context(), id); ok || owner != nil {
			t.Fatalf("non-session reference acquired ownership: %s %+v", id, owner)
		}
	}
	id := "ocm_" + uuid.NewString()
	p.sessions = []db.Session{{ID: id}}
	r.RememberSessions(p.ID(), p.sessions)
	if owner, ok := r.PlatformForSession(t.Context(), id); !ok || owner != p {
		t.Fatalf("canonical owner lookup failed: %+v %v", owner, ok)
	}
}
