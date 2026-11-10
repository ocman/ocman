package platforms_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
)

func TestConnectionRegistryCachesExactKey(t *testing.T) {
	r := platforms.NewRegistry()
	a := &platformtest.Fake{PlatformID: "claudecode", SessionList: []db.Session{{ID: "a"}}}
	b := &platformtest.Fake{PlatformID: "claudecode", SessionList: []db.Session{{ID: "b"}}}
	if err := r.RegisterConnection("cnx_a", a); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterConnection("cnx_b", b); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterConnection("cnx_a", b); err == nil {
		t.Fatal("replaced live adapter")
	}
	for range 2 {
		if p, ok := r.PlatformForSession(t.Context(), "a"); !ok || p != a {
			t.Fatal("lost A")
		}
		if p, ok := r.PlatformForSession(t.Context(), "b"); !ok || p != b {
			t.Fatal("lost B")
		}
	}
	if key, ok := r.KeyFor(a); !ok || key != "cnx_a" {
		t.Fatal(key)
	}
	r.Unregister("cnx_a")
	if _, ok := r.PlatformForSession(t.Context(), "a"); ok {
		t.Fatal("retained removed key")
	}
}

type remoteSourceSet struct {
	*platformtest.Fake
	sources []string
}

func (p remoteSourceSet) CanonicalSources() (string, string, []string) {
	return "owner", "claudecode", p.sources
}

func TestConnectionRegistryUsesAuthenticatedSourceSetsAndRejectsAmbiguity(t *testing.T) {
	r := platforms.NewRegistry()
	p := &remoteSourceSet{Fake: &platformtest.Fake{PlatformID: "r-owner:cnx_work"}, sources: []string{"active", "retained"}}
	if err := r.RegisterConnection(p.ID(), p); err != nil {
		t.Fatal(err)
	}
	if got, key, ok := r.PlatformForCanonicalSource(t.Context(), "owner", "claudecode", "retained"); !ok || got != p || key != p.ID() {
		t.Fatalf("retained source lost: %v %s %v", got, key, ok)
	}
	if _, _, ok := r.PlatformForCanonicalSource(t.Context(), "other", "claudecode", "retained"); ok {
		t.Fatal("cross-owner source")
	}
	if _, _, ok := r.PlatformForCanonicalSource(t.Context(), "owner", "opencode", "retained"); ok {
		t.Fatal("cross-kind source")
	}
	q := &remoteSourceSet{Fake: &platformtest.Fake{PlatformID: "r-owner:cnx_other"}, sources: []string{"retained"}}
	if err := r.RegisterConnection(q.ID(), q); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := r.PlatformForCanonicalSource(t.Context(), "owner", "claudecode", "retained"); ok {
		t.Fatal("ambiguous source chose first account")
	}
	r.SetSourceResolver(func(context.Context, string, string, string) (platforms.ID, error) { return p.ID(), nil })
	if got, key, ok := r.PlatformForCanonicalSource(t.Context(), "local", "claudecode", "local"); !ok || got != p || key != p.ID() {
		t.Fatal(key, ok)
	}
	r.SetSourceResolver(func(context.Context, string, string, string) (platforms.ID, error) { return "", errors.New("offline") })
	if _, _, ok := r.PlatformForCanonicalSource(t.Context(), "local", "claudecode", "local"); ok {
		t.Fatal("failed resolver fell back")
	}
	r.SetLegacyResolver(func(context.Context, string) (platforms.ID, error) { return "cnx_original", nil })
	if key, err := r.ResolveLegacyScope(t.Context(), "claudecode"); err != nil || key != "cnx_original" {
		t.Fatal(key, err)
	}
}
