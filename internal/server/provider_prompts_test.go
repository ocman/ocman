package server

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// promptSource is a provider adapter stand-in with a headless event stream.
type promptSource struct {
	*fakePlatform
	handler func(providerprotocol.Event)
	watched chan struct{}
}

func (p *promptSource) SetEventHandler(h func(providerprotocol.Event)) { p.handler = h }
func (p *promptSource) WatchEvents(ctx context.Context) error {
	close(p.watched)
	<-ctx.Done()
	return ctx.Err()
}

// The prompt pipeline is installed before a provider's headless stream
// starts, and non-OpenCode sessions resolve their directory for the judge.
func TestWatchProviderPrompts(t *testing.T) {
	session := db.Session{ID: "c1", Directory: "/proj"}
	src := &promptSource{watched: make(chan struct{}), fakePlatform: &fakePlatform{id: "claudecode", sessions: []db.Session{session},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &session}, nil
		}}}
	reg := platforms.NewRegistry()
	reg.Register(src)
	reg.Register(&fakePlatform{id: "opencode"})
	s := &Server{registry: reg}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.watchProviderPrompts(ctx)
	<-src.watched
	if src.handler == nil {
		t.Fatal("handler not installed before the stream started")
	}
	src.handler(providerprotocol.Event{Type: "question.asked", Properties: []byte(`{"id":"que_1","sessionID":"c1"}`)})
	src.handler(providerprotocol.Event{Type: "permission.replied", Properties: []byte(`{"sessionID":"c1","requestID":"per_1","reply":"once"}`)})

	if dir, err := s.autoApproveSessionDir("c1"); err != nil || dir != "/proj" {
		t.Fatalf("dir = %q %v", dir, err)
	}
	if _, err := s.autoApproveSessionDir("missing"); err == nil {
		t.Fatal("unknown session must fail")
	}
}

// The Claude judge is the fallback only when Claude Code is enabled.
func TestClaudeFallbackJudgeWiring(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "claudecode"})
	s := &Server{registry: reg}
	if s.aaSvc() == nil {
		t.Fatal("no service")
	}
}
