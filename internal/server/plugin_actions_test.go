package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestPluginActionEndpoints(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", dir)
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &Server{stateDB: db, pluginCtx: t.Context(), auth: newTestAuth(t, "secret")}
	t.Cleanup(s.stopPluginProcesses)
	d := plugins.Description{ID: "org.example.actions", Name: "Actions", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 1,
		Capabilities: []plugins.Capability{plugins.ActionCapability}, RequestedGrants: []string{"context.owner"},
		Actions: []plugins.ActionDescriptor{{ID: "run", Label: "Run", Placement: "global", Surfaces: []string{"command-palette"}, RequiredGrants: []string{"context.owner"}, Confirmation: "Run action?"}}}
	hello, _ := json.Marshal(plugins.Envelope{Type: plugins.TypeHello, Hello: &plugins.Hello{Mode: plugins.Mode("$1"), Token: "$OCMAN_PLUGIN_TOKEN", Description: &d}})
	// The fixture records exactly what the host sends, then returns every result kind.
	result := `{"type":"result","result":{"id":"1","value":{"results":[{"kind":"notice","text":"Done"},{"kind":"link","label":"Website","url":"https://example.com"},{"kind":"artifact","label":"report.txt","data":"aGk="},{"kind":"navigation","target":"settings"},{"kind":"refresh","target":"actions"}]}}}`
	script := "#!/bin/sh\n/bin/cat <<EOF\n" + string(hello) + "\nEOF\nif [ \"$1\" = serve ]; then\nread -r ack\nread -r call\nprintf '%s\\n' \"$call\" >> calls\nprintf '%s\\n' '" + result + "'\nread -r shutdown\nfi\n"
	if err := os.WriteFile(filepath.Join(dir, "ocman-plugin-actions"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RescanPlugins(t.Context()); err != nil {
		t.Fatal(err)
	}
	mux, err := s.routes()
	if err != nil {
		t.Fatal(err)
	}
	cookies := httptest.NewRecorder()
	s.auth.issueCookie(cookies, httptest.NewRequest(http.MethodGet, "/", nil))
	cookie := cookies.Result().Cookies()[0]
	request := func(method, path, body string, auth bool, origin string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://localhost:8228"+path, strings.NewReader(body))
		if auth {
			r.AddCookie(cookie)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	list := "/api/plugins/actions?placement=global&surface=command-palette"
	invoke := "/api/plugins/actions/invoke"
	artifact := "/api/plugins/actions/artifact"
	for _, endpoint := range []struct{ method, path string }{{"GET", list}, {"POST", invoke}, {"GET", artifact}} {
		if w := request(endpoint.method, endpoint.path, "{}", false, ""); w.Code != 401 {
			t.Fatalf("auth %s: %d", endpoint.path, w.Code)
		}
	}
	if w := request("GET", invoke, "", true, ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request("POST", list, "", true, ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request("POST", artifact, "", true, ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	if w := request("POST", invoke, "{}", true, "https://evil.example"); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := request("GET", list, "", true, ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body.String())
	}
	if err := db.SetPluginEnabled(t.Context(), d.ID, true, d.RequestedGrants); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncPluginProcesses(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.pluginProcesses[d.ID].Health().Status != "ready" {
		if time.Now().After(deadline) {
			t.Fatal("plugin not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if w := request("GET", list, "", true, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"run"`) || strings.Contains(w.Body.String(), "executablePath") {
		t.Fatal(w.Body.String())
	}
	for _, path := range []string{list + "&ownerId=remote", list + "&ownerId=missing"} {
		// Global actions always resolve to the hub, regardless of viewed owner.
		if w := request("GET", path, "", true, ""); w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	if w := request("GET", "/api/plugins/actions?placement=project&surface=command-palette", "", true, ""); w.Code != 400 {
		t.Fatal(w.Body.String())
	}
	r := plugins.ActionRequest{PluginID: d.ID, ActionID: "run", OperationID: "op", Placement: "global", Surface: "command-palette", Context: plugins.ActionContext{ProjectID: "project", SessionID: "ses", Route: "session"}}
	call := func() (*httptest.ResponseRecorder, plugins.ActionResponse) {
		t.Helper()
		data, _ := json.Marshal(r)
		w := request("POST", invoke, string(data), true, "")
		var response plugins.ActionResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return w, response
	}
	w, response := call()
	if w.Code != 409 || response.Confirmation == nil {
		t.Fatal(w.Body.String())
	}
	dataDir, err := db.PluginDataDir(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "calls")); !os.IsNotExist(err) {
		t.Fatal("plugin called before confirmation")
	}
	r.ConfirmationToken = response.Confirmation.Token
	w, response = call()
	if w.Code != 200 || len(response.Results) != 5 || strings.Contains(w.Body.String(), `"data"`) {
		t.Fatal(w.Body.String())
	}
	if w, _ := call(); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	calls, err := os.ReadFile(filepath.Join(dataDir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "\n") != 1 || strings.Contains(string(calls), "projectId") || strings.Contains(string(calls), "sessionId") || strings.Contains(string(calls), "confirmation") || !strings.Contains(string(calls), `"ownerId":"local"`) {
		t.Fatalf("leaked/replayed call: %s", calls)
	}
	handle := response.Results[2].Handle
	// A new broker has no cached response, but durable admission prevents replay.
	restarted := &Server{stateDB: db, pluginProcesses: s.pluginProcesses}
	challenge := restarted.actionBroker().Invoke(t.Context(), r)
	if challenge.Confirmation == nil {
		t.Fatal("missing fresh confirmation")
	}
	retry := r
	retry.ConfirmationToken = challenge.Confirmation.Token
	if got := restarted.actionBroker().Invoke(t.Context(), retry); got.Error == nil || got.Error.Category != plugins.ErrorConflict {
		t.Fatalf("restart replay: %+v", got)
	}
	if w := request("GET", artifact+"?handle="+handle, "", true, ""); w.Code != 200 || w.Body.String() != "hi" || w.Header().Get("Content-Disposition") != "attachment; filename=report.txt" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download: %d %s", w.Code, w.Body.String())
	}
	if err := db.SetPluginGrants(t.Context(), d.ID, nil); err != nil {
		t.Fatal(err)
	}
	if w, _ := call(); w.Code != 403 {
		t.Fatal(w.Body.String())
	}
	if w := request("GET", artifact+"?handle="+handle, "", true, ""); w.Code != 403 {
		t.Fatal(w.Body.String())
	}
	if w := request("GET", list, "", true, ""); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body.String())
	}
	if err := db.SetPluginEnabled(t.Context(), d.ID, false, nil); err != nil {
		t.Fatal(err)
	}
	if w, _ := call(); w.Code != 503 {
		t.Fatal(w.Body.String())
	}
	if w := request("GET", artifact+"?handle="+handle, "", true, ""); w.Code != 503 {
		t.Fatal(w.Body.String())
	}
	if w := request("GET", artifact+"?handle=unknown", "", true, ""); w.Code != 404 {
		t.Fatal(w.Body.String())
	}
	r.Context.OwnerID = "remote"
	if w, _ := call(); w.Code != 503 {
		t.Fatal(w.Body.String())
	}
	for _, body := range []string{`{}`, `null`, `{} {}`, `{"context":{"transcript":"secret"}}`, strings.Repeat("x", 33<<10)} {
		if w := request("POST", invoke, body, true, ""); w.Code != 400 {
			t.Fatalf("invalid body: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestPluginActionUnavailableAndErrors(t *testing.T) {
	s := &Server{}
	r := plugins.ActionRequest{PluginID: "org.example.test", ActionID: "run", OperationID: "op", Placement: "global", Surface: "command-palette"}
	if got := s.actionBroker().Invoke(t.Context(), r); got.Error.Category != plugins.ErrorUnavailable {
		t.Fatal(got)
	}
	w := httptest.NewRecorder()
	s.handlePluginActions(w, httptest.NewRequest(http.MethodGet, "/?placement=global&surface=command-palette", nil))
	if strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatal(w.Body.String())
	}
	for _, tc := range []struct {
		category plugins.ErrorCategory
		status   int
	}{
		{plugins.ErrorInvalidArgument, 400}, {plugins.ErrorPermissionDenied, 403}, {plugins.ErrorNotFound, 404}, {plugins.ErrorConflict, 409}, {plugins.ErrorUnavailable, 503}, {plugins.ErrorDeadlineExceeded, 504}, {plugins.ErrorCancelled, 408}, {plugins.ErrorInternal, 500},
	} {
		w := httptest.NewRecorder()
		writeActionResponse(w, plugins.ActionResponse{Error: &plugins.WireError{Category: tc.category}})
		if w.Code != tc.status {
			t.Fatalf("%s: %d", tc.category, w.Code)
		}
	}
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	s = &Server{stateDB: db}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	s.handlePluginActions(w, httptest.NewRequest(http.MethodGet, "/?placement=global&surface=command-palette", nil))
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
	if got := s.actionBroker().Invoke(context.Background(), r); got.Error.Category != plugins.ErrorInternal {
		t.Fatal(got)
	}
}
