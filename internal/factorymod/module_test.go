package factorymod

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/module"
)

type idleService struct {
	Service
	woken []module.SessionIdle
}

func (s *idleService) NotifySessionIdle(platform, session string) {
	s.woken = append(s.woken, module.SessionIdle{Platform: platform, SessionID: session})
}

func TestIdleEdgeWakesDispatchWithOwner(t *testing.T) {
	svc := &idleService{}
	bus := &module.Bus{}
	NewModule(Deps{Service: svc}).Subscribe(bus)
	bus.PublishSessionIdle(context.Background(), module.SessionIdle{Platform: "r-owner:opencode", SessionID: "s"})
	if len(svc.woken) != 1 || svc.woken[0] != (module.SessionIdle{Platform: "r-owner:opencode", SessionID: "s"}) {
		t.Fatalf("woken = %v", svc.woken)
	}
}
