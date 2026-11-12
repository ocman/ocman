package server

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type createConnectionOwner struct {
	createPathHost
	hostsvc.ConnectionHost
	inventory    hostsvc.ConnectionInventory
	inventoryErr error
}

func (h *createConnectionOwner) InventoryPlatformConnections(context.Context) (hostsvc.ConnectionInventory, error) {
	return h.inventory, h.inventoryErr
}

type createNamedRemote struct {
	*platformtest.Fake
	kind platforms.ID
}

func (p *createNamedRemote) NativeKind() platforms.ID { return p.kind }

func TestOmittedNamedConnectionResolvesSavedAccountBeforeModelNamespace(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{ExistingInstallation: true, Connections: []platformconnections.Input{{Kind: "claudecode", Name: "Personal", Configuration: "existing", Directory: t.TempDir(), Enabled: true}}, DefaultKind: "claudecode"}); err != nil {
		t.Fatal(err)
	}
	work, err := store.Create(t.Context(), platformconnections.Input{Kind: "claudecode", Name: "Work", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefault(t.Context(), work.ID); err != nil {
		t.Fatal(err)
	}
	reg := platforms.NewRegistry()
	if err := reg.RegisterConnection(platforms.ID(work.ID), &platformtest.Fake{PlatformID: "claudecode"}); err != nil {
		t.Fatal(err)
	}
	s := New(nil, core, "127.0.0.1:0", reg, nil)
	s.connections = platformconnections.NewRouter(store)
	s.connectionStore = store
	local := &createPathHost{}
	id, base, err := s.createPlatform(t.Context(), local, "", "claudecode:anthropic/opus")
	if err != nil || id != work.ID || base != work.ID {
		t.Fatalf("model selected legacy Personal instead of saved Work: %q %q %v", id, base, err)
	}
	if id, _, err := s.createPlatform(t.Context(), local, "claudecode", "claudecode:anthropic/opus"); err != nil || id != "claudecode" {
		t.Fatalf("explicit legacy semantics changed: %q %v", id, err)
	}
	if _, _, err := s.createPlatform(t.Context(), local, "", "codex:openai/model"); err == nil {
		t.Fatal("different model namespace switched the default account")
	}
	if _, _, err := s.createPlatform(t.Context(), local, work.ID, "codex:openai/model"); err == nil {
		t.Fatal("explicit account accepted a different native model kind")
	}
	if id, _, err := s.createPlatform(t.Context(), local, "", work.ID+":anthropic/opus"); err != nil || id != work.ID {
		t.Fatalf("named model did not select its account: %q %v", id, err)
	}
	if _, _, err := s.createPlatform(t.Context(), local, work.ID, "cnx_other:anthropic/opus"); err == nil {
		t.Fatal("explicit account accepted a sibling model reference")
	}
}

func TestRemoteDefaultSupportIsPerOwnerAndOnlyExplicitUnsupportedAllowsLegacy(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	reg := platforms.NewRegistry()
	reg.Register(&platformtest.Fake{PlatformID: "r-box:claudecode"})
	if err := reg.RegisterConnection("r-box:cnx_work", &createNamedRemote{Fake: &platformtest.Fake{PlatformID: "r-box:cnx_work"}, kind: "claudecode"}); err != nil {
		t.Fatal(err)
	}
	if err := core.SetSetting(t.Context(), state.DefaultPlatformSetting, "claudecode"); err != nil {
		t.Fatal(err)
	}
	s := New(nil, core, "127.0.0.1:0", reg, nil)
	s.connections = platformconnections.NewRouter(nil)
	h := &createConnectionOwner{createPathHost: createPathHost{owner: "box"}}
	for _, tt := range []struct {
		name      string
		inventory hostsvc.ConnectionInventory
		err       error
		want      string
	}{
		{"unsupported flag", hostsvc.ConnectionInventory{Supported: false}, nil, "r-box:claudecode"},
		{"unsupported sentinel", hostsvc.ConnectionInventory{}, platforms.ErrUnsupported, "r-box:claudecode"},
		{"unsupported RPC", hostsvc.ConnectionInventory{}, status.Error(codes.Unimplemented, "old peer"), "r-box:claudecode"},
		{"disconnected", hostsvc.ConnectionInventory{}, platforms.ErrRemoteOffline, ""},
		{"transport", hostsvc.ConnectionInventory{}, status.Error(codes.Unavailable, "offline"), ""},
		{"probe failure", hostsvc.ConnectionInventory{}, errors.New("probe failed"), ""},
		{"named missing default", hostsvc.ConnectionInventory{Supported: true}, nil, ""},
		{"named disabled", hostsvc.ConnectionInventory{Supported: true, Connections: []hostsvc.ConnectionView{{ID: "cnx_work", Kind: "claudecode", IsDefault: true, Status: "ready"}}}, nil, ""},
		{"named unchecked", hostsvc.ConnectionInventory{Supported: true, Connections: []hostsvc.ConnectionView{{ID: "cnx_work", Kind: "claudecode", IsDefault: true, Enabled: true, Status: "unchecked"}}}, nil, ""},
		{"named ready", hostsvc.ConnectionInventory{Supported: true, Connections: []hostsvc.ConnectionView{{ID: "cnx_work", Kind: "claudecode", IsDefault: true, Enabled: true, Status: "ready"}}}, nil, "r-box:cnx_work"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h.inventory, h.inventoryErr = tt.inventory, tt.err
			id, _, err := s.createPlatform(t.Context(), h, "", "")
			if tt.want == "" {
				if err == nil {
					t.Fatalf("unavailable owner fell back to %q", id)
				}
			} else if err != nil || id != tt.want {
				t.Fatalf("got %q %v want %q", id, err, tt.want)
			}
		})
	}
	if id, _, err := s.createPlatform(t.Context(), h, "", "claudecode:anthropic/opus"); err != nil || id != "r-box:cnx_work" {
		t.Fatalf("named hub inferred remote legacy account: %q %v", id, err)
	}
	// A legacy-mode hub must still respect a named remote's default account.
	s.connections = nil
	h.inventory = hostsvc.ConnectionInventory{Supported: true, Connections: []hostsvc.ConnectionView{{ID: "cnx_work", Kind: "claudecode", IsDefault: true, Enabled: true, Status: "ready"}}}
	h.inventoryErr = nil
	if id, _, err := s.createPlatform(t.Context(), h, "", "claudecode:anthropic/opus"); err != nil || id != "r-box:cnx_work" {
		t.Fatalf("legacy hub guessed remote mode: %q %v", id, err)
	}
}
