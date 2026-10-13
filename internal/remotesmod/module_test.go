package remotesmod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func openStore(t *testing.T) *state.DB {
	t.Helper()
	st, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// realManager dials nothing reachable: the tests never touch a real remote.
func realManager(t *testing.T, st *state.DB) *remote.Manager {
	t.Helper()
	mgr := remote.NewManager(platforms.NewRegistry(), hostsvc.NewRouter(nil), st, "opencode")
	t.Cleanup(mgr.Stop)
	return mgr
}

func pass(h http.HandlerFunc) http.HandlerFunc { return h }

func mux(d Deps) *http.ServeMux {
	if d.Guards.Auth == nil {
		d.Guards = module.Guards{Auth: pass, Localhost: pass}
	}
	m := http.NewServeMux()
	NewModule(d).Routes(muxRouter{m})
	return m
}

type muxRouter struct{ m *http.ServeMux }

func (r muxRouter) Handle(p string, h http.HandlerFunc)       { r.m.HandleFunc(p, h) }
func (r muxRouter) Local(p string, h http.HandlerFunc)        { r.m.HandleFunc(p, h) }
func (r muxRouter) Setting(p string, h http.HandlerFunc)      { r.m.HandleFunc(p, h) }
func (r muxRouter) LocalSetting(p string, h http.HandlerFunc) { r.m.HandleFunc(p, h) }

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func managerOf(m Manager) func() Manager { return func() Manager { return m } }

func TestNoManager(t *testing.T) {
	for _, d := range []Deps{{}, {Manager: func() Manager { return nil }}} {
		h := mux(d)
		if rr := do(h, http.MethodGet, "/api/remotes", ""); rr.Code != 200 || rr.Body.String() != "[]\n" {
			t.Fatalf("GET = %d %q, want empty list", rr.Code, rr.Body.String())
		}
		if rr := do(h, http.MethodPost, "/api/remotes", `{}`); rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("POST = %d, want 503", rr.Code)
		}
		if rr := do(h, http.MethodDelete, "/api/remotes/1", ""); rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("DELETE = %d, want 503", rr.Code)
		}
	}
}

// The remote CRUD routes sit behind the localhost guard: registering a
// remote makes the hub dial an operator-supplied address, so a peer
// that merely reaches the port must not be able to add or edit one.
func TestRemoteCRUDIsLocalhostGuarded(t *testing.T) {
	deny := func(http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }
	}
	h := mux(Deps{Manager: managerOf(realManager(t, openStore(t))), Guards: module.Guards{Auth: pass, Localhost: deny}})
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/remotes", ""},
		{http.MethodPost, "/api/remotes", `{"address":"a:1","token":"t"}`},
		{http.MethodPut, "/api/remotes/1", `{}`},
	} {
		if rr := do(h, c.method, c.path, c.body); rr.Code != http.StatusForbidden {
			t.Errorf("%s %s answered %d, want 403 when the localhost guard denies", c.method, c.path, rr.Code)
		}
	}
}

func TestMethodAndValidationErrors(t *testing.T) {
	h := mux(Deps{Manager: managerOf(realManager(t, openStore(t)))})
	cases := []struct {
		method, path, body string
		want               int
	}{
		{http.MethodPut, "/api/remotes", "", 405},
		{http.MethodPost, "/api/remotes", `{"address":""}`, 400},
		{http.MethodPost, "/api/remotes", `{"address":"a:1","token":"  "}`, 400},
		{http.MethodDelete, "/api/remotes/notanint", "", 400},
		{http.MethodGet, "/api/remotes/1/bogus", "", 404},
		{http.MethodGet, "/api/remotes/1/reconnect", "", 405},
		{http.MethodPatch, "/api/remotes/1", "", 405},
		{http.MethodPut, "/api/remotes/1", `{"address":" "}`, 400},
		{http.MethodPost, "/api/settings/remote-access", "", 405},
		{http.MethodGet, "/api/settings/remote-access/reveal-token", "", 405},
	}
	for _, c := range cases {
		if rr := do(h, c.method, c.path, c.body); rr.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rr.Code, c.want)
		}
	}
}

