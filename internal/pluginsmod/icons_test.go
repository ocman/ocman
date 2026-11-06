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
	"testing/fstest"
	"time"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/sdk/plugin"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

const iconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="12" cy="12" r="8"/><path d="M4 12h16"/></svg>`

func iconDescription() plugins.Description {
	return plugins.Description{ID: "org.example.maintenance", Name: "Maintenance", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 2, Icon: &plugins.IconDescriptor{Path: "assets/wrench.svg"}}
}

func TestIconProcessHelper(t *testing.T) {
	if os.Getenv("ICON_PROCESS_HELPER") != "1" {
		return
	}
	mode := plugin.Mode(os.Args[len(os.Args)-1])
	if mode == plugin.ModeServe {
		_ = os.NewFile(3, "configuration").Close()
	}
	d := iconDescription()
	handler := plugin.AssetHandler(d, fstest.MapFS{"assets/wrench.svg": &fstest.MapFile{Data: []byte(iconSVG)}})
	h := func(ctx context.Context, call plugin.Call, emit func(json.RawMessage) error) (json.RawMessage, error) {
		switch os.Getenv("ICON_PROCESS_BAD") {
		case "script":
			return json.Marshal(plugins.AssetResult{Data: []byte(`<svg onload="evil()"/>`)})
		case "chunk":
			_ = emit(json.RawMessage(`{}`))
			return json.RawMessage(`{}`), nil
		case "error":
			return nil, plugin.Failure(plugin.ErrorNotFound)
		case "wait":
			<-ctx.Done()
			return nil, ctx.Err()
		case "revoke":
			marker := os.Getenv("ICON_PROCESS_MARKER")
			_ = os.WriteFile(marker, nil, 0o600)
			for {
				if _, err := os.Stat(marker + ".release"); err == nil {
					break
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Millisecond):
				}
			}
		}
		return handler(ctx, call, emit)
	}
	if plugin.Run(context.Background(), mode, os.Getenv("OCMAN_PLUGIN_TOKEN"), d, os.Stdin, os.Stdout, h) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func startIconHelper(t *testing.T, bad string) (*Module, *state.DB) {
	t.Helper()
	db := statetest.Open(t, Schema)
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "ocman-plugin-maintenance")
	script := "#!/bin/sh\nexport ICON_PROCESS_HELPER=1\nexport ICON_PROCESS_BAD='" + bad + "'\nexport ICON_PROCESS_MARKER='" + filepath.Join(root, "admitted") + "'\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run='^TestIconProcessHelper$' -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	candidates, err := plugins.Scan(t.Context(), root, nil)
	if err != nil || len(candidates) != 1 || candidates[0].Err != nil {
		t.Fatalf("discover: %+v %v", candidates, err)
	}
	c := candidates[0]
	if err := db.DiscoverPlugin(t.Context(), c.Description, path, c.Checksum, nil); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPluginEnabled(t.Context(), c.Description.ID, true, nil); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	if err := os.Chmod(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := plugins.StartProcess(t.Context(), plugins.LaunchConfig{Candidate: c, Supported: []plugins.Capability{plugins.AssetCapability}, DataDir: dataDir, Configuration: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Stop)
	deadline := time.Now().Add(10 * time.Second)
	for p.Health().Status != "ready" {
		if time.Now().After(deadline) {
			t.Fatalf("not ready: %+v", p.Health())
		}
		time.Sleep(10 * time.Millisecond)
	}
	m := NewModule(Deps{Store: db})
	m.processes = map[string]*plugins.Process{c.Description.ID: p}
	return m, db
}

const iconURL = "/api/plugins/icon?ownerId=local&pluginId=org.example.maintenance&path=assets/wrench.svg"

func TestPluginIconNoGrantsAndCatalogRoundtrip(t *testing.T) {
	m, db := startIconHelper(t, "")
	w := serve(m.handlePluginIcon, "GET", iconURL, "", nil)
	if w.Code != 200 || w.Body.String() != iconSVG || w.Header().Get("Content-Type") != "image/svg+xml" || w.Header().Get("Content-Security-Policy") != httpx.InertFileCSP || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("icon: %d %s %v", w.Code, w.Body.String(), w.Header())
	}
	registrations, err := db.ListPlugins(t.Context())
	if err != nil || len(registrations) != 1 || registrations[0].Description.Icon.Path != "assets/wrench.svg" || len(registrations[0].Grants) != 0 || len(registrations[0].Description.Capabilities) != 0 {
		t.Fatalf("catalog: %+v %v", registrations, err)
	}
	// Remote metadata reads use the same no-grant standard method.
	response := m.RemoteOperation(t.Context(), remote.PluginRequest{Operation: "asset-read", PluginID: iconDescription().ID, Input: remote.PluginInput{Values: map[string]json.RawMessage{"asset": json.RawMessage(`{"path":"assets/wrench.svg"}`)}}})
	var data []byte
	if response.Error != nil || json.Unmarshal(response.Value, &data) != nil || string(data) != iconSVG {
		t.Fatalf("remote: %+v", response)
	}
	for _, url := range []string{strings.Replace(iconURL, "assets/wrench.svg", "../secret.svg", 1), strings.Replace(iconURL, "assets/wrench.svg", "https://evil.example/x.svg", 1), strings.Replace(iconURL, "assets/wrench.svg", "other.svg", 1), strings.Replace(iconURL, "ownerId=local", "ownerId=missing", 1), strings.Replace(iconURL, "ownerId=local", "ownerId=", 1), strings.Replace(iconURL, "org.example.maintenance", "org.example.missing", 1)} {
		if w := serve(m.handlePluginIcon, "GET", url, "", nil); w.Code == 200 {
			t.Fatalf("accepted %s", url)
		}
	}
	if err := db.SetPluginEnabled(t.Context(), iconDescription().ID, false, nil); err != nil {
		t.Fatal(err)
	}
	if w := serve(m.handlePluginIcon, "GET", iconURL, "", nil); w.Code == 200 {
		t.Fatal("disabled icon returned")
	}
}

func TestPluginIconUntrustedProcessErrors(t *testing.T) {
	for _, bad := range []string{"script", "chunk", "error", "wait"} {
		t.Run(bad, func(t *testing.T) {
			m, _ := startIconHelper(t, bad)
			if w := serve(m.handlePluginIcon, "GET", iconURL, "", nil); w.Code == 200 || strings.Contains(w.Body.String(), "evil") {
				t.Fatalf("unsafe response: %d %s", w.Code, w.Body.String())
			}
		})
	}
	m := NewModule(Deps{})
	if w := serve(m.handlePluginIcon, "GET", iconURL, "", nil); w.Code == 200 {
		t.Fatal("missing store accepted")
	}
}

func TestPluginIconNormalAuthentication(t *testing.T) {
	m := NewModule(Deps{})
	h := http.NewServeMux()
	m.Routes(moduletest.Router{Mux: h, Auth: func(http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }
	}})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, iconURL, nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatal("unauthorized icon served")
	}
}

func TestPluginIconRechecksEnablementAfterRead(t *testing.T) {
	m, db := startIconHelper(t, "revoke")
	registrations, _ := db.ListPlugins(t.Context())
	marker := filepath.Join(filepath.Dir(registrations[0].ExecutablePath), "admitted")
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() { result <- serve(m.handlePluginIcon, "GET", iconURL, "", nil) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("read was not admitted")
		}
		time.Sleep(time.Millisecond)
	}
	if err := db.SetPluginEnabled(t.Context(), iconDescription().ID, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker+".release", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if w := <-result; w.Code == 200 {
		t.Fatal("read returned an icon after disablement")
	}
}

func TestPluginIconNavigationCatalogs(t *testing.T) {
	d := iconDescription()
	d.Capabilities = []plugins.Capability{plugins.PageCapability}
	d.RequestedGrants = []string{plugins.PageProjectGrant}
	d.Pages = []plugins.PageDescriptor{{ID: "documents", Label: "Documents"}}
	d.Icon = &plugins.IconDescriptor{Bootstrap: "journal-text"}
	page := state.PluginRegistration{Description: d, Enabled: true, Grants: []string{plugins.PageProjectGrant}}
	pages := pluginPageDeclarations([]state.PluginRegistration{page}, "owner")
	if len(pages) == 0 || pages[0].Icon.Bootstrap != "journal-text" {
		t.Fatalf("pages: %+v", pages)
	}
	ui := state.PluginRegistration{Description: uiTestDescription(), Enabled: true, Grants: []string{plugins.UIAppGrant}}
	ui.Description.Icon = iconDescription().Icon
	uis := pluginUIDeclarations([]state.PluginRegistration{ui})
	if len(uis) != 1 || uis[0].Icon.Path != "assets/wrench.svg" {
		t.Fatalf("uis: %+v", uis)
	}
	ui.Description.Icon = nil
	if pluginUIDeclarations([]state.PluginRegistration{ui})[0].Icon != nil {
		t.Fatal("default icon changed")
	}
}
