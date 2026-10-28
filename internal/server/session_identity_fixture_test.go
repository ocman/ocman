package server

import (
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

// identityHTTPFixture supplies a native integration and a durable binding.
// canonicalHTTPFixture additionally wraps it as the application does at startup.
func identityHTTPFixture(t *testing.T) (*Server, *fakePlatform, string) {
	t.Helper()
	core := statetest.Open(t, ModuleSchemas()...)
	history := statev2test.Open(t)
	p := &fakePlatform{id: "test"}
	registry := platforms.NewRegistry()
	registry.Register(&historyTestPlatform{fakePlatform: p})
	s := New(nil, core, "", registry, nil).WithConversationHistory(history, "owner")
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "source"}
	if _, err := catalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "native", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	row, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "source", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	return s, p, row.ID
}