func TestAddListUpdateReconnectDelete(t *testing.T) {
	st := openStore(t)
	mgr := realManager(t, st)
	h := mux(Deps{Manager: managerOf(mgr)})

	// Unreachable address: the background dial fails, the row persists.
	rr := do(h, http.MethodPost, "/api/remotes", `{"address":"127.0.0.1:59999","token":"secret","displayName":"Box"}`)
	if rr.Code != 200 {
		t.Fatalf("add %d %s", rr.Code, rr.Body)
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("secret")) {
		t.Fatal("token leaked in add response")
	}
	var added remote.RemoteStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &added); err != nil || added.LocalID == 0 || added.DisplayName != "Box" {
		t.Fatalf("add result %+v %v", added, err)
	}
	rr = do(h, http.MethodGet, "/api/remotes", "")
	if bytes.Contains(rr.Body.Bytes(), []byte("secret")) {
		t.Fatal("token leaked in list response")
	}
	var list []remote.RemoteStatus
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].Address != "127.0.0.1:59999" {
		t.Fatalf("list %+v %v", list, err)
	}

	id := strconv.FormatInt(added.LocalID, 10)
	// An empty token leaves the stored one unchanged.
	if rr := do(h, http.MethodPut, "/api/remotes/"+id, `{"address":"127.0.0.1:59999","displayName":"Renamed","enabled":false,"token":"  "}`); rr.Code != 200 {
		t.Fatalf("update %d %s", rr.Code, rr.Body)
	}
	got, _ := st.GetRemote(t.Context(), added.LocalID)
	if got.DisplayName != "Renamed" || got.Enabled {
		t.Fatalf("update not persisted: %+v", got)
	}
	if tok, _ := st.RemoteToken(t.Context(), added.LocalID); tok != "secret" {
		t.Fatalf("blank token replaced the stored one: %q", tok)
	}
	if rr := do(h, http.MethodPut, "/api/remotes/"+id, `{"address":"127.0.0.1:59999","enabled":false,"token":"newtok"}`); rr.Code != 200 {
		t.Fatalf("token update %d", rr.Code)
	}
	if tok, _ := st.RemoteToken(t.Context(), added.LocalID); tok != "newtok" {
		t.Fatalf("token not replaced: %q", tok)
	}
	// Reconnecting a disabled remote only disconnects it.
	if rr := do(h, http.MethodPost, "/api/remotes/"+id+"/reconnect", ""); rr.Code != 200 {
		t.Fatalf("reconnect %d", rr.Code)
	}
	if _, ok := mgr.Conn(added.LocalID); ok {
		t.Fatal("disabled remote still managed after reconnect")
	}
	if rr := do(h, http.MethodDelete, "/api/remotes/"+id, ""); rr.Code != 200 {
		t.Fatalf("delete %d", rr.Code)
	}
	if l, _ := mgr.List(t.Context()); len(l) != 0 {
		t.Fatalf("remaining after delete: %+v", l)
	}
	// Unknown ids surface the store error.
	if rr := do(h, http.MethodPost, "/api/remotes/"+id+"/reconnect", ""); rr.Code != 500 {
		t.Fatalf("reconnect missing = %d, want 500", rr.Code)
	}
}

type fakeManager struct {
	Manager
	err     error
	addID   int64
	list    []remote.RemoteStatus
	runDone chan struct{}
	gotIval time.Duration
}

func (f *fakeManager) List(context.Context) ([]remote.RemoteStatus, error) { return f.list, f.err }
func (f *fakeManager) Add(context.Context, string, string, string) (int64, error) {
	return f.addID, f.err
}
func (f *fakeManager) Update(context.Context, int64, string, string, bool, *string) error {
	return f.err
}
func (f *fakeManager) Remove(context.Context, int64) error { return f.err }
func (f *fakeManager) RunInventoryLoop(ctx context.Context, d time.Duration) {
	f.gotIval = d
	<-ctx.Done()
	close(f.runDone)
}

func TestManagerErrors(t *testing.T) {
	h := mux(Deps{Manager: managerOf(&fakeManager{err: errors.New("boom")})})
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/remotes", ""},
		{http.MethodPost, "/api/remotes", `{"address":"a:1","token":"t"}`},
		{http.MethodPut, "/api/remotes/1", `{"address":"a:1"}`},
		{http.MethodDelete, "/api/remotes/1", ""},
	} {
		if rr := do(h, c.method, c.path, c.body); rr.Code != 500 || strings.Contains(rr.Body.String(), "boom") {
			t.Errorf("%s %s = %d %q, want opaque 500", c.method, c.path, rr.Code, rr.Body)
		}
	}
	// A nil list renders as [] and an added id missing from the list
	// falls back to {"localId"}.
	h = mux(Deps{Manager: managerOf(&fakeManager{addID: 7})})
	if rr := do(h, http.MethodGet, "/api/remotes", ""); rr.Body.String() != "[]\n" {
		t.Fatalf("nil list = %q", rr.Body)
	}
	if rr := do(h, http.MethodPost, "/api/remotes", `{"address":"a:1","token":"t"}`); rr.Body.String() != "{\"localId\":7}\n" {
		t.Fatalf("add fallback = %q", rr.Body)
	}
}

