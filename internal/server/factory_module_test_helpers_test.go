package server

import (
	"testing"

	"github.com/ocman/ocman/internal/factorymod"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

// useFactory mounts Factory over svc. Build the mux after calling it: one
// built earlier keeps the previous module's handlers.
func useFactory(s *Server, svc factorymod.Service) { s.factoryMod.set(s.newFactoryModule(svc)) }

type factoryIdleNotifier struct {
	factorymod.Service
	platform, session string
}

func (f *factoryIdleNotifier) NotifySessionIdle(platform, session string) {
	f.platform, f.session = platform, session
}

// The idle edge reaches Factory through the module's typed bus
// subscription, owner-qualified, not a duck-typed server call.
func TestSessionIdleNotifiesFactoryWithOwner(t *testing.T) {
	f := &factoryIdleNotifier{}
	s := &Server{stateDB: statetest.Open(t, ModuleSchemas()...), registry: platforms.NewRegistry()}
	useFactory(s, f)
	s.onSessionIdle("r-owner:opencode", "session")
	if f.platform != "r-owner:opencode" || f.session != "session" {
		t.Fatalf("notified %q/%q", f.platform, f.session)
	}
}
