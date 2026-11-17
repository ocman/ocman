package routines

import (
	"context"
	"errors"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func TestPatchRoutinePreservesConnection(t *testing.T) {
	h := newHarness(t)
	input := validInput()
	input.Platform = "cnx_account"
	routine, err := h.svc.Create(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"model": "anthropic/opus"}
	patched, err := patchRoutineInput(routine, req, req.Params.Arguments.(map[string]any), h.svc.now())
	if err != nil {
		t.Fatal(err)
	}
	updated, err := h.svc.Update(t.Context(), routine.ID, patched)
	if err != nil || updated.Platform != input.Platform {
		t.Fatalf("patch moved connection: %q -> %q, %v", input.Platform, updated.Platform, err)
	}
}

type selectionPlatform struct {
	*testPlatform
	creation *platforms.CreationIdentity
	catalog  *platforms.DirectoryCatalog
	err      error
}

func (p *selectionPlatform) DirectoryCatalog(context.Context, platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
	return p.catalog, p.err
}

func (p *selectionPlatform) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	p.creation = req.Creation
	if p.creation == nil || p.creation.Model == "" {
		return nil, platforms.ErrUnsupported
	}
	return p.testPlatform.CreateSession(ctx, req)
}

func TestRoutineResolvesDefaultModelBeforeAllocation(t *testing.T) {
	for _, tc := range []struct {
		name, model, want string
		catalog           *platforms.DirectoryCatalog
		err               error
	}{
		{name: "directory default", catalog: &platforms.DirectoryCatalog{DefaultModel: "anthropic/opus"}, want: "anthropic/opus"},
		{name: "session default", catalog: &platforms.DirectoryCatalog{Models: &platforms.SessionModelsResponse{SessionDefault: "anthropic/opus"}}, want: "anthropic/opus"},
		{name: "explicit wins", model: "anthropic/sonnet", catalog: &platforms.DirectoryCatalog{DefaultModel: "anthropic/opus"}, want: "anthropic/sonnet"},
		{name: "catalog error", err: errors.New("catalog offline")},
		{name: "unavailable default", catalog: &platforms.DirectoryCatalog{DefaultModel: "anthropic/removed", Models: &platforms.SessionModelsResponse{HasProviders: true, Models: []platforms.SessionModel{{Provider: "anthropic", Model: "removed"}}}}},
		{name: "available default", catalog: &platforms.DirectoryCatalog{DefaultModel: "anthropic/opus", Models: &platforms.SessionModelsResponse{HasProviders: true, Models: []platforms.SessionModel{{Provider: "anthropic", Model: "opus", IsAvailable: true}}}}, want: "anthropic/opus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			p := &selectionPlatform{testPlatform: h.platform, catalog: tc.catalog, err: tc.err}
			h.svc.platforms.Register(p)
			input := validInput()
			input.Model = tc.model
			routine, err := h.svc.Create(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			run, err := h.svc.RunNow(t.Context(), routine.ID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if run.State != RunFailure || p.creation != nil || h.host.calls.Load() != 0 || !strings.Contains(run.Error, "catalog") && !strings.Contains(run.Error, "select an available model") {
					t.Fatalf("invalid catalog allocated a session: %+v", run)
				}
			} else if run.State != RunRunning || p.creation == nil || p.creation.Model != tc.want || len(p.sent) != 1 || p.sent[0].Model != tc.want {
				t.Fatalf("launch lost model: %+v, creation %+v, sent %+v", run, p.creation, p.sent)
			}
		})
	}
}

func TestRoutineLaunchCatalogCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name, session string
		err           error
		catalog       *platforms.DirectoryCatalog
	}{
		{name: "no catalog"},
		{name: "unsupported", err: platforms.ErrUnsupported},
		{name: "existing session", session: "already-created", err: errors.New("must not read catalog")},
		{name: "default agent", catalog: &platforms.DirectoryCatalog{DefaultAgent: "plan"}},
		{name: "native default", catalog: &platforms.DirectoryCatalog{Models: &platforms.SessionModelsResponse{HasProviders: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.svc.platforms.Register(&selectionPlatform{testPlatform: h.platform, catalog: tc.catalog, err: tc.err})
			run := state.RoutineRun{TargetSessionID: tc.session, Directory: "/repo"}
			if err := h.svc.prepareLaunchSelections(t.Context(), "opencode", &run); err != nil {
				t.Fatal(err)
			}
			if tc.catalog != nil && run.Agent != tc.catalog.DefaultAgent {
				t.Fatalf("agent default = %q", run.Agent)
			}
		})
	}
}

func TestRoutineConfiguresInitialSelection(t *testing.T) {
	for _, worktree := range []bool{false, true} {
		t.Run(map[bool]string{false: "checkout", true: "worktree"}[worktree], func(t *testing.T) {
			h := newHarness(t)
			p := &selectionPlatform{testPlatform: h.platform}
			h.svc.platforms.Register(p)
			owner := &routineWorktreeHost{testHost: h.host, path: "/wt"}
			h.svc.router = hostsvc.NewRouter(owner)
			input := validInput()
			input.Agent, input.Model, input.Worktree = "plan", "anthropic/opus", worktree
			routine, err := h.svc.Create(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			run, err := h.svc.RunNow(t.Context(), routine.ID)
			if err != nil || run.State != RunRunning {
				t.Fatalf("launch = %+v, %v", run, err)
			}
			creation := p.creation
			if worktree {
				creation = owner.request.Creation
			}
			if creation == nil || creation.Model != input.Model || creation.Agent != input.Agent {
				t.Fatalf("initial selection = %+v", creation)
			}
		})
	}
}
