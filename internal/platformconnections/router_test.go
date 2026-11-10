package platformconnections

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
)

func TestRouterSeparatesAccountsWithoutChangingPlatformKind(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	r := NewRouter(s)
	var calls [2]atomic.Int32
	for i := range 2 {
		c := create(t, s, "Same display name")
		p := &platformtest.Fake{PlatformID: c.Kind, SendMessageFn: func(req platforms.SendMessageRequest) error {
			if req.SessionID != "same-native-id" {
				t.Errorf("native identity changed: %q", req.SessionID)
			}
			calls[i].Add(1)
			return nil
		}}
		if err := r.Bind(t.Context(), c.ID, p); err != nil {
			t.Fatal(err)
		}
		route, err := r.Resolve(t.Context(), Reference{ConnectionID: c.ID})
		if err != nil || route.Connection.ID != c.ID || route.Platform != p || route.Platform.ID() != platforms.ID(c.Kind) {
			t.Fatalf("named route changed platform identity: %+v %v", route, err)
		}
		if err := route.Platform.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: "same-native-id"}); err != nil {
			t.Fatal(err)
		}
		if calls[i].Load() != 1 {
			t.Fatal("selected another connection's adapter")
		}
	}
	if calls[0].Load() != 1 || calls[1].Load() != 1 {
		t.Fatal("same-kind registration replaced an account")
	}
}

func TestRouterNeverFallsBack(t *testing.T) {
	for _, mode := range []string{"missing", "unbound", "disabled", "removed", "offline"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := openStore(t)
			initialize(t, s)
			selected, sibling := create(t, s, "Selected"), create(t, s, "Sibling")
			if err := s.SetDefault(t.Context(), selected.ID); err != nil {
				t.Fatal(err)
			}
			r := NewRouter(s)
			if err := r.Bind(t.Context(), sibling.ID, &platformtest.Fake{PlatformID: sibling.Kind}); err != nil {
				t.Fatal(err)
			}
			if mode != "unbound" {
				if err := r.Bind(t.Context(), selected.ID, &platformtest.Fake{PlatformID: selected.Kind, Availability: new(mode != "offline")}); err != nil {
					t.Fatal(err)
				}
			}
			var err error
			switch mode {
			case "disabled":
				err = s.SetEnabled(t.Context(), selected.ID, false)
			case "removed":
				err = s.Remove(t.Context(), selected.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			refs := []Reference{{ConnectionID: selected.ID}, {}}
			want := ErrUnavailable
			if mode == "missing" {
				refs, want = []Reference{{ConnectionID: "cnx_missing"}}, ErrNotFound
			}
			for _, ref := range refs {
				got, err := r.Resolve(t.Context(), ref)
				if !errors.Is(err, want) || got.Platform != nil || got.Connection.ID != "" {
					t.Fatalf("unavailable selection fell back: %+v %v", got, err)
				}
			}
		})
	}
}

