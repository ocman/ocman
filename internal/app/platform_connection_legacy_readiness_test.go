package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/sessionsvc"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2"
)

type legacyInspectionNative struct {
	*platformtest.Fake
	source string
}

func (p *legacyInspectionNative) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "claudecode", SourceKey: p.source}, nil
}
func (*legacyInspectionNative) InspectConnection(context.Context) (platforms.ConnectionInspection, error) {
	return platforms.ConnectionInspection{Message: "Machine configuration is not an isolated named account"}, nil
}

func TestSavedMigratedMachineReadinessDoesNotRequireNamedIsolation(t *testing.T) {
	for _, migrated := range []bool{true, false} {
		t.Run(map[bool]string{true: "migrated", false: "new-existing"}[migrated], func(t *testing.T) {
			core := statetest.Open(t, server.ModuleSchemas()...)
			history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = history.Close() })
			store := platformconnections.New(core)
			input := platformconnections.Input{Kind: "claudecode", Name: "Machine", Configuration: "existing", Directory: t.TempDir(), Enabled: true}
			seed := platformconnections.Seed{ExistingInstallation: migrated}
			if migrated {
				seed.Connections = []platformconnections.Input{input}
				seed.DefaultKind = "claudecode"
			}
			if _, err := store.Initialize(t.Context(), seed); err != nil {
				t.Fatal(err)
			}
			var c platformconnections.Connection
			if migrated {
				c, err = store.Legacy(t.Context(), "claudecode")
			} else {
				c, err = store.Create(t.Context(), input)
			}
			if err != nil {
				t.Fatal(err)
			}
			created, sent := 0, 0
			p := &legacyInspectionNative{Fake: &platformtest.Fake{PlatformID: "claudecode", CreateSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
				created++
				return &platforms.CreateSessionResponse{ID: "native-created"}, nil
			}, SendMessageFn: func(req platforms.SendMessageRequest) error {
				if req.SessionID != "native-created" {
					t.Fatal("native identity rewritten")
				}
				sent++
				return nil
			}}, source: c.Directory}
			registry := platforms.NewRegistry()
			router := platformconnections.NewRouter(store)
			if _, err := composeConnectionSessionCore(t.Context(), core, history, registry, []platforms.Integration{p}, "owner", []platforms.ID{platforms.ID(c.ID)}, store, router); err != nil {
				t.Fatal(err)
			}
			catalogue, err := sessioncatalog.New(core)
			if err != nil {
				t.Fatal(err)
			}
			svc := sessionsvc.New(registry, sessionsvc.Hooks{})
			if err := svc.WithConnections(router, catalogue, "owner"); err != nil {
				t.Fatal(err)
			}
			response, err := svc.Create(t.Context(), c.ID, platforms.CreateSessionRequest{Directory: t.TempDir(), Creation: &platforms.CreationIdentity{RequestKey: "start", Origin: "user"}})
			if !migrated {
				if err == nil || created != 0 {
					t.Fatal("new existing account bypassed strict inspection")
				}
				return
			}
			if err != nil {
				t.Fatalf("migrated create rejected: %v", err)
			}
			if err := svc.SendMessage(t.Context(), c.ID, platforms.SendMessageRequest{SessionID: response.ID, Message: "hello"}); err != nil {
				t.Fatal(err)
			}
			if created != 1 || sent != 1 {
				t.Fatalf("native calls create=%d send=%d", created, sent)
			}
		})
	}
}
