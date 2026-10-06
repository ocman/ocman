package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

// These tests exercise the external executable as an integration fixture;
// core pane declarations and behavior remain provider-independent.
func newBeadsPaneTest(t *testing.T) *pluginManagementTest {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", dir)
	binary := filepath.Join(dir, "ocman-plugin-beads")
	if out, err := exec.Command("go", "build", "-trimpath", "-o", binary, "../../examples/ocman-plugin-beads").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	bd := filepath.Join(dir, "bd")
	script := `#!/bin/sh
printf '%s\n' "$*" >> '` + filepath.Join(dir, "commands") + `'
case "$*" in
  'version --json') printf '%s\n' '{"version":"1.1.0"}';;
  '--readonly where --json') printf '%s\n' '{"schema_version":1,"data":{"path":".beads"}}';;
  *'--readonly list --json')
    if [ -f '` + filepath.Join(dir, "hang") + `' ]; then exec /bin/sleep 10; fi
    printf '%s\n' '[{"id":"bd-1","title":"Parent","status":"open","priority":1,"issue_type":"epic"},{"id":"bd-2","title":"Child","status":"blocked","priority":2}]';;
  *'--type parent-child --json') printf '%s\n' '[{"issue_id":"bd-2","depends_on_id":"bd-1","type":"parent-child"}]';;
  *) exit 1;;
esac
`
	if err := os.WriteFile(bd, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := &Server{stateDB: db, pluginCtx: context.Background(), auth: newTestAuth(t, "password")}
	t.Cleanup(s.stopPluginProcesses)
	mux, err := s.routes()
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.auth.issueCookie(w, httptest.NewRequest(http.MethodGet, "/", nil))
	f := &pluginManagementTest{s: s, mux: mux, cookie: w.Result().Cookies()[0], dir: dir, description: plugins.Description{ID: "org.ocman.beads"}}
	f.call(t, "POST", "/rescan", `{}`, 200)
	config, _ := json.Marshal(map[string]any{"values": map[string]string{"executable": bd}})
	f.call(t, "POST", "/org.ocman.beads/configuration", string(config), 200)
	p, err := s.stateDB.GetPlugin(t.Context(), f.description.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.description = p.Description
	f.call(t, "POST", "/org.ocman.beads/enable", f.approval(t), 200)
	return f
}

func paneURL(f *pluginManagementTest, owner string) string {
	return "/panes/read?" + url.Values{"ownerId": {owner}, "pluginId": {f.description.ID}, "paneId": {"tickets"}, "directory": {f.dir}}.Encode()
}

func TestPluginPaneBeadsAcceptance(t *testing.T) {
	f := newBeadsPaneTest(t)
	if _, err := os.Stat(filepath.Join(f.dir, "commands")); !os.IsNotExist(err) {
		t.Fatal("discovery/enable ran bd")
	}
	body := f.call(t, "GET", "/panes?ownerId=local", "", 200)
	if !strings.Contains(body, `"label":"Beads"`) {
		t.Fatal(body)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "commands")); !os.IsNotExist(err) {
		t.Fatal("pane catalog ran bd")
	}
	body = f.call(t, "GET", paneURL(f, "local"), "", 200)
	var tree plugins.PaneTree
	if json.Unmarshal([]byte(body), &tree) != nil || len(tree.Nodes) != 2 || tree.Nodes[1].ParentID != "bd-1" || tree.Nodes[0].Badge != "P1" {
		t.Fatal(body)
	}
	commands, _ := os.ReadFile(filepath.Join(f.dir, "commands"))
	if strings.Count(string(commands), "\n") != 4 {
		t.Fatalf("commands: %s", commands)
	}
	for _, path := range []string{"/panes?ownerId=gone", paneURL(f, "gone")} {
		f.call(t, "GET", path, "", 503)
	}
	f.call(t, "GET", "/panes", "", 400)
	f.call(t, "GET", "/panes/read?ownerId=local&pluginId=org.ocman.beads&paneId=tickets&directory=relative", "", 400)
	if got := f.request("GET", paneURL(f, "local"), "", "127.0.0.1:1234", "", false).Code; got != 401 {
		t.Fatalf("auth %d", got)
	}
	f.call(t, "POST", "/panes?ownerId=local", `{}`, 405)
	f.call(t, "POST", "/org.ocman.beads/grants", `{"grants":[]}`, 200)
	if got := f.call(t, "GET", "/panes?ownerId=local", "", 200); got != "[]\n" {
		t.Fatal(got)
	}
	f.call(t, "GET", paneURL(f, "local"), "", 403)
	f.call(t, "POST", "/org.ocman.beads/disable", `{}`, 200)
	f.call(t, "GET", paneURL(f, "local"), "", 503)
	after, _ := os.ReadFile(filepath.Join(f.dir, "commands"))
	if string(after) != string(commands) {
		t.Fatal("denied reads ran bd")
	}
}

func TestPluginPaneRemoteOwner(t *testing.T) {
	hub, owner := newBeadsPaneTest(t), newBeadsPaneTest(t)
	disconnect := connectPluginOwner(t, hub.s, owner.s)
	body := hub.call(t, "GET", "/panes?ownerId=machine", "", 200)
	if !strings.Contains(body, `"ownerId":"machine"`) {
		t.Fatal(body)
	}
	body = hub.call(t, "GET", paneURL(owner, "machine"), "", 200)
	if !strings.Contains(body, `"parentId":"bd-1"`) {
		t.Fatal(body)
	}
	if _, err := os.Stat(filepath.Join(hub.dir, "commands")); !os.IsNotExist(err) {
		t.Fatal("hub ran bd for remote pane")
	}
	fake := hub.s.routePluginOperation(t.Context(), "machine", remote.PluginRequest{Operation: "pane-read", Pane: plugins.PaneRequest{OwnerID: "local", PluginID: owner.description.ID, PaneID: "tickets", Directory: owner.dir}})
	if fake.Error == nil || fake.Error.Category != plugins.ErrorPermissionDenied {
		t.Fatalf("owner mismatch: %+v", fake)
	}
	disconnect()
	hub.call(t, "GET", paneURL(owner, "machine"), "", 503)
}

func TestPluginPaneReadCancellation(t *testing.T) {
	f := newBeadsPaneTest(t)
	if err := os.WriteFile(filepath.Join(f.dir, "hang"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan remote.PluginResponse, 1)
	go func() {
		done <- f.s.routePluginOperation(ctx, "local", remote.PluginRequest{Operation: "pane-read", Pane: plugins.PaneRequest{PluginID: f.description.ID, PaneID: "tickets", OwnerID: "local", Directory: f.dir}})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		commands, _ := os.ReadFile(filepath.Join(f.dir, "commands"))
		if strings.Contains(string(commands), "list --json") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("read did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case reply := <-done:
		if reply.Error == nil || reply.Error.Category != plugins.ErrorCancelled {
			t.Fatalf("%+v", reply)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled pane kept waiting")
	}
}
