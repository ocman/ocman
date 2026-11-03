package factorymod

import (
	"context"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
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

type lifecycleService struct {
	Service
	starts, stops, resumes int
}

func (s *lifecycleService) OnAttention(func(context.Context, string, []string)) {}
func (s *lifecycleService) Start(context.Context) error                         { s.starts++; return nil }
func (s *lifecycleService) StopDispatch()                                       { s.stops++ }
func (s *lifecycleService) ResumeDispatch()                                     { s.resumes++ }
func (s *lifecycleService) Close()                                              {}

func TestDispatchFollowsEnabled(t *testing.T) {
	enabled := false
	svc := &lifecycleService{}
	m := NewModule(Deps{Service: svc, Enabled: func(context.Context) bool { return enabled }})
	enabled = true
	if err := m.SyncEnabled(t.Context()); err != nil || svc.starts != 0 {
		t.Fatalf("started before launch: %+v %v", svc, err)
	}
	enabled = false
	if err := m.Launch(t.Context()); err != nil || svc.starts != 0 {
		t.Fatalf("disabled launch started: %+v %v", svc, err)
	}
	for _, on := range []bool{true, false, true} {
		enabled = on
		if err := m.SyncEnabled(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if svc.starts != 1 || svc.resumes != 1 || svc.stops != 2 {
		t.Fatalf("lifecycle = %+v", svc)
	}
	if tools := m.FilterTools(t.Context(), []mcplib.Tool{{Name: "factory"}, {Name: "inbox"}}); len(tools) != 2 {
		t.Fatalf("enabled tools filtered: %v", tools)
	}
	enabled = false
	if tools := m.FilterTools(t.Context(), []mcplib.Tool{{Name: "factory"}, {Name: "factory_control"}, {Name: "inbox"}}); len(tools) != 1 || tools[0].Name != "inbox" {
		t.Fatalf("disabled tools = %v", tools)
	}
	m.Close()
	enabled = true
	if err := m.SyncEnabled(t.Context()); err != nil || svc.resumes != 1 {
		t.Fatalf("closed module resumed: %+v %v", svc, err)
	}
}
