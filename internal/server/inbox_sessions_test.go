package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/inbox"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type inboxSessionPlatform struct {
	fakePlatform
	calls int
}

func (p *inboxSessionPlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	p.calls++
	return nil, platforms.ErrNotFound
}

func (p *inboxSessionPlatform) Sessions(context.Context, string, int64) ([]db.Session, error) {
	p.calls++
	return nil, nil
}

func TestInboxSessionContextOwnerRouting(t *testing.T) {
	s := testServer(t)
	adapter := &inboxSessionPlatform{fakePlatform: fakePlatform{id: "r-laptop:opencode"}}
	s.registry.Register(adapter)
	permission := &state.InboxPermission{Platform: "opencode", SessionID: "ses-child", Permission: "bash"}
	origin := &state.InboxSession{Platform: "opencode", SessionID: "ses-child", Title: "Owner title"}
	input := []state.InboxItem{
		{ID: "permission", Permission: permission, Session: origin},
		{ID: "routine", Session: origin},
		{ID: "legacy", Title: "Legacy"},
	}
	useInboxRemotes(s, []string{"laptop"}, func(context.Context, string) ([]state.InboxItem, error) { return input, nil })
	rec := httptest.NewRecorder()
	s.inboxModule().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/inbox", nil))
	var response struct{ Items []inbox.ItemView }
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 3 || adapter.calls != 0 {
		t.Fatalf("items=%+v transcript/listing reads=%d", response.Items, adapter.calls)
	}
	for _, item := range response.Items {
		if item.ID == "legacy" {
			if item.Session != nil {
				t.Fatal("invented a legacy origin")
			}
			continue
		}
		if item.Session == nil || item.Session.Platform != "r-laptop:opencode" || item.Session.Title != "Owner title" {
			t.Fatalf("wrong origin: %+v", item)
		}
		if item.Permission != nil && item.Title != "Owner title: Permission requested: bash" {
			t.Fatalf("wrong permission context: %+v", item)
		}
	}
	if origin.Platform != "opencode" || permission.Platform != "opencode" || input[0].Title != "" {
		t.Fatal("modified source data")
	}
}
