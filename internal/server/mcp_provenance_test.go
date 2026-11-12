package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/mcpauth"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type provenanceNative struct{ *fakePlatform }

func (*provenanceNative) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "opencode", SourceKey: "source"}, nil
}

func provenanceFixture(t *testing.T) (*Server, string, *mcpCreateHost) {
	t.Helper()
	s, reg := newSessionsTestServer(t)
	store, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: "owner", Platform: "opencode", SourceKey: "source"}
	if _, err := store.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "native-caller", Directory: "/source/project"}}); err != nil {
		t.Fatal(err)
	}
	row, err := store.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "source", NativeID: "native-caller"})
	if err != nil {
		t.Fatal(err)
	}
	native := &provenanceNative{&fakePlatform{PlatformID: "opencode", SessionFn: func(string) (*platforms.SessionDetail, error) {
		t.Fatal("provenance must not load conversation history")
		return nil, platforms.ErrHistoryUnavailable
	}}}
	adapter, err := owned.New(t.Context(), native, store, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterConnection("cnx_caller", adapter); err != nil {
		t.Fatal(err)
	}
	reg.SetSourceResolver(func(_ context.Context, owner, kind, source string) (platforms.ID, error) {
		if owner == "owner" && kind == "opencode" && source == "source" {
			return "cnx_caller", nil
		}
		return "", platforms.ErrNotFound
	})
	s.sessions.WithSessionCatalogue(store, "owner")
	s.historyOwner = "owner"
	host := &mcpCreateHost{ensureHost: &ensureHost{ensure: func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
		return &hostsvc.EnsureRuntimeResult{}, nil
	}}}
	s.hostRouter = hostsvc.NewRouter(host)
	reg.Register(&fakePlatform{PlatformID: "claudecode", SessionFn: func(id string) (*platforms.SessionDetail, error) {
		return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/another/project"}}, nil
	}, SendMessageFn: func(platforms.SendMessageRequest) error { return nil }})
	return s, row.ID, host
}

func callProvenanceCreate(t *testing.T, s *Server, meta map[string]any, args map[string]any, headers ...map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "sessions", "arguments": args, "_meta": meta}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json, text/event-stream")
	for _, values := range headers {
		for k, v := range values {
			r.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	internalmcp.New(internalmcp.Deps{SessionService: sessionMCPService{s}}).Handler().ServeHTTP(w, r)
	return w
}

func TestMCPCreateHiddenProvenanceAcrossNamedConnections(t *testing.T) {
	s, parent, _ := provenanceFixture(t)
	// This is the retry that used to lose provenance: every destination field
	// is explicit and the model supplies no caller session_id.
	w := callProvenanceCreate(t, s, map[string]any{"ai.opencode/sessionID": "native-caller"}, map[string]any{
		"action": "create", "prompt": "fix", "directory": "/another/project", "platform": "claudecode", "model": "p/model", "worktree": true,
	})
	link, err := s.stateDB.GetSessionCreator(t.Context(), "claudecode", "ses-new")
	if err != nil || link == nil || link.CreatorSessionID != parent || link.CreatorPlatform != "cnx_caller" {
		t.Fatalf("lost hidden caller: %+v, %v; response %d %s", link, err, w.Code, w.Body.String())
	}
}

func TestMCPNamedCallerReferenceRetainsRoutingKey(t *testing.T) {
	s, parent, _ := provenanceFixture(t)
	for _, reference := range []string{parent, "native-caller"} {
		info, err := (sessionMCPService{s}).createCaller(t.Context(), internalmcp.CreateSessionRequest{Platform: "cnx_caller", SessionID: reference})
		if err != nil || info.Session.Platform != "cnx_caller" || info.Session.ID != parent {
			t.Fatalf("named caller reference %s: %+v, %v", reference, info, err)
		}
	}
}

func TestMCPCreateRejectsUnresolvedHiddenCallerBeforeLaunch(t *testing.T) {
	for _, scenario := range []string{"unknown", "malformed", "conflicting argument", "disconnected", "storage failure"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, host := provenanceFixture(t)
			args := map[string]any{"action": "create", "prompt": "fix", "directory": "/another/project", "platform": "claudecode", "model": "p/model", "worktree": true}
			meta := map[string]any{"ai.opencode/sessionID": "native-caller"}
			switch scenario {
			case "unknown":
				meta["ai.opencode/sessionID"] = "unknown"
			case "malformed":
				meta["ai.opencode/sessionID"] = 42
			case "conflicting argument":
				args["session_id"] = "unrelated"
			case "disconnected":
				s.registry.Unregister("cnx_caller")
			case "storage failure":
				if _, err := s.stateDB.SQL().Exec(`DROP TABLE session_catalog`); err != nil {
					t.Fatal(err)
				}
			}
			w := callProvenanceCreate(t, s, meta, args)
			if !strings.Contains(w.Body.String(), `"isError":true`) || host.request.ProjectDir != "" {
				t.Fatalf("lost caller silently launched a child: %s; %+v", w.Body.String(), host.request)
			}
		})
	}
}

