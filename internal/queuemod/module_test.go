package queuemod

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type lifecyclePlatform struct {
	platforms.Platform
	status db.SessionStatus
}

func (p lifecyclePlatform) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	status := p.status
	if status == "" {
		status = db.StatusBusy
	}
	return &platforms.SessionLifecycle{Status: status}, nil
}

func TestShutdownSweptSendCommitsReceipt(t *testing.T) {
	store, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	entered, release := make(chan struct{}), make(chan struct{})
	m := NewModule(Deps{
		Store: store, Adapter: func(context.Context, string, string) (platforms.Platform, bool) {
			return lifecyclePlatform{status: db.StatusDone}, true
		},
		SendNow: func(ctx context.Context, _ string, _ platforms.SendMessageRequest) error {
			close(entered)
			<-release
			if ctx.Err() != nil {
				t.Error("admitted sweep send was cancelled")
			}
			return nil
		},
		Broadcast: func(string, []byte) {},
	})
	if err := m.Service().Enqueue(t.Context(), "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "held"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	<-entered
	cancel()
	close(release)
	<-done
	if rows, err := m.Service().List(t.Context(), "opencode", "s1"); err != nil || len(rows) != 0 {
		t.Fatalf("accepted send remains replayable: %+v %v", rows, err)
	}
}
func (lifecyclePlatform) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	return &platforms.SessionDetail{}, nil
}

func TestShutdownJoinsAdmittedSendAndPreservesReceipt(t *testing.T) {
	store, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	entered, release := make(chan struct{}), make(chan struct{})
	m := NewModule(Deps{
		Store: store, Sessions: func() Sessions { return nil },
		Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return lifecyclePlatform{}, true },
		SendNow: func(ctx context.Context, owner string, req platforms.SendMessageRequest) error {
			close(entered)
			<-release
			if ctx.Err() != nil {
				t.Error("shutdown cancelled an admitted send before its receipt")
			}
			if owner != "r-owner:claudecode" || req.Message != "held" || req.Agent != "plan" || req.Model != "p/m" || len(req.Images) != 1 {
				t.Errorf("payload changed: %s %+v", owner, req)
			}
			return nil
		},
		Broadcast: func(string, []byte) {},
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Service().Enqueue(t.Context(), "r-owner:claudecode", true, platforms.SendMessageRequest{
		SessionID: "same", Message: "held", Agent: "plan", Model: "p/m", Images: []platforms.ImageAttachment{{URL: "data:image/png;base64,a", Mime: "image/png"}},
	}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	m.OnSessionIdle("r-owner:claudecode", "same")
	<-entered
	cancel()
	select {
	case <-done:
		t.Fatal("Run returned while an admitted send was still active")
	default:
	}
	close(release)
	<-done
	if rows, err := m.Service().List(t.Context(), "r-owner:claudecode", "same"); err != nil || len(rows) != 0 {
		t.Fatalf("receipt not committed: %+v %v", rows, err)
	}
	// Shutdown rejects later edges rather than starting work after DB teardown.
	m.OnSessionIdle("r-owner:claudecode", "same")
	if err := m.Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithoutStoreAndRecovery(t *testing.T) {
	m := NewModule(Deps{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	m.Run(ctx)
	m.OnSessionIdle("opencode", "s")
	runWithRecover("test", func() { panic("test panic") })
}

func TestSettingsAndProjectModelInvalidation(t *testing.T) {
	store, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := NewModule(Deps{Store: store})
	if m.Name() != "queue" {
		t.Fatal(m.Name())
	}
	for _, body := range []string{`{`, `{"patienceMinutes":0,"fallbackMinutes":1}`, `{"patienceMinutes":1,"fallbackMinutes":1441}`} {
		rec := httptest.NewRecorder()
		m.Settings(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("invalid %s: %d", body, rec.Code)
		}
	}
	if err := store.SetProjectSettings(t.Context(), "/src/repo", state.ProjectSettings{Models: []string{"a/x"}}); err != nil {
		t.Fatal(err)
	}
	models, _ := m.ProjectModelList(t.Context(), "/src/repo")
	if len(models) != 1 || models[0] != "a/x" {
		t.Fatal(models)
	}
	if err := store.SetProjectSettings(t.Context(), "/src/repo", state.ProjectSettings{Models: []string{"b/y"}, Off: true}); err != nil {
		t.Fatal(err)
	}
	m.ForgetProjectModels("/src/repo")
	models, off := m.ProjectModelList(t.Context(), "/src/repo")
	if len(models) != 1 || models[0] != "b/y" || !off {
		t.Fatalf("stale models %v off=%v", models, off)
	}
	_ = store.Close()
	m.ForgetProjectModels("/src/repo")
	if models, _ := m.ProjectModelList(t.Context(), "/src/repo"); models != nil {
		t.Fatal(models)
	}
	if models, off := NewModule(Deps{}).ProjectModelList(t.Context(), "/src/repo"); models != nil || off {
		t.Fatalf("absent store: %v %v", models, off)
	}
}

func TestPreparationCancellationDoesNotSpendQueueRetries(t *testing.T) {
	for _, preparationErr := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(preparationErr.Error(), func(t *testing.T) {
			store, err := state.Open(statetest.Path(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			m := NewModule(Deps{
				Store: store, Adapter: func(context.Context, string, string) (platforms.Platform, bool) { return lifecyclePlatform{}, true },
				PrepareSend: func(context.Context, string, platforms.SendMessageRequest) (func(context.Context) error, error) {
					return nil, errors.Join(errors.New("owner preparation"), preparationErr)
				},
				Broadcast: func(string, []byte) {},
			})
			if err := m.Service().Enqueue(t.Context(), "opencode", true, platforms.SendMessageRequest{SessionID: "s", Message: "held"}); err != nil {
				t.Fatal(err)
			}
			m.Service().Flush(t.Context(), "opencode", "s")
			if rows, err := m.Service().List(t.Context(), "opencode", "s"); err != nil || len(rows) != 1 || rows[0].Attempts != 0 {
				t.Fatalf("preparation spent retry budget: %+v %v", rows, err)
			}
		})
	}
}
