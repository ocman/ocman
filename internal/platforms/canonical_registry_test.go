package platforms_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	"github.com/ocman/ocman/internal/platforms/platformtest"
)

type canonicalPlatform struct{ *platformtest.Fake }

func (*canonicalPlatform) CanonicalSessionIDs() bool { return true }

func TestCanonicalRegistryRejectsNativeAdaptersAndCacheKeys(t *testing.T) {
	r := platforms.NewRegistry()
	r.Register(&platformtest.Fake{PlatformID: "raw"})
	if err := r.EnableCanonicalSessions(); err == nil || r.CanonicalSessionIDs() {
		t.Fatal("registry enabled a mixed identity domain")
	}
	r.Unregister("raw")
	p := &canonicalPlatform{Fake: &platformtest.Fake{PlatformID: "canonical", SessionList: []db.Session{{ID: "native"}}}}
	r.Register(p)
	r.RememberSessions(p.ID(), []db.Session{{ID: "native"}})
	if err := r.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	if !r.CanonicalSessionIDs() {
		t.Fatal("canonical mode was not retained")
	}
	r.Register(&platformtest.Fake{PlatformID: "raw"})
	if _, ok := r.Get("raw"); ok {
		t.Fatal("native adapter reentered canonical registry")
	}
	r.Register(&platformtest.Fake{PlatformID: string(p.ID())})
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
	p.SessionList = []db.Session{{ID: id}}
	r.RememberSessions(p.ID(), p.SessionList)
	if owner, ok := r.PlatformForSession(t.Context(), id); !ok || owner != p {
		t.Fatalf("canonical owner lookup failed: %+v %v", owner, ok)
	}
}