func TestMCPCreateHeadersAndLegacyArgumentsPreserveProvenance(t *testing.T) {
	for _, transport := range []string{"headers", "explicit"} {
		t.Run(transport, func(t *testing.T) {
			s, parent, _ := provenanceFixture(t)
			args := map[string]any{"action": "create", "prompt": "fix", "directory": "/another/project", "model": "p/model", "worktree": true}
			headers := map[string]string{}
			platform := "claudecode"
			if transport == "headers" {
				args["platform"] = platform
				headers = mcpauth.SessionHeaders("unused-in-this-handler-test", "opencode", "native-caller")
			} else {
				platform = "cnx_caller"
				args["platform"], args["session_id"] = platform, parent
			}
			w := callProvenanceCreate(t, s, nil, args, headers)
			link, err := s.stateDB.GetSessionCreator(t.Context(), platform, "ses-new")
			if err != nil || link == nil || link.CreatorSessionID != parent || link.CreatorPlatform != "cnx_caller" {
				t.Fatalf("lost %s caller: %+v, %v; %s", transport, link, err, w.Body.String())
			}
		})
	}
}

type cancellingProvenanceHost struct {
	*mcpCreateHost
	cancel context.CancelFunc
}

func (h *cancellingProvenanceHost) CreateWorktreeSession(ctx context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	res, err := h.mcpCreateHost.CreateWorktreeSession(ctx, req)
	h.cancel()
	return res, err
}

func TestMCPCreateCancellationAfterAllocationKeepsProvenance(t *testing.T) {
	s, parent, host := provenanceFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.hostRouter = hostsvc.NewRouter(&cancellingProvenanceHost{host, cancel})
	worktree := true
	created, err := (sessionMCPService{s}).CreateSession(ctx, internalmcp.CreateSessionRequest{
		Platform: "claudecode", Model: "p/model", Directory: "/another/project", Prompt: "fix", Worktree: &worktree,
		Caller: &internalmcp.SessionCaller{Kind: "opencode", SessionID: "native-caller"},
	})
	if created.SessionID != "ses-new" || err == nil {
		t.Fatalf("cancelled launch = %+v, %v", created, err)
	}
	link, err := s.stateDB.GetSessionCreator(t.Context(), "claudecode", created.SessionID)
	if err != nil || link == nil || link.CreatorSessionID != parent {
		t.Fatalf("cancellation lost allocated child's provenance: %+v, %v", link, err)
	}
}

func TestMCPHiddenCallerKeepsAccountWithExplicitModel(t *testing.T) {
	s, parent, _ := provenanceFixture(t)
	w := callProvenanceCreate(t, s, map[string]any{"ai.opencode/sessionID": "native-caller"}, map[string]any{
		"action": "create", "prompt": "fix", "model": "p/model", "worktree": true,
	})
	link, err := s.stateDB.GetSessionCreator(t.Context(), "cnx_caller", "ses-new")
	if err != nil || link == nil || link.CreatorSessionID != parent {
		t.Fatalf("explicit model replaced caller's account: %+v, %v; %s", link, err, w.Body.String())
	}
}

type provenanceTarget struct{ *fakePlatform }

func (*provenanceTarget) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "claudecode", SourceKey: "target-source"}, nil
}

type provenanceIdentityHost struct {
	*mcpCreateHost
	id string
}

func (h *provenanceIdentityHost) CreateWorktreeSession(ctx context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	res, err := h.mcpCreateHost.CreateWorktreeSession(ctx, req)
	res.SessionID = h.id
	return res, err
}

func TestMCPCreateRecordsResolvedDestinationAccount(t *testing.T) {
	s, parent, host := provenanceFixture(t)
	store, err := sessioncatalog.New(s.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: "owner", Platform: "claudecode", SourceKey: "target-source"}
	if _, err := store.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "native-child", Directory: "/target"}}); err != nil {
		t.Fatal(err)
	}
	child, err := store.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "claudecode", SourceKey: "target-source", NativeID: "native-child"})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := owned.New(t.Context(), &provenanceTarget{&fakePlatform{PlatformID: "claudecode"}}, store, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	s.registry.Register(adapter) // Legacy kind selects this target account.
	if err := s.registry.RegisterConnection("cnx_target", adapter); err != nil {
		t.Fatal(err)
	}
	s.registry.SetSourceResolver(func(_ context.Context, owner, kind, source string) (platforms.ID, error) {
		if owner == "owner" && kind == "opencode" && source == "source" {
			return "cnx_caller", nil
		}
		if owner == "owner" && kind == "claudecode" && source == "target-source" {
			return "cnx_target", nil
		}
		return "", platforms.ErrNotFound
	})
	s.hostRouter = hostsvc.NewRouter(&provenanceIdentityHost{host, child.ID})
	w := callProvenanceCreate(t, s, map[string]any{"ai.opencode/sessionID": "native-caller"}, map[string]any{
		"action": "create", "prompt": "fix", "directory": "/target", "platform": "claudecode", "model": "p/model", "worktree": true,
	})
	link, err := s.stateDB.GetSessionCreator(t.Context(), "cnx_target", child.ID)
	if err != nil || link == nil || link.CreatorSessionID != parent {
		t.Fatalf("creator stored under destination alias instead of account: %+v, %v; %s", link, err, w.Body.String())
	}
}
