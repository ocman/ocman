package pluginsmod

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

	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	plugin "github.com/ocman/ocman/sdk/plugin"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

var uiTestBundle = plugin.UIBundle{Expose: plugin.UIIndexExpose, Data: []byte(strings.Repeat("export default function Index() { return null }\n", 20000))}

func uiTestDescription() plugins.Description {
	return plugins.Description{ID: "org.example.ui", Name: "Catalog", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 2,
		Capabilities: []plugins.Capability{plugins.UICapability, {Name: "action", Version: plugins.Version{Major: 1}}}, RequestedGrants: []string{plugins.UIAppGrant},
		Actions: []plugins.ActionDescriptor{{ID: "reset", Label: "Reset", Placement: "global", Surfaces: []string{"command-palette"}}},
		UI:      plugin.NewUIDescriptor("^1.0.0", uiTestBundle)}
}

func TestUIProcessHelper(t *testing.T) {
	if os.Getenv("UI_PROCESS_HELPER") != "1" {
		return
	}
	mode := plugin.Mode(os.Args[len(os.Args)-1])
	if mode == plugin.ModeServe {
		_ = os.NewFile(3, "configuration").Close()
	}
	d := uiTestDescription()
	bundle := uiTestBundle
	if os.Getenv("UI_PROCESS_TAMPER") == "1" {
		// Serve bytes that differ from the approved checksum.
		bundle.Data = append([]byte("//"), bundle.Data[2:]...)
	}
	ui := plugin.UIHandler(d, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "items.list":
			return map[string]any{"items": []string{"alpha"}, "params": params}, nil
		case "chunky":
			return nil, plugin.Failure(plugin.ErrorConflict)
		}
		return nil, plugin.Failure(plugin.ErrorNotFound)
	}, bundle)
	handler := func(ctx context.Context, call plugin.Call, emit func(json.RawMessage) error) (json.RawMessage, error) {
		if os.Getenv("UI_PROCESS_TAMPER") == "1" && call.Method == "bundle" {
			chunks, _ := plugins.EncodeUIBundleChunks(bundle.Data)
			for _, c := range chunks {
				if err := emit(c); err != nil {
					return nil, err
				}
			}
			return json.RawMessage(`{}`), nil
		}
		return ui(ctx, call, emit)
	}
	if plugin.Run(context.Background(), mode, os.Getenv("OCMAN_PLUGIN_TOKEN"), d, os.Stdin, os.Stdout, handler) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func startUIHelper(t *testing.T, db *state.DB, tamper bool) (*Module, plugins.Discovery) {
	t.Helper()
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := "export UI_PROCESS_HELPER=1\n"
	if tamper {
		env += "export UI_PROCESS_TAMPER=1\n"
	}
	path := filepath.Join(root, "ocman-plugin-ui")
	script := "#!/bin/sh\n" + env + "exec '" + strings.ReplaceAll(executable, "'", "'\\''") + "' -test.run='^TestUIProcessHelper$' -- \"$@\"\n"
	if err = os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	candidates, err := plugins.Scan(t.Context(), root, nil)
	if err != nil || len(candidates) != 1 || candidates[0].Err != nil {
		t.Fatalf("discovery: %+v %v", candidates, err)
	}
	candidate := candidates[0]
	if err = db.DiscoverPlugin(t.Context(), candidate.Description, path, candidate.Checksum, nil); err != nil {
		t.Fatal(err)
	}
	if err = db.SetPluginEnabled(t.Context(), candidate.Description.ID, true, []string{plugins.UIAppGrant}); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	if err = os.Chmod(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	process, err := plugins.StartProcess(t.Context(), plugins.LaunchConfig{Candidate: candidate, Supported: []plugins.Capability{plugins.UICapability}, DataDir: dataDir, Configuration: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(process.Stop)
	deadline := time.Now().Add(10 * time.Second)
	for process.Health().Status != "ready" {
		if time.Now().After(deadline) {
			t.Fatalf("plugin not ready: %+v", process.Health())
		}
		time.Sleep(10 * time.Millisecond)
	}
	m := NewModule(Deps{Store: db})
	m.processes = map[string]*plugins.Process{candidate.Description.ID: process}
	return m, candidate
}

func serve(h http.HandlerFunc, method, target, body string, header http.Header) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	for k, v := range header {
		r.Header[k] = v
	}
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func TestPluginUIListingBundleAndCalls(t *testing.T) {
	db := statetest.Open(t, Schema)
	m, candidate := startUIHelper(t, db, false)
	id := candidate.Description.ID
	sha := candidate.Description.UI.Exposes["index"].SHA256

	w := serve(m.handlePluginUIs, "GET", "/api/plugins/ui", "", nil)
	var listed []listedPluginUI
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &listed) != nil || len(listed) != 1 {
		t.Fatalf("listing %d %s", w.Code, w.Body)
	}
	if l := listed[0]; l.OwnerID != "local" || !l.Compatible || l.Index == nil || l.Index.URL != uiBundleURL(id, sha) || l.HostSDK != plugins.UISDKVersion || len(l.Actions) != 1 || l.PluginLabel != "Catalog" {
		t.Fatalf("listed %+v", l)
	}

	bundleURL := uiBundleURL(id, sha)
	w = serve(m.handlePluginUIBundle, "GET", bundleURL, "", nil)
	if w.Code != 200 || w.Body.String() != string(uiTestBundle.Data) {
		t.Fatalf("bundle %d len=%d", w.Code, w.Body.Len())
	}
	if h := w.Header(); h.Get("Content-Type") != "text/javascript; charset=utf-8" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("ETag") != `"`+sha+`"` || h.Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("headers %v", h)
	}
	w = serve(m.handlePluginUIBundle, "GET", bundleURL, "", http.Header{"If-None-Match": {`"` + sha + `"`}})
	if w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatalf("conditional %d", w.Code)
	}
	if m.uiBundles[id].sha != sha {
		t.Fatal("verified bundle not cached")
	}
	m.mu.Lock()
	m.stopOnePluginLocked("org.example.unrelated")
	_, kept := m.uiBundles[id]
	m.mu.Unlock()
	if !kept {
		t.Fatal("stopping another plugin dropped this bundle")
	}
	for _, bad := range []string{uiBundleURL(id, strings.Repeat("0", 64)), strings.Replace(bundleURL, "expose=index", "expose=settings", 1), uiBundleURL(id, "short")} {
		if w = serve(m.handlePluginUIBundle, "GET", bad, "", nil); w.Code == 200 {
			t.Fatalf("served %s", bad)
		}
	}

	call := func(body string) *httptest.ResponseRecorder {
		return serve(m.handlePluginUICall, "POST", "/api/plugins/ui/call", body, http.Header{"Content-Type": {"application/json"}})
	}
	w = call(`{"pluginId":"` + id + `","method":"items.list","params":{"limit":2}}`)
	if w.Code != 200 || w.Body.String() != `{"items":["alpha"],"params":{"limit":2}}`+"\n" && w.Body.String() != `{"items":["alpha"],"params":{"limit":2}}` {
		t.Fatalf("call %d %q", w.Code, w.Body)
	}
	for body, status := range map[string]int{
		`{"pluginId":"` + id + `","method":"missing"}`:  404,
		`{"pluginId":"` + id + `","method":"chunky"}`:   409,
		`{"pluginId":"` + id + `","method":"bad name"}`: 400,
		`{"pluginId":"org.example.other","method":"x"}`: 503,
	} {
		if w = call(body); w.Code != status {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}

	// A hub never loads UI code from another machine.
	raw, _ := json.Marshal(plugins.UICall{Method: "items.list"})
	if r := m.RemoteOperation(t.Context(), remote.PluginRequest{Operation: "ui-call", PluginID: id, Input: remote.PluginInput{Values: map[string]json.RawMessage{"call": raw}}}); r.Error == nil || r.Error.Category != plugins.ErrorPermissionDenied {
		t.Fatalf("remote ui call: %+v", r)
	}

	// Revocation denies calls and cached bundles alike.
	if err := db.SetPluginGrants(t.Context(), id, nil); err != nil {
		t.Fatal(err)
	}
	if w = call(`{"pluginId":"` + id + `","method":"items.list"}`); w.Code != 403 {
		t.Fatalf("revoked call %d", w.Code)
	}
	if w = serve(m.handlePluginUIBundle, "GET", bundleURL, "", nil); w.Code != 403 {
		t.Fatalf("revoked bundle %d", w.Code)
	}
	if w = serve(m.handlePluginUIs, "GET", "/api/plugins/ui", "", nil); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("ungranted plugin listed: %s", w.Body)
	}
	m.mu.Lock()
	m.stopOnePluginLocked(id)
	_, kept = m.uiBundles[id]
	m.mu.Unlock()
	if kept {
		t.Fatal("stopped plugin kept its bundle in memory")
	}
}

