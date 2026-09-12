package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestInboxHTTPKeepsSuccessfulSourcesWhenRemoteListFails(t *testing.T) {
	srv := testServer(t)
	srv.inboxSourcesFn = func() []string { return []string{"local", "remote-1"} }
	srv.inboxItemsFn = func(_ context.Context, source string) ([]state.InboxItem, error) {
		if source == "remote-1" {
			return nil, errors.New("remote unavailable")
		}
		return []state.InboxItem{{ID: "local-item", Title: "local", Body: "body", CreatedAt: 1}}, nil
	}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/inbox", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("partial list status = %d, want 200", rec.Code)
	}
	var response struct {
		Items []inboxItemView `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].RemoteID != "local" {
		t.Fatalf("partial list response = %+v", response)
	}
}

func TestInboxHTTPListAndMutations(t *testing.T) {
	srv := testServer(t)
	item, err := srv.stateDB.CreateInboxItem(t.Context(), "title", "body")
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}

	list := httptest.NewRecorder()
	mux.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/inbox", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", list.Code)
	}
	var response struct {
		Items       []inboxItemView `json:"items"`
		UnreadTotal int             `json:"unreadTotal"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Items) != 1 || response.Items[0].RemoteID != "local" || response.UnreadTotal != 1 {
		t.Fatalf("list response = %+v", response)
	}

	read := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"remoteId":"local","id":"` + item.ID + `"}`)
	mux.ServeHTTP(read, httptest.NewRequest(http.MethodPost, "/api/inbox/open", body))
	if read.Code != http.StatusNoContent {
		t.Fatalf("open status = %d, want 204", read.Code)
	}

	archive := httptest.NewRecorder()
	body = bytes.NewBufferString(`{"remoteId":"local","ids":["` + item.ID + `","` + item.ID + `"]}`)
	mux.ServeHTTP(archive, httptest.NewRequest(http.MethodPost, "/api/inbox/archive", body))
	if archive.Code != http.StatusNoContent {
		t.Fatalf("archive duplicate status = %d, want 204", archive.Code)
	}
	items, err := srv.stateDB.ListInboxItems(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("active items after duplicate archive = %+v", items)
	}
}

func TestInboxHTTPRejectsMalformedRequests(t *testing.T) {
	srv := testServer(t)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		body string
	}{
		{"/api/inbox/open", "{"},
		{"/api/inbox/archive", `{"items":[{"id":"x"}]}`},
		{"/api/inbox/archive-all-read", `{}`},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tc.path, bytes.NewBufferString(tc.body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s status = %d, want 400", tc.path, rec.Code)
		}
	}
}

func TestInboxHTTPUnavailableOwnerDoesNotFallbackLocally(t *testing.T) {
	srv := testServer(t)
	item, err := srv.stateDB.CreateInboxItem(t.Context(), "keep", "body")
	if err != nil {
		t.Fatal(err)
	}
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	body := bytes.NewBufferString(`{"remoteId":"gone","id":"` + item.ID + `"}`)
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/inbox/read", body))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable owner status = %d, want 503", rec.Code)
	}
	items, err := srv.stateDB.ListInboxItems(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ReadAt != 0 {
		t.Fatalf("local item changed after unavailable owner: %+v", items)
	}
}
