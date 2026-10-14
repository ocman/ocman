package pluginsmod

import (
	"context"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Start binds the lifetime context and runs discovery; Stop unbinds it, so
// a later sync starts nothing; Run returns once its context is cancelled.
func TestModuleLifecycle(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	m := NewModule(Deps{Store: openStore(t)})
	ctx, cancel := context.WithCancel(t.Context())
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if m.ctx != ctx {
		t.Fatal("Start did not bind the lifetime context")
	}
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	m.Stop()
	if m.ctx != nil || m.Running() != 0 {
		t.Fatal("Stop left the module bound")
	}
}

// A discovery failure is logged, never fatal to startup.
func TestModuleStartToleratesDiscoveryFailure(t *testing.T) {
	t.Setenv("OCMAN_PLUGIN_DIR", "")
	t.Setenv("HOME", "")
	if err := NewModule(Deps{}).Start(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// The idle reaction runs on its own goroutine, where the bus cannot recover
// a panic; the module's own recovery must keep the process alive.
func TestModuleIdleReplyRecoversPanic(t *testing.T) {
	store := openStore(t)
	key := state.PluginConversationKey{PluginID: "org.example.chatops", AccountID: "T1", ThreadID: "C1:1"}
	if _, _, err := store.LinkPluginConversation(t.Context(), key, state.PluginConversationSession{PlatformID: "opencode", SessionID: "ses"}); err != nil {
		t.Fatal(err)
	}
	reached := make(chan struct{})
	m := NewModule(Deps{Store: store, Adapter: func(context.Context, string, string) (platforms.Platform, bool) {
		close(reached)
		panic("adapter exploded")
	}})
	bus := &module.Bus{}
	m.Subscribe(bus)
	bus.PublishSessionIdle(t.Context(), module.SessionIdle{Platform: "opencode", SessionID: "ses"})
	select {
	case <-reached:
	case <-time.After(5 * time.Second):
		t.Fatal("idle edge never reached the linked conversation")
	}
	time.Sleep(50 * time.Millisecond) // an unrecovered panic would abort the test binary here
}

// The idle reply runs under the module's lifetime and Run joins it, so it
// never reads state.db after shutdown.
func TestModuleRunJoinsIdleReply(t *testing.T) {
	store := openStore(t)
	key := state.PluginConversationKey{PluginID: "org.example.chatops", AccountID: "T1", ThreadID: "C1:1"}
	if _, _, err := store.LinkPluginConversation(t.Context(), key, state.PluginConversationSession{PlatformID: "opencode", SessionID: "ses"}); err != nil {
		t.Fatal(err)
	}
	entered, returned := make(chan struct{}), make(chan struct{})
	m := NewModule(Deps{Store: store, Adapter: func(ctx context.Context, _, _ string) (platforms.Platform, bool) {
		close(entered)
		<-ctx.Done()
		close(returned)
		return nil, false
	}})
	bus := &module.Bus{}
	m.Subscribe(bus)
	ctx, cancel := context.WithCancel(t.Context())
	m.Bind(ctx)
	ran := make(chan struct{})
	go func() { m.Run(ctx); close(ran) }()
	bus.PublishSessionIdle(t.Context(), module.SessionIdle{Platform: "opencode", SessionID: "ses"})
	<-entered
	cancel()
	<-ran
	select {
	case <-returned:
	default:
		t.Fatal("Run returned while the idle reply still ran")
	}
}
