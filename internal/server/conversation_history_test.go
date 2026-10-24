package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type historyTestPlatform struct {
	*fakePlatform
	mu   sync.Mutex
	text string
}

func (*historyTestPlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "test", SourceKey: "source", Name: "test"}, nil
}
func (*historyTestPlatform) HistoryInventory(context.Context) ([]db.HistoryEntry, error) {
	return []db.HistoryEntry{{ID: "session", Version: "constant"}}, nil
}
func (p *historyTestPlatform) HistorySnapshot(context.Context, string) (*db.HistorySnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	raw, err := json.Marshal(map[string]any{"type": "text", "text": p.text})
	if err != nil {
		return nil, err
	}
	return &db.HistorySnapshot{Session: db.Session{ID: "session"}, Version: "constant",
		Messages: []db.Message{{ID: "message", SessionID: "session", Data: json.RawMessage(`{"role":"assistant"}`)}},
		Parts:    []db.Part{{ID: "part", MessageID: "message", SessionID: "session", Data: raw}}}, nil
}

func TestConversationHistoryModuleWiringAndInvalidation(t *testing.T) {
	store, err := statev2.Open(filepath.Join(t.TempDir(), "statev2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p := &historyTestPlatform{fakePlatform: &fakePlatform{id: "test"}, text: "initial"}
	p.sessionDetailFn = func(string) (*platforms.SessionDetail, error) {
		t.Error("shadow importer called UI session reader")
		return nil, nil
	}
	registry := platforms.NewRegistry()
	registry.Register(p)
	registry.Register(&fakePlatform{id: "non-history"})
	s := New(nil, nil, "127.0.0.1:0", registry, nil).WithConversationHistory(store, "owner")
	if _, ready := s.historyAnalytics.Instances(); ready {
		t.Fatal("enabled reads with a platform missing a history adapter")
	}
	registered := false
	for _, m := range s.modules() {
		if m == s.historyIngest {
			registered = true
		}
	}
	if !registered {
		t.Fatal("history worker missing from server module registry")
	}
	if err := s.historyIngest.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.historyIngest.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	waitText := func(want string) {
		t.Helper()
		for deadline := time.Now().Add(4 * time.Second); ; {
			sessions, err := store.MetadataPage(t.Context(), "owner", statev2.Key{}, 500)
			if err == nil && len(sessions) > 0 {
				detail, err := store.ReadConversation(t.Context(), sessions[len(sessions)-1].Session.Key, 10, 0)
				if err == nil && len(detail.Parts) == 1 && detail.Parts[0].Text == want {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("history never reached %q", want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitText("initial")
	p.mu.Lock()
	p.text = "updated"
	p.mu.Unlock()
	s.broadcastGlobalEvent("ocman.session.changed", []byte(`{"platform":"test","sessionID":"session"}`))
	waitText("updated")
	s.observeHistoryChange("other", nil)
	s.observeHistoryChange("ocman.session.changed", []byte(`{`))
	(&Server{}).observeHistoryChange("ocman.session.changed", nil)
}

func TestConversationHistoryDoesNotRegisterNativeAnalyticsParity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.db")
	rw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Exec(`CREATE TABLE session (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	_ = rw.Close()
	source, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	store, err := statev2.Open(filepath.Join(t.TempDir(), "statev2.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	s := New(source, nil, "127.0.0.1:0", platforms.NewRegistry(), nil).WithConversationHistory(store, "owner")
	if s.historyAnalytics.Store != store {
		t.Fatal("mandatory readers were not connected to ingestion")
	}
	for _, m := range s.modules() {
		if m.Name() == "conversation-parity" {
			t.Fatal("native parity reader registered in application modules")
		}
	}
}

func TestConversationHistoryWithoutStoreIsDisabled(t *testing.T) {
	registry := platforms.NewRegistry()
	registry.Register(&historyTestPlatform{fakePlatform: &fakePlatform{id: "test"}})
	s := New(nil, nil, "127.0.0.1:0", registry, nil).WithConversationHistory(nil, "owner")
	if s.historyAnalytics != nil {
		t.Fatal("missing history unexpectedly configured analytics")
	}
	if s.historyIngest != nil {
		t.Fatal("ingestion enabled without a history store")
	}
}
