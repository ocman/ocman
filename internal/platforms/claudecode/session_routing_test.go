package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
)

func sessionRouteFixture(t *testing.T, initial db.SessionStatus) (*claudeProjects, *provider.Adapter, *provider.Adapter, *atomic.Value) {
	t.Helper()
	status := &atomic.Value{}
	status.Store(initial)
	const id = "moved-session"
	statePath := filepath.Join(t.TempDir(), "owned.json")
	raw, _ := json.Marshal(map[string]any{id: map[string]any{"directory": "/new-project", "projectRoot": "/old-project", "started": true}})
	if err := os.WriteFile(statePath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	old, _ := reloadTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sessions" {
			_ = json.NewEncoder(w).Encode([]db.Session{{ID: id, Status: db.StatusDone}})
			return
		}
		http.Error(w, "wrong supervisor", http.StatusConflict)
	})
	current, _ := reloadTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sessions":
			_ = json.NewEncoder(w).Encode([]db.Session{{ID: id, Status: status.Load().(db.SessionStatus)}})
		case "/sessions/moved-session/lifecycle":
			_ = json.NewEncoder(w).Encode(platforms.SessionLifecycle{Status: status.Load().(db.SessionStatus)})
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "id: 1\ndata: {\"type\":\"message.updated\",\"properties\":{\"sessionID\":\"moved-session\",\"current\":true}}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}, platforms.Capabilities{Abort: true, Composer: true, Events: true})
	c := &claudeProjects{statePath: statePath, attached: make(chan struct{}), roots: map[string]*claudeProject{
		"/old-project":       {watched: watched{adapter: old}},
		"/unrelated-project": {watched: watched{adapter: current}},
	}}
	return c, old, current, status
}

func TestLiveSessionIdentityOverridesProjectMetadataForEveryRoute(t *testing.T) {
	c, _, current, _ := sessionRouteFixture(t, db.StatusBusy)
	// No directory exists and no runtime manager is installed. Routing an
	// already-running session must neither inspect its checkout nor launch it.
	for _, launch := range []bool{false, true} {
		a, owned, err := c.route(t.Context(), "moved-session", launch)
		if err != nil || !owned || a != current {
			t.Fatalf("launch=%v: session identity lost to project metadata: owned=%v err=%v", launch, owned, err)
		}
	}
	p := &claudePlatform{projects: c}
	lifecycle, err := p.SessionLifecycle(t.Context(), "moved-session")
	if err != nil || lifecycle.Status != db.StatusBusy {
		t.Fatalf("lifecycle reached a different supervisor: %+v %v", lifecycle, err)
	}
	if err := p.Abort(t.Context(), platforms.AbortRequest{SessionID: "moved-session"}); err != nil {
		t.Fatalf("abort reached a different supervisor: %v", err)
	}
	if err := p.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: "moved-session", Message: "test"}); err != nil {
		t.Fatalf("send reached a different supervisor: %v", err)
	}
}

func TestSessionStreamSwitchesFromStaleProjectToLiveIdentity(t *testing.T) {
	c, _, _, status := sessionRouteFixture(t, db.StatusDone)
	opened := make(chan struct{}, 1)
	old, err := provider.New(t.Context(), sseSocket(t, "moved-session", opened))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(old.Close)
	c.roots["/old-project"] = &claudeProject{watched: watched{adapter: old}}
	p := &claudePlatform{projects: c}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.ProxyEvents(ctx, "moved-session", &strings.Builder{}, func() {}) }()
	select {
	case <-opened:
	case <-ctx.Done():
		t.Fatal("stale stream never connected")
	}
	status.Store(db.StatusBusy)
	if _, _, err := c.route(ctx, "moved-session", false); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("rebind did not end stale stream cleanly: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("stale stream survived corrected session routing")
	}
	streamCtx, stop := context.WithTimeout(t.Context(), 2*time.Second)
	defer stop()
	out := &cancelStreamWriter{cancel: stop}
	_ = p.ProxyEvents(streamCtx, "moved-session", out, func() {})
	if !strings.Contains(out.String(), `"current":true`) {
		t.Fatalf("new content did not reach corrected session stream: %s", out.String())
	}
}

