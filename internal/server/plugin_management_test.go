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

type pluginManagementTest struct {
	s           *Server
	mux         *http.ServeMux
	cookie      *http.Cookie
	dir         string
	description plugins.Description
}

func newPluginManagementTest(t *testing.T) *pluginManagementTest {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", dir)
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &Server{stateDB: db, pluginCtx: context.Background(), auth: newTestAuth(t, "password")}
	t.Cleanup(s.stopPluginProcesses)
	d := plugins.Description{ID: "org.example.management", Name: "Management", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 1,
		Capabilities:    []plugins.Capability{plugins.ActionCapability},
		Actions:         []plugins.ActionDescriptor{{ID: "run", Label: "Run", Placement: "global", Surfaces: []string{"command-palette"}, RequiredGrants: []string{"context.owner"}}},
		RequestedGrants: []string{"context.owner"}, Settings: []plugins.Setting{
			{Key: "mode", Label: "Mode", Type: "string", Default: json.RawMessage(`"good"`), Enum: []string{"good", "bad", "hang"}},
			{Key: "token", Label: "Token", Type: "string", Secret: true, Required: true},
		}}
	hello, _ := json.Marshal(plugins.Envelope{Type: plugins.TypeHello, Hello: &plugins.Hello{Mode: "$1", Token: "$OCMAN_PLUGIN_TOKEN", Description: &d}})
	script := "#!/bin/sh\nif [ \"$1\" = serve ]; then\nIFS= read -r config <&3\nprintf '%s\\n' \"$config\" >&2\ncase \"$config\" in *'\"bad\"'*) exit 1;; *'\"hang\"'*) /bin/sleep 10; exit 1;; esac\nfi\n/bin/cat <<EOF\n" + string(hello) + "\nEOF\nif [ \"$1\" = serve ]; then\nread -r ack\necho started >> starts\nread -r shutdown\nfi\n"
	if err := os.WriteFile(filepath.Join(dir, "ocman-plugin-management"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	mux, err := s.routes()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.auth.issueCookie(w, httptest.NewRequest(http.MethodGet, "/", nil))
	return &pluginManagementTest{s, mux, w.Result().Cookies()[0], dir, d}
}

func (f *pluginManagementTest) request(method, path, body, peer, origin string, authenticated bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:8228/api/plugins"+path, strings.NewReader(body))
	r.RemoteAddr = peer
	if authenticated {
		r.AddCookie(f.cookie)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	return w
}

func (f *pluginManagementTest) call(t *testing.T, method, path, body string, status int) string {
	t.Helper()
	w := f.request(method, path, body, "127.0.0.1:1234", "", true)
	if w.Code != status {
		t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w.Body.String()
}

func (f *pluginManagementTest) approval(t *testing.T) string {
	t.Helper()
	p, err := f.s.stateDB.GetPlugin(t.Context(), f.description.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(pluginManagementInput{Approval: p.Approval, Grants: &p.Description.RequestedGrants})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestPluginManagementGuards(t *testing.T) {
	f := newPluginManagementTest(t)
	base := "/" + f.description.ID
	for _, endpoint := range []struct{ method, path string }{
		{"GET", ""}, {"GET", "/discovery"}, {"GET", base + "/health"}, {"GET", base + "/configuration"}, {"GET", base + "/grants"}, {"GET", base + "/stderr"},
		{"POST", "/rescan"}, {"POST", base + "/enable"}, {"POST", base + "/disable"}, {"POST", base + "/grants"},
		{"POST", base + "/configuration/validate"}, {"POST", base + "/configuration"}, {"POST", base + "/restart"}, {"POST", base + "/retry"}, {"POST", base + "/remove-data"},
	} {
		t.Run(endpoint.method+endpoint.path, func(t *testing.T) {
			if w := f.request(endpoint.method, endpoint.path, "{}", "127.0.0.1:1", "", false); w.Code != 401 {
				t.Fatalf("auth: %d", w.Code)
			}
			if w := f.request("DELETE", endpoint.path, "{}", "127.0.0.1:1", "", true); w.Code != 405 {
				t.Fatalf("method: %d", w.Code)
			}
			if endpoint.method == "POST" || strings.HasSuffix(endpoint.path, "/stderr") {
				for _, source := range []struct{ peer, origin string }{{"192.0.2.1:1", ""}, {"127.0.0.1:1", "https://evil.example"}} {
					if w := f.request(endpoint.method, endpoint.path, "{}", source.peer, source.origin, true); w.Code != 403 {
						t.Fatalf("origin/peer: %d", w.Code)
					}
				}
			}
		})
	}
}

func TestPluginEnableRequiresReviewedRegistration(t *testing.T) {
	f := newPluginManagementTest(t)
	base := "/" + f.description.ID
	f.call(t, "POST", "/rescan", `{}`, 200)
	f.call(t, "POST", base+"/configuration", `{"secrets":{"token":"working"}}`, 200)
	f.call(t, "POST", base+"/enable", `{"grants":["context.owner"]}`, 409)
	approved := f.approval(t)
	p, err := f.s.stateDB.GetPlugin(t.Context(), f.description.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"checksum", "description", "path"} {
		switch change {
		case "checksum":
			p.Checksum = strings.Repeat("a", 64)
		case "description":
			p.Description.Name = "Changed without new grants"
		case "path":
			p.ExecutablePath += "-replacement"
		}
		if err := f.s.stateDB.DiscoverPlugin(t.Context(), p.Description, p.ExecutablePath, p.Checksum, nil); err != nil {
			t.Fatal(err)
		}
		f.call(t, "POST", base+"/enable", approved, 409)
		approved = f.approval(t)
	}
	p, err = f.s.stateDB.GetPlugin(t.Context(), f.description.ID)
	if err != nil || p.Enabled || len(f.s.pluginProcesses) != 0 {
		t.Fatalf("stale approval enabled replacement: %+v %v", p, err)
	}
}

func TestPluginRejectedDiscoveryDiagnostics(t *testing.T) {
	f := newPluginManagementTest(t)
	if err := os.WriteFile(filepath.Join(f.dir, "ocman-plugin-broken"), []byte("#!/bin/sh\necho malformed-private-output\n"), 0700); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/rescan", `{}`, 200)
	body := f.call(t, "GET", "/discovery", "", 200)
	if !strings.Contains(body, "ocman-plugin-broken") || !strings.Contains(body, plugins.ErrInvalidMessage.Error()) || strings.Contains(body, "private-output") {
		t.Fatalf("missing or unsafe discovery diagnostic: %s", body)
	}
	if err := os.Remove(filepath.Join(f.dir, "ocman-plugin-broken")); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/rescan", `{}`, 200)
	if body := f.call(t, "GET", "/discovery", "", 200); strings.TrimSpace(body) != "[]" {
		t.Fatalf("stale discovery diagnostic: %s", body)
	}
}

func TestPluginStartupRecoversInterruptedConfiguration(t *testing.T) {
	f := newPluginManagementTest(t)
	base := "/" + f.description.ID
	f.call(t, "POST", "/rescan", `{}`, 200)
	f.call(t, "POST", base+"/configuration", `{"secrets":{"token":"working"}}`, 200)
	f.call(t, "POST", base+"/enable", f.approval(t), 200)
	f.s.stopPluginProcesses()
	// Reproduce a host exit after persisting the candidate but before readiness.
	if err := f.s.stateDB.SetPluginConfiguration(t.Context(), f.description.ID, map[string]json.RawMessage{"mode": json.RawMessage(`"bad"`)}, map[string]string{"token": "candidate"}); err != nil {
		t.Fatal(err)
	}
	restarted := &Server{stateDB: f.s.stateDB, pluginCtx: context.Background()}
	t.Cleanup(restarted.stopPluginProcesses)
	if _, err := restarted.RescanPlugins(t.Context()); err != nil {
		t.Fatal(err)
	}
	p, err := f.s.stateDB.GetPlugin(t.Context(), f.description.ID)
	if err != nil || string(p.Configuration.Values["mode"]) != `"good"` {
		t.Fatalf("unverified configuration survived restart: %+v %v", p.Configuration, err)
	}
	if err := f.s.stateDB.WithPluginSecrets(t.Context(), f.description.ID, func(secrets map[string]string) error {
		if secrets["token"] != "working" {
			t.Error("unverified secret survived restart")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPluginManagementLifecycle(t *testing.T) {
	f := newPluginManagementTest(t)
	base := "/" + f.description.ID
	f.call(t, "POST", "/rescan", "{}", 200)
	f.call(t, "POST", base+"/enable", `{}`, 400)
	f.call(t, "POST", base+"/enable", `{"grants":[]}`, 400)
	f.call(t, "POST", base+"/enable", f.approval(t), 400) // Required config missing.
	f.call(t, "POST", base+"/configuration/validate", `{"values":{"token":"wrong-channel"}}`, 400)
	f.call(t, "POST", base+"/configuration", `{"values":{"mode":"good"},"secrets":{"token":"first-secret"}}`, 200)
	// A removed schema field may still have a retained secret snapshot. Never
	// pass that undeclared secret to the current executable.
	if err := f.s.stateDB.SetPluginConfiguration(t.Context(), f.description.ID, map[string]json.RawMessage{"mode": json.RawMessage(`"good"`)}, map[string]string{"obsolete": "obsolete-secret"}); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", base+"/enable", f.approval(t), 200)
	first := f.s.pluginProcesses[f.description.ID]
	if first == nil || first.Health().Status != "ready" {
		t.Fatal("not ready")
	}
	if strings.Contains(first.RedactedStderr(func(text string) string { return text }), "obsolete") {
		t.Fatal("undeclared secret delivered")
	}
	list := "/actions?placement=global&surface=command-palette"
	if body := f.call(t, "GET", list, "", 200); !strings.Contains(body, `"id":"run"`) {
		t.Fatal(body)
	}
	for _, path := range []string{"", base + "/health", base + "/configuration", base + "/stderr"} {
		body := f.call(t, "GET", path, "", 200)
		if strings.Contains(body, "first-secret") {
			t.Fatalf("secret leaked on %s: %s", path, body)
		}
	}
	if body := f.call(t, "GET", base+"/stderr", "", 200); !strings.Contains(body, "[REDACTED]") {
		t.Fatal(body)
	}
	f.call(t, "POST", base+"/configuration", `{"values":{"mode":"bad"},"secrets":{"token":"failed-secret"}}`, 503)
	p, err := f.s.stateDB.GetPlugin(t.Context(), f.description.ID)
	if err != nil || string(p.Configuration.Values["mode"]) != `"good"` || p.Health.Status != "ready" {
		t.Fatalf("rollback: %+v %v", p, err)
	}
	if err := f.s.stateDB.WithPluginSecrets(t.Context(), f.description.ID, func(secrets map[string]string) error {
		if secrets["token"] != "first-secret" {
			t.Error("secret not rolled back")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", base+"/configuration", `{"values":{"mode":"good"},"secrets":{"token":"next-secret"}}`, 200)
	f.call(t, "POST", base+"/restart", `{}`, 200)
	f.call(t, "POST", base+"/retry", `{}`, 200)
	f.call(t, "POST", base+"/grants", `{"grants":[]}`, 200)
	if body := f.call(t, "GET", list, "", 200); strings.TrimSpace(body) != "[]" {
		t.Fatalf("revoked action: %s", body)
	}
	p, _ = f.s.stateDB.GetPlugin(t.Context(), f.description.ID)
	if len(p.Grants) != 0 {
		t.Fatal("grants not revoked")
	}
	f.call(t, "POST", base+"/remove-data", `{}`, 409)
	dataDir, err := f.s.stateDB.PluginDataDir(t.Context(), f.description.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", base+"/disable", `{}`, 200)
	p, _ = f.s.stateDB.GetPlugin(t.Context(), f.description.ID)
	if p.Enabled || !p.Configuration.Secrets["token"] || len(f.s.pluginProcesses) != 0 {
		t.Fatalf("disable: %+v", p)
	}
	if body := f.call(t, "GET", base+"/stderr", "", 200); !strings.Contains(body, "[REDACTED]") || strings.Contains(body, "next-secret") {
		t.Fatal(body)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "starts")); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", base+"/restart", `{}`, 409)
	f.call(t, "POST", base+"/remove-data", `{}`, 200)
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("data retained: %v", err)
	}
	f.call(t, "GET", base+"/health", "", 404)
}

func TestPluginManagementValidation(t *testing.T) {
	f := newPluginManagementTest(t)
	base := "/" + f.description.ID
	f.call(t, "POST", "/rescan", "{}", 200)
	for _, input := range []string{
		`{`, `{}`, `null`, `{} {}`, `{"unknown":true}`, `{"values":{"mode":null}}`, `{"values":{"mode":"no"}}`,
		`{"values":{"undeclared":"value"}}`, `{"secrets":{"undeclared":"value"}}`, `{"secrets":{"mode":"wrong-channel"}}`,
		`{"secrets":{"token":""}}`, `{"secrets":{"token":true}}`,
		`{"secrets":{"token":"` + strings.Repeat("x", plugins.MaxMessageBytes) + `"}}`,
	} {
		f.call(t, "POST", base+"/configuration/validate", input, 400)
	}
	f.call(t, "POST", base+"/configuration/validate", `{"secrets":{"token":"validation-only"}}`, 200)
	p, _ := f.s.stateDB.GetPlugin(t.Context(), f.description.ID)
	if p.Configuration.Secrets["token"] {
		t.Fatal("validation wrote secret")
	}
	f.call(t, "POST", base+"/configuration", `{"secrets":{"token":"retained"}}`, 200)
	f.call(t, "POST", base+"/configuration/validate", `{"values":{}}`, 200)
	f.call(t, "GET", base+"/configuration", "", 200)
	f.call(t, "GET", base+"/grants", "", 200)
	for _, grants := range []string{`{}`, `{"grants":null}`, `{"grants":["unknown"]}`, `{"grants":["context.owner","context.owner"]}`} {
		f.call(t, "POST", base+"/grants", grants, 400)
		f.call(t, "POST", base+"/enable", grants, 400)
	}
	f.call(t, "POST", base+"/grants", `{"grants":[]}`, 200)
	f.call(t, "GET", base+"/unknown", "", 404)
	f.call(t, "GET", "/unknown", "", 404)
	f.call(t, "POST", "/missing/disable", `{}`, 404)
	f.call(t, "POST", "/missing/remove-data", `{}`, 200)
	if err := f.s.stateDB.RemovePlugin(t.Context(), f.description.ID); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", base+"/enable", `{"grants":["context.owner"]}`, 409)
}

func TestPluginManagementFailedLaunchAndRetry(t *testing.T) {
	f := newPluginManagementTest(t)
	base := "/" + f.description.ID
	f.call(t, "POST", "/rescan", `{}`, 200)
	f.call(t, "POST", base+"/configuration", `{"values":{"mode":"bad"},"secrets":{"token":"failed-secret"}}`, 200)
	f.call(t, "POST", base+"/enable", f.approval(t), 503)
	if body := f.call(t, "GET", base+"/health", "", 200); !strings.Contains(body, "unhealthy") {
		t.Fatal(body)
	}
	if body := f.call(t, "GET", base+"/stderr", "", 200); !strings.Contains(body, "[REDACTED]") || strings.Contains(body, "failed-secret") {
		t.Fatal(body)
	}
	f.call(t, "POST", base+"/configuration", `{"values":{"mode":"good"}}`, 409)
	f.call(t, "POST", base+"/disable", `{}`, 200)
	f.call(t, "POST", base+"/configuration", `{"values":{"mode":"good"}}`, 200)
	f.call(t, "POST", base+"/enable", f.approval(t), 200)
	f.s.pluginMu.Lock()
	f.s.stopOnePluginLocked(f.description.ID)
	f.s.pluginMu.Unlock()
	if err := f.s.stateDB.SetPluginHealth(t.Context(), f.description.ID, state.PluginHealth{Status: "unhealthy", RestartCount: 5}); err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", base+"/retry", `{}`, 200)
	if body := f.call(t, "GET", base+"/health", "", 200); !strings.Contains(body, `"status":"ready"`) || !strings.Contains(body, `"restartCount":0`) {
		t.Fatal(body)
	}
}

func TestPluginConfigurationCancellationRollsBack(t *testing.T) {
	f := newPluginManagementTest(t)
	base := "/" + f.description.ID
	f.call(t, "POST", "/rescan", `{}`, 200)
	f.call(t, "POST", base+"/configuration", `{"secrets":{"token":"working"}}`, 200)
	f.call(t, "POST", base+"/enable", f.approval(t), 200)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest(http.MethodPost, "http://localhost:8228/api/plugins"+base+"/configuration", strings.NewReader(`{"values":{"mode":"hang"},"secrets":{"token":"candidate"}}`)).WithContext(ctx)
	r.RemoteAddr = "127.0.0.1:1234"
	r.AddCookie(f.cookie)
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatalf("cancel: %d %s", w.Code, w.Body.String())
	}
	p, err := f.s.stateDB.GetPlugin(t.Context(), f.description.ID)
	if err != nil || p.Health.Status != "ready" || string(p.Configuration.Values["mode"]) != `"good"` {
		t.Fatalf("rollback: %+v %v", p, err)
	}
}

func TestPluginManagementUnavailable(t *testing.T) {
	f := newPluginManagementTest(t)
	db := f.s.stateDB
	f.s.stateDB = nil
	f.call(t, "GET", "", "", 503)
	f.call(t, "POST", "/rescan", `{}`, 503)
	f.call(t, "GET", "/org.example.management/health", "", 503)
	f.s.stateDB = db
	f.call(t, "POST", "/rescan", `{}`, 200)
	f.call(t, "POST", "/org.example.management/configuration", `{"secrets":{"token":"secret"}}`, 200)
	f.s.stopPluginProcesses()
	f.call(t, "POST", "/org.example.management/enable", f.approval(t), 503)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	f.call(t, "GET", "", "", 500)
	f.call(t, "POST", "/rescan", `{}`, 500)
	f.call(t, "GET", "/org.example.management/health", "", 500)
}

func TestPluginConfigurationSchema(t *testing.T) {
	p := state.PluginRegistration{Description: plugins.Description{Settings: []plugins.Setting{
		{Key: "count", Type: "integer", Required: true},
		{Key: "optional", Type: "boolean"},
		{Key: "token", Type: "string", Secret: true, Enum: []string{"allowed"}},
	}}}
	for _, tt := range []struct {
		values, secrets string
		valid           bool
	}{
		{`{}`, `{}`, false},
		{`{"count":1.5}`, `{}`, false},
		{`{"count":1}`, `{}`, true},
		{`{"count":1,"optional":false}`, `{"token":"allowed"}`, true},
		{`{"count":1}`, `{"token":"denied"}`, false},
		{`{"count":1}`, `{"token":""}`, true},
	} {
		var input pluginManagementInput
		if err := json.Unmarshal([]byte(tt.values), &input.Values); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(tt.secrets), &input.Secrets); err != nil {
			t.Fatal(err)
		}
		values, err := validatePluginConfiguration(p, input)
		if (err == nil) != tt.valid {
			t.Fatalf("%+v: %v", tt, err)
		}
		if _, ok := values["token"]; ok {
			t.Fatal("secret in public values")
		}
	}
}

func TestPluginManagementConflicts(t *testing.T) {
	f := newPluginManagementTest(t)
	base := "/" + f.description.ID
	f.call(t, "POST", "/rescan", "{}", 200)
	writeDiscoveryPlugin(t, f.dir, "ocman-plugin-duplicate", f.description.ID, "1")
	f.call(t, "POST", "/rescan", "{}", 200)
	if body := f.call(t, "GET", base+"/health", "", 200); !strings.Contains(body, "conflict") {
		t.Fatal(body)
	}
	for _, action := range []string{"enable", "restart", "retry"} {
		f.call(t, "POST", base+"/"+action, `{"grants":["context.owner"]}`, 409)
	}
	f.call(t, "POST", base+"/disable", `{}`, 200)
	if body := f.call(t, "GET", base+"/health", "", 200); !strings.Contains(body, "conflict") {
		t.Fatal(body)
	}
	if len(f.s.pluginProcesses) != 0 {
		t.Fatal("conflicted plugin launched")
	}
}
