package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

type factoryPlanningLauncher struct{ server *Server }

func (l factoryPlanningLauncher) LaunchPlanningSession(ctx context.Context, req factory.PlanningSessionRequest) (factory.PlanningSession, error) {
	var platformID string
	for _, candidate := range l.server.registry.Platforms() {
		remoteID, _ := remote.SplitPlatformID(string(candidate.ID()))
		if remoteID != "" || !candidate.Available(ctx) || !candidate.Capabilities().PermissionRules {
			continue
		}
		if platformID != "" {
			return factory.PlanningSession{}, errors.New("multiple local planning platforms are available")
		}
		platformID = string(candidate.ID())
	}
	if platformID == "" {
		return factory.PlanningSession{}, errors.New("no local planning platform with permission rules is available")
	}
	ensured, err := l.server.router().Local().EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: req.Repository})
	if err != nil {
		return factory.PlanningSession{}, fmt.Errorf("ensure planning runtime: %w", err)
	}
	if ensured == nil {
		return factory.PlanningSession{}, errors.New("ensure planning runtime returned no instance")
	}
	rules := []platforms.PermissionRule{
		{Permission: "read", Pattern: "*", Action: "allow"},
		{Permission: "glob", Pattern: "*", Action: "allow"},
		{Permission: "grep", Pattern: "*", Action: "allow"},
		{Permission: "list", Pattern: "*", Action: "allow"},
		{Permission: "external_directory", Pattern: "*", Action: "deny"},
		{Permission: "bash", Pattern: "*", Action: "deny"},
		{Permission: "edit", Pattern: "*", Action: "deny"},
		{Permission: "task", Pattern: "*", Action: "deny"},
		{Permission: "webfetch", Pattern: "*", Action: "deny"},
	}
	created, err := l.server.sessions.CreateConfigured(ctx, platformID, platforms.CreateSessionRequest{Directory: req.Repository, Title: req.Title, Port: ensured.Port()}, rules)
	if err != nil {
		var cleanup *sessionsvc.ConfiguredSessionCleanupError
		if errors.As(err, &cleanup) {
			return factory.PlanningSession{Platform: platformID, ID: cleanup.SessionID}, fmt.Errorf("create bounded Planning Session: %w", err)
		}
		return factory.PlanningSession{}, fmt.Errorf("create bounded Planning Session: %w", err)
	}
	return factory.PlanningSession{Platform: platformID, ID: created.ID}, nil
}

func (l factoryPlanningLauncher) StopPlanningSession(ctx context.Context, session factory.PlanningSession) error {
	return l.server.sessions.Dispose(ctx, session.Platform, platforms.DisposeSessionRequest{SessionID: session.ID})
}

func (l factoryPlanningLauncher) ProbePlanningSession(ctx context.Context, session factory.PlanningSession) (bool, error) {
	platform, ok := l.server.registry.Get(platforms.ID(session.Platform))
	if !ok {
		return false, fmt.Errorf("planning platform %q is unavailable", session.Platform)
	}
	return platform.Owns(ctx, session.ID), nil
}
