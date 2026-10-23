package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

type preparationLifetimeModule struct {
	resourceModule
	stopped chan struct{}
}

func (m *preparationLifetimeModule) Run(ctx context.Context) {
	<-ctx.Done()
	close(m.stopped)
}

func startPreparationTestServer(t *testing.T, srv *Server) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	t.Setenv("OCMAN_PLUGIN_DIR", t.TempDir())
	ctx, cancel := context.WithCancel(t.Context())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.StartOnListener(ctx, ln) }()
	return "http://" + ln.Addr().String(), cancel, done
}

func preparationHTTP(t *testing.T, url string, status int) {
	t.Helper()
	client := &http.Client{Timeout: 300 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode != status {
				t.Fatalf("%s: got HTTP %d, want %d", url, resp.StatusCode, status)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func joinPreparationTestServer(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not join preparation and session workers")
	}
}

func TestHistoricalPreparationDoesNotBlockHTTPOrTaskStatus(t *testing.T) {
	srv := testServer(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	srv.WithSessionPreparation(func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	resource := &preparationLifetimeModule{stopped: make(chan struct{})}
	srv.registry.Register(&startupModulePlatform{fakePlatform: &fakePlatform{}, mod: resource})
	url, cancel, done := startPreparationTestServer(t, srv)
	defer func() { cancel(); unblock(); joinPreparationTestServer(t, done) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("historical work did not start")
	}
	preparationHTTP(t, url+"/api/auth/me", http.StatusOK)
	preparationHTTP(t, url+"/api/background-tasks", http.StatusOK)
	preparationHTTP(t, url+"/api/sessions", http.StatusServiceUnavailable)
	preparationHTTP(t, url+"/mcp", http.StatusServiceUnavailable)
	if resource.opened.Load() != 0 {
		t.Fatal("session module started while legacy keys were being rewritten")
	}
	tasks, err := srv.taskManager().List(t.Context(), tasksvc.Running, "session.preparation", 10)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("preparation is not a visible background task: %+v %v", tasks, err)
	}
	unblock()
	select {
	case <-srv.sessionPreparation.readyCh:
	case <-time.After(5 * time.Second):
		t.Fatal("session admission did not open after preparation")
	}
	preparationHTTP(t, url+"/api/sessions", http.StatusOK)
	if resource.opened.Load() != 1 {
		t.Fatal("session module did not start exactly once")
	}
	select {
	case <-resource.stopped:
		t.Fatal("session worker inherited the completed task's cancelled context")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPreparationShutdownJoinsAdmittedMigration(t *testing.T) {
	srv := testServer(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	srv.WithSessionPreparation(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		<-release
		return ctx.Err()
	})
	url, cancel, done := startPreparationTestServer(t, srv)
	defer cancel()
	preparationHTTP(t, url+"/api/auth/me", http.StatusOK)
	<-started
	cancel()
	select {
	case err := <-done:
		t.Fatalf("server returned before migration released its databases: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	unblock()
	joinPreparationTestServer(t, done)
}

func TestPreparationFailureKeepsSessionAdmissionClosed(t *testing.T) {
	srv := testServer(t).WithSessionPreparation(func(context.Context) error { return errors.New("identity collision") })
	url, cancel, done := startPreparationTestServer(t, srv)
	defer func() { cancel(); joinPreparationTestServer(t, done) }()
	preparationHTTP(t, url+"/api/auth/me", http.StatusOK)
	preparationHTTP(t, url+"/api/sessions", http.StatusServiceUnavailable)
	for deadline := time.Now().Add(5 * time.Second); ; {
		tasks, err := srv.taskManager().List(t.Context(), tasksvc.Failed, "session.preparation", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) == 1 {
			if tasks[0].Error != "identity collision" || srv.SessionsReady() {
				t.Fatalf("failed preparation lost failure or opened admission: %+v", tasks)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("preparation failure was not persisted")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDedicatedMCPFailsClosedUntilPrepared(t *testing.T) {
	srv := &Server{}
	srv.WithSessionPreparation(func(context.Context) error { return nil })
	h := srv.withMCPReadiness(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for _, ready := range []bool{false, true} {
		srv.sessionPreparation.prepared.Store(ready)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/hook", nil))
		want := http.StatusServiceUnavailable
		if ready {
			want = http.StatusNoContent
		}
		if w.Code != want {
			t.Fatalf("ready=%v: got %d, want %d", ready, w.Code, want)
		}
	}
}
