package server

import (
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factorymod"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// useFactory mounts Factory over svc. Build the mux after calling it: one
// built earlier keeps the previous module's handlers.
func useFactory(s *Server, svc factorymod.Service) { s.factoryMod = s.newFactoryModule(svc) }

// serveFactory serves one request through Factory's mounted routes.
func serveFactory(s *Server, w http.ResponseWriter, r *http.Request) {
	mux := http.NewServeMux()
	s.factoryModule().Routes(moduleRouter{s: s, mux: mux})
	mux.ServeHTTP(w, r)
}

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
	s := &Server{stateDB: openTestStateDB(t), registry: platforms.NewRegistry()}
	useFactory(s, f)
	s.onSessionIdle("r-owner:opencode", "session")
	if f.platform != "r-owner:opencode" || f.session != "session" {
		t.Fatalf("notified %q/%q", f.platform, f.session)
	}
}
