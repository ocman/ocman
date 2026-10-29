package pluginsmod

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

type originTestSessions struct {
	Sessions
	creations []platforms.CreationIdentity
}

func (s *originTestSessions) CreateConfigured(_ context.Context, _ string, request platforms.CreateSessionRequest, _ []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
	s.creations = append(s.creations, *request.Creation)
	return &platforms.CreateSessionResponse{ID: "plugin-session"}, nil
}

func TestPluginRunsAreTaggedAndArchiveOnlyOnRequest(t *testing.T) {
	for _, archived := range []bool{false, true} {
		creator := new(originTestSessions)
		create, lookup := localPlatforms("opencode")
		m := &Module{store: new(state.DB), plugins: openPluginStore(t), deps: Deps{
			Local: func() hostsvc.Host { return modelTestHost{} }, Sessions: creator, CreatePlatform: create, Platform: lookup,
			Enqueue: func(context.Context, string, bool, platforms.SendMessageRequest) error { return nil },
		}}
		root := t.TempDir()
		params := plugins.SessionRunParams{Prompt: "verify", Archived: archived}
		if _, err := m.startSessionRun(t.Context(), "org.example.background", plugins.Config{Project: root}, params, root, "op-1"); err != nil {
			t.Fatal(err)
		}
		got := creator.creations[0]
		if got.Origin != "plugin" || got.OriginID != "org.example.background" || got.Archived != archived || got.RequestKey != "plugin:org.example.background:op-1" {
			t.Fatalf("archived=%v creation %+v", archived, got)
		}
	}
}
