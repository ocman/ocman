package server

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// projectsFetchRecorder counts db.GetProjects invocations and the peak
// number that ran at the same time, so tests can assert FR-8's "at most
// one project refresh runs at a time per owner".
type projectsFetchRecorder struct {
	mu          sync.Mutex
	calls       int
	inFlight    int
	maxInFlight int
}

func (r *projectsFetchRecorder) enter() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.inFlight++
	if r.inFlight > r.maxInFlight {
		r.maxInFlight = r.inFlight
	}
	return r.calls
}

func (r *projectsFetchRecorder) leave() {
	r.mu.Lock()
	r.inFlight--
	r.mu.Unlock()
}

func (r *projectsFetchRecorder) counts() (calls, maxInFlight int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, r.maxInFlight
}

// waitProjectsDirty blocks until a concurrent refresh request has been
// registered on the running cycle. The worker clears the dirty flag when
// it starts an iteration, so observing it set again means a later
// request arrived — a deterministic handshake that needs no sleeps.
func waitProjectsDirty(t *testing.T, srv *Server) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		srv.projects.mu.RLock()
		dirty := srv.projects.dirty
		srv.projects.mu.RUnlock()
		if dirty {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Error("timed out waiting for a concurrent refresh request to be recorded")
}

// TestRefreshProjectsIndex_ConcurrentCallersShareOneRefresh covers FR-8:
// ten concurrent callers must share one refresh cycle instead of firing
// ten multi-second GetProjects scans at the read pool.
func TestRefreshProjectsIndex_ConcurrentCallersShareOneRefresh(t *testing.T) {
	srv := testServer(t)

	var rec projectsFetchRecorder
	joined := make(chan struct{})
	release := make(chan struct{})

	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		n := rec.enter()
		defer rec.leave()
		if n == 1 {
			close(joined)
			<-release
		}
		return []db.ProjectStats{{Directory: "/repo"}}, nil
	}

	const callers = 10
	var wg sync.WaitGroup
	errs := make([]error, callers)

	wg.Add(1)
	go func() {
		defer wg.Done()
		errs[0] = srv.refreshProjectsIndex()
	}()

	// Only start the other nine once the first refresh is inside the
	// query, so they must all join a running cycle.
	<-joined
	var arrived atomic.Int32
	for i := 1; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			arrived.Add(1)
			errs[i] = srv.refreshProjectsIndex()
		}(i)
	}
	for arrived.Load() < callers-1 {
		time.Sleep(time.Millisecond)
	}
	waitProjectsDirty(t, srv)
	time.Sleep(20 * time.Millisecond) // let the last joiners reach the lock
	close(release)
	wg.Wait()

	calls, maxInFlight := rec.counts()
	if maxInFlight != 1 {
		t.Errorf("concurrent GetProjects scans = %d, want 1 at a time", maxInFlight)
	}
	// One running cycle plus at most one dirty follow-up.
	if calls < 1 || calls > 2 {
		t.Errorf("GetProjects calls = %d, want 1 or 2 for %d concurrent callers", calls, callers)
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if _, loaded := srv.projectsSnapshot(); !loaded {
		t.Error("expected the snapshot to be loaded after the shared refresh")
	}
}

// TestRefreshProjectsIndex_EventDuringRunRunsOneFollowUp covers FR-8: a
// request arriving while a refresh runs is never dropped by completion
// of the older refresh; it produces exactly one follow-up.
func TestRefreshProjectsIndex_EventDuringRunRunsOneFollowUp(t *testing.T) {
	srv := testServer(t)

	var rec projectsFetchRecorder
	var followUp sync.WaitGroup
	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		n := rec.enter()
		defer rec.leave()
		if n == 1 {
			followUp.Add(1)
			go func() {
				defer followUp.Done()
				if err := srv.refreshProjectsIndex(); err != nil {
					t.Errorf("follow-up requester: %v", err)
				}
			}()
			waitProjectsDirty(t, srv)
		}
		return []db.ProjectStats{{Directory: "/repo"}}, nil
	}

	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("refreshProjectsIndex: %v", err)
	}
	followUp.Wait()

	calls, maxInFlight := rec.counts()
	if calls != 2 {
		t.Errorf("GetProjects calls = %d, want 2 (initial + one dirty follow-up)", calls)
	}
	if maxInFlight != 1 {
		t.Errorf("concurrent GetProjects scans = %d, want 1 at a time", maxInFlight)
	}
	srv.projects.mu.RLock()
	dirty, running := srv.projects.dirty, srv.projects.running
	srv.projects.mu.RUnlock()
	if dirty || running {
		t.Errorf("after a settled cycle dirty=%v running=%v, want false/false", dirty, running)
	}
}

