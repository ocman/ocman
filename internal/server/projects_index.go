package server

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/remote"
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
	fetch  func() ([]db.ProjectStats, error)
	enrich func(context.Context, []db.ProjectStats) error // nil uses owner-local discovery
}

// projectsIndexTickFn is the refresh body of runProjectsIndexLoop,
// lifted to a package-level variable so tests can inject a panicking
// implementation (FR-11) and assert the loop survives.
var projectsIndexTickFn = func(ctx context.Context, s *Server) {
	if err := s.refreshProjectsIndex(ctx); err != nil {
		log.WithError(err).Warn("refreshing projects index")
	}
}

func (s *Server) runProjectsIndexTick(ctx context.Context) {
	if s.HasDemand("projects") {
		projectsIndexTickFn(ctx, s)
		return
	}
	s.projects.mu.Lock()
	s.projects.dirty = true
	s.projects.mu.Unlock()
}

func (s *Server) runProjectsIndexLoop(ctx context.Context) {
	if s.db == nil {
		return
	}

	runWithRecover("projects-index", func() { s.runProjectsIndexTick(ctx) })

	ticker := time.NewTicker(projectsScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWithRecover("projects-index", func() { s.runProjectsIndexTick(ctx) })
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
// The first caller starts a bounded server-lifetime worker; every caller
// waits independently and receives its result. Because a
// joining caller may have observed state the running query already read
// past, joining also marks the cycle dirty, which makes the driver run
// exactly one follow-up query afterwards — a request is never silently
// cleared by the completion of an older refresh. A failed cycle keeps
// the previous snapshot and the dirty indication, and stops instead of
// looping, so the next event or 5-minute tick retries without spinning.
func (s *Server) refreshProjectsIndex(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if s.db == nil && s.projects.fetch == nil {
		return nil
	}

	st := &s.projects
	st.mu.Lock()
	st.dirty = true
	if !st.running {
		st.running = true
		st.done = make(chan struct{})
		go s.runProjectsRefresh(st.done)
	}
	done := st.done
	st.mu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.err
}

// Shared scans belong to the server, while every caller waits independently.
func (s *Server) runProjectsRefresh(done chan struct{}) {
	// Plugin cleanup must not erase the lifetime of queued shared work.
	s.pluginMu.Lock()
	ctx := s.lifetimeCtx
	s.pluginMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	runWithRecover("projects-index", func() {
		if err := s.driveProjectsRefresh(ctx, done); err != nil {
			log.WithError(err).Warn("refreshing projects index")
		}
	})
}

// driveProjectsRefresh runs refresh iterations until one completes with
// no newer request pending, then settles the cycle and releases every
// joined caller. See refreshProjectsIndex for the state machine.
func (s *Server) driveProjectsRefresh(ctx context.Context, done chan struct{}) error {
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

		err := s.refreshProjectsIndexOnce(ctx)

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

func (s *Server) refreshProjectsIndexOnce(ctx context.Context) error {
	ctx, span := telemetry.Tracer().Start(ctx, "ocman.projects_index.refresh")
	defer span.End()

	start := time.Now()
	projects, err := s.getProjects(ctx)
	if err == nil {
		enrich := s.projects.enrich
		if enrich == nil {
			enrich = remote.EnrichProjectStats
		}
		err = enrich(ctx, projects)
	}
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

	refreshedAt := time.Now()
	s.projects.mu.Lock()
	changed := !s.projects.loaded || !reflect.DeepEqual(s.projects.data, projects)
	s.projects.data = cloneProjectStats(projects)
	s.projects.loaded = true
	s.projects.refreshedAt = refreshedAt
	s.projects.mu.Unlock()

	s.persistProjectsIndex(ctx, projects, refreshedAt)
	if changed {
		s.broadcastGlobalEvent("ocman.projects.changed", []byte(`{}`))
	}

	return nil
}

func (s *Server) loadProjectsIndexCache(ctx context.Context) {
	if s.stateDB == nil {
		return
	}
	data, refreshedAt, err := s.stateDB.ProjectsCache(ctx)
	if err != nil {
		log.WithError(err).Warn("loading projects cache")
		return
	}
	if data == nil {
		return
	}
	var projects []db.ProjectStats
	if err := json.Unmarshal(data, &projects); err != nil {
		log.WithError(err).Warn("decoding projects cache")
		return
	}
	s.projects.mu.Lock()
	s.projects.data = projects
	s.projects.loaded = true
	s.projects.dirty = true
	s.projects.refreshedAt = refreshedAt
	s.projects.mu.Unlock()
}

func (s *Server) persistProjectsIndex(ctx context.Context, projects []db.ProjectStats, refreshedAt time.Time) {
	if s.stateDB == nil {
		return
	}
	data, err := json.Marshal(projects)
	if err == nil {
		err = s.stateDB.SaveProjectsCache(ctx, data, refreshedAt)
	}
	if err != nil {
		log.WithError(err).Warn("saving projects cache")
	}
}

func (s *Server) getProjects(ctx context.Context) ([]db.ProjectStats, error) {
	if fetch := s.projects.fetch; fetch != nil {
		return fetch()
	}
	return s.db.GetProjects(ctx)
}

func (s *Server) projectsSnapshot() ([]db.ProjectStats, bool) {
	projects, loaded, _ := s.projectsSnapshotState()
	return projects, loaded
}

func (s *Server) projectsSnapshotState() ([]db.ProjectStats, bool, bool) {
	s.projects.mu.RLock()
	defer s.projects.mu.RUnlock()
	return cloneProjectStats(s.projects.data), s.projects.loaded, s.projects.dirty
}

func cloneProjectStats(projects []db.ProjectStats) []db.ProjectStats {
	if len(projects) == 0 {
		return nil
	}
	cloned := make([]db.ProjectStats, len(projects))
	copy(cloned, projects)
	for i := range cloned {
		cloned[i].UpstreamKeys = slices.Clone(projects[i].UpstreamKeys)
	}
	return cloned
}
