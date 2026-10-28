package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/statev2"
	"github.com/ocman/ocman/internal/testutil"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestPluginActionEndpoints(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", dir)
	db, err := state.Open(testStatePath(t), ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := bindPlugins(&Server{stateDB: db, auth: newTestAuth(t, "secret")}, t.Context())
	t.Cleanup(s.stopPluginProcesses)
	d := plugins.Description{ID: "org.example.actions", Name: "Actions", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 1,
		Capabilities: []plugins.Capability{plugins.ActionCapability}, RequestedGrants: []string{"context.owner"},
		Actions: []plugins.ActionDescriptor{{ID: "run", Label: "Run", Placement: "global", Surfaces: []string{"command-palette"}, RequiredGrants: []string{"context.owner"}, Confirmation: "Run action?"}}}
	hello, _ := json.Marshal(plugins.Envelope{Type: plugins.TypeHello, Hello: &plugins.Hello{Mode: plugins.Mode("$1"), Token: "$OCMAN_PLUGIN_TOKEN", Description: &d}})
	// The fixture records exactly what the host sends, then returns every result kind.
	result := `{"type":"result","result":{"id":"1","value":{"results":[{"kind":"notice","text":"Done"},{"kind":"link","label":"Website","url":"https://example.com"},{"kind":"artifact","label":"report.txt","data":"aGk="},{"kind":"navigation","target":"settings"},{"kind":"refresh","target":"actions"}]}}}`
	script := "#!/bin/sh\nIFS= read -r hello <<EOF\n" + string(hello) + "\nEOF\nprintf '%s\\n' \"$hello\"\nif [ \"$1\" = serve ]; then\nread -r ack\nread -r call\nprintf '%s\\n' \"$call\" >> calls\nprintf '%s\\n' '" + result + "'\nread -r shutdown\nfi\n"
	testutil.WriteScript(t, filepath.Join(dir, "ocman-plugin-actions"), script)
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
	for s.pluginsModule().Process(d.ID).Health().Status != "ready" {
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
	restarted := &Server{stateDB: db}
	restarted.pluginsModule().AdoptProcesses(s.pluginsModule())
	challenge := restarted.pluginsModule().Actions().Invoke(t.Context(), r)
	if challenge.Confirmation == nil {
		t.Fatal("missing fresh confirmation")
	}
	retry := r
	retry.ConfirmationToken = challenge.Confirmation.Token
	if got := restarted.pluginsModule().Actions().Invoke(t.Context(), retry); got.Error == nil || got.Error.Category != plugins.ErrorConflict {
		t.Fatalf("restart replay: %+v", got)
	}
	if w := request("GET", artifact+"?handle="+handle, "", true, ""); w.Code != 200 || w.Body.String() != "hi" || w.Header().Get("Content-Disposition") != "attachment; filename=report.txt" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("download: %d %s", w.Code, w.Body.String())
	}
	// A restarted broker serves the prior handle from the plugin's data root.
	if name, data, err := (&Server{stateDB: db}).pluginsModule().Actions().Artifact(t.Context(), handle); err != nil || name != "report.txt" || string(data) != "hi" {
		t.Fatalf("restart download: %q %q %v", name, data, err)
	}
	artifacts, _ := filepath.Glob(filepath.Join(dataDir, "artifacts", "*-report.txt"))
	if len(artifacts) != 1 {
		t.Fatalf("persisted artifact: %v", artifacts)
	}
	if info, err := os.Stat(artifacts[0]); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("artifact mode: %v %v", info, err)
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
	if err := db.DeletePluginPermanently(t.Context(), d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(artifacts[0]); !os.IsNotExist(err) {
		t.Fatalf("artifact survived permanent deletion: %v", err)
	}
	if _, _, err := (&Server{stateDB: db}).pluginsModule().Actions().Artifact(t.Context(), handle); err == nil {
		t.Fatal("deleted plugin artifact served")
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

func TestPluginActionProjectPathGrant(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", dir)
	sdb, err := state.Open(testStatePath(t), ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sdb.Close() })
	s := &Server{stateDB: sdb, auth: newTestAuth(t, "secret")}
	history := storedProjectFixture(t, s)
	for _, directory := range []string{"/home/dev/.worktrees/project-a/b", "/home/dev/project-a"} {
		if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "fixture", SessionID: directory}, ProjectID: "prd_1", Directory: directory, Extensions: json.RawMessage(`{"ProjectDirectory":"/home/dev/project-a"}`)}}); err != nil {
			t.Fatal(err)
		}
	}
	bindPlugins(s, t.Context())
	t.Cleanup(s.stopPluginProcesses)
	d := plugins.Description{ID: "org.example.path", Name: "Path", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 1,
		Capabilities: []plugins.Capability{plugins.ActionCapability}, RequestedGrants: []string{"context.project", "context.project.path"},
		Actions: []plugins.ActionDescriptor{{ID: "open", Label: "Open", Placement: "project", Surfaces: []string{"command-palette"}, RequiredGrants: []string{"context.project", "context.project.path"}}}}
	hello, _ := json.Marshal(plugins.Envelope{Type: plugins.TypeHello, Hello: &plugins.Hello{Mode: plugins.Mode("$1"), Token: "$OCMAN_PLUGIN_TOKEN", Description: &d}})
	script := "#!/bin/sh\nIFS= read -r hello <<EOF\n" + string(hello) + "\nEOF\nprintf '%s\\n' \"$hello\"\nif [ \"$1\" = serve ]; then\nread -r ack\nwhile IFS= read -r line; do\nif printf '%s' \"$line\" | grep -q '\"type\":\"shutdown\"'; then break; fi\nid=$(printf '%s' \"$line\" | sed -n 's/.*\"id\":\"\\([0-9]*\\)\".*/\\1/p')\nprintf '%s\\n' \"$line\" >> calls\nprintf '%s\\n' \"{\\\"type\\\":\\\"result\\\",\\\"result\\\":{\\\"id\\\":\\\"$id\\\",\\\"value\\\":{\\\"results\\\":[{\\\"kind\\\":\\\"notice\\\",\\\"text\\\":\\\"Done\\\"}]}}}\"\ndone\nfi\n"
	exe := filepath.Join(dir, "ocman-plugin-path")
	if err := os.WriteFile(exe, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RescanPlugins(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sdb.SetPluginEnabled(t.Context(), d.ID, true, d.RequestedGrants); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncPluginProcesses(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for s.pluginsModule().Process(d.ID).Health().Status != "ready" {
		if time.Now().After(deadline) {
			t.Fatal("plugin not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mux, err := s.routes()
	if err != nil {
		t.Fatal(err)
	}
	cookies := httptest.NewRecorder()
	s.auth.issueCookie(cookies, httptest.NewRequest(http.MethodGet, "/", nil))
	cookie := cookies.Result().Cookies()[0]
	invoke := func(op, projectID string, path bool) (int, string) {
		t.Helper()
		body := `{"pluginId":"org.example.path","actionId":"open","operationId":"` + op + `","placement":"project","surface":"command-palette","context":{"projectId":"` + projectID + `"}}`
		if path {
			body = `{"pluginId":"org.example.path","actionId":"open","operationId":"` + op + `","placement":"project","surface":"command-palette","context":{"projectId":"` + projectID + `","directory":"/etc/secret"}}`
		}
		r := httptest.NewRequest(http.MethodPost, "http://localhost:8228/api/plugins/actions/invoke", strings.NewReader(body))
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	dataDir, err := sdb.PluginDataDir(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	calls := func() string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dataDir, "calls"))
		if err != nil {
			return ""
		}
		return string(data)
	}
	if code, body := invoke("op-1", "prd_1", false); code != 200 {
		t.Fatalf("granted invoke: %d %s", code, body)
	}
	if got := calls(); !strings.Contains(got, `"projectId":"prd_1"`) || !strings.Contains(got, `"directory":"/home/dev/project-a"`) {
		t.Fatalf("granted call missing resolved directory: %s", got)
	}
	if code, body := invoke("op-2", "prd_missing", false); code != 200 {
		t.Fatalf("unknown id invoke: %d %s", code, body)
	}
	lines := strings.Split(strings.TrimSuffix(calls(), "\n"), "\n")
	if len(lines) != 2 || !strings.Contains(lines[1], `"projectId":"prd_missing"`) || strings.Contains(lines[1], `"directory"`) {
		t.Fatalf("unknown id must omit the field without failing: %q", lines)
	}
	if code, _ := invoke("op-3", "prd_1", true); code != 400 {
		t.Fatalf("caller-injected path accepted: %d", code)
	}
	if err := sdb.SetPluginGrants(t.Context(), d.ID, []string{"context.project"}); err != nil {
		t.Fatal(err)
	}
	if code, _ := invoke("op-4", "prd_1", false); code != 403 {
		t.Fatalf("unapproved path grant: %d", code)
	}
	if got := calls(); strings.Count(got, "\n") != 2 {
		t.Fatalf("unapproved grant dispatched a call: %s", got)
	}
	// Without an OpenCode database the owner cannot resolve anything: the call
	// still proceeds, the field stays absent.
	if err := sdb.SetPluginEnabled(t.Context(), d.ID, true, d.RequestedGrants); err != nil {
		t.Fatal(err)
	}
	uninventoried := &Server{stateDB: sdb}
	uninventoried.pluginsModule().AdoptProcesses(s.pluginsModule())
	got := uninventoried.pluginsModule().Actions().Invoke(t.Context(), plugins.ActionRequest{PluginID: d.ID, ActionID: "open", OperationID: "op-5", Placement: "project", Surface: "command-palette", Context: plugins.ActionContext{OwnerID: "local", ProjectID: "prd_1"}})
	if got.Error != nil || len(got.Results) != 1 {
		t.Fatalf("missing inventory: %+v", got)
	}
	if lines := strings.Split(strings.TrimSuffix(calls(), "\n"), "\n"); len(lines) != 3 || !strings.Contains(lines[2], `"projectId":"prd_1"`) || strings.Contains(lines[2], `"directory"`) {
		t.Fatalf("missing inventory leaked or omitted the call: %q", lines)
	}
	// A checksum change revokes the stored approval, so the directory is gone
	// even though every grant was previously approved.
	if err := os.WriteFile(exe, []byte(script+"# changed\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RescanPlugins(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p, err := sdb.GetPlugin(t.Context(), d.ID); err != nil || p.Enabled || len(p.Grants) != 0 {
		t.Fatalf("checksum change kept approval: %+v %v", p, err)
	}
	if code, _ := invoke("op-6", "prd_1", false); code == 200 {
		t.Fatal("revoked approval still invoked the action")
	}
	if got := calls(); strings.Count(got, "\n") != 3 {
		t.Fatalf("revoked approval dispatched a call: %s", got)
	}
}