// TestRefreshProjectsIndex_EventDuringFollowUpRunsOneMore covers FR-8's
// "events arriving during the follow-up apply the same rule".
func TestRefreshProjectsIndex_EventDuringFollowUpRunsOneMore(t *testing.T) {
	srv := testServer(t)

	var rec projectsFetchRecorder
	var requesters sync.WaitGroup
	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		n := rec.enter()
		defer rec.leave()
		if n <= 2 {
			requesters.Add(1)
			go func() {
				defer requesters.Done()
				if err := srv.refreshProjectsIndex(); err != nil {
					t.Errorf("follow-up requester: %v", err)
				}
			}()
			waitProjectsDirty(t, srv)
		}
		return []db.ProjectStats{{Directory: "/repo"}}, nil
	}

	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("refreshProjectsIndex: %v", err)
	}
	requesters.Wait()

	calls, maxInFlight := rec.counts()
	if calls != 3 {
		t.Errorf("GetProjects calls = %d, want 3 (initial + two chained follow-ups)", calls)
	}
	if maxInFlight != 1 {
		t.Errorf("concurrent GetProjects scans = %d, want 1 at a time", maxInFlight)
	}
}

// TestRefreshProjectsIndex_FailureRetainsSnapshotAndDirty covers FR-8:
// a failed refresh keeps the previous snapshot, reports the error to
// every waiter, keeps the dirty indication, and stays retryable without
// spinning.
func TestRefreshProjectsIndex_FailureRetainsSnapshotAndDirty(t *testing.T) {
	srv := testServer(t)

	var rec projectsFetchRecorder
	wantErr := errors.New("boom")
	var mode atomic.Value // "ok" | "fail"
	mode.Store("ok")
	var waiters sync.WaitGroup
	waiterErrs := make([]error, 3)

	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		n := rec.enter()
		defer rec.leave()
		if mode.Load() == "fail" {
			if n == 2 {
				// Three joiners plus one dirty request while the
				// failing scan is running.
				for i := range waiterErrs {
					waiters.Add(1)
					go func(i int) {
						defer waiters.Done()
						waiterErrs[i] = srv.refreshProjectsIndex()
					}(i)
				}
				waitProjectsDirty(t, srv)
				time.Sleep(20 * time.Millisecond)
			}
			return nil, wantErr
		}
		return []db.ProjectStats{{Directory: "/good"}}, nil
	}

	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("seeding refresh: %v", err)
	}

	mode.Store("fail")
	if err := srv.refreshProjectsIndex(); !errors.Is(err, wantErr) {
		t.Fatalf("failed refresh error = %v, want %v", err, wantErr)
	}
	waiters.Wait()
	for i, err := range waiterErrs {
		if !errors.Is(err, wantErr) {
			t.Errorf("waiter %d error = %v, want %v", i, err, wantErr)
		}
	}

	// Previous snapshot retained.
	snap, loaded := srv.projectsSnapshot()
	if !loaded || len(snap) != 1 || snap[0].Directory != "/good" {
		t.Errorf("after a failed refresh snapshot = %+v loaded=%v, want the previous snapshot", snap, loaded)
	}

	// The dirty indication survives the failure, and the failure did
	// not spin: the failing cycle ran exactly one query.
	srv.projects.mu.RLock()
	dirty, running := srv.projects.dirty, srv.projects.running
	srv.projects.mu.RUnlock()
	if !dirty {
		t.Error("dirty indication was discarded by a failed refresh")
	}
	if running {
		t.Error("refresh still marked running after failure")
	}
	callsAfterFailure, maxInFlight := rec.counts()
	if callsAfterFailure != 2 {
		t.Errorf("GetProjects calls = %d, want 2 (one seed + one failing scan, no spin)", callsAfterFailure)
	}
	if maxInFlight != 1 {
		t.Errorf("concurrent GetProjects scans = %d, want 1 at a time", maxInFlight)
	}

	// Still retryable.
	mode.Store("ok")
	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	if calls, _ := rec.counts(); calls != 3 {
		t.Errorf("GetProjects calls = %d, want 3 after the retry", calls)
	}
}

// TestRefreshProjectsIndex_NoGoroutineLeak covers FR-8's bounded work:
// a burst of concurrent and async refreshes must leave no goroutines
// behind once it settles.
func TestRefreshProjectsIndex_NoGoroutineLeak(t *testing.T) {
	srv := testServer(t)
	srv.projects.fetch = func() ([]db.ProjectStats, error) {
		return []db.ProjectStats{{Directory: "/repo"}}, nil
	}

	// Warm up once so lazily started runtime goroutines don't count.
	if err := srv.refreshProjectsIndex(); err != nil {
		t.Fatalf("warmup refresh: %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	before := runtime.NumGoroutine()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.refreshProjectsIndex(); err != nil {
				t.Errorf("refreshProjectsIndex: %v", err)
			}
		}()
		srv.refreshProjectsIndexAsync()
	}
	wg.Wait()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutines after burst = %d, want <= %d", runtime.NumGoroutine(), before)
}
