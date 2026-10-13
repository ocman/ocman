package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDirAndOwnerParams(t *testing.T) {
	for raw, want := range map[string]string{"": "", "  /a/b/ ": "/a/b", "/": "/", "rel/": "rel"} {
		if got := NormaliseDir(raw); got != want {
			t.Errorf("NormaliseDir(%q) = %q, want %q", raw, got, want)
		}
	}
	for query, wantOK := range map[string]bool{"": false, "dir=rel": false, "dir=/repo/": true} {
		rr := httptest.NewRecorder()
		dir, ok := AbsDir(rr, httptest.NewRequest(http.MethodGet, "/?"+query, nil))
		if ok != wantOK || (ok && dir != "/repo") || (!ok && rr.Code != http.StatusBadRequest) {
			t.Errorf("AbsDir(%q) = %q, %v, status %d", query, dir, ok, rr.Code)
		}
	}
	rr := httptest.NewRecorder()
	if _, ok := RemoteID(rr, "  "); ok || rr.Code != http.StatusBadRequest {
		t.Fatalf("blank remoteId accepted: %d", rr.Code)
	}
	if id, ok := RemoteID(httptest.NewRecorder(), " local "); !ok || id != "local" {
		t.Fatalf("RemoteID = %q, %v", id, ok)
	}
}
