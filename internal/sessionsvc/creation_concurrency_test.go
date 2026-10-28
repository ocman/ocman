package sessionsvc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type overlappingCreationNative struct {
	*identityPlatform
	mu                sync.Mutex
	rows              map[string]db.Session
	catalogue         *sessioncatalog.Store
	creates           atomic.Int32
	configurations    atomic.Int32
	disposals         atomic.Int32
	disposedPublished atomic.Bool
	configure         func(context.Context, int32) error
}

func (*overlappingCreationNative) PlannedSessionID(id string) string {
	return "ses_" + strings.TrimPrefix(id, "ocm_")
}

func (p *overlappingCreationNative) CreateSession(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	p.creates.Add(1)
	p.mu.Lock()
	p.rows[req.NativeID] = db.Session{ID: req.NativeID, Directory: req.Directory}
	p.mu.Unlock()
	return &platforms.CreateSessionResponse{ID: req.NativeID}, nil
}

func (p *overlappingCreationNative) SessionIdentityMetadata(_ context.Context, id string) (*db.Session, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	row, ok := p.rows[id]
	if !ok {
		return nil, platforms.ErrNotFound
	}
	return &row, nil
}

func (p *overlappingCreationNative) SetPermissionRules(ctx context.Context, _ platforms.SetPermissionRulesRequest) error {
	return p.configure(ctx, p.configurations.Add(1))
}

func (p *overlappingCreationNative) DisposeSession(ctx context.Context, req platforms.DisposeSessionRequest) error {
	p.disposals.Add(1)
	row, err := p.catalogue.FindBinding(ctx, sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "source", NativeID: req.SessionID})
	if err == nil && row.CreationState == sessioncatalog.Ready {
		p.disposedPublished.Store(true)
	}
	p.mu.Lock()
	delete(p.rows, req.SessionID)
	p.mu.Unlock()
	return nil
}

func overlappingCreationService(t *testing.T) (*Service, *overlappingCreationNative) {
	t.Helper()
	svc, base, catalogue, _ := identityService(t)
	native := &overlappingCreationNative{identityPlatform: base, catalogue: catalogue, rows: map[string]db.Session{}}
	adapter, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	svc.registry = &fakeRegistry{byID: map[platforms.ID]platforms.Platform{adapter.ID(): adapter}}
	return svc, native
}

type creationResult struct {
	response *platforms.CreateSessionResponse
	err      error
}

func TestOverlappingRecoveryCannotDisposePublishedCreation(t *testing.T) {
	for _, phase := range []string{"configuration", "application linkage"} {
		t.Run(phase, func(t *testing.T) {
			svc, native := overlappingCreationService(t)
			ctx, cancel := context.WithCancel(t.Context())
			var workers sync.WaitGroup
			defer workers.Wait()
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			wait := func() error {
				close(entered)
				select {
				case <-release:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			native.configure = func(_ context.Context, attempt int32) error {
				if phase == "configuration" && attempt == 1 {
					return wait()
				}
				return nil
			}
			var links, publications atomic.Int32
			link := func(string) error {
				if links.Add(1) == 1 && phase == "application linkage" {
					return wait()
				}
				return nil
			}
			svc.hooks.SessionCreated = func(CreatedSession) { publications.Add(1) }
			request := identityRequest()
			first, retry := make(chan creationResult, 1), make(chan creationResult, 1)
			workers.Go(func() {
				r, err := svc.CreateRoutine(ctx, "opencode", request, nil, "routine", link)
				first <- creationResult{r, err}
			})
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("original creation never reached the controlled phase")
			}
			retryCtx, retryCancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
			defer retryCancel()
			workers.Go(func() {
				r, err := svc.CreateRoutine(retryCtx, "opencode", request, nil, "routine", link)
				retry <- creationResult{r, err}
			})
			second := <-retry
			if second.err == nil {
				cancel()
				<-first
				t.Fatalf("overlapping recovery published before the original completed; cancellation disposed published session=%v", native.disposedPublished.Load())
			}
			if !errors.Is(second.err, context.DeadlineExceeded) {
				t.Fatalf("waiting retry did not retain cancellation: %v", second.err)
			}
			close(release)
			original := <-first
			if original.err != nil || original.response == nil {
				t.Fatalf("cancelled waiter damaged original creation: %v", original.err)
			}
			if native.creates.Load() != 1 || native.configurations.Load() != 1 || native.disposals.Load() != 0 || links.Load() != 1 || publications.Load() != 1 {
				t.Fatalf("keyed operation repeated side effects: creates=%d configurations=%d disposals=%d links=%d publications=%d", native.creates.Load(), native.configurations.Load(), native.disposals.Load(), links.Load(), publications.Load())
			}
			replayed, err := svc.CreateRoutine(t.Context(), "opencode", request, nil, "routine", link)
			if err != nil || !replayed.Replayed || replayed.ID != original.response.ID || native.configurations.Load() != 1 {
				t.Fatalf("receipt was not reread after acquiring ownership: %+v %v", replayed, err)
			}
		})
	}
}

func TestIndependentCreationKeyDoesNotWaitForAnotherConfiguration(t *testing.T) {
	svc, native := overlappingCreationService(t)
	entered, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	native.configure = func(ctx context.Context, attempt int32) error {
		if attempt == 1 {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	first := make(chan creationResult, 1)
	workers.Go(func() {
		r, err := svc.CreateConfigured(ctx, "opencode", identityRequest(), []platforms.PermissionRule{})
		first <- creationResult{r, err}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first creation did not enter configuration")
	}
	request := identityRequest()
	request.Creation.RequestKey = "independent"
	independentCtx, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if _, err := svc.CreateConfigured(independentCtx, "opencode", request, []platforms.PermissionRule{}); err != nil {
		t.Fatalf("unrelated request waited behind another creation: %v", err)
	}
	close(release)
	if result := <-first; result.err != nil {
		t.Fatal(result.err)
	}
	if native.creates.Load() != 2 || native.disposals.Load() != 0 {
		t.Fatal("independent requests shared an allocation or cleanup")
	}
}
