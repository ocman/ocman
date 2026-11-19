package pluginsmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/testutil"
	"github.com/ocman/ocman/sdk/plugin"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func TestNavBadgeProcessHelper(t *testing.T) {
	if os.Getenv("NAV_BADGE_PROCESS_HELPER") != "1" {
		return
	}
	d := pagesTestDescription()
	d.Capabilities = append(d.Capabilities, plugin.NavBadgeCapability)
	d.RequestedGrants = append(d.RequestedGrants, plugin.NavBadgeGrant)
	mode := plugin.Mode(os.Args[len(os.Args)-1])
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan plugin.Event)
	if mode == plugin.ModeServe {
		go func() {
			tick := time.NewTicker(5 * time.Millisecond)
			defer tick.Stop()
			for range tick.C {
				data, err := os.ReadFile("badge-next")
				if err != nil {
					continue
				}
				_ = os.Remove("badge-next")
				if string(data) == "exit" {
					os.Exit(1)
				}
				// Send a raw frame to exercise the host's decoder independently
				// of the SDK's validated constructor.
				events <- plugin.Event{Capability: plugin.NavBadgeCapability.Name, Name: plugins.NavBadgeSetEvent, Data: data}
			}
		}()
	}
	if plugin.RunWithEvents(ctx, mode, os.Getenv("OCMAN_PLUGIN_TOKEN"), d, os.Stdin, os.Stdout, nil, events) != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestPluginNavBadgesFromBackendLifecycle(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", root)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	testutil.WriteScript(t, filepath.Join(root, "ocman-plugin-badges"), "#!/bin/sh\nexport NAV_BADGE_PROCESS_HELPER=1\nexec '"+strings.ReplaceAll(executable, "'", "'\\''")+"' -test.run='^TestNavBadgeProcessHelper$' -- \"$@\"\n")
	db := statetest.Open(t, Schema)
	m := bound(NewModule(Deps{Store: db}), ctx)
	t.Cleanup(m.Stop)
	if _, err := m.RescanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	id := pagesTestDescription().ID
	grants := []string{plugins.PageProjectGrant, plugins.NavBadgeGrant}
	if err := db.SetPluginEnabled(ctx, id, true, grants); err != nil {
		t.Fatal(err)
	}
	if err := m.SyncPluginProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	dataDir, err := db.PluginDataDir(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	send := func(data string) {
		t.Helper()
		tmp := filepath.Join(dataDir, "badge-tmp")
		if err := os.WriteFile(tmp, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, filepath.Join(dataDir, "badge-next")); err != nil {
			t.Fatal(err)
		}
	}
	read := func() map[string]plugins.NavBadge {
		t.Helper()
		w := serve(m.handlePluginNavBadges, "GET", "/api/plugins/nav-badges?ownerId=local", "", nil)
		var badges map[string]plugins.NavBadge
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(w.Body.Bytes(), &badges) != nil {
			t.Fatalf("badge response: %d %s", w.Code, w.Body)
		}
		return badges
	}
	wantBadge := func(want plugins.NavBadge) {
		t.Helper()
		waitForBadgeCondition(t, func() bool {
			badges := read()
			if want.Kind == "" {
				return len(badges) == 0
			}
			return len(badges) == 1 && badges[id] == want
		})
	}
	if w := serve(m.handlePluginPages, "GET", "/api/plugins/pages?ownerId=local", "", nil); !strings.Contains(w.Body.String(), `"navBadge":true`) {
		t.Fatalf("missing badge opt-in in page list: %s", w.Body)
	}
	wantBadge(plugins.NavBadge{})
	// No plugin page is mounted or called. The serve process alone emits updates.
	send(`{"kind":"dot"}`)
	wantBadge(plugins.NavBadge{Kind: plugins.NavBadgeDot})
	send(`{"kind":"count","count":12}`)
	wantBadge(plugins.NavBadge{Kind: plugins.NavBadgeCount, Count: 12})
	send(`{"kind":"clear"}`)
	wantBadge(plugins.NavBadge{})
	send(`{"kind":"dot"}`)
	wantBadge(plugins.NavBadge{Kind: plugins.NavBadgeDot})
	send("exit")
	proc := m.Process(id)
	waitForBadgeCondition(t, func() bool {
		h := proc.Health()
		return h.Status == "ready" && h.RestartCount == 1
	})
	wantBadge(plugins.NavBadge{})
	send(`{"kind":"dot"}`)
	wantBadge(plugins.NavBadge{Kind: plugins.NavBadgeDot})
	if err := db.SetPluginGrants(ctx, id, []string{plugins.PageProjectGrant}); err != nil {
		t.Fatal(err)
	}
	// A snapshot must respect current grants even before process reconciliation.
	wantBadge(plugins.NavBadge{})
	if err := db.SetPluginGrants(ctx, id, grants); err != nil {
		t.Fatal(err)
	}
	if err := db.SetPluginEnabled(ctx, id, false, nil); err != nil {
		t.Fatal(err)
	}
	wantBadge(plugins.NavBadge{})
	if err := m.SyncPluginProcesses(ctx); err != nil || m.Process(id) != nil {
		t.Fatalf("process survived disable: %v", err)
	}
}

func waitForBadgeCondition(t *testing.T, condition func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if condition() {
			return
		}
	}
	t.Fatal("timed out waiting for badge state")
}

type navBadgeRemotes struct{ Remotes }

func (navBadgeRemotes) PluginOperation(_ context.Context, owner string, req remote.PluginRequest) (remote.PluginResponse, error) {
	if owner != "owner-2" {
		return remote.PluginResponse{}, platforms.ErrRemoteOffline
	}
	if req.Operation != "nav-badges" || !req.Read {
		return remote.PluginResponse{}, errors.New("unexpected operation")
	}
	return pluginResponse(map[string]plugins.NavBadge{pagesTestDescription().ID: {Kind: plugins.NavBadgeCount, Count: 2}}, nil), nil
}

func TestPluginNavBadgeOwnerRouting(t *testing.T) {
	m := NewModule(Deps{Store: statetest.Open(t, Schema), Remotes: func() Remotes { return navBadgeRemotes{} }})
	for _, tt := range []struct {
		query string
		code  int
		body  string
	}{
		{"", http.StatusBadRequest, ""},
		{"?ownerId=local", http.StatusOK, `{}`},
		{"?ownerId=owner-2", http.StatusOK, `{"org.example.pages":{"kind":"count","count":2}}`},
		{"?ownerId=offline", http.StatusServiceUnavailable, ""},
	} {
		w := serve(m.handlePluginNavBadges, "GET", "/api/plugins/nav-badges"+tt.query, "", nil)
		if w.Code != tt.code || (tt.body != "" && strings.TrimSpace(w.Body.String()) != tt.body) {
			t.Errorf("%s = %d %s", tt.query, w.Code, w.Body)
		}
	}
	if r := m.RemoteOperation(t.Context(), remote.PluginRequest{Operation: "nav-badges"}); r.Error == nil || r.Error.Category != plugins.ErrorInvalidArgument {
		t.Fatalf("accepted non-read RPC: %+v", r)
	}
}
