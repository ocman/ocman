package inbox

import (
	"testing"

	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state"
)

func TestNamedPermissionNotifyPreservesPolicyRequests(t *testing.T) {
	for _, resolution := range []string{"pending", "resolved", "user-once", string(state.PermissionResolutionUserRejected)} {
		t.Run(resolution, func(t *testing.T) {
			f := newPermissionFixture(t)
			f.m.NotifyPermission(f.permission(f.connection))
			pending, err := f.store.PendingInboxPermissions(t.Context())
			if err != nil || len(pending) != 1 || pending[0].Platform != "opencode" || pending[0].ConnectionID != f.connection {
				t.Fatalf("audit identity replaced by routing identity: %+v, %v", pending, err)
			}
			if resolution == string(state.PermissionResolutionUserRejected) {
				if err := f.store.UpsertPermissionLifecycle(t.Context(), state.PermissionLifecycle{Platform: "opencode", SessionID: f.id, PermissionID: "request", ResolvedAt: 42, Resolution: state.PermissionResolutionUserRejected}); err != nil {
					t.Fatal(err)
				}
			}
			if resolution == "user-once" {
				if err := f.store.RecordApprovedPermission(t.Context(), "opencode", f.id, state.ApprovedPermission{PermissionID: "request", PermissionText: "supplemental", ApprovedBy: state.ApprovalActorUser, Reply: state.ApprovalReplyOnce, ApprovedAt: 42}); err != nil {
					t.Fatal(err)
				}
			}
			if resolution != "pending" {
				f.m.ResolvePermission(t.Context(), f.connection, f.id, "request")
			}
			policy := autoapprove.NewModule(autoapprove.ModuleDeps{Store: f.store, Engine: autoapprove.Deps{Store: f.store}})
			adapter := &platformtest.Fake{PlatformID: string(f.adapter.ID()), SessionFn: func(string) (*platforms.SessionDetail, error) {
				return &platforms.SessionDetail{Session: &db.Session{ID: f.id}}, nil
			}}
			got, err := policy.SessionPermissionPolicy(t.Context(), adapter, f.id, nil)
			if err != nil || got == nil || len(got.Requests) != 1 {
				t.Fatalf("notified permission missing from policy: %+v, %v", got, err)
			}
			request := got.Requests[0]
			if request.PermissionID != "request" || request.Resolution != resolution || request.Platform != "opencode" || request.Permission != "bash" {
				t.Fatalf("permission audit correlation changed: %+v", request)
			}
			var foreign int
			if err := f.store.SQL().QueryRowContext(t.Context(), `SELECT count(*) FROM permission_lifecycle WHERE platform = ?`, f.connection).Scan(&foreign); err != nil || foreign != 0 {
				t.Fatalf("connection namespace split lifecycle audit: %d, %v", foreign, err)
			}
		})
	}
}

func TestNativeAuditTombstoneKeepsNamedInboxNavigation(t *testing.T) {
	f := newPermissionFixture(t)
	key, err := f.adapter.PermissionInboxKey(t.Context(), "opencode", f.id, "request")
	if err != nil {
		t.Fatal(err)
	}
	// An auto-approval or manual-rule path can archive before the Inbox hook.
	if err := f.store.ResolvePermissionInboxItemKey(t.Context(), key, "opencode", f.id, "request"); err != nil {
		t.Fatal(err)
	}
	f.m.NotifyPermission(f.permission(f.connection))
	items, err := f.store.ListArchivedInboxItems(t.Context())
	if err != nil || len(items) != 1 || items[0].Session.Platform != f.connection || items[0].Permission.Platform != f.connection {
		t.Fatalf("audit namespace leaked into named Inbox navigation: %+v, %v", items, err)
	}
}

func TestNamedPermissionSeparatesStoredAuditAndInboxRouting(t *testing.T) {
	f := newPermissionFixture(t)
	f.m.NotifyPermission(f.permission(f.connection))
	for range 2 {
		pending, err := f.store.PendingInboxPermissions(t.Context())
		if err != nil || len(pending) != 1 || pending[0].Platform != "opencode" || pending[0].ConnectionID != f.connection {
			t.Fatalf("stored audit namespace changed: %+v, %v", pending, err)
		}
		items, err := f.store.ListInboxItems(t.Context())
		if err != nil || len(items) != 1 || items[0].Permission.Platform != f.connection || items[0].Session.Platform != f.connection {
			t.Fatalf("Inbox response lost exact account: %+v, %v", items, err)
		}
	}
	f.m.ResolvePermission(t.Context(), f.connection, f.id, "request")
	f.assertPending(t, 0)
	history, err := f.store.SessionPermissionHistory(t.Context(), "opencode", []string{f.id})
	if err != nil || len(history) != 1 || history[0].Resolution != "resolved" || history[0].ResolvedAt == 0 {
		t.Fatalf("resolution lost native audit correlation: %+v, %v", history, err)
	}
}
