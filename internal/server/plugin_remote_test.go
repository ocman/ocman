package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
)

func TestPluginManagementOwnerCapabilities(t *testing.T) {
	hub, owner := newPluginManagementTest(t), newPluginManagementTest(t)
	hub.s.registry = platforms.NewRegistry()
	disconnect := connectPluginOwner(t, hub.s, owner.s)
	hub.s.router().RegisterRemote("machine", local.New(local.Deps{}))
	hub.s.router().RegisterRemote("unsupported", local.New(local.Deps{}))
	check := func(remoteAvailable bool) {
		t.Helper()
		w := httptest.NewRecorder()
		hub.s.handleCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
		var response struct {
			Hosts []hostCapabilityEntry `json:"hosts"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		got := map[string]bool{}
		for _, host := range response.Hosts {
			got[host.RemoteID] = host.PluginManagement
		}
		want := map[string]bool{"local": true, "machine": remoteAvailable, "unsupported": false}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("capabilities = %v, want %v", got, want)
		}
	}
	check(true)
	disconnect()
	check(false)
}

func connectPluginOwner(t *testing.T, hub, owner *Server) func() {
	t.Helper()
	registry := platforms.NewRegistry()
	registry.Register(&fakePlatform{id: "opencode"})
	host := local.New(local.Deps{})
	service := remote.NewServer(registry, host, "machine", "test").UsePlugins(owner.RemotePluginOperation)
	listener, err := remote.NewListener(remote.ListenConfig{Addr: "127.0.0.1:0", Token: "token", TrustedOverlay: true}, service)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = listener.Serve() }()
	t.Cleanup(listener.Stop)
	mgr := remote.NewManager(platforms.NewRegistry(), hostsvc.NewRouter(host), hub.stateDB, "opencode")
	hub.SetRemoteManager(mgr)
	t.Cleanup(mgr.Stop)
	id, err := mgr.Add(t.Context(), "grpc://"+listener.Addr(), "token", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		response := hub.routePluginOperation(t.Context(), "machine", remote.PluginRequest{Operation: "catalog"})
		if response.Error == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("remote not ready: %+v", response)
		}
		time.Sleep(5 * time.Millisecond)
	}
	return func() {
		if err := mgr.Remove(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPluginRemoteManagementParityAndSecrets(t *testing.T) {
	hub := newPluginManagementTest(t)
	owner := newPluginManagementTest(t)
	// Both owners have the same plugin ID but independent durable registrations.
	for _, f := range []*pluginManagementTest{hub, owner} {
		if err := f.s.stateDB.DiscoverPlugin(t.Context(), f.description, filepath.Join(f.dir, "plugin"), strings.Repeat("a", 64), []state.PluginInstance{{ID: "action", Capability: plugins.ActionCapability, Scope: plugins.ScopeOwner}}); err != nil {
			t.Fatal(err)
		}
	}
	disconnect := connectPluginOwner(t, hub.s, owner.s)
	base := "/" + hub.description.ID
	for _, source := range []string{"local", "machine"} {
		body := hub.call(t, "GET", "?ownerId="+source, "", 200)
		var catalog []ownedPlugin
		if err := json.Unmarshal([]byte(body), &catalog); err != nil || len(catalog) != 1 || catalog[0].OwnerID != source || catalog[0].Instances[0].ID != "action" {
			t.Fatalf("qualified catalog: %s, %v", body, err)
		}
		body = hub.call(t, "POST", base+"/configuration?ownerId="+source, `{"values":{"mode":"good"},"secrets":{"token":"`+source+`-private"}}`, 200)
		if strings.Contains(body, "-private") {
			t.Fatal("mutation returned secret")
		}
	}
	for _, path := range []string{"/configuration", "/health", "/grants", "/stderr"} {
		local := hub.call(t, "GET", base+path+"?ownerId=local", "", 200)
		other := hub.call(t, "GET", base+path+"?ownerId=machine", "", 200)
		if local != other || strings.Contains(other, "-private") {
			t.Fatalf("parity %s: local=%s remote=%s", path, local, other)
		}
	}
	for i, f := range []*pluginManagementTest{hub, owner} {
		want := []string{"local-private", "machine-private"}[i]
		if err := f.s.stateDB.WithPluginSecrets(t.Context(), f.description.ID, func(secrets map[string]string) error {
			if secrets["token"] != want {
				t.Fatalf("owner secret = %v", secrets)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	hub.call(t, "POST", base+"/configuration/validate?ownerId=machine", `{"secrets":{"token":"validate-only"}}`, 200)
	hub.call(t, "POST", base+"/configuration?ownerId=machine", `{"values":{"token":"leak"}}`, 400)
	hub.call(t, "POST", base+"/remove-data?ownerId=machine", `{}`, 200)
	hub.call(t, "GET", base+"/health?ownerId=machine", "", 404)
	hub.call(t, "GET", base+"/health?ownerId=local", "", 200)
	disconnect()
	for _, source := range []string{"machine", "missing"} {
		hub.call(t, "GET", "?ownerId="+source, "", 503)
		hub.call(t, "POST", base+"/disable?ownerId="+source, `{}`, 503)
		hub.call(t, "POST", "/rescan?ownerId="+source, `{}`, 503)
	}
}

func TestPluginRemoteApprovalAndDiscovery(t *testing.T) {
	hub, owner := newPluginManagementTest(t), newPluginManagementTest(t)
	owner.call(t, "POST", "/rescan", `{}`, 200)
	connectPluginOwner(t, hub.s, owner.s)
	base := "/" + owner.description.ID
	hub.call(t, "POST", base+"/configuration?ownerId=machine", `{"secrets":{"token":"working"}}`, 200)
	hub.call(t, "POST", base+"/enable?ownerId=machine", `{"grants":["context.owner"]}`, 409)
	hub.call(t, "POST", base+"/enable?ownerId=machine", owner.approval(t), 200)
	owner.s.pluginMu.Lock()
	owner.s.pluginDiscovery = []pluginDiscoveryFailure{{Filename: "ocman-plugin-broken", Error: plugins.ErrInvalidMessage.Error()}}
	owner.s.pluginMu.Unlock()
	if body := hub.call(t, "GET", "/discovery?ownerId=machine", "", 200); !strings.Contains(body, "ocman-plugin-broken") {
		t.Fatal(body)
	}
	if body := hub.call(t, "GET", "/discovery?ownerId=local", "", 200); strings.TrimSpace(body) != "[]" {
		t.Fatalf("remote diagnostics leaked into hub catalog: %s", body)
	}
}

func installRoutedAction(t *testing.T, s *Server, scope plugins.Scope, label string) string {
	t.Helper()
	d := plugins.Description{ID: "org.example." + string(scope), Name: "Action", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: scope, MaxConcurrency: 1,
		Capabilities: []plugins.Capability{plugins.ActionCapability}, RequestedGrants: []string{"context.owner", "context.project"}}
	for _, placement := range []string{"global", "project", "session"} {
		d.Actions = append(d.Actions, plugins.ActionDescriptor{ID: placement, Label: label, Placement: placement, Surfaces: []string{"command-palette"}, RequiredGrants: d.RequestedGrants, Confirmation: "Run?"})
	}
	hello, _ := json.Marshal(plugins.Envelope{Type: plugins.TypeHello, Hello: &plugins.Hello{Mode: "$1", Token: "$OCMAN_PLUGIN_TOKEN", Description: &d}})
	result := fmt.Sprintf(`{"type":"result","result":{"id":"$n","value":{"results":[{"kind":"notice","text":%q},{"kind":"artifact","label":"report.txt","data":"aGk="}]}}}`, label)
	script := "#!/bin/sh\n/bin/cat <<EOF\n" + string(hello) + "\nEOF\nif [ \"$1\" = serve ]; then\nread -r ack\nn=0\nwhile IFS= read -r call; do\nn=$((n + 1))\nprintf '%s\\n' \"$call\" >> calls\n/bin/cat <<EOF\n" + result + "\nEOF\ndone\nfi\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "ocman-plugin-test")
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	catalog, err := plugins.Scan(t.Context(), dir, nil)
	if err != nil || len(catalog) != 1 || catalog[0].Err != nil {
		t.Fatalf("scan: %+v %v", catalog, err)
	}
	if err := s.stateDB.DiscoverPlugin(t.Context(), d, path, catalog[0].Checksum, nil); err != nil {
		t.Fatal(err)
	}
	grants := d.RequestedGrants
	p, err := s.stateDB.GetPlugin(t.Context(), d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.manageLocalPlugin(t.Context(), d.ID, "enable", false, pluginManagementInput{Approval: p.Approval, Grants: &grants}); err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func TestPluginRemoteActionsScopeAndInvocation(t *testing.T) {
	hub, owner := newPluginManagementTest(t), newPluginManagementTest(t)
	for _, scope := range []plugins.Scope{plugins.ScopeOwner, plugins.ScopeHub} {
		installRoutedAction(t, hub.s, scope, "hub-"+string(scope))
		installRoutedAction(t, owner.s, scope, "remote-"+string(scope))
	}
	disconnect := connectPluginOwner(t, hub.s, owner.s)
	for _, placement := range []string{"project", "session", "global"} {
		body := hub.call(t, "GET", "/actions?ownerId=machine&projectId=project&sessionId=session&placement="+placement+"&surface=command-palette", "", 200)
		var actions []listedPluginAction
		if err := json.Unmarshal([]byte(body), &actions); err != nil {
			t.Fatal(err)
		}
		labels := map[string]string{}
		for _, action := range actions {
			labels[action.Action.Label] = action.OwnerID
		}
		want := map[string]string{"remote-owner": "machine", "hub-hub": "local"}
		if placement == "global" {
			want = map[string]string{"hub-owner": "local", "hub-hub": "local"}
		}
		if !reflect.DeepEqual(labels, want) {
			t.Fatalf("%s actions = %v", placement, labels)
		}
	}
	invoke := func(request plugins.ActionRequest, status int) plugins.ActionResponse {
		t.Helper()
		body, _ := json.Marshal(request)
		result := hub.call(t, "POST", "/actions/invoke", string(body), status)
		var response plugins.ActionResponse
		if err := json.Unmarshal([]byte(result), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	for i, tc := range []struct{ plugin, owner, placement, label string }{
		{"owner", "machine", "project", "remote-owner"},
		{"owner", "machine", "session", "remote-owner"},
		{"hub", "local", "project", "hub-hub"},
		{"owner", "machine", "global", "hub-owner"},
	} {
		r := plugins.ActionRequest{PluginID: "org.example." + tc.plugin, OwnerID: tc.owner, ActionID: tc.placement, Placement: tc.placement, OperationID: fmt.Sprintf("op%d", i), Surface: "command-palette", Context: plugins.ActionContext{OwnerID: "machine", ProjectID: "project", SessionID: "session"}}
		challenge := invoke(r, 409)
		if challenge.Confirmation == nil {
			t.Fatal("missing confirmation")
		}
		r.ConfirmationToken = challenge.Confirmation.Token
		result := invoke(r, 200)
		if len(result.Results) != 2 || result.Results[0].Text != tc.label || len(result.Results[1].Data) != 0 {
			t.Fatalf("result: %+v", result)
		}
		if replay := invoke(r, 200); !reflect.DeepEqual(result, replay) {
			t.Fatal("replay differs")
		}
		artifactOwner := tc.owner
		if tc.placement == "global" {
			artifactOwner = "local"
		}
		if body := hub.call(t, "GET", "/actions/artifact?ownerId="+artifactOwner+"&handle="+result.Results[1].Handle, "", 200); body != "hi" {
			t.Fatal(body)
		}
		if i == 1 {
			hub.call(t, "POST", "/org.example.owner/grants?ownerId=machine", `{"grants":[]}`, 200)
			invoke(r, 403)
			hub.call(t, "GET", "/actions/artifact?ownerId=machine&handle="+result.Results[1].Handle, "", 403)
		}
	}
	request := plugins.ActionRequest{PluginID: "org.example.hub", OwnerID: "machine", ActionID: "project", Placement: "project", OperationID: "forbidden", Surface: "command-palette", Context: plugins.ActionContext{OwnerID: "machine", ProjectID: "project"}}
	invoke(request, 403) // Remote hub-scope installation cannot execute for the hub.
	request.PluginID, request.OwnerID = "org.example.owner", "local"
	invoke(request, 403) // Local owner-scope installation cannot act for a remote project.
	request.OwnerID, request.Context.OwnerID = "machine", "wrong-machine"
	invoke(request, 403)
	disconnect()
	request.OwnerID, request.Context.OwnerID = "machine", "machine"
	invoke(request, 503)
	request.PluginID, request.OwnerID = "org.example.hub", "local"
	invoke(request, 503) // Explicit disconnected context must not silently run on hub.
	hub.call(t, "GET", "/actions?ownerId=machine&projectId=p&placement=project&surface=command-palette", "", 503)
	hub.call(t, "GET", "/actions/artifact?ownerId=machine&handle=anything", "", 503)
}

func TestRemotePluginClosedOperations(t *testing.T) {
	f := newPluginManagementTest(t)
	for _, req := range []remote.PluginRequest{{Operation: "stdio"}, {Operation: "health"}, {Operation: "enable", Read: true}, {Operation: "actions"}} {
		got := f.s.RemotePluginOperation(context.Background(), req)
		if got.Error == nil || got.Error.Category != plugins.ErrorInvalidArgument {
			t.Fatalf("%+v: %+v", req, got)
		}
	}
	request := plugins.ActionRequest{PluginID: f.description.ID, ActionID: "run", OperationID: "global", Placement: "global", Surface: "command-palette"}
	for _, operation := range []string{"actions", "invoke"} {
		got := f.s.RemotePluginOperation(t.Context(), remote.PluginRequest{Operation: operation, Action: request})
		if got.Error == nil || got.Error.Category != plugins.ErrorPermissionDenied {
			t.Fatal(got)
		}
	}
}

func TestPluginRemoteConfigurationBoundaryParity(t *testing.T) {
	hub, owner := newPluginManagementTest(t), newPluginManagementTest(t)
	for _, f := range []*pluginManagementTest{hub, owner} {
		if err := f.s.stateDB.DiscoverPlugin(t.Context(), f.description, filepath.Join(f.dir, "plugin"), strings.Repeat("a", 64), nil); err != nil {
			t.Fatal(err)
		}
	}
	connectPluginOwner(t, hub.s, owner.s)
	body := `{"secrets":{"token":"` + strings.Repeat("x", plugins.MaxMessageBytes-100) + `"}}`
	for _, source := range []string{"local", "machine"} {
		hub.call(t, "POST", "/"+hub.description.ID+"/configuration?ownerId="+source, body, 200)
		// Raw input fits but canonical escaping exceeds the shared limit.
		escaped := `{"secrets":{"token":"` + strings.Repeat("<", plugins.MaxMessageBytes/2) + `"}}`
		hub.call(t, "POST", "/"+hub.description.ID+"/configuration?ownerId="+source, escaped, 400)
	}
}

func TestPluginRemoteLargeCatalog(t *testing.T) {
	hub, owner := newPluginManagementTest(t), newPluginManagementTest(t)
	for i := range 6 {
		d := owner.description
		d.ID = fmt.Sprintf("org.example.catalog%d", i)
		d.Settings = append(d.Settings, plugins.Setting{Key: "data", Label: "Data", Type: "string"})
		if err := owner.s.stateDB.DiscoverPlugin(t.Context(), d, filepath.Join(owner.dir, d.ID), strings.Repeat("a", 64), nil); err != nil {
			t.Fatal(err)
		}
		value, _ := json.Marshal(strings.Repeat("x", 900<<10))
		if err := owner.s.stateDB.SetPluginConfiguration(t.Context(), d.ID, map[string]json.RawMessage{"data": value}, nil); err != nil {
			t.Fatal(err)
		}
	}
	connectPluginOwner(t, hub.s, owner.s)
	local := owner.call(t, "GET", "?ownerId=local", "", 200)
	projected := hub.call(t, "GET", "?ownerId=machine", "", 200)
	if len(projected) < 4<<20 || strings.ReplaceAll(projected, `"ownerId":"machine"`, `"ownerId":"local"`) != local {
		t.Fatal("large catalog differs between transports")
	}
	if got := pluginResponse(strings.Repeat("x", remote.MaxPluginResponseBytes), nil); got.Error == nil || got.Error.Category != plugins.ErrorUnavailable {
		t.Fatal("unbounded catalog response")
	}
}
