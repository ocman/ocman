package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func mcpTestCatalog(models ...string) func(platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
	return func(platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
		catalog := &platforms.DirectoryCatalog{Models: &platforms.SessionModelsResponse{HasProviders: true}}
		for _, model := range models {
			provider, id, _ := strings.Cut(model, "/")
			catalog.Models.Models = append(catalog.Models.Models, platforms.SessionModel{Provider: provider, Model: id, IsAvailable: true})
		}
		return catalog, nil
	}
}

func TestMCPCreateRejectsUnresolvedSelectionBeforeLaunch(t *testing.T) {
	for _, scenario := range []string{"unknown platform", "platform mismatch", "unknown model", "unavailable model", "catalog error", "missing catalog", "missing models"} {
		t.Run(scenario, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			launched := false
			srv.hostRouter = hostsvc.NewRouter(&mcpCreateHost{ensureHost: &ensureHost{ensure: func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
				launched = true
				return &hostsvc.EnsureRuntimeResult{}, nil
			}}})
			reg.Register(&fakePlatform{PlatformID: "opencode",
				DirectoryCatalogFn: func(req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
					switch scenario {
					case "catalog error":
						return nil, errors.New("catalog unavailable")
					case "missing catalog":
						return nil, nil
					case "missing models":
						return &platforms.DirectoryCatalog{}, nil
					}
					catalog, _ := mcpTestCatalog("anthropic/opus")(req)
					catalog.Models.Models[0].IsAvailable = scenario != "unavailable model"
					return catalog, nil
				},
				CreateSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					launched = true
					return &platforms.CreateSessionResponse{ID: "child"}, nil
				},
				SendMessageFn: func(platforms.SendMessageRequest) error { launched = true; return nil },
			})
			platform, model := "opencode", "anthropic/opus"
			if scenario == "unknown platform" {
				platform = "claude"
			}
			if scenario == "unknown model" {
				model = "anthropic/misspelled-opus"
			}
			if scenario == "platform mismatch" {
				model = "claudecode:anthropic/opus"
			}
			worktree := false
			got, err := (sessionMCPService{srv}).CreateSession(t.Context(), internalmcp.CreateSessionRequest{
				Platform: platform, Model: model, Directory: "/repo", Prompt: "investigate", Worktree: &worktree,
			})
			if !errors.Is(err, internalmcp.ErrInvalidSessionCreate) || !strings.Contains(err.Error(), "ask the user") || launched || got.SessionID != "" {
				t.Fatalf("unresolved selection must ask before launch: created=%+v err=%v launched=%v", got, err, launched)
			}
		})
	}
}

func TestMCPCreateCarriesInitialSelections(t *testing.T) {
	for _, worktree := range []bool{false, true} {
		t.Run(map[bool]string{false: "current checkout", true: "worktree"}[worktree], func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			host := &mcpCreateHost{ensureHost: &ensureHost{ensure: func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
				return &hostsvc.EnsureRuntimeResult{}, nil
			}}}
			srv.hostRouter = hostsvc.NewRouter(host)
			if err := srv.stateDB.SetProjectSettings(t.Context(), "/repo", state.ProjectSettings{Models: []string{"openai/sol"}}); err != nil {
				t.Fatal(err)
			}
			srv.sessions.RecordCooldown(t.Context(), "anthropic", time.Minute)
			var creation *platforms.CreationIdentity
			var sent platforms.SendMessageRequest
			reg.Register(&fakePlatform{PlatformID: "opencode", DirectoryCatalogFn: mcpTestCatalog("anthropic/opus"),
				CreateSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
					creation = req.Creation
					return &platforms.CreateSessionResponse{ID: "child"}, nil
				}, SendMessageFn: func(req platforms.SendMessageRequest) error { sent = req; return nil },
			})
			_, err := (sessionMCPService{srv}).CreateSession(t.Context(), internalmcp.CreateSessionRequest{
				Platform: "opencode", Model: "anthropic/opus", Agent: "plan", Directory: "/repo", Prompt: "investigate", Worktree: &worktree,
			})
			if worktree {
				creation = host.request.Creation
			}
			if err != nil || creation == nil || creation.Model != "anthropic/opus" || creation.Agent != "plan" || sent.Message != "investigate" || sent.Model != "anthropic/opus" || !sent.ExactModel || !sent.FirstPrompt {
				t.Fatalf("initial selections missing: creation=%+v sent=%+v err=%v", creation, sent, err)
			}
		})
	}
}