// The manager is read when the request arrives, not when routes mount.
func TestManagerReadAtCallTime(t *testing.T) {
	var mgr Manager
	h := mux(Deps{Manager: func() Manager { return mgr }})
	if rr := do(h, http.MethodPost, "/api/remotes", `{"address":"a:1","token":"t"}`); rr.Code != 503 {
		t.Fatalf("before = %d", rr.Code)
	}
	mgr = &fakeManager{addID: 1, list: []remote.RemoteStatus{{LocalID: 1, DisplayName: "late"}}}
	if rr := do(h, http.MethodPost, "/api/remotes", `{"address":"a:1","token":"t"}`); rr.Code != 200 || !strings.Contains(rr.Body.String(), "late") {
		t.Fatalf("after = %d %s", rr.Code, rr.Body)
	}
}

func TestGuardsWrapEveryRoute(t *testing.T) {
	var auth, local []string
	guard := func(log *[]string) func(http.HandlerFunc) http.HandlerFunc {
		return func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				*log = append(*log, r.URL.Path)
				http.Error(w, "denied", http.StatusForbidden)
			}
		}
	}
	h := mux(Deps{Guards: module.Guards{Auth: guard(&auth), Localhost: guard(&local)}})
	do(h, http.MethodGet, "/api/settings/remote-access", "")
	do(h, http.MethodPost, "/api/settings/remote-access/reveal-token", "")
	do(h, http.MethodGet, "/api/remotes", "")
	do(h, http.MethodDelete, "/api/remotes/1", "")
	if len(auth) != 4 {
		t.Fatalf("auth guarded %v", auth)
	}
	// Auth wraps localhost on reveal, so a denied login stops first.
	if len(local) != 0 {
		t.Fatalf("localhost ran before auth: %v", local)
	}
	auth, local = nil, nil
	h = mux(Deps{Guards: module.Guards{Auth: pass, Localhost: guard(&local)}})
	do(h, http.MethodPost, "/api/settings/remote-access/reveal-token", "")
	do(h, http.MethodGet, "/api/settings/remote-access", "")
	if len(local) != 1 || local[0] != "/api/settings/remote-access/reveal-token" {
		t.Fatalf("localhost guarded %v", local)
	}
}

func TestAccessReportsIdentityWithoutToken(t *testing.T) {
	st := openStore(t)
	ident, err := st.InstanceIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		access    Access
		identity  Identity
		wantID    string
		transport string
		tokenSet  bool
	}{
		{Access{InstanceID: "inst123", ListenAddr: "0.0.0.0:8230", Listening: true}, st, "inst123", "trusted-overlay", true},
		{Access{Listening: true, TLS: true}, st, ident.InstanceID, "tls", true},
		{Access{}, nil, "", "", false},
	}
	for _, c := range cases {
		rr := do(mux(Deps{Identity: c.identity, Access: func() Access { return c.access }}), http.MethodGet, "/api/settings/remote-access", "")
		if strings.Contains(rr.Body.String(), ident.RemoteToken) {
			t.Fatal("status leaked the token")
		}
		var got accessStatus
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.InstanceID != c.wantID || got.Transport != c.transport || got.TokenSet != c.tokenSet || got.Listening != c.access.Listening {
			t.Errorf("access %+v → %+v", c.access, got)
		}
	}
}

func TestRevealToken(t *testing.T) {
	st := openStore(t)
	ident, _ := st.InstanceIdentity(t.Context())
	rr := do(mux(Deps{Identity: st}), http.MethodPost, "/api/settings/remote-access/reveal-token", "")
	var got map[string]string
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if rr.Code != 200 || got["token"] == "" || got["token"] != ident.RemoteToken {
		t.Fatalf("reveal = %d %v", rr.Code, got)
	}
	if rr := do(mux(Deps{}), http.MethodPost, "/api/settings/remote-access/reveal-token", ""); rr.Code != 503 {
		t.Fatalf("no store = %d, want 503", rr.Code)
	}
	_ = st.Close()
	if rr := do(mux(Deps{Identity: st}), http.MethodPost, "/api/settings/remote-access/reveal-token", ""); rr.Code != 500 {
		t.Fatalf("closed store = %d, want 500", rr.Code)
	}
}

// Run owns the inventory loop: it reads the manager when the server starts
// its workers and returns only once the loop has.
func TestRunJoinsInventoryLoop(t *testing.T) {
	NewModule(Deps{}).Run(t.Context()) // no manager: returns at once
	NewModule(Deps{Manager: func() Manager { return nil }}).Run(t.Context())

	f := &fakeManager{runDone: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	returned := make(chan struct{})
	go func() { NewModule(Deps{Manager: managerOf(f)}).Run(ctx); close(returned) }()
	cancel()
	<-returned
	select {
	case <-f.runDone:
	default:
		t.Fatal("Run returned before the inventory loop")
	}
	if f.gotIval != InventoryInterval {
		t.Fatalf("interval %v", f.gotIval)
	}
}
