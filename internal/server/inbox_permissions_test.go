package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type refreshingInboxPlatform struct {
	fakePlatform
	prompts []platforms.LivePrompt
	err     error
}

func (p *refreshingInboxPlatform) RefreshPermissions(context.Context, string) ([]platforms.LivePrompt, error) {
	return p.prompts, p.err
}

func TestPermissionInboxReconciliationUsesLiveSnapshot(t *testing.T) {
	srv := testServer(t)
	adapter := &refreshingInboxPlatform{fakePlatform: fakePlatform{id: "opencode"}, prompts: []platforms.LivePrompt{{"id": "perm-1"}}}
	srv.registry.Register(adapter)
	srv.notifyPermissionInbox(state.InboxPermission{Platform: "opencode", SessionID: "ses-1", PermissionID: "perm-1", Permission: "bash"})
	srv.reconcilePermissionInbox(t.Context())
	items, _ := srv.stateDB.ListInboxItems(t.Context())
	if len(items) != 1 {
		t.Fatal("empty cached list overrode live pending request")
	}
	adapter.prompts, adapter.err = nil, errors.New("offline")
	srv.reconcilePermissionInbox(t.Context())
	items, _ = srv.stateDB.ListInboxItems(t.Context())
	if len(items) != 1 {
		t.Fatal("failed live snapshot archived request")
	}
	adapter.err = nil
	srv.reconcilePermissionInbox(t.Context())
	items, _ = srv.stateDB.ListInboxItems(t.Context())
	if len(items) != 0 {
		t.Fatal("successful empty snapshot did not archive request")
	}
}

func TestPermissionInboxReplyLifecycle(t *testing.T) {
	for _, reply := range []string{"once", "always", "reject"} {
		t.Run(reply, func(t *testing.T) {
			srv := testServer(t)
			adapter := &fakePlatform{id: "opencode"}
			srv.registry.Register(adapter)
			permission := state.InboxPermission{Platform: "opencode", SessionID: "ses-child", PermissionID: "perm-1", Permission: "bash", Patterns: []string{"git status"}}
			srv.notifyPermissionInbox(permission)
			srv.notifyPermissionInbox(permission)
			items, _ := srv.stateDB.ListInboxItems(t.Context())
			if len(items) != 1 {
				t.Fatalf("duplicate asks: %+v", items)
			}
			mux, err := srv.routes()
			if err != nil {
				t.Fatal(err)
			}
			adapter.respondPermissionFn = func(req platforms.RespondPermissionRequest) error {
				if req.SessionID != permission.SessionID || req.PermissionID != permission.PermissionID || req.Reply != reply {
					t.Errorf("wrong permission: %+v", req)
				}
				return errors.New("upstream unavailable")
			}
			respond := func() *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/session/ses-child/permissions/perm-1?platform=opencode", strings.NewReader(`{"reply":"`+reply+`"}`)))
				return rec
			}
			if rec := respond(); rec.Code < 400 {
				t.Fatalf("failed reply = %d", rec.Code)
			}
			items, _ = srv.stateDB.ListInboxItems(t.Context())
			if len(items) != 1 {
				t.Fatal("failed reply archived the prompt")
			}
			adapter.respondPermissionFn = func(platforms.RespondPermissionRequest) error { return nil }
			if rec := respond(); rec.Code != http.StatusNoContent {
				t.Fatalf("reply: %d %s", rec.Code, rec.Body.String())
			}
			items, _ = srv.stateDB.ListInboxItems(t.Context())
			if len(items) != 0 {
				t.Fatalf("handled prompt remains: %+v", items)
			}
		})
	}
}

func TestPermissionInboxExternalResolutionAndReconciliation(t *testing.T) {
	srv := testServer(t)
	adapter := &fakePlatform{id: "opencode"}
	srv.registry.Register(adapter)
	p := state.InboxPermission{Platform: "opencode", SessionID: "ses-1", PermissionID: "perm-1", Permission: "bash"}
	srv.notifyPermissionInbox(p)
	adapter.listPermissionsFn = func(string) ([]platforms.LivePrompt, error) { return nil, errors.New("offline") }
	srv.reconcilePermissionInbox(t.Context())
	items, _ := srv.stateDB.ListInboxItems(t.Context())
	if len(items) != 1 {
		t.Fatal("offline owner treated as resolved")
	}
	adapter.listPermissionsFn = func(string) ([]platforms.LivePrompt, error) {
		return []platforms.LivePrompt{{"id": p.PermissionID}}, nil
	}
	srv.reconcilePermissionInbox(t.Context())
	items, _ = srv.stateDB.ListInboxItems(t.Context())
	if len(items) != 1 {
		t.Fatal("pending prompt archived")
	}
	adapter.listPermissionsFn = func(string) ([]platforms.LivePrompt, error) { return nil, nil }
	srv.reconcilePermissionInbox(t.Context())
	items, _ = srv.stateDB.ListInboxItems(t.Context())
	if len(items) != 0 {
		t.Fatal("stale prompt not archived")
	}
	for _, reason := range []string{"replied", "rejected", "auto-approved"} {
		p.PermissionID = reason
		srv.notifyPermissionInbox(p)
		srv.broadcastPermissionResolved(p.SessionID, p.PermissionID, reason)
		srv.notifyPermissionInbox(p)
		items, _ = srv.stateDB.ListInboxItems(t.Context())
		if len(items) != 0 {
			t.Fatalf("%s did not archive: %+v", reason, items)
		}
	}
	p.Platform = "r-remote:opencode"
	srv.notifyPermissionInbox(p)
	items, _ = srv.stateDB.ListInboxItems(t.Context())
	if len(items) != 0 {
		t.Fatal("hub duplicated a remote prompt")
	}
}