func TestPluginUIRejectsBundleNotMatchingApproval(t *testing.T) {
	db := statetest.Open(t, Schema)
	m, candidate := startUIHelper(t, db, true)
	sha := candidate.Description.UI.Exposes["index"].SHA256
	if w := serve(m.handlePluginUIBundle, "GET", uiBundleURL(candidate.Description.ID, sha), "", nil); w.Code != 400 || len(m.uiBundles) != 0 {
		t.Fatalf("tampered bundle served: %d", w.Code)
	}
}

func TestPluginUIDeclarations(t *testing.T) {
	d := uiTestDescription()
	ready := state.PluginRegistration{Description: d, Enabled: true, Grants: []string{plugins.UIAppGrant}}
	incompatible := ready
	incompatible.Description.UI = &plugins.UIDescriptor{SDK: "^2.0.0", Exposes: d.UI.Exposes}
	incompatible.Description.ID = "org.example.next"
	disabled, removed, conflict, ungranted, noUI := ready, ready, ready, ready, ready
	disabled.Enabled = false
	removed.Removed = true
	conflict.Health.Status = "conflict"
	ungranted.Grants = nil
	noUI.Description.UI = nil
	listed := pluginUIDeclarations([]state.PluginRegistration{ready, incompatible, disabled, removed, conflict, ungranted, noUI})
	if len(listed) != 2 || listed[0].Index == nil || listed[1].Compatible || listed[1].Index != nil || listed[1].SDK != "^2.0.0" {
		t.Fatalf("%+v", listed)
	}
	withActions := ready
	withActions.Description.Actions = []plugins.ActionDescriptor{
		{ID: "palette", Label: "Palette", Placement: "global", Surfaces: []string{"command-palette"}},
		{ID: "ungranted", Label: "Ungranted", Placement: "session", Surfaces: []string{"command-palette"}, RequiredGrants: []string{"context.session"}},
	}
	if got := pluginUIDeclarations([]state.PluginRegistration{withActions})[0].Actions; len(got) != 1 || got[0].ID != "palette" {
		t.Fatalf("actions %+v", got)
	}
	if m := NewModule(Deps{}); m.localPluginUI(t.Context(), remote.PluginRequest{Operation: "ui-call"}, false).Error == nil {
		t.Fatal("call without store admitted")
	}
}
