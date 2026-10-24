package claudecode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/conversationingest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/statev2"
	"github.com/NoUseFreak/ocman/internal/statev2/statev2test"
)

func TestSubagentRuntimeHistoryCanonicalInspection(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Join(config, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var history *claudeprovider.HistoryReader
	opts := claudeprovider.DefaultOptions(root)
	opts.ConfigDir, opts.CLI = config, claudetest.Binary(t)
	opts.HistoryReady = func(h *claudeprovider.HistoryReader) { history = h }
	if _, err := claudeprovider.NewSupervisor(ctx, opts, filepath.Join(root, "spool")); err != nil {
		t.Fatal(err)
	}
	var completed atomic.Bool
	var sequence atomic.Uint64
	changed := make(chan struct{}, 1)
	rows := func() []db.Session {
		childStatus := db.StatusBusy
		if completed.Load() {
			childStatus = db.StatusDone
		}
		return []db.Session{
			{ID: "root", Directory: "/repo", Title: "Parent", Status: db.StatusBusy, TimeCreated: 1},
			{ID: "agent-child", ParentID: "root", Directory: "/repo", Title: "Child", Status: childStatus, TimeCreated: 2},
		}
	}
	adapter, _ := reloadTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			for {
				for _, row := range rows() {
					_, _ = fmt.Fprintf(w, "id: %d\ndata: {\"type\":\"session.status\",\"properties\":{\"sessionID\":%q,\"status\":{\"type\":%q}}}\n\n", sequence.Add(1), row.ID, row.Status)
				}
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
					return
				case <-changed:
				}
			}
		case "/sessions":
			_ = json.NewEncoder(w).Encode(rows())
		case "/sessions/root", "/sessions/agent-child":
			s := rows()[0]
			if r.URL.Path == "/sessions/agent-child" {
				s = rows()[1]
			}
			_ = json.NewEncoder(w).Encode(platforms.SessionDetail{Session: &s,
				Messages: []db.Message{{ID: s.ID + "-message", SessionID: s.ID, TimeCreated: 3, Data: json.RawMessage(`{"role":"assistant","time":{"created":3}}`)}},
				Parts:    []db.Part{{ID: s.ID + "-part", MessageID: s.ID + "-message", SessionID: s.ID, Data: json.RawMessage(`{"type":"text","text":"Working"}`)}}})
		default:
			http.NotFound(w, r)
		}
	}, platforms.Capabilities{Events: true})
	browser, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode([]db.Session{}) })
	p := &claudePlatform{Adapter: browser, history: history, configDir: config, projects: &claudeProjects{
		roots: map[string]*claudeProject{"/repo": {watched: watched{adapter: adapter}}},
	}}
	watchDone := make(chan struct{})
	go func() { defer close(watchDone); _ = adapter.WatchEvents(ctx) }()
	defer func() { cancel(); <-watchDone }()
	eventually(t, "child busy observation", func() bool { live := adapter.LiveStatus("agent-child"); return live != nil && live.Status == "busy" })
	metadata, err := p.SessionIdentityMetadata(t.Context(), "agent-child")
	if err != nil || metadata.ParentID != "root" || metadata.LiveConnection {
		t.Fatalf("pre-transcript identity: %+v %v", metadata, err)
	}
	core, err := state.Open(statetest.Path(t, sessioncatalog.Schema), sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	catalog, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	store := statev2test.Open(t)
	a, err := owned.New(t.Context(), p, catalog, store, "owner")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := a.HistoryIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	instance := statev2.Instance{ID: statev2.SourceInstanceID("owner", identity.Platform, identity.SourceKey), OwnerID: "owner", Platform: identity.Platform, SourceKey: identity.SourceKey}
	if err := store.RegisterInstance(t.Context(), instance); err != nil {
		t.Fatal(err)
	}
	entries, err := a.HistoryInventory(t.Context())
	if err != nil || len(entries) != 2 {
		t.Fatalf("runtime inventory: %+v %v", entries, err)
	}
	var child, parent string
	for _, entry := range entries {
		snapshot, err := a.HistorySnapshot(t.Context(), entry.ID)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Subagent {
			child, parent = entry.ID, snapshot.Session.ParentID
		}
		key := statev2.Key{InstanceID: instance.ID, SessionID: entry.ID}
		batch, _, err := conversationingest.Normalize(key, snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Commit(t.Context(), batch); err != nil {
			t.Fatal(err)
		}
	}
	if !sessioncatalog.IsID(child) || !sessioncatalog.IsID(parent) || child == parent {
		t.Fatalf("canonical child=%q parent=%q", child, parent)
	}
	for _, done := range []bool{false, true} {
		completed.Store(done)
		changed <- struct{}{}
		want := db.StatusBusy
		if done {
			want = db.StatusDone
		}
		eventually(t, "child lifecycle observation", func() bool {
			live := adapter.LiveStatus("agent-child")
			return live != nil && live.Status == string(want)
		})
		detail, err := a.Session(t.Context(), child, 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Session.Status != want || detail.Session.ParentID != parent || len(detail.Messages) != 1 || len(detail.Parts) != 1 || detail.Messages[0].SessionID != child {
			t.Fatalf("canonical child inspection: %+v", detail)
		}
	}
	p.projects.mu.Lock()
	p.projects.sessions = nil // reattached supervisors after a dashboard restart
	p.projects.mu.Unlock()
	if live := p.CachedSessionObservations()["agent-child"]; live.Status != "done" {
		t.Fatalf("completed child lost on reconnect: %+v", live)
	}
	// The eventual transcript replaces the runtime bootstrap under the same
	// canonical identity, without duplicating the streamed message.
	project := filepath.Join(config, "projects", "repo")
	subagents := filepath.Join(project, "root", "subagents")
	if err := os.MkdirAll(subagents, 0o700); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		filepath.Join(project, "root.jsonl"):          `{"type":"user","uuid":"root-user","sessionId":"root","cwd":"/repo","message":{"role":"user","content":"Work"}}`,
		filepath.Join(subagents, "agent-child.jsonl"): `{"type":"assistant","uuid":"child-record","sessionId":"root","cwd":"/repo","message":{"id":"agent-child-message","role":"assistant","model":"haiku","stop_reason":"end_turn","content":[{"type":"text","text":"Finished"}]}}`,
	} {
		if err := os.WriteFile(path, []byte(content+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.HistoryInventory(t.Context()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.HistorySnapshot(t.Context(), child)
	if err != nil || snapshot.Partial || snapshot.Session.ID != child || snapshot.Session.ParentID != parent || !snapshot.Subagent {
		t.Fatalf("transcript reconciliation: %+v %v", snapshot, err)
	}
	key := statev2.Key{InstanceID: instance.ID, SessionID: child}
	batch, _, err := conversationingest.Normalize(key, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.SessionState(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	batch.ExpectedRevision = current.Revision
	if _, err := store.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	detail, err := a.Session(t.Context(), child, 10, 0)
	if err != nil || len(detail.Messages) != 1 || detail.Messages[0].ID != "agent-child-message" || len(detail.Parts) != 1 {
		t.Fatalf("duplicate runtime/transcript messages: %+v %v", detail, err)
	}
	other, _ := reloadTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" {
			_ = json.NewEncoder(w).Encode([]db.Session{})
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "id: 1\ndata: {\"type\":\"session.status\",\"properties\":{\"sessionID\":\"agent-child\",\"status\":{\"type\":\"done\"}}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}, platforms.Capabilities{Events: true})
	otherDone := make(chan struct{})
	go func() { defer close(otherDone); _ = other.WatchEvents(ctx) }()
	defer func() { cancel(); <-otherDone }()
	eventually(t, "second terminal observation", func() bool { return other.LiveStatus("agent-child") != nil })
	p.projects.mu.Lock()
	p.projects.roots["/other"] = &claudeProject{watched: watched{adapter: other}}
	p.projects.mu.Unlock()
	if _, exists := p.CachedSessionObservations()["agent-child"]; exists {
		t.Fatal("ambiguous terminal child observations borrowed an owner")
	}
}
