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

func TestQueryInt(t *testing.T) {
	for query, want := range map[string]int{"": 7, "n=": 7, "n=%205%20": 5, "n=-3": -3, "n=+4": 4, "n=x": 7, "n=1.5": 7, "n=99999999999999999999": 7} {
		if got := QueryInt(httptest.NewRequest(http.MethodGet, "/?"+query, nil), "n", 7); got != want {
			t.Errorf("QueryInt(%q) = %d, want %d", query, got, want)
		}
	}
	if got := QueryInt[int64](httptest.NewRequest(http.MethodGet, "/?since=1700000000000", nil), "since", 0); got != 1700000000000 {
		t.Errorf("QueryInt[int64] = %d", got)
	}
}
