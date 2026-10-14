package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

// Only process launch is faked. Readiness probes, runtime inventory, session
// creation and first-prompt delivery use their production HTTP paths.
type slowStartRuntime struct {
	ocruntime.Runtime
	endpoint string
	launches atomic.Int32
}

func (r *slowStartRuntime) Launch(_ context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	r.launches.Add(1)
	return &ocruntime.Instance{Endpoint: r.endpoint, RepoRoot: spec.RepoRoot, Kind: ocruntime.KindNativeTmux, ID: "slow-project"}, nil
}

func TestStartSessionWaitsForSlowOpenCodeAndSucceedsFirstTime(t *testing.T) {
	srv, raw := testServerWithRawDB(t)
	dir := t.TempDir()
	var ready atomic.Bool
	var creates, prompts atomic.Int32
	probeSeen := make(chan struct{})
	var once sync.Once
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/config":
			if !ready.Load() {
				once.Do(func() { close(probeSeen) })
				http.Error(w, "still starting", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		case r.URL.Path == "/project/current":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "global", "worktree": dir})
		case r.URL.Path == "/path":
			_ = json.NewEncoder(w).Encode(map[string]string{"directory": dir})
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			if !ready.Load() {
				t.Error("session creation overtook readiness")
			}
			if directory, _ := url.PathUnescape(r.Header.Get("x-opencode-directory")); directory != dir {
				t.Errorf("session directory = %q", directory)
			}
			creates.Add(1)
			if _, err := raw.ExecContext(r.Context(), `INSERT INTO session(id,directory,time_created,time_updated) VALUES ('slow-start-session',?,1,1)`, dir); err != nil {
				t.Error(err)
				http.Error(w, "persisting mock session", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "slow-start-session", "directory": dir})
		case r.Method == http.MethodGet && r.URL.Path == "/session/slow-start-session":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "slow-start-session", "directory": dir, "time": map[string]int64{"created": 1, "updated": 1}})
		case r.Method == http.MethodGet && r.URL.Path == "/session/slow-start-session/message":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodPost && r.URL.Path == "/session/slow-start-session/prompt_async":
			prompts.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	runtime := &slowStartRuntime{Runtime: ocruntime.NewNativeRuntime(), endpoint: upstream.URL}
	srv.runtime = runtime
	srv.router()
	// Browser/background reads have already cached that this new project has
	// no running instance. The same first submission must still succeed.
	if ports := opencode.DiscoverOpenCodePortsContext(t.Context()); len(ports) != 0 {
		t.Fatal(ports)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	body, err := json.Marshal(map[string]any{"directory": dir, "platform": "opencode", "send": map[string]string{"message": "first prompt"}})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		srv.handleStartSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions/start", strings.NewReader(string(body))).WithContext(ctx))
		close(done)
	}()
	select {
	case <-probeSeen:
	case <-ctx.Done():
		t.Fatal("start never probed the launched server")
	}
	select {
	case <-done:
		t.Fatalf("first submission failed while server was starting: %d %s", w.Code, w.Body.String())
	case <-time.After(250 * time.Millisecond):
	}
	ready.Store(true)
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("first submission did not finish after server became ready")
	}
	var response struct {
		SessionID         string `json:"sessionId"`
		FirstMessageSent  bool   `json:"firstMessageSent"`
		FirstMessageError string `json:"firstMessageError"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("response %d %s: %v", w.Code, w.Body.String(), err)
	}
	if w.Code != http.StatusOK || response.SessionID != "slow-start-session" || !response.FirstMessageSent || response.FirstMessageError != "" {
		t.Fatalf("first submission failed: %d %s", w.Code, w.Body.String())
	}
	if runtime.launches.Load() != 1 || creates.Load() != 1 || prompts.Load() != 1 {
		t.Fatalf("submission duplicated work: launches=%d sessions=%d prompts=%d", runtime.launches.Load(), creates.Load(), prompts.Load())
	}
	if port := opencode.DiscoverOpenCodePortContext(t.Context(), dir); port == "" {
		t.Fatal("successful first submission left the project invisible to discovery")
	}
}
