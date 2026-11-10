package local

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platforms"
)

// InspectOpenCodeConnection bootstraps only an unsaved reservation's private
// runtime, then stops it. root is the reservation's runtime directory; each
// invocation allocates a fresh child directory and tmux identity. No default
// runtime, shared service, native agent or model request is selected here.
func InspectOpenCodeConnection(ctx context.Context, root string, runtime ocruntime.Runtime, inspect func(context.Context, string) (platforms.ConnectionInspection, error)) (result platforms.ConnectionInspection, err error) {
	if inspect == nil {
		return result, errors.New("OpenCode account inspector is required")
	}
	if !filepath.IsAbs(root) || runtime == nil {
		return result, errors.New("private OpenCode inspection root and runtime are required")
	}
	release, err := lockOpenCodeInspection(ctx, filepath.Clean(root))
	if err != nil {
		return result, err
	}
	defer release()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return result, errors.New("prepare private OpenCode inspection directory failed")
	}
	privateRoot, err := os.MkdirTemp(root, "inspection-")
	if err != nil {
		return result, errors.New("allocate private OpenCode inspection directory failed")
	}
	// Clone only native runtime storage; selected DB/config/data stay in place.
	if factory, ok := runtime.(interface {
		ForInspection(string) (ocruntime.Runtime, error)
	}); ok {
		runtime, err = factory.ForInspection(privateRoot)
		if err != nil {
			return result, err
		}
	}
	owned := &inspectionOwnedRuntime{Runtime: runtime}
	store := &inspectionOpenCodeStore{rows: map[string]ManagedInstance{}}
	host, err := NewOpenCodeConnection(Deps{Runtime: owned, ManagedStore: store}, privateRoot)
	if err != nil {
		return result, err
	}
	defer func() {
		// Cancel and join detached startup before testing launch ownership.
		// Otherwise cancellation could race a launch after cleanup looked.
		host.Finalize()
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		stopErr := owned.stopOwned(stopCtx)
		if stopErr != nil {
			result.Ready = false
			err = errors.Join(err, errors.New("stopping private OpenCode inspection runtime failed"))
		}
	}()
	server, err := host.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: host.machineRoot()})
	if err != nil {
		return result, errors.New("starting private OpenCode inspection runtime failed")
	}
	if !server.Launched {
		return result, errors.New("OpenCode inspection refused an existing runtime")
	}
	return inspect(ctx, server.Endpoint)
}

type inspectionOwnedRuntime struct {
	ocruntime.Runtime
	mu       sync.Mutex
	instance *ocruntime.Instance
	stopped  bool
}

func (r *inspectionOwnedRuntime) Launch(ctx context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	inst, err := r.Runtime.Launch(ctx, spec)
	if err == nil && inst != nil {
		r.mu.Lock()
		copy := *inst
		r.instance = &copy
		r.stopped = false
		r.mu.Unlock()
	}
	return inst, err
}

func (r *inspectionOwnedRuntime) Stop(ctx context.Context, inst *ocruntime.Instance) error {
	r.mu.Lock()
	own := r.instance != nil && inst != nil && r.instance.ID == inst.ID && r.instance.Endpoint == inst.Endpoint && r.instance.Kind == inst.Kind
	r.mu.Unlock()
	if !own {
		return errors.New("OpenCode inspection cannot stop an unowned runtime")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := r.Runtime.Stop(ctx, inst)
	if err == nil {
		r.mu.Lock()
		r.stopped = true
		r.mu.Unlock()
	}
	return err
}

func (r *inspectionOwnedRuntime) stopOwned(ctx context.Context) error {
	r.mu.Lock()
	if r.instance == nil || r.stopped {
		r.mu.Unlock()
		return nil
	}
	inst := *r.instance
	r.mu.Unlock()
	return r.Stop(ctx, &inst)
}

var openCodeInspectionLocks = struct {
	sync.Mutex
	entries map[string]*openCodeInspectionLock
}{entries: map[string]*openCodeInspectionLock{}}

type openCodeInspectionLock struct {
	token chan struct{}
	refs  int
}

// Serialize same-reservation migration writers. Waiters can cancel; entries
// disappear after the final waiter or owner releases.
func lockOpenCodeInspection(ctx context.Context, key string) (func(), error) {
	openCodeInspectionLocks.Lock()
	entry := openCodeInspectionLocks.entries[key]
	if entry == nil {
		entry = &openCodeInspectionLock{token: make(chan struct{}, 1)}
		openCodeInspectionLocks.entries[key] = entry
	}
	entry.refs++
	openCodeInspectionLocks.Unlock()
	drop := func() {
		openCodeInspectionLocks.Lock()
		defer openCodeInspectionLocks.Unlock()
		entry.refs--
		if entry.refs == 0 {
			delete(openCodeInspectionLocks.entries, key)
		}
	}
	select {
	case entry.token <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-entry.token
			drop()
			return nil, err
		}
		return func() { <-entry.token; drop() }, nil
	case <-ctx.Done():
		drop()
		return nil, ctx.Err()
	}
}

// Reservation runtime rows never reach durable storage or another account.
type inspectionOpenCodeStore struct {
	mu   sync.Mutex
	rows map[string]ManagedInstance
}

func (s *inspectionOpenCodeStore) Upsert(_ context.Context, root string, row ManagedInstance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[root] = row
	return nil
}

func (s *inspectionOpenCodeStore) Get(_ context.Context, root string) (ManagedInstance, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, found := s.rows[root]
	return row, found, nil
}

func (s *inspectionOpenCodeStore) Delete(_ context.Context, root string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, root)
	return nil
}

func (s *inspectionOpenCodeStore) List(_ context.Context) (map[string]ManagedInstance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.rows), nil
}
