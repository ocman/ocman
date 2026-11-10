package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestIdentityReadAdapterRejectsUnknownAndCancelledScopes(t *testing.T) {
	r := newConnectionRig(t)
	if _, err := r.svc.identityReadAdapter(t.Context(), "cnx_missing"); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("missing account borrowed another registration: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.svc.identityReadAdapter(ctx, r.connections[0].ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled retained read: %v", err)
	}
}

func TestNamedRetainedIdentityLookupDoesNotRequireExecutionAdmission(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "removed"}[removed], func(t *testing.T) {
			r := newConnectionRig(t)
			c := r.connections[1]
			created, err := r.svc.Create(t.Context(), c.ID, identityRequest())
			if err != nil {
				t.Fatal(err)
			}
			adapter, err := r.svc.resolve(t.Context(), created.ID, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			registry := r.svc.registry.(*platforms.Registry)
			if err := registry.RegisterConnection(platforms.ID(c.ID), adapter); err != nil {
				t.Fatal(err)
			}
			registry.SetSourceResolver(func(_ context.Context, owner, kind, source string) (platforms.ID, error) {
				if owner != "owner" || kind != c.Kind || source != c.Directory {
					return "", platforms.ErrNotFound
				}
				return platforms.ID(c.ID), nil
			})
			if removed {
				err = r.store.Remove(t.Context(), c.ID)
			} else {
				err = r.store.SetEnabled(t.Context(), c.ID, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, scope := range []string{c.ID, ""} {
				info, err := r.svc.LookupSessionIdentity(t.Context(), scope, platforms.SessionIdentityRequest{ID: created.ID})
				if err != nil || info == nil || info.ID != created.ID || info.SourceKey != c.Directory || info.NativeID != "" {
					t.Fatalf("retained identity scope=%q: %+v %v", scope, info, err)
				}
			}
			if _, err := r.svc.LookupSessionIdentity(t.Context(), r.connections[0].ID, platforms.SessionIdentityRequest{ID: created.ID}); err == nil {
				t.Fatal("retained identity switched to sibling")
			}
			if err := r.svc.Abort(t.Context(), c.ID, platforms.AbortRequest{SessionID: created.ID}); err == nil {
				t.Fatal("read-only identity granted execution after detach")
			}
		})
	}
}
