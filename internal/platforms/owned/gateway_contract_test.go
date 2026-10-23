package owned

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/google/uuid"
)

func TestGatewayRejectsMissingStoresAndMalformedCompatibilityBatches(t *testing.T) {
	var absent *IdentityGateway
	for name, lookup := range map[string]func() error{
		"single": func() error {
			_, err := absent.Lookup(t.Context(), "test", platforms.SessionIdentityRequest{NativeID: "native"})
			return err
		},
		"batch": func() error { _, err := absent.LookupBatch(t.Context(), "test", []string{"native"}); return err },
		"legacy batch": func() error {
			_, err := absent.ResolveLegacyReferences(t.Context(), "test", []string{"native"})
			return err
		},
	} {
		if err := lookup(); !errors.Is(err, platforms.ErrHistoryUnavailable) {
			t.Errorf("%s invented a catalogue: %v", name, err)
		}
	}
	_, _, store := fixture(t)
	registry := platforms.NewRegistry()
	gateway := NewIdentityGateway(store, "owner", func() IdentityRegistry { return registry }, func(context.Context, string) (platforms.Platform, error) {
		return nil, platforms.ErrPlatformUnreachable
	}, func(string) error { return sessioncatalog.ErrInvalid })
	for _, req := range []platforms.SessionIdentityRequest{{}, {ID: "invalid"}, {ID: "invalid", NativeID: "native"}} {
		if _, err := gateway.Lookup(t.Context(), "test", req); !errors.Is(err, sessioncatalog.ErrInvalid) {
			t.Fatalf("malformed lookup accepted: %v", err)
		}
	}
	for _, refs := range [][]string{nil, make([]string, 501)} {
		if _, err := gateway.LookupBatch(t.Context(), "test", refs); !errors.Is(err, sessioncatalog.ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := gateway.ResolveLegacyReferences(t.Context(), "test", refs); !errors.Is(err, sessioncatalog.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := gateway.LookupBatch(t.Context(), "test", []string{"native"}); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatal(err)
	}
	if _, err := gateway.ResolveLegacyReferences(t.Context(), "test", []string{"native"}); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatal(err)
	}
	if absent.CoreReference(nil) != "" || absent.CoreReference(&platforms.SessionIdentity{ID: "owned"}) != "owned" {
		t.Fatal("missing compatibility gateway lost canonical identity")
	}
}

func TestGatewayOwnershipAndCompatibilityProjectionFailClosed(t *testing.T) {
	a, native, store := fixture(t)
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	registry.Register(a)
	gateway := NewIdentityGateway(store, "owner", func() IdentityRegistry { return registry }, func(_ context.Context, scope string) (platforms.Platform, error) {
		if p, ok := registry.Get(platforms.ID(scope)); ok {
			return p, nil
		}
		return nil, platforms.ErrPlatformUnreachable
	}, func(string) error { return sessioncatalog.ErrInvalid })
	for _, scope := range []string{"", "test"} {
		if owner, err := gateway.OwnerPlatform(t.Context(), row.ID, scope); err != nil || owner != a {
			t.Fatalf("durable owner not selected: %v %v", owner, err)
		}
	}
	for _, tc := range []struct {
		id, scope string
		want      error
	}{
		{"native", "test", sessioncatalog.ErrInvalid},
		{identity.PendingReference(), "test", platforms.ErrPlatformUnreachable},
		{row.ID, "r-other:test", platforms.ErrNotFound},
	} {
		if _, err := gateway.OwnerPlatform(t.Context(), tc.id, tc.scope); !errors.Is(err, tc.want) {
			t.Errorf("owner %q/%q: %v", tc.scope, tc.id, err)
		}
	}
	if _, err := gateway.LookupReference(t.Context(), "test", identity.PendingReference()); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatal(err)
	}
	registry.Unregister("test")
	if _, err := gateway.OwnerPlatform(t.Context(), row.ID, ""); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("offline owner fell back: %v", err)
	}
	registry.Register(native)
	if _, err := gateway.Lookup(t.Context(), "test", platforms.SessionIdentityRequest{NativeID: "native"}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("raw adapter gained legacy-link execution: %v", err)
	}
	if _, err := gateway.LookupBatch(t.Context(), "test", []string{"native"}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("batch bypassed canonical boundary: %v", err)
	}
	if _, err := gateway.OwnerPlatform(t.Context(), row.ID, ""); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("raw adapter gained canonical execution: %v", err)
	}
	registry.Register(a)
	pending, _, err := store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "pending-owner", PayloadHash: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.OwnerPlatform(t.Context(), pending.ID, ""); !errors.Is(err, sessioncatalog.ErrCreating) {
		t.Fatalf("pending creation gained execution: %v", err)
	}
	if _, err := gateway.OwnerPlatform(t.Context(), "ocm_"+uuid.NewString(), "test"); err == nil {
		t.Fatal("unknown canonical handle acquired owner")
	}
	if err := store.Scrub(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "source"}, "native"); err != nil {
		t.Fatal(err)
	}
	if _, err := gateway.OwnerPlatform(t.Context(), row.ID, ""); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("purged handle gained owner: %v", err)
	}
	var absent *IdentityGateway
	if _, err := absent.OwnerPlatform(t.Context(), row.ID, ""); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("missing gateway invented authority: %v", err)
	}
}
