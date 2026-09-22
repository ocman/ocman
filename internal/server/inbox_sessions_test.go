package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type inboxSessionPlatform struct {
	fakePlatform
	calls int
	err   error
}

func (p *inboxSessionPlatform) Session(_ context.Context, id string, _, _ int) (*platforms.SessionDetail, error) {
	p.calls++
	return &platforms.SessionDetail{Session: &db.Session{ID: id, Title: "Deploy service"}}, p.err
}

func TestInboxSessionContextOwnerRouting(t *testing.T) {
	s := testServer(t)
	adapter := &inboxSessionPlatform{fakePlatform: fakePlatform{id: "r-laptop:opencode"}}
	s.registry.Register(adapter)
	permission := &state.InboxPermission{Platform: "opencode", SessionID: "ses-child", Permission: "bash"}
	origin := &state.InboxSession{Platform: "opencode", SessionID: "ses-child"}
	input := []state.InboxItem{
		{ID: "permission", Permission: permission},
		{ID: "routine", Session: origin},
		{ID: "legacy", Title: "Legacy"},
	}
	s.inboxSourcesFn = func() []string { return []string{"laptop"} }
	s.inboxItemsFn = func(context.Context, string) ([]state.InboxItem, error) { return input, nil }
	rec := httptest.NewRecorder()
	s.handleInboxList(rec, httptest.NewRequest(http.MethodGet, "/api/inbox", nil))
	var response struct{ Items []inboxItemView }
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 3 || adapter.calls != 1 {
		t.Fatalf("items=%+v calls=%d", response.Items, adapter.calls)
	}
	for _, item := range response.Items {
		if item.ID == "legacy" {
			if item.Session != nil {
				t.Fatal("invented a legacy origin")
			}
			continue
		}
		if item.Session == nil || item.Session.Platform != "r-laptop:opencode" || item.Session.Title != "Deploy service" || item.Session.SessionID != "ses-child" {
			t.Fatalf("wrong origin: %+v", item)
		}
		if item.Permission != nil && (item.Title != "Deploy service: Permission requested: bash" || item.Permission.Platform != "r-laptop:opencode") {
			t.Fatalf("wrong permission context: %+v", item)
		}
	}
	if origin.Platform != "opencode" || permission.Platform != "opencode" || input[0].Title != "" {
		t.Fatal("modified source data")
	}
}

func TestInboxSessionContextUnavailable(t *testing.T) {
	s := testServer(t)
	adapter := &inboxSessionPlatform{fakePlatform: fakePlatform{id: "opencode"}, err: errors.New("deleted")}
	s.registry.Register(adapter)
	input := []state.InboxItem{
		{Permission: &state.InboxPermission{Platform: "opencode", SessionID: "ses-gone", Permission: "edit"}},
		{Session: &state.InboxSession{Platform: "opencode", SessionID: "ses-gone", Title: "Stored title"}},
	}
	items := s.inboxSessionContext(t.Context(), "local", input)
	if items[0].Title != "ses-gone: Permission requested: edit" || items[1].Session.Title != "Stored title" || adapter.calls != 1 {
		t.Fatalf("fallback items=%+v calls=%d", items, adapter.calls)
	}
	s.registry = nil
	items = s.inboxSessionContext(t.Context(), "local", input)
	if items[0].Session.SessionID != "ses-gone" {
		t.Fatal("missing registry lost the session reference")
	}
}
