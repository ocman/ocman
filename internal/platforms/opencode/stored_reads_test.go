package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/ocv2"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestStoredPanelsAndCataloguesDoNotNeedNativeDatabase(t *testing.T) {
	a := New(nil, nil, nil, ocapi.Auth{})
	a.rt = NewDiscovery()
	a.turns.disc = a.rt
	detail := &platforms.SessionDetail{Session: &db.Session{ID: "s", Directory: "/repo"}, DefaultModel: "p/model", Messages: []db.Message{{ID: "a", Data: json.RawMessage(`{"role":"assistant","providerID":"p","modelID":"model","cost":0.5,"tokens":{"input":7,"output":3,"cache":{"read":2,"write":1}}}`)}}, Parts: []db.Part{editPart(t, "edit", 20, "/repo/file", "before", "after", 2, 1), {ID: "todo", TimeCreated: 30, Data: json.RawMessage(`{"type":"tool","tool":"todowrite","state":{"input":{"todos":[{"content":"stored todo","status":"pending"}]}}}`)}}}
	changes, err := a.ChangesFromStored(t.Context(), detail)
	if err != nil || !changes.Supported || changes.FilesChanged != 1 || changes.TotalAdditions != 2 || changes.Files[0].Edits[0].PartID != "edit" {
		t.Fatalf("stored changes: %+v %v", changes, err)
	}
	info, err := a.InfoFromStored(t.Context(), detail)
	if err != nil || info.Supported || info.Tokens.Input != 7 || info.Tokens.CacheRead != 2 || info.Context.Cost != 0.5 || len(info.Todos) != 1 || info.Todos[0].Content != "stored todo" {
		t.Fatalf("stored info: %+v %v", info, err)
	}
	catalog, err := a.DirectoryCatalogFromStored(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/repo", Port: "must-not-connect"}, []db.RecentModel{{Provider: "p", Model: "model"}}, db.SessionDefaults{Agent: "agent", Model: "p/model"}, false)
	if err != nil || catalog.LiveConnection || catalog.DefaultAgent != "agent" || len(catalog.Models.Models) != 1 || catalog.Models.Models[0].Model != "model" {
		t.Fatalf("stored catalogue: %+v %v", catalog, err)
	}
	if a.StoredReadConfig("") == nil || !a.StoredReadConfig("").DirectoryDefaults {
		t.Fatal("stored read configuration lost")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.ChangesFromStored(ctx, detail); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := a.InfoFromStored(ctx, detail); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := a.InfoFromStored(t.Context(), nil); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := a.ChangesFromStored(t.Context(), nil); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestStoredPanelsEnrichOnlyLiveConfiguration(t *testing.T) {
	defer ocv2.SetInstalledV2(false)()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mcp":
			_, _ = w.Write([]byte(`{"mcp":{"status":"connected"}}`))
		case "/lsp":
			_, _ = w.Write([]byte(`[{"id":"gopls","name":"gopls","status":"connected"}]`))
		case "/provider":
			_, _ = w.Write([]byte(`{"all":[{"id":"p","models":{"model":{"id":"model","limit":{"context":1000}}}}],"connected":["p"]}`))
		case "/agent":
			_, _ = w.Write([]byte(`[{"name":"build","mode":"primary"}]`))
		case "/command":
			_, _ = w.Write([]byte(`[{"name":"test"}]`))
		default:
			t.Errorf("stored panel fetched historical native endpoint: %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	_, port, _ := net.SplitHostPort(server.Listener.Addr().String())
	a := New(nil, nil, nil, ocapi.Auth{})
	a.rt = NewDiscovery()
	a.turns.disc = a.rt
	a.rt.SetManagedPortSource(func(context.Context) map[string]string { return map[string]string{"/stored-live-config": port} })
	a.rt.rememberSessionPort("stored", port)
	info, err := a.InfoFromStored(t.Context(), &platforms.SessionDetail{Session: &db.Session{ID: "stored", Directory: "/stored-live-config", LiveConnection: true}, DefaultModel: "p/model"})
	if err != nil || !info.Supported || info.Context.Limit != 1000 || len(info.MCPServers) != 1 || len(info.LSPServers) != 1 {
		t.Fatalf("live config on stored info: %+v %v", info, err)
	}
	catalog, err := a.DirectoryCatalogFromStored(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/stored-live-config", Port: port}, nil, db.SessionDefaults{}, true)
	if err != nil || !catalog.LiveConnection || len(catalog.Agents) != 1 || len(catalog.Commands) != 1 {
		t.Fatalf("live config on stored catalogue: %+v %v", catalog, err)
	}
}

func TestStoredHaltsUseCanonicalReceiptsAndStoredNativeHierarchy(t *testing.T) {
	core, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	a := New(nil, nil, nil, ocapi.Auth{})
	a.rt = NewDiscovery()
	a.turns.disc = a.rt
	a.prompts.haltStore = core
	a.SetSessionHaltIdentity(func(_ context.Context, id string) (string, error) { return "canonical-" + id, nil }, func(_ context.Context, id string) (string, error) {
		if id == "canonical-child" {
			return "child", nil
		}
		return "", nil
	})
	a.ObservePromptAsked("7777", "/repo", "permission", platforms.LivePrompt{"sessionID": "child", "id": "permission"})
	halts, err := core.SessionHalts(t.Context())
	if err != nil || halts["canonical-child"] == 0 || halts["child"] != 0 {
		t.Fatalf("unscoped halt persisted: %v %v", halts, err)
	}
	rows := []db.Session{{ID: "root"}}
	if err := a.enrichStoredHalts(t.Context(), rows, map[string]string{"child": "root"}); err != nil || rows[0].LastHaltAt == 0 {
		t.Fatalf("stored halt not bubbled: %+v %v", rows, err)
	}
	a.SetSessionHaltIdentity(nil, func(context.Context, string) (string, error) { return "", errors.New("scope unavailable") })
	if err := a.enrichStoredHalts(t.Context(), rows, nil); err == nil {
		t.Fatal("halt identity failure ignored")
	}
	a.SetSessionHaltIdentity(func(context.Context, string) (string, error) { return "", errors.New("identity unavailable") }, nil)
	a.ObservePromptAsked("7777", "/repo", "permission", platforms.LivePrompt{"sessionID": "other", "id": "other-request"})
	if halts, err := core.SessionHalts(t.Context()); err != nil || halts["other"] != 0 {
		t.Fatalf("failed identity persisted a halt: %v %v", halts, err)
	}
}

func TestStoredSessionOverlayUsesOnlySSEObservationsAndIngestedParents(t *testing.T) {
	defer ocv2.SetInstalledV2(false)()
	a := New(nil, nil, nil, ocapi.Auth{})
	a.rt = NewDiscovery()
	a.turns.disc = a.rt
	a.rt.SetManagedPortSource(func(context.Context) map[string]string { return map[string]string{"/repo": "7777"} })
	a.ObserveSessionStatus("7777", 0, "root", "busy")
	a.ObservePromptAsked("7777", "/repo", "permission", platforms.LivePrompt{"sessionID": "child", "id": "permission"})
	rows := []db.Session{{ID: "root", Directory: "/repo", Title: "stored", Status: db.StatusDone}}
	if err := a.EnrichStoredSessions(t.Context(), rows, map[string]string{"child": "root"}); err != nil {
		t.Fatal(err)
	}
	if !rows[0].PendingPermission || !rows[0].LiveConnection || rows[0].Title != "stored" || rows[0].Status != db.StatusBusy {
		t.Fatalf("SSE overlay lost: %+v", rows[0])
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.EnrichStoredSessions(ctx, rows, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
