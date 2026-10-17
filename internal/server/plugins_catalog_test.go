package server

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
)

// writeLaunchCatalogPlugin writes a serve plugin that dumps its fd-3 payload
// to configuration.json in the data directory before announcing readiness.
func writeLaunchCatalogPlugin(t *testing.T, dir, name, id string, wants bool) {
	t.Helper()
	d := plugins.Description{ID: id, Name: "Catalog", Version: "1", Protocol: plugins.Version{Major: 1}, Scope: plugins.ScopeOwner, MaxConcurrency: 1, WantsCatalog: wants,
		Settings: []plugins.Setting{{Key: "project", Label: "Project", Type: "string", Required: true}, {Key: "notes", Label: "Notes", Type: "string"}}}
	hello, err := json.Marshal(plugins.Envelope{Type: plugins.TypeHello, Hello: &plugins.Hello{Mode: plugins.ModeDescribe, Token: "$OCMAN_PLUGIN_TOKEN", Description: &d}})
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nIFS= read -r hello <<EOF\n" + string(hello) + "\nEOF\n" +
		"if [ \"$1\" = serve ]; then\n  cat <&3 > configuration.json\nfi\n" +
		"printf '%s\\n' \"$hello\"\n" +
		"if [ \"$1\" = serve ]; then\n  read -r ack\n  echo started >> starts\n  read -r shutdown\nfi\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.ReplaceAll(script, `"mode":"describe"`, `"mode":"$1"`)), 0700); err != nil {
		t.Fatal(err)
	}
}

func TestPluginLaunchCatalog(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", dir)
	db, err := state.Open(testStatePath(t), ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := bindPlugins(&Server{stateDB: db}, ctx)
	t.Cleanup(s.stopPluginProcesses)

	live := &platforms.DirectoryCatalog{
		Agents:       []platforms.AgentCatalogEntry{{Name: "build", Mode: "primary", BuiltIn: true}, {Name: "review", Mode: "subagent", Kind: "subagent"}},
		Models:       &platforms.SessionModelsResponse{HasProviders: true, Models: []platforms.SessionModel{{Provider: "acme", Model: "quill", IsAvailable: true}}},
		DefaultAgent: "build", DefaultModel: "acme/quill", LiveConnection: true,
	}
	var asked []platforms.DirectoryCatalogRequest
	registry := platforms.NewRegistry()
	registry.Register(&fakePlatform{id: "opencode", directoryCatalogFn: func(req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
		asked = append(asked, req)
		switch req.Directory {
		case "/unreachable":
			return nil, errors.New("instance unreachable")
		case "/history":
			return &platforms.DirectoryCatalog{LiveConnection: false}, nil
		default:
			return live, nil
		}
	}})
	s.registry = registry

	for _, id := range []string{"org.example.live", "org.example.noproject", "org.example.optedout", "org.example.unreachable", "org.example.history", "org.example.nearlimit"} {
		writeLaunchCatalogPlugin(t, dir, "ocman-plugin-"+id, id, id != "org.example.optedout")
	}
	if _, err := s.RescanPlugins(ctx); err != nil {
		t.Fatal(err)
	}
	projects := map[string]string{
		"org.example.live":        "/srv/repo",
		"org.example.noproject":   "",
		"org.example.optedout":    "/srv/repo",
		"org.example.unreachable": "/unreachable",
		"org.example.history":     "/history",
		"org.example.nearlimit":   "/srv/repo",
	}
	// Settings alone fit the message limit, settings plus catalog do not.
	notes := json.RawMessage(`"` + strings.Repeat("n", plugins.MaxMessageBytes-100) + `"`)
	for id, project := range projects {
		var values map[string]json.RawMessage
		if project != "" {
			values = map[string]json.RawMessage{"project": json.RawMessage(`"` + project + `"`)}
		}
		if id == "org.example.nearlimit" {
			values["notes"] = notes
		}
		if err := db.SetPluginConfiguration(ctx, id, values, nil); err != nil {
			t.Fatal(err)
		}
		if err := db.SetPluginEnabled(ctx, id, true, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SyncPluginProcesses(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(15 * time.Second)
	for _, id := range []string{"org.example.live", "org.example.noproject", "org.example.optedout", "org.example.unreachable", "org.example.history", "org.example.nearlimit"} {
		for {
			p, err := db.GetPlugin(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if p.Health.Status == "ready" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s not ready: %+v", id, p.Health)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	read := func(id string) map[string]json.RawMessage {
		t.Helper()
		dataDir, err := db.PluginDataDir(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(dataDir, "configuration.json"))
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]json.RawMessage
		if json.Unmarshal(raw, &payload) != nil {
			t.Fatalf("payload: %s", raw)
		}
		return payload
	}

	livePayload := read("org.example.live")
	if string(livePayload["project"]) != `"/srv/repo"` {
		t.Fatalf("settings: %v", livePayload)
	}
	var catalog plugins.LaunchCatalog
	if len(livePayload) != 2 || json.Unmarshal(livePayload["catalog"], &catalog) != nil {
		t.Fatalf("want project+catalog only: %v", livePayload)
	}
	if len(catalog.Agents) != 2 || catalog.Agents[0].Name != "build" || !catalog.Agents[0].BuiltIn || catalog.Agents[1].Name != "review" {
		t.Fatalf("agents: %+v", catalog.Agents)
	}
	if catalog.Models == nil || !catalog.Models.HasProviders || len(catalog.Models.Models) != 1 || catalog.Models.Models[0].Provider != "acme" || !catalog.Models.Models[0].IsAvailable {
		t.Fatalf("models: %+v", catalog.Models)
	}
	if catalog.DefaultAgent != "build" || catalog.DefaultModel != "acme/quill" {
		t.Fatalf("defaults: %+v", catalog)
	}

	// Opted-in without a resolved project, opted-out, an unreachable instance
	// and a historical (not live) catalog all receive no catalog section, and
	// none of them fails startup: the ready wait above already proved it.
	for _, id := range []string{"org.example.noproject", "org.example.optedout", "org.example.unreachable", "org.example.history", "org.example.nearlimit"} {
		payload := read(id)
		if _, exists := payload["catalog"]; exists {
			t.Fatalf("%s received a catalog: %v", id, payload)
		}
	}
	if near := read("org.example.nearlimit"); string(near["notes"]) != string(notes) {
		t.Fatal("near-limit settings not delivered")
	}
	want := map[string]bool{"/srv/repo": true, "/unreachable": true, "/history": true}
	if len(asked) != 4 {
		t.Fatalf("catalog reads: %+v", asked)
	}
	for _, req := range asked {
		if !want[req.Directory] {
			t.Fatalf("unexpected catalog read: %+v", req)
		}
	}
}
