package sessionsvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// CreatedSession is the I/O-free projection passed to the creation hook.
// Directory and title may be empty when only the moved session's ID is known.
type CreatedSession struct {
	ID        string
	Platform  string
	Directory string
	Title     string
	RoutineID string
}

// Create creates a session, auto-picking the only available platform when empty.
func (s *Service) Create(ctx context.Context, platformID string, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	return s.create(ctx, platformID, req, nil, routineCreation{})
}

// CreateConfigured applies permission rules before publishing the session.
func (s *Service) CreateConfigured(ctx context.Context, platformID string, req platforms.CreateSessionRequest, rules []platforms.PermissionRule) (*platforms.CreateSessionResponse, error) {
	if err := normalizePermissionRules(rules); err != nil {
		return nil, err
	}
	return s.create(ctx, platformID, req, rules, routineCreation{})
}

// CreateRoutine links filter metadata before publishing a configured session.
// The normal creation hook then publishes a row carrying the routine identity.
func (s *Service) CreateRoutine(ctx context.Context, platformID string, req platforms.CreateSessionRequest, rules []platforms.PermissionRule, routineID string, link func(string) error) (*platforms.CreateSessionResponse, error) {
	if rules == nil {
		rules = []platforms.PermissionRule{}
	}
	if err := normalizePermissionRules(rules); err != nil {
		return nil, err
	}
	finish := s.routinePublication.begin()
	defer finish()
	return s.create(ctx, platformID, req, rules, routineCreation{id: routineID, link: link})
}

type routineCreation struct {
	id   string
	link func(string) error
}

// pickAdapter auto-picks when exactly one platform is available.
func (s *Service) pickAdapter(ctx context.Context, platformID string) (platforms.Platform, error) {
	if platformID != "" {
		p, ok := s.registry.Get(platforms.ID(platformID))
		if !ok {
			return nil, validation("unknown platform")
		}
		return p, nil
	}
	var adapter platforms.Platform
	for _, p := range s.registry.Platforms() {
		if !p.Available(ctx) {
			continue
		}
		if adapter != nil {
			return nil, validation("multiple platforms available — specify ?platform=<id>")
		}
		adapter = p
	}
	if adapter == nil {
		return nil, ErrNoPlatformAvailable
	}
	return adapter, nil
}

// ResolvePlatformID returns the platform Create would use.
func (s *Service) ResolvePlatformID(ctx context.Context, platformID string) (string, error) {
	adapter, err := s.pickAdapter(ctx, platformID)
	if err != nil {
		return "", err
	}
	return string(adapter.ID()), nil
}

// DirectoryCatalog resolves the platform the same way Create does.
func (s *Service) DirectoryCatalog(ctx context.Context, platformID string, req platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, string, error) {
	if req.Directory == "" {
		return nil, "", validation("directory is required")
	}
	adapter, err := s.pickAdapter(ctx, platformID)
	if err != nil {
		return nil, "", err
	}
	catalog, err := adapter.DirectoryCatalog(ctx, req)
	return catalog, string(adapter.ID()), err
}

func (s *Service) create(ctx context.Context, platformID string, req platforms.CreateSessionRequest, rules []platforms.PermissionRule, routine routineCreation) (*platforms.CreateSessionResponse, error) {
	if req.Directory == "" {
		return nil, validation("directory is required")
	}
	adapter, err := s.pickAdapter(ctx, platformID)
	if err != nil {
		return nil, err
	}
	var disposer platforms.SessionDisposer
	if rules != nil {
		var ok bool
		disposer, ok = adapter.(platforms.SessionDisposer)
		if !ok {
			return nil, platforms.ErrUnsupported
		}
	}
	createPhase := srvtiming.Begin(ctx, "create_session")
	resp, err := adapter.CreateSession(ctx, req)
	createPhase.EndWithDesc("adapter.CreateSession")
	if err != nil {
		return nil, err
	}
	if resp == nil || strings.TrimSpace(resp.ID) == "" {
		return nil, fmt.Errorf("platform %s returned no session", adapter.ID())
	}
	if rules != nil {
		err = adapter.SetPermissionRules(ctx, platforms.SetPermissionRulesRequest{SessionID: resp.ID, Rules: rules})
	}
	if err == nil && routine.link != nil {
		err = routine.link(resp.ID)
	}
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		cleanupErr := disposer.DisposeSession(cleanupCtx, platforms.DisposeSessionRequest{SessionID: resp.ID, Port: req.Port})
		if cleanupErr != nil {
			return nil, &ConfiguredSessionCleanupError{SessionID: resp.ID, Err: errors.Join(err, cleanupErr)}
		}
		return nil, err
	}
	// The first send looks up project defaults by directory; we already know it.
	s.sessionDirs.Store(resp.ID, req.Directory)
	if s.hooks.SessionCreated != nil {
		s.hooks.SessionCreated(CreatedSession{
			ID: resp.ID, Platform: string(adapter.ID()), Directory: req.Directory,
			Title: req.Title, RoutineID: routine.id,
		})
	}
	return resp, nil
}