func TestResolvedSessionBindingSurvivesTurnCompletion(t *testing.T) {
	c, _, current, status := sessionRouteFixture(t, db.StatusBusy)
	if _, _, err := c.route(t.Context(), "moved-session", false); err != nil {
		t.Fatal(err)
	}
	status.Store(db.StatusDone)
	for _, launch := range []bool{false, true} {
		a, _, err := c.route(t.Context(), "moved-session", launch)
		if err != nil || a != current {
			t.Fatalf("completed session returned to stale project: launch=%v err=%v", launch, err)
		}
	}
	if c.owner("moved-session") != current {
		t.Fatal("cached observations ignored resolved session binding")
	}
	if root, _ := claudeprovider.OwnedProjectRoot(c.statePath, "moved-session"); root != "/unrelated-project" {
		t.Fatalf("restart would restore stale runtime binding: %q", root)
	}
	c.sessions = nil // a new dashboard lifetime with the turn already settled
	if a, _, err := c.route(t.Context(), "moved-session", false); err != nil || a != current {
		t.Fatalf("restart lost corrected native identity routing: %v", err)
	}
}

func TestAmbiguousLiveSessionFailsClosed(t *testing.T) {
	c, _, _, _ := sessionRouteFixture(t, db.StatusBusy)
	duplicate, _ := reloadTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]db.Session{{ID: "moved-session", Status: db.StatusBusy}})
	})
	c.roots["/duplicate"] = &claudeProject{watched: watched{adapter: duplicate}}
	for _, launch := range []bool{false, true} {
		if _, _, err := c.route(t.Context(), "moved-session", launch); !errors.Is(err, platforms.ErrBusy) {
			t.Fatalf("ambiguous session routed arbitrarily: %v", err)
		}
	}
}

func TestCancelledSessionResolutionDoesNotFallBack(t *testing.T) {
	c, _, _, _ := sessionRouteFixture(t, db.StatusBusy)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := c.route(ctx, "moved-session", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled resolution chose a project: %v", err)
	}
}

func TestSessionBindingsInvalidateOnlyTheirOwnStreamsAndRetiredAdapters(t *testing.T) {
	c, old, current, _ := sessionRouteFixture(t, db.StatusBusy)
	c.mu.Lock()
	changed := c.sessionChangedLocked("moved-session")
	unrelated := c.sessionChangedLocked("another-session")
	c.mu.Unlock()
	c.bindSession("moved-session", current)
	select {
	case <-changed:
	default:
		t.Fatal("corrected binding did not notify its stream")
	}
	select {
	case <-unrelated:
		t.Fatal("session routing interrupted an unrelated stream")
	default:
	}
	c.mu.Lock()
	unchanged := c.sessionChangedLocked("moved-session")
	c.mu.Unlock()
	c.bindSession("moved-session", current)
	select {
	case <-unchanged:
		t.Fatal("unchanged binding restarted its stream")
	default:
	}
	delete(c.roots, "/unrelated-project")
	if c.owner("moved-session") != old {
		t.Fatal("retired adapter remained bound instead of requiring recovery")
	}
	c.closed = true
	c.bindSession("another-session", current)
	if c.sessions["another-session"] != nil {
		t.Fatal("closed platform accepted another binding")
	}
}

func TestSharedTranscriptCannotRouteAnUnownedNativeSession(t *testing.T) {
	c, old, _, _ := sessionRouteFixture(t, db.StatusDone)
	if err := os.Remove(c.statePath); err != nil {
		t.Fatal(err)
	}
	if _, err := old.RuntimeSessions(t.Context()); err != nil {
		t.Fatal(err)
	}
	if c.owner("moved-session") != nil {
		t.Fatal("shared transcript cache claimed live ownership")
	}
	if a, owned, err := c.route(t.Context(), "moved-session", false); a != nil || owned || err != nil {
		t.Fatalf("unowned session routed from transcript metadata: %v %v %v", a, owned, err)
	}
}
