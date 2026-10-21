package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestHistoryCatalogDefaultsStayOnReservedSnapshot(t *testing.T) {
	path, store := historyPerformanceStore(t)
	key := statev2.Key{InstanceID: "one", SessionID: "catalog"}
	_, err := store.Commit(t.Context(), statev2.Batch{
		Session: statev2.Session{Key: key, Directory: "/project", UpdatedAt: 20},
		Messages: []statev2.Message{{ID: "model-message", Role: "assistant", CreatedAt: 20,
			Agent: "build", Provider: "provider", Model: "old", Extensions: json.RawMessage(`{"agent":"build","providerID":"provider","modelID":"old"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := OpenHistoryAnalytics(path, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	base.db.SetMaxOpenConns(1)
	reader, release, err := base.ScopedHistoryReader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = store.Commit(t.Context(), statev2.Batch{
		Session: statev2.Session{Key: key, Directory: "/project", UpdatedAt: 30}, ExpectedRevision: 1,
		Messages: []statev2.Message{{ID: "new-message", Role: "assistant", CreatedAt: 30,
			Agent: "new-agent", Provider: "provider", Model: "new", Extensions: json.RawMessage(`{"agent":"new-agent","providerID":"provider","modelID":"new"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	defaults, err := reader.GetSessionDefaults(ctx, "", "/project")
	if err != nil || defaults.Agent != "build" || defaults.Model != "provider/old" {
		t.Fatalf("draft defaults left the reserved snapshot or blocked: %+v %v", defaults, err)
	}
}

func TestHistoryCatalogDoesNotReadMessagePayloads(t *testing.T) {
	path, store := historyPerformanceStore(t)
	_, err := store.Commit(t.Context(), statev2.Batch{
		Session: statev2.Session{Key: statev2.Key{InstanceID: "one", SessionID: "catalog"}, Directory: "/project", UpdatedAt: 20},
		Messages: []statev2.Message{{ID: "model-message", Role: "assistant", CreatedAt: 20,
			Agent: "build", Provider: "provider", Model: "model", Extensions: json.RawMessage(`{"agent":"build","providerID":"provider","modelID":"model"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := OpenHistoryAnalytics(path, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	base.db.SetMaxOpenConns(1)
	// Fail deterministically if catalog preferences depend on retained JSON.
	if _, err := base.db.Exec(`DROP VIEW message; CREATE TEMP VIEW message AS SELECT instance_id,id,session_id,time_created,settled,'not-json' AS data FROM main.analytics_message`); err != nil {
		t.Fatal(err)
	}
	reader, release, err := base.ScopedHistoryReader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	recents, err := reader.GetRecentModels(t.Context(), 50, 10)
	if err != nil || len(recents) != 1 || recents[0].Model != "model" {
		t.Fatalf("draft recents read message payloads: %+v %v", recents, err)
	}
	defaults, err := reader.GetSessionDefaults(t.Context(), "", "/project")
	if err != nil || defaults.Agent != "build" || defaults.Model != "provider/model" {
		t.Fatalf("draft defaults read message payloads: %+v %v", defaults, err)
	}
}

func TestHistoryCatalogDefaultsKeepDirectoryAndGlobalFallbacks(t *testing.T) {
	path, store := historyPerformanceStore(t)
	for _, row := range []struct {
		id, directory, title, agent, provider, model string
		at                                           int64
	}{
		{"local", "/project", "local", "build", "provider", "local", 10},
		{"global", "/other", "global", "plan", "", "global", 20},
		{"child", "/project", "(helper subagent)", "helper", "provider", "child", 30},
		{"empty", "/project", "no preferences", "", "", "", 40},
	} {
		metadata, err := json.Marshal(map[string]string{"agent": row.agent, "providerID": row.provider, "modelID": row.model})
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.Commit(t.Context(), statev2.Batch{
			Session:  statev2.Session{Key: statev2.Key{InstanceID: "one", SessionID: row.id}, Directory: row.directory, Title: row.title, UpdatedAt: row.at},
			Messages: []statev2.Message{{ID: "message-" + row.id, Role: "assistant", CreatedAt: row.at, Extensions: metadata}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	base, err := OpenHistoryAnalytics(path, []string{"one"})
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	reader, release, err := base.ScopedHistoryReader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for _, row := range []struct{ exclude, directory, agent, model string }{
		{"", "/project", "build", "provider/local"},
		{"local", "/project", "plan", "global"},
		{"", "/new", "plan", "global"},
		{"global", "/new", "build", "provider/local"},
	} {
		got, err := reader.GetSessionDefaults(t.Context(), row.exclude, row.directory)
		if err != nil || got.Agent != row.agent || got.Model != row.model {
			t.Fatalf("defaults for %+v: %+v %v", row, got, err)
		}
	}
	recents, err := reader.GetRecentModels(t.Context(), 0, 0)
	if err != nil || len(recents) != 3 {
		t.Fatalf("recents: %+v %v", recents, err)
	}
	recents, err = reader.GetRecentModels(t.Context(), 1, 1)
	if err != nil || len(recents) != 0 {
		t.Fatalf("sampling should not reach older sessions: %+v %v", recents, err)
	}
}
