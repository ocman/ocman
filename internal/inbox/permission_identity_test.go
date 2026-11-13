package inbox

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type permissionSource struct {
	*platformtest.Fake
	source string
	err    error
}

func (p *permissionSource) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "opencode", SourceKey: p.source}, p.err
}

func (p *permissionSource) RefreshPermissions(ctx context.Context, id string) ([]platforms.LivePrompt, error) {
	return p.ListPermissions(ctx, id)
}

type permissionFixture struct {
	m              *Module
	store          *state.DB
	catalogue      *sessioncatalog.Store
	registry       *platforms.Registry
	native         *permissionSource
	adapter        *owned.Adapter
	id, connection string
}

func newPermissionFixture(t *testing.T) permissionFixture {
	t.Helper()
	d := statetest.Open(t, sessioncatalog.Schema)
	catalogue, err := sessioncatalog.New(d)
	if err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: "owner", Platform: "opencode", SourceKey: "account"}
	if _, err := catalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "native", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	row, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "account", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	native := &permissionSource{Fake: &platformtest.Fake{PlatformID: "opencode"}, source: "account"}
	adapter, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithSourceScope()
	registry := platforms.NewRegistry()
	connection := "cnx_" + uuid.NewString()
	if err := registry.RegisterConnection(platforms.ID(connection), adapter); err != nil {
		t.Fatal(err)
	}
	m := NewModule(Deps{Store: d, Platform: registry.PermissionPlatform, Broadcast: func(string, []byte) {}, EnsurePending: func(platforms.Platform, string, []platforms.LivePrompt) {}})
	return permissionFixture{m, d, catalogue, registry, native, adapter, row.ID, connection}
}

func (f permissionFixture) permission(scope string) state.InboxPermission {
	return state.InboxPermission{Platform: scope, SessionID: f.id, PermissionID: "request", Permission: "bash"}
}

func (f permissionFixture) assertPending(t *testing.T, want int) {
	t.Helper()
	pending, err := f.store.PendingInboxPermissions(t.Context())
	if err != nil || len(pending) != want {
		t.Fatalf("pending permissions = %+v, %v; want %d", pending, err, want)
	}
}

func TestNamedPermissionNotifyResolve(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, named := range []bool{false, true} {
			t.Run(fmt.Sprintf("legacy=%t/named=%t", legacy, named), func(t *testing.T) {
				f := newPermissionFixture(t)
				seed := f.id
				if legacy {
					if err := f.catalogue.RetainLegacyKeys(t.Context(), "owner", f.id); err != nil {
						t.Fatal(err)
					}
					seed = "native"
				}
				scope, replyScope := "opencode", f.connection
				if named {
					scope, replyScope = replyScope, scope
				}
				f.m.NotifyPermission(f.permission(scope))
				f.assertPending(t, 1)
				f.m.NotifyPermission(f.permission(replyScope))
				f.assertPending(t, 1)
				items, err := f.store.ListInboxItems(t.Context())
				want := "permission-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte("opencode\x00"+seed+"\x00request")).String()
				if err != nil || len(items) != 1 || items[0].ID != want || items[0].Permission.SessionID != f.id || items[0].Permission.Platform != f.connection {
					t.Fatalf("wrong key or canonical session/connection: %+v, %v", items, err)
				}
				f.m.ResolvePermission(t.Context(), replyScope, f.id, "request")
				f.assertPending(t, 0)
				f.m.NotifyPermission(f.permission(scope))
				f.assertPending(t, 0)
			})
		}
	}
}

func TestPermissionIdentityFailuresLeaveUnresolvedItems(t *testing.T) {
	for _, failure := range []string{"unavailable", "source_changed", "disconnected", "purged"} {
		t.Run(failure, func(t *testing.T) {
			f := newPermissionFixture(t)
			f.m.NotifyPermission(f.permission(f.connection))
			f.assertPending(t, 1)
			switch failure {
			case "unavailable":
				f.native.err = platforms.ErrPlatformUnreachable
			case "source_changed":
				f.native.source = "replacement-account"
			case "disconnected":
				f.registry.Unregister(platforms.ID(f.connection))
			case "purged":
				if err := f.catalogue.Scrub(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "opencode", SourceKey: "account"}, "native"); err != nil {
					t.Fatal(err)
				}
			}
			f.m.ResolvePermission(t.Context(), f.connection, f.id, "request")
			f.m.Reconcile(t.Context())
			f.assertPending(t, 1)
			f.m.NotifyPermission(f.permission(f.connection))
			f.assertPending(t, 1)
			if failure == "unavailable" {
				f.native.err = nil
				f.m.ResolvePermission(t.Context(), f.connection, f.id, "request")
				f.assertPending(t, 0)
			}
		})
	}
}

func TestPermissionReplyBeforeNotifyKeepsTombstone(t *testing.T) {
	f := newPermissionFixture(t)
	f.m.ResolvePermission(t.Context(), f.connection, f.id, "request")
	f.m.NotifyPermission(f.permission("opencode"))
	f.assertPending(t, 0)
	items, err := f.store.ListArchivedInboxItems(t.Context())
	if err != nil || len(items) != 1 || items[0].Permission == nil {
		t.Fatalf("tombstone lost permission: %+v, %v", items, err)
	}
}

