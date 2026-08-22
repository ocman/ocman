package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Coverage for the Platform operations that only had indirect
// (port-resolved helper) tests: SessionModels, RunShell,
// ExecuteCommand, Abort, RenameSession and Compact. The happy paths
// assert the OpenCode HTTP contract (method + path + body); the
// failure paths cover the three ways port resolution can fail and the
// 4xx/5xx upstream split.

const (
	opsSID = "sess-ops-more"
	opsDir = "/tmp/proj-ops-more"
)

type opsCall struct {
	method string
	path   string
	body   string
}

// opsRecorder captures every request the fake OpenCode instance saw.
type opsRecorder struct {
	mu    sync.Mutex
	calls []opsCall
}

func (r *opsRecorder) add(c opsCall) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, c)
}

// find returns the first recorded call for path.
func (r *opsRecorder) find(path string) (opsCall, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c.path == path {
			return c, true
		}
	}
	return opsCall{}, false
}

func (r *opsRecorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// newOpsFixture stands up a fake OpenCode instance discoverable for
// opsDir and returns an Adapter wired to a DB holding opsSID. A nil
// handler replies `{}` (JSON) to everything, which is what every
// mutating endpoint needs. Package-level HTTP/model caches are reset
// so each subtest actually reaches the fake.
func newOpsFixture(t *testing.T, favorites FavoritesReader, handler http.HandlerFunc) (*Adapter, *opsRecorder) {
	t.Helper()
	rec := &opsRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.add(opsCall{method: r.Method, path: r.URL.Path, body: string(body)})
		if handler != nil {
			handler(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)

	catalogCache = newHTTPCache(catalogCache.ttl)
	ResetCachesForTests()

	withTestPort(t, opsDir, strings.TrimPrefix(srv.URL, "http://127.0.0.1:"))
	return New(newTestDBWithSession(t, opsSID, opsDir), favorites), rec
}

// newOpsAdapterNoInstance returns an Adapter whose session exists in
// the DB but for which no OpenCode instance is discoverable — neither
// by directory nor by the per-session probe.
func newOpsAdapterNoInstance(t *testing.T) *Adapter {
	t.Helper()
	restorePorts := setDiscoverPortsImplForTests(func() map[string]string { return map[string]string{} })
	restoreServers := setDiscoverServersImplForTests(func() []openCodeServer { return nil })
	resetPortCacheForTests()
	resetSessionPortAffinityForTests()
	t.Cleanup(func() {
		restorePorts()
		restoreServers()
		resetPortCacheForTests()
		resetSessionPortAffinityForTests()
	})
	return New(newTestDBWithSession(t, opsSID, opsDir), nil)
}

// opsInvocation names one Platform operation and how to call it for
// opsSID, so the shared failure tables can drive every method.
type opsInvocation struct {
	name string
	call func(context.Context, *Adapter) error
}

// mutatingOps are the operations that resolve a port and then POST/PATCH.
func mutatingOps() []opsInvocation {
	return []opsInvocation{
		{"RunShell", func(ctx context.Context, a *Adapter) error {
			return a.RunShell(ctx, platforms.RunShellRequest{SessionID: opsSID, Command: "echo hi"})
		}},
		{"ExecuteCommand", func(ctx context.Context, a *Adapter) error {
			return a.ExecuteCommand(ctx, platforms.ExecuteCommandRequest{SessionID: opsSID, Command: "init"})
		}},
		{"Abort", func(ctx context.Context, a *Adapter) error {
			return a.Abort(ctx, platforms.AbortRequest{SessionID: opsSID})
		}},
		{"RenameSession", func(ctx context.Context, a *Adapter) error {
			return a.RenameSession(ctx, platforms.RenameSessionRequest{SessionID: opsSID, Title: "new title"})
		}},
		{"Compact", func(ctx context.Context, a *Adapter) error {
			return a.Compact(ctx, platforms.CompactRequest{SessionID: opsSID, ProviderID: "anthropic", ModelID: "claude"})
		}},
	}
}

// allOps adds SessionModels, which resolves the session itself rather
// than going through resolvePort.
func allOps() []opsInvocation {
	return append(mutatingOps(), opsInvocation{"SessionModels", func(ctx context.Context, a *Adapter) error {
		_, err := a.SessionModels(ctx, opsSID)
		return err
	}})
}

// TestAdapterOperations_SessionLookupFailures asserts that an adapter
// with no OpenCode DB, or one that doesn't know the session, reports
// ErrNotFound rather than attempting an upstream call.
func TestAdapterOperations_SessionLookupFailures(t *testing.T) {
	cases := []struct {
		name    string
		adapter func(*testing.T) *Adapter
	}{
		{"nil db", func(*testing.T) *Adapter { return New(nil, nil) }},
		{"unknown session", func(t *testing.T) *Adapter {
			return New(newTestDBWithSession(t, "some-other-session", opsDir), nil)
		}},
	}
	for _, tc := range cases {
		for _, op := range allOps() {
			t.Run(tc.name+"/"+op.name, func(t *testing.T) {
				err := op.call(context.Background(), tc.adapter(t))
				if !errors.Is(err, platforms.ErrNotFound) {
					t.Errorf("error = %v, want ErrNotFound", err)
				}
			})
		}
	}
}

// TestAdapterOperations_NoRunningInstance covers the branch where the
// session is known but no instance is reachable: the mutating
// operations must surface ErrPlatformUnreachable.
func TestAdapterOperations_NoRunningInstance(t *testing.T) {
	for _, op := range mutatingOps() {
		t.Run(op.name, func(t *testing.T) {
			err := op.call(context.Background(), newOpsAdapterNoInstance(t))
			if !errors.Is(err, platforms.ErrPlatformUnreachable) {
				t.Errorf("error = %v, want ErrPlatformUnreachable", err)
			}
		})
	}
}

// TestAdapterOperations_UpstreamContract pins the method, path and
// JSON body each operation sends to OpenCode.
func TestAdapterOperations_UpstreamContract(t *testing.T) {
	tests := []struct {
		name       string
		call       func(context.Context, *Adapter) error
		wantMethod string
		wantPath   string
		wantBody   map[string]string
	}{
		{
			name: "RunShell defaults the agent",
			call: func(ctx context.Context, a *Adapter) error {
				return a.RunShell(ctx, platforms.RunShellRequest{SessionID: opsSID, Command: "  ls -l  "})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/session/" + opsSID + "/shell",
			wantBody:   map[string]string{"command": "ls -l", "agent": "build"},
		},
		{
			name: "RunShell honours an explicit agent",
			call: func(ctx context.Context, a *Adapter) error {
				return a.RunShell(ctx, platforms.RunShellRequest{SessionID: opsSID, Command: "ls", Agent: "plan"})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/session/" + opsSID + "/shell",
			wantBody:   map[string]string{"command": "ls", "agent": "plan"},
		},
		{
			name: "ExecuteCommand without model or agent",
			call: func(ctx context.Context, a *Adapter) error {
				return a.ExecuteCommand(ctx, platforms.ExecuteCommandRequest{
					SessionID: opsSID, Command: "init", Arguments: "--force",
				})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/session/" + opsSID + "/command",
			wantBody:   map[string]string{"command": "init", "arguments": "--force"},
		},
		{
			name: "ExecuteCommand forwards model and agent",
			call: func(ctx context.Context, a *Adapter) error {
				return a.ExecuteCommand(ctx, platforms.ExecuteCommandRequest{
					SessionID: opsSID, Command: "review", Arguments: "", Model: "anthropic/claude", Agent: "plan",
				})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/session/" + opsSID + "/command",
			wantBody: map[string]string{
				"command": "review", "arguments": "", "model": "anthropic/claude", "agent": "plan",
			},
		},
		{
			name: "Abort posts an empty body",
			call: func(ctx context.Context, a *Adapter) error {
				return a.Abort(ctx, platforms.AbortRequest{SessionID: opsSID})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/session/" + opsSID + "/abort",
			wantBody:   map[string]string{},
		},
		{
			name: "RenameSession patches the title",
			call: func(ctx context.Context, a *Adapter) error {
				return a.RenameSession(ctx, platforms.RenameSessionRequest{SessionID: opsSID, Title: "renamed"})
			},
			wantMethod: http.MethodPatch,
			wantPath:   "/session/" + opsSID,
			wantBody:   map[string]string{"title": "renamed"},
		},
		{
			// No small_model in /config, so the caller's choice stands.
			name: "Compact falls back to the requested model",
			call: func(ctx context.Context, a *Adapter) error {
				return a.Compact(ctx, platforms.CompactRequest{
					SessionID: opsSID, ProviderID: "anthropic", ModelID: "claude-opus",
				})
			},
			wantMethod: http.MethodPost,
			wantPath:   "/session/" + opsSID + "/summarize",
			wantBody:   map[string]string{"providerID": "anthropic", "modelID": "claude-opus"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, rec := newOpsFixture(t, nil, nil)
			if err := tc.call(context.Background(), a); err != nil {
				t.Fatalf("call: %v", err)
			}
			got, ok := rec.find(tc.wantPath)
			if !ok {
				t.Fatalf("no request to %q (saw %d requests)", tc.wantPath, rec.len())
			}
			if got.method != tc.wantMethod {
				t.Errorf("method = %q, want %q", got.method, tc.wantMethod)
			}
			var body map[string]string
			if err := json.Unmarshal([]byte(got.body), &body); err != nil {
				t.Fatalf("body %q is not a JSON object: %v", got.body, err)
			}
			if len(body) != len(tc.wantBody) {
				t.Errorf("body = %v, want %v", body, tc.wantBody)
			}
			for k, want := range tc.wantBody {
				if body[k] != want {
					t.Errorf("body[%q] = %q, want %q", k, body[k], want)
				}
			}
		})
	}
}

// TestCompact_PrefersConfiguredSmallModel covers the branch where
// OpenCode's resolved config carries a small_model: it wins over the
// caller-supplied provider/model pair.
func TestCompact_PrefersConfiguredSmallModel(t *testing.T) {
	a, rec := newOpsFixture(t, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/config" {
			_, _ = w.Write([]byte(`{"small_model":"anthropic/claude-haiku"}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})

	err := a.Compact(context.Background(), platforms.CompactRequest{
		SessionID: opsSID, ProviderID: "openai", ModelID: "gpt-5",
	})
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	got, ok := rec.find("/session/" + opsSID + "/summarize")
	if !ok {
		t.Fatal("no summarize request")
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decoding body %q: %v", got.body, err)
	}
	if body["providerID"] != "anthropic" || body["modelID"] != "claude-haiku" {
		t.Errorf("body = %v, want the configured small_model (anthropic/claude-haiku)", body)
	}
}

// TestAdapterOperations_UpstreamStatusHandling asserts the 4xx/5xx
// split every mutating operation inherits from sendJSON: a 4xx wraps a
// *platforms.UpstreamError (so the HTTP layer can pass the upstream
// message through as a 422), a 5xx stays a plain error.
func TestAdapterOperations_UpstreamStatusHandling(t *testing.T) {
	statuses := []struct {
		name            string
		status          int
		body            string
		wantRejected    bool
		wantUpstreamMsg string
	}{
		{"4xx is an upstream rejection", http.StatusBadRequest,
			`{"name":"BadRequest","data":{"message":"nope"}}`, true, "nope"},
		{"5xx is not an upstream rejection", http.StatusInternalServerError,
			"boom", false, ""},
	}
	for _, st := range statuses {
		for _, op := range mutatingOps() {
			t.Run(st.name+"/"+op.name, func(t *testing.T) {
				a, _ := newOpsFixture(t, nil, func(w http.ResponseWriter, _ *http.Request) {
					http.Error(w, st.body, st.status)
				})
				err := op.call(context.Background(), a)
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				if got := errors.Is(err, platforms.ErrUpstreamRejected); got != st.wantRejected {
					t.Errorf("errors.Is(err, ErrUpstreamRejected) = %v, want %v (err=%v)", got, st.wantRejected, err)
				}
				if !st.wantRejected {
					return
				}
				var upstream *platforms.UpstreamError
				if !errors.As(err, &upstream) {
					t.Fatalf("error does not carry *UpstreamError: %v", err)
				}
				if upstream.Status != st.status {
					t.Errorf("UpstreamError.Status = %d, want %d", upstream.Status, st.status)
				}
				if upstream.Message != st.wantUpstreamMsg {
					t.Errorf("UpstreamError.Message = %q, want %q", upstream.Message, st.wantUpstreamMsg)
				}
			})
		}
	}
}

// stubFavorites is a FavoritesReader that returns a fixed answer, so
// SessionModels' favorites merge (and its soft-failure branch) can be
// driven without a state.db.
type stubFavorites struct {
	models []state.ModelFavorite
	err    error
}

func (s stubFavorites) ModelFavorites(context.Context, string) ([]state.ModelFavorite, error) {
	return s.models, s.err
}

// TestSessionModels_MergesLiveProviders covers the live-instance path:
// /provider data marks models available, and ProviderDefaults is
// filtered down to the *connected* providers only.
func TestSessionModels_MergesLiveProviders(t *testing.T) {
	const providerBody = `{
		"all":[
			{"id":"anthropic","name":"Anthropic","models":{
				"claude-sonnet":{"id":"claude-sonnet","name":"Claude Sonnet","status":"active"}
			}},
			{"id":"openai","name":"OpenAI","models":{
				"gpt-5":{"id":"gpt-5","name":"GPT-5","status":"active"}
			}}
		],
		"connected":["anthropic"],
		"default":{"anthropic":"claude-sonnet","openai":"gpt-5"}
	}`

	a, _ := newOpsFixture(t, stubFavorites{models: []state.ModelFavorite{
		{Platform: string(PlatformID), Provider: "anthropic", Model: "claude-sonnet"},
	}}, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/provider" {
			_, _ = w.Write([]byte(providerBody))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})

	resp, err := a.SessionModels(context.Background(), opsSID)
	if err != nil {
		t.Fatalf("SessionModels: %v", err)
	}
	if !resp.HasProviders {
		t.Error("HasProviders = false, want true with a live instance")
	}
	// openai is in `default` but not in `connected`, so it must not
	// leak into ProviderDefaults.
	if len(resp.ProviderDefaults) != 1 || resp.ProviderDefaults["anthropic"] != "claude-sonnet" {
		t.Errorf("ProviderDefaults = %v, want only anthropic → claude-sonnet", resp.ProviderDefaults)
	}
	var sonnet *platforms.SessionModel
	for i := range resp.Models {
		if resp.Models[i].Provider == "anthropic" && resp.Models[i].Model == "claude-sonnet" {
			sonnet = &resp.Models[i]
		}
		if resp.Models[i].Provider == "openai" {
			t.Errorf("unconnected provider openai leaked into Models: %+v", resp.Models[i])
		}
	}
	if sonnet == nil {
		t.Fatalf("anthropic/claude-sonnet missing from Models: %+v", resp.Models)
	}
	if !sonnet.IsAvailable {
		t.Error("claude-sonnet IsAvailable = false, want true")
	}
	if !sonnet.IsFavorite {
		t.Error("claude-sonnet IsFavorite = false, want true (favorites merge)")
	}
}

// TestSessionModels_NoLiveInstance covers the degraded path: with no
// reachable instance there is no /provider call, so HasProviders is
// false and ProviderDefaults stays nil — but the call still succeeds.
func TestSessionModels_NoLiveInstance(t *testing.T) {
	a := newOpsAdapterNoInstance(t)
	ResetCachesForTests()

	resp, err := a.SessionModels(context.Background(), opsSID)
	if err != nil {
		t.Fatalf("SessionModels: %v", err)
	}
	if resp.HasProviders {
		t.Error("HasProviders = true, want false without a live instance")
	}
	if resp.ProviderDefaults != nil {
		t.Errorf("ProviderDefaults = %v, want nil", resp.ProviderDefaults)
	}
}

// TestSessionModels_FavoritesFailureIsSoft ensures a broken favorites
// store degrades to "no favorites" instead of failing the picker.
func TestSessionModels_FavoritesFailureIsSoft(t *testing.T) {
	a, _ := newOpsFixture(t, stubFavorites{err: errors.New("state.db unavailable")}, nil)

	resp, err := a.SessionModels(context.Background(), opsSID)
	if err != nil {
		t.Fatalf("SessionModels: %v", err)
	}
	for _, m := range resp.Models {
		if m.IsFavorite {
			t.Errorf("model %s/%s marked favorite despite the store failing", m.Provider, m.Model)
		}
	}
}
