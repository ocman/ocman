package moduletest

import (
	"io"
	"net/http"
	"testing"
)

func TestDoSendsJSONBodyFromPeer(t *testing.T) {
	var got struct{ method, path, body, contentType, peer string }
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.method, got.path, got.body = r.Method, r.URL.Path, string(b)
		got.contentType, got.peer = r.Header.Get("Content-Type"), r.RemoteAddr
		w.WriteHeader(http.StatusAccepted)
	})
	rec := Do(h, http.MethodPost, "/x", `{"a":1}`, "127.0.0.1:1")
	if rec.Code != http.StatusAccepted || got.method != http.MethodPost || got.path != "/x" ||
		got.body != `{"a":1}` || got.contentType != "application/json" || got.peer != "127.0.0.1:1" {
		t.Fatalf("code %d, request %+v", rec.Code, got)
	}
	Do(h, http.MethodGet, "/y", "", "")
	if got.contentType != "" || got.peer == "127.0.0.1:1" {
		t.Fatalf("empty body/peer request = %+v", got)
	}
}

func TestMountAppliesPassThroughGuards(t *testing.T) {
	ok := func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
	r := Router{Mux: http.NewServeMux()}
	r.Handle("/h", ok)
	r.Public("/p", ok)
	r.Local("/l", ok)
	r.Setting("/s", ok)
	r.LocalSetting("/ls", ok)
	for _, p := range []string{"/h", "/p", "/l", "/s", "/ls"} {
		if rec := Do(r.Mux, http.MethodGet, p, "", ""); rec.Code != http.StatusNoContent {
			t.Errorf("%s = %d", p, rec.Code)
		}
	}
}
