package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestFactoryInboxLinkStaysOnItsOwner(t *testing.T) {
	s := testServer(t)
	body := "Plan approval required\n\n[Open Factory actions](/factory/epics/ship)"
	item := state.InboxItem{ID: "factory-action-ship:review", Title: "Ship", Body: body, Category: state.InboxFactory}
	s.inboxSourcesFn = func() []string { return []string{"local", "laptop"} }
	s.inboxItemsFn = func(context.Context, string) ([]state.InboxItem, error) { return []state.InboxItem{item}, nil }
	rec := httptest.NewRecorder()
	s.handleInboxList(rec, httptest.NewRequest(http.MethodGet, "/api/inbox", nil))
	var response struct{ Items []inboxItemView }
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 2 {
		t.Fatalf("items = %#v", response.Items)
	}
	for _, got := range response.Items {
		if got.RemoteID == "local" {
			if got.Body != body {
				t.Fatal("local action lost its link")
			}
		} else if strings.Contains(got.Body, "/factory/") || !strings.Contains(got.Body, "owning machine") || !strings.Contains(got.Body, "Plan approval required") {
			t.Fatalf("remote action points at hub or lost context: %q", got.Body)
		}
	}
	if item.Body != body {
		t.Fatal("projection changed owner data")
	}
}