func TestRouterUsesExplicitDefaultAndFreshMetadata(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	a, b := create(t, s, "A"), create(t, s, "B")
	r := NewRouter(s)
	for _, c := range []Connection{a, b} {
		if err := r.Bind(t.Context(), c.ID, &platformtest.Fake{PlatformID: c.Kind}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Resolve(t.Context(), Reference{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("selected an implicit default: %v", err)
	}
	for _, c := range []Connection{b, a} {
		if err := s.SetDefault(t.Context(), c.ID); err != nil {
			t.Fatal(err)
		}
		got, err := r.Resolve(t.Context(), Reference{})
		if err != nil || got.Connection.ID != c.ID {
			t.Fatalf("did not reread the explicit default: %+v %v", got, err)
		}
	}
	if err := s.Rename(t.Context(), a.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Resolve(t.Context(), Reference{ConnectionID: a.ID})
	if err != nil || got.Connection.Name != "Renamed" {
		t.Fatalf("metadata cached across rename: %+v %v", got, err)
	}
	if _, err := r.Resolve(t.Context(), Reference{ConnectionID: "Renamed"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("display name accepted as routing identity")
	}
}

func TestRouterRetainsLegacyMappingAfterRemoval(t *testing.T) {
	s, _ := openStore(t)
	_, err := s.Initialize(t.Context(), Seed{ExistingInstallation: true, Connections: []Input{{Kind: "claudecode", Name: "Legacy", Configuration: "existing", Enabled: true}}})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := s.Legacy(t.Context(), "claudecode")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter(s)
	for _, c := range []Connection{legacy, create(t, s, "New")} {
		if err := r.Bind(t.Context(), c.ID, &platformtest.Fake{PlatformID: c.Kind}); err != nil {
			t.Fatal(err)
		}
	}
	ref := Reference{LegacyKind: "claudecode"}
	got, err := r.Resolve(t.Context(), ref)
	if err != nil || got.Connection.ID != legacy.ID {
		t.Fatalf("legacy reference not pinned: %+v %v", got, err)
	}
	if err := s.Remove(t.Context(), legacy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(t.Context(), ref); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("legacy mapping fell through to a sibling: %v", err)
	}
	if _, err := r.Resolve(t.Context(), Reference{LegacyKind: "codex"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unmapped kind accepted: %v", err)
	}
}

func TestRouterBindingValidationAndConcurrentRegistration(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	c := create(t, s, "Account")
	r := NewRouter(s)
	p := &platformtest.Fake{PlatformID: c.Kind}
	for _, tc := range []struct {
		id   string
		p    platforms.Platform
		want error
	}{{c.ID, nil, ErrInvalid}, {"missing", p, ErrNotFound}, {c.ID, &platformtest.Fake{PlatformID: "codex"}, ErrInvalid}, {c.Kind, p, ErrNotFound}} {
		if err := r.Bind(t.Context(), tc.id, tc.p); !errors.Is(err, tc.want) {
			t.Fatalf("binding validation: %v, want %v", err, tc.want)
		}
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			if err := r.Bind(t.Context(), c.ID, p); err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrAlreadyBound) {
				t.Errorf("concurrent binding: %v", err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("registration replaced a previously bound adapter")
	}
	if err := s.Remove(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	if err := NewRouter(s).Bind(t.Context(), c.ID, p); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("bound a tombstone: %v", err)
	}
}

func TestRouterCancellationAndUnavailableStore(t *testing.T) {
	s, db := openStore(t)
	initialize(t, s)
	c := create(t, s, "Account")
	r := NewRouter(s)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := r.Bind(ctx, c.ID, &platformtest.Fake{PlatformID: c.Kind}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := r.Resolve(ctx, Reference{ConnectionID: c.ID}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := r.Resolve(t.Context(), Reference{ConnectionID: c.ID, LegacyKind: c.Kind}); !errors.Is(err, ErrInvalid) {
		t.Fatal("ambiguous reference accepted")
	}
	if _, err := NewRouter(nil).Resolve(t.Context(), Reference{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := NewRouter(nil).Bind(t.Context(), c.ID, &platformtest.Fake{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(t.Context(), Reference{ConnectionID: c.ID}); err == nil {
		t.Fatal("unreadable store bypassed persisted admission")
	}
}

func TestRouterRechecksEnabledIntentAndReadiness(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	c := create(t, s, "Account")
	if err := s.SetEnabled(t.Context(), c.ID, false); err != nil {
		t.Fatal(err)
	}
	ready := false
	r := NewRouter(s)
	if err := r.Bind(t.Context(), c.ID, &platformtest.Fake{PlatformID: c.Kind, Availability: &ready}); err != nil {
		t.Fatal(err)
	}
	ref := Reference{ConnectionID: c.ID}
	if _, err := r.Resolve(t.Context(), ref); !errors.Is(err, ErrUnavailable) {
		t.Fatal("binding a disabled connection admitted it")
	}
	if err := s.SetEnabled(t.Context(), c.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(t.Context(), ref); !errors.Is(err, ErrUnavailable) {
		t.Fatal("enabled intent was mistaken for runtime readiness")
	}
	ready = true
	if _, err := r.Resolve(t.Context(), ref); err != nil {
		t.Fatal(err)
	}
	ready = false
	if _, err := r.Resolve(t.Context(), ref); !errors.Is(err, ErrUnavailable) {
		t.Fatal("readiness was cached across operations")
	}
}

func TestRouterDoesNotResolveAnotherOwnersConnection(t *testing.T) {
	local, _ := openStore(t)
	remote, _ := openStore(t)
	initialize(t, local)
	initialize(t, remote)
	lc, rc := create(t, local, "Work"), create(t, remote, "Work")
	r := NewRouter(local)
	if err := r.Bind(t.Context(), lc.ID, &platformtest.Fake{PlatformID: lc.Kind}); err != nil {
		t.Fatal(err)
	}
	if err := local.SetDefault(t.Context(), lc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(t.Context(), Reference{ConnectionID: rc.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown owner connection fell back to local default: %v", err)
	}
	if err := r.Bind(t.Context(), rc.ID, &platformtest.Fake{PlatformID: rc.Kind}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("registered another owner's connection: %v", err)
	}
}

type cancelAvailability struct {
	platformtest.Fake
	cancel context.CancelFunc
}

func (p *cancelAvailability) Available(context.Context) bool {
	p.cancel()
	return true
}

func TestRouterRejectsCancellationDuringAvailabilityProbe(t *testing.T) {
	s, _ := openStore(t)
	initialize(t, s)
	c := create(t, s, "Account")
	r := NewRouter(s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := r.Bind(ctx, c.ID, &cancelAvailability{Fake: platformtest.Fake{PlatformID: c.Kind}, cancel: cancel}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(ctx, Reference{ConnectionID: c.ID}); !errors.Is(err, context.Canceled) {
		t.Fatalf("returned a canceled admission: %v", err)
	}
}
