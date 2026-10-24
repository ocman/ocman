package pluginsmod

import (
	"context"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
)

type shutdownSessionReader struct {
	platforms.Platform
	entered  chan struct{}
	finished chan struct{}
}

type shutdownSessionCreator struct {
	Sessions
	entered, cancelled, release chan struct{}
}

func (s *shutdownSessionCreator) CreateConfigured(ctx context.Context, _ string, _ platforms.CreateSessionRequest, _ []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
	close(s.entered)
	<-ctx.Done()
	close(s.cancelled)
	<-s.release // cancellation alone must not let Run abandon this work
	return nil, ctx.Err()
}

func TestShutdownCancelsAndJoinsAdmittedRun(t *testing.T) {
	db := openStore(t)
	creator := &shutdownSessionCreator{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	m := NewModule(Deps{Store: db, Local: func() hostsvc.Host { return stubHost{} }, Sessions: creator})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	m.Bind(ctx)
	config := plugins.Config{Project: t.TempDir()}
	result := make(chan error, 1)
	go func() {
		_, err := m.startSession(t.Context(), "p", config, plugins.SessionRunParams{Prompt: "once"}, config.Project, "op")
		result <- err
	}()
	<-creator.entered
	done := make(chan struct{})
	// No mapping exists yet, so Run's reconciliation cannot touch the stub.
	go func() { m.Run(ctx); close(done) }()
	cancel()
	select {
	case <-creator.cancelled:
	case <-time.After(time.Second):
		t.Fatal("run did not inherit cancellation")
	}
	select {
	case <-done:
		t.Fatal("shutdown abandoned admitted run")
	default:
	}
	close(creator.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join run")
	}
	if err := <-result; err == nil {
		t.Fatal("cancelled creation succeeded")
	}
	_, claimed, err := m.plugins.reserveSessionRun(t.Context(), "p", "op", config.Project, plugins.SessionRunParams{Prompt: "once"})
	if claimed || plugins.RequestError(err).Category != plugins.ErrorCancelled {
		t.Fatalf("cancelled result was not durable: claimed=%v err=%v", claimed, err)
	}
}

func (s *shutdownSessionReader) Session(ctx context.Context, _ string, _, _ int) (*platforms.SessionDetail, error) {
	close(s.entered)
	<-ctx.Done()
	close(s.finished)
	return nil, ctx.Err()
}

func TestSessionIdleReadEndsWithModuleLifetime(t *testing.T) {
	db := openStore(t)
	reader := &shutdownSessionReader{entered: make(chan struct{}), finished: make(chan struct{})}
	m := NewModule(Deps{Store: db, Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return reader, true }})
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	m.Bind(ctx)
	if err := m.plugins.LinkPluginSession(t.Context(), "opencode", "ses-1", "p"); err != nil {
		t.Fatal(err)
	}
	bus := &module.Bus{}
	m.Subscribe(bus)
	bus.PublishSessionIdle(t.Context(), module.SessionIdle{Platform: "opencode", SessionID: "ses-1"})
	select {
	case <-reader.entered:
	case <-time.After(time.Second):
		t.Fatal("completion read did not start")
	}
	cancel()
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("module did not join cancelled read")
	}
	select {
	case <-reader.finished:
	default:
		t.Fatal("module returned before read finished")
	}
	// A late idle edge and pump kick are rejected after shutdown, not launched
	// on Background against a database the caller may already have closed.
	bus.PublishSessionIdle(t.Context(), module.SessionIdle{Platform: "opencode", SessionID: "ses-1"})
	m.sessionOutbox.kick()
	if m.workers.Go(func(context.Context) { t.Error("late worker ran") }) {
		t.Fatal("shutdown admitted a worker")
	}
	if _, err := m.startSession(t.Context(), "p", plugins.Config{}, plugins.SessionRunParams{}, "", "late"); err == nil {
		t.Fatal("late run admitted")
	}
}

func TestSessionWorkersCannotInitializeAfterShutdown(t *testing.T) {
	m := NewModule(Deps{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m.Run(ctx)
	// Run initializes and closes even unused dispatchers. A late lookup must
	// return those same closed instances, including during backend reload.
	jobs, deliveries := m.sessionJobs, m.sessionDeliveries
	if jobs == nil || deliveries == nil || m.sessionWorker() != jobs || m.sessionDeliveryWorker() != deliveries {
		t.Fatal("late initialization created unjoined workers")
	}
}

func TestRunStopsWorkersBoundBeforeRunner(t *testing.T) {
	m := NewModule(Deps{})
	bound, release := context.WithCancel(t.Context())
	defer release()
	m.Bind(bound)
	entered := make(chan struct{})
	m.workers.Go(func(ctx context.Context) { close(entered); <-ctx.Done() })
	<-entered
	run, cancel := context.WithCancel(t.Context())
	cancel()
	done := make(chan struct{})
	go func() { m.Run(run); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("runner shutdown did not stop workers bound during preparation")
		release()
		<-done
	}
}