func TestPermissionIdentityRejectsOtherScopesAndSessions(t *testing.T) {
	f := newPermissionFixture(t)
	for _, scope := range []string{"cnx_" + uuid.NewString(), "r-offline:" + f.connection, "claudecode"} {
		for _, id := range []string{f.id, "native"} {
			p := f.permission(scope)
			p.SessionID = id
			f.m.NotifyPermission(p)
			f.m.ResolvePermission(t.Context(), scope, id, "request")
		}
	}
	for _, id := range []string{"native", "ocm_" + uuid.NewString()} {
		p := f.permission(f.connection)
		p.SessionID = id
		f.m.NotifyPermission(p)
		f.m.ResolvePermission(t.Context(), f.connection, id, "request")
	}
	f.assertPending(t, 0)
	items, err := f.store.ListArchivedInboxItems(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("invalid identity created tombstone: %+v, %v", items, err)
	}
	if _, err := f.adapter.PermissionInboxKey(t.Context(), "opencode", f.id, ""); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatal(err)
	}
}

func TestPermissionIdentityNeverBorrowsSiblingAccountOrOwner(t *testing.T) {
	f := newPermissionFixture(t)
	sibling := &permissionSource{Fake: &platformtest.Fake{PlatformID: "opencode"}, source: "sibling"}
	adapter, err := owned.New(t.Context(), sibling, f.catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithSourceScope()
	// Put a same-kind sibling first. Kind-only routing must prove the session,
	// while an explicit sibling must never be replaced by the actual account.
	f.registry.Unregister(platforms.ID(f.connection))
	if err := f.registry.RegisterConnection("cnx_sibling", adapter); err != nil {
		t.Fatal(err)
	}
	if err := f.registry.RegisterConnection(platforms.ID(f.connection), f.adapter); err != nil {
		t.Fatal(err)
	}
	f.m.NotifyPermission(f.permission("cnx_sibling"))
	f.m.ResolvePermission(t.Context(), "cnx_sibling", f.id, "request")
	f.assertPending(t, 0)
	f.m.NotifyPermission(f.permission("opencode"))
	f.assertPending(t, 1)
	f.m.ResolvePermission(t.Context(), "cnx_sibling", f.id, "request")
	f.assertPending(t, 1)
	f.m.ResolvePermission(t.Context(), f.connection, f.id, "request")
	f.assertPending(t, 0)
	if _, err := f.catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "stranger", Platform: "opencode", SourceKey: "account"}, []sessioncatalog.NativeSession{{ID: "native", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	row, err := f.catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "stranger", Platform: "opencode", SourceKey: "account", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	p := f.permission(f.connection)
	p.SessionID = row.ID
	f.m.NotifyPermission(p)
	f.m.ResolvePermission(t.Context(), f.connection, row.ID, "request")
	items, err := f.store.ListArchivedInboxItems(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("foreign owner minted tombstone: %+v, %v", items, err)
	}
}

func TestPermissionReconcileUsesNamedAccountForKindCallback(t *testing.T) {
	f := newPermissionFixture(t)
	active := true
	f.native.ListPermissionsFn = func(id string) ([]platforms.LivePrompt, error) {
		if id != "native" {
			t.Fatalf("runtime received non-native ID %q", id)
		}
		if active {
			return []platforms.LivePrompt{{"id": "request", "sessionID": "native", "permission": "bash"}}, nil
		}
		return nil, nil
	}
	// Simulate an existing row recorded under a native kind before named
	// connection routing. Reconciliation must still select its actual account.
	key, err := f.adapter.PermissionInboxKey(t.Context(), "opencode", f.id, "request")
	if err != nil {
		t.Fatal(err)
	}
	p := f.permission("opencode")
	p.ItemID = key
	if err := f.store.EnsurePermissionInboxItem(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	f.m.Reconcile(t.Context())
	f.assertPending(t, 1)
	active = false
	f.m.Reconcile(t.Context())
	f.assertPending(t, 0)
}

func TestPermissionIdentitySeparatesSameNativeRequestAcrossAccounts(t *testing.T) {
	f := newPermissionFixture(t)
	source := sessioncatalog.Source{OwnerID: "owner", Platform: "opencode", SourceKey: "sibling"}
	if _, err := f.catalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "native", Directory: "/other"}}); err != nil {
		t.Fatal(err)
	}
	row, err := f.catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "sibling", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	native := &permissionSource{Fake: &platformtest.Fake{PlatformID: "opencode"}, source: "sibling"}
	adapter, err := owned.New(t.Context(), native, f.catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.registry.RegisterConnection("cnx_sibling", adapter.WithSourceScope()); err != nil {
		t.Fatal(err)
	}
	f.m.NotifyPermission(f.permission(f.connection))
	p := f.permission("cnx_sibling")
	p.SessionID = row.ID
	f.m.NotifyPermission(p)
	f.assertPending(t, 2)
	f.m.ResolvePermission(t.Context(), "opencode", f.id, "request")
	f.assertPending(t, 1)
	f.m.ResolvePermission(t.Context(), "cnx_sibling", row.ID, "request")
	f.assertPending(t, 0)
}
