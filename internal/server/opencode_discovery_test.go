package server

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

// The local Host publishes to the registered adapter's own discovery, so a
// reloaded server (new registry, new adapter) never sees the old routing.
func TestHostHooksPublishToRegisteredAdapter(t *testing.T) {
	empty := &Server{registry: platforms.NewRegistry()}
	if empty.openCodeDiscovery() != nil || (&Server{}).openCodeDiscovery() != nil {
		t.Fatal("no OpenCode adapter must mean no discovery")
	}
	empty.setOpenCodeMachineServer("4600") // no-op, must not panic
	empty.openCodeInventoryChanged()

	adapter := opencode.New(nil, nil, nil, ocapi.Auth{})
	registry := platforms.NewRegistry()
	registry.Register(nativeOpenCode{Adapter: adapter})
	srv := &Server{registry: registry}
	srv.setOpenCodeMachineServer("4600")
	srv.openCodeInventoryChanged()
	if got := adapter.Discovery().MachineServerPort(); got != "4600" {
		t.Fatalf("machine server = %q, want 4600", got)
	}

	reloaded := opencode.New(nil, nil, nil, ocapi.Auth{})
	if got := reloaded.Discovery().MachineServerPort(); got != "" {
		t.Fatalf("reloaded adapter inherited machine server %q", got)
	}
}
