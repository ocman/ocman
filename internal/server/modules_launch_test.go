package server

import (
	"context"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/module"
)

type startProbe struct{ got *context.Context }

func (startProbe) Name() string                      { return "probe" }
func (startProbe) Routes(module.Router)              {}
func (p startProbe) Start(ctx context.Context) error { *p.got = ctx; return nil }

// Starters receive the server's cancellable context: plugin processes and
// discovery bound at Start must end when the server does.
func TestStartModulesPassesCancellableContext(t *testing.T) {
	var got context.Context
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	if err := startModules(ctx, []module.Module{startProbe{&got}}, &workers); err != nil {
		t.Fatal(err)
	}
	cancel()
	workers.Wait()
	if got == nil || got.Err() == nil {
		t.Fatal("starter context was not cancelled with the server")
	}
}
