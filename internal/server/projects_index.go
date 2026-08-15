package server

import (
	"context"
	"errors"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/telemetry"
)

type projectsIndexState struct {
	mu          sync.RWMutex
	data        []db.ProjectStats
	loaded      bool
	refreshedAt time.Time

	// running/dirty/done/err implement FR-8's per-owner singleflight
	// with a dirty follow-up: at most one refresh runs, concurrent
	// callers join it, and a request that arrives mid-run causes
	// exactly one follow-up rather than being cleared by the older
	// refresh completing.
	running bool
	dirty   bool
	done    chan struct{}
	err     error

	// fetch overrides the db.GetProjects query. Nil in production;
	// tests set it to control timing, failures, and call counts.
	fetch func() ([]db.ProjectStats, error)
}

// projectsIndexTickFn is the per-tick body of runProjectsIndexLoop,
// lifted to a package-level variable so tests can inject a panicking
// implementation (FR-11) and assert the loop survives.
var projectsIndexTickFn = func(s *Server) {
	if err := s.refreshProjectsIndex(); err != nil {
		log.WithError(err).Warn("refreshing projects index")
	}
}

func (s *Server) runProjectsIndexLoop(ctx context.Context) {
	if s.db == nil {
		return
	}

	runWithRecover("projects-index", func() { projectsIndexTickFn(s) })

	ticker := time.NewTicker(projectsScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWithRecover("projects-index", func() { projectsIndexTickFn(s) })
		}
	}
}

// errProjectsRefreshAborted is reported to waiters when the refresh
// worker unwound through a panic. The panic itself still propagates to
// the caller's runWithRecover; this only keeps waiters from blocking
// forever on a cycle that will never settle.
var errProjectsRefreshAborted = errors.New("projects index refresh aborted")

// refreshProjectsIndex runs one project inventory refresh for this
// owner, singleflighted with a dirty follow-up (FR-8).
//
// The caller that finds no refresh running drives the cycle inline;
// every other caller joins it and receives the same result. Because a
// joining caller may have observed state the running query already read
// past, joining also marks the cycle dirty, which makes the driver run
// exactly one follow-up query afterwards — a request is never silently
// cleared by the completion of an older refresh. A failed cycle keeps
// the previous snapshot and the dirty indication, and stops instead of
// looping, so the next event or 5-minute tick retries without spinning.
func (s *Server) refreshProjectsIndex() error {
	if s.db == nil && s.projects.fetch == nil {
		return nil
	}

	st := &s.projects
	st.mu.Lock()
	st.dirty = true
	if st.running {
		done := st.done
		st.mu.Unlock()
		<-done
		st.mu.RLock()
		defer st.mu.RUnlock()
		return st.err
	}
	st.running = true
	st.done = make(chan struct{})
	done := st.done
	st.mu.Unlock()

	return s.driveProjectsRefresh(done)
}

// driveProjectsRefresh runs refresh iterations until one completes with
// no newer request pending, then settles the cycle and releases every
// joined caller. See refreshProjectsIndex for the state machine.
func (s *Server) driveProjectsRefresh(done chan struct{}) error {
	st := &s.projects
	settled := false
	defer func() {
		if settled {
			return
		}
		// Panic path: never leave running=true, which would wedge
		// every future refresh (and its waiters) permanently.
		st.mu.Lock()
		st.dirty = true
		st.err = errProjectsRefreshAborted
		st.running = false
		close(done)
		st.mu.Unlock()
	}()

	for {
		st.mu.Lock()
		st.dirty = false
		st.mu.Unlock()

		err := s.refreshProjectsIndexOnce()

		// The dirty check and the settle must share one lock hold:
		// otherwise a request slipping in between would join a cycle
		// that is already finishing and lose its follow-up.
		st.mu.Lock()
		if err == nil && st.dirty {
			st.mu.Unlock()
			continue
		}
		if err != nil {
			st.dirty = true
		}
		st.err = err
		st.running = false
		settled = true
		close(done)
		st.mu.Unlock()
		return err
	}
}

func (s *Server) refreshProjectsIndexOnce() error {
	ctx, span := telemetry.Tracer().Start(context.Background(), "ocman.projects_index.refresh")
	defer span.End()

	start := time.Now()
	projects, err := s.getProjects()
	dur := time.Since(start)
	if projectsIndexRefreshDuration != nil {
		projectsIndexRefreshDuration.Record(ctx, float64(dur.Microseconds())/1000.0)
	}
	if err != nil {
		span.RecordError(err)
		if projectsIndexRefreshErrors != nil {
			projectsIndexRefreshErrors.Add(ctx, 1)
		}
		return err
	}

	s.projects.mu.Lock()
	s.projects.data = cloneProjectStats(projects)
	s.projects.loaded = true
	s.projects.refreshedAt = time.Now()
	s.projects.mu.Unlock()

	return nil
}

func (s *Server) getProjects() ([]db.ProjectStats, error) {
	if fetch := s.projects.fetch; fetch != nil {
		return fetch()
	}
	return s.db.GetProjects()
}

func (s *Server) projectsSnapshot() ([]db.ProjectStats, bool) {
	s.projects.mu.RLock()
	defer s.projects.mu.RUnlock()
	return cloneProjectStats(s.projects.data), s.projects.loaded
}

func cloneProjectStats(projects []db.ProjectStats) []db.ProjectStats {
	if len(projects) == 0 {
		return nil
	}
	cloned := make([]db.ProjectStats, len(projects))
	copy(cloned, projects)
	return cloned
}
