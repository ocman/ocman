package server

import (
	"context"
	"fmt"
	"net/http"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
	"github.com/ocman/ocman/internal/sourcecontrol"
	log "github.com/sirupsen/logrus"
)

func (s *Server) sourceControlModule() *sourcecontrol.Module {
	return s.sourceControlMod.get(func() *sourcecontrol.Module {
		return sourcecontrol.New(sourcecontrol.Deps{DB: s.stateDB, Discover: func() []sourcecontrol.Discovered { return s.integrations.Connections() }, Rediscover: func() []sourcecontrol.Discovered { return discoverForgeClients().Connections() }})
	})
}

func (s *Server) prWorkflowModule() *prworkflow.Module {
	return s.prWorkflowMod.get(func() *prworkflow.Module {
		return prworkflow.New(prworkflow.Deps{
			ProjectHandler: s.projectForgeModule().PRToolHandler(),
			DB:             s.stateDB, Target: s.prTarget, Owner: s.prOwner, ResolveForge: s.resolveForge, Ready: s.prWatchReady,
			Remote: s.remotePRWorkflow,
			Changed: func(platform, sessionID string) {
				s.broadcastSessionPatch(platform, sessionID, nil)
			},
			Enqueue: func(ctx context.Context, platform string, req platforms.SendMessageRequest) error {
				route, err := s.prWatchRoute(ctx, platform, req.SessionID)
				if err != nil {
					return err
				}
				// Readiness already waits for completion. Let the queue deliver now
				// rather than holding the notice for an idle edge that may never come.
				return s.queueSvc().Enqueue(ctx, route, false, req)
			},
		})
	})
}

func (s *Server) remotePRWorkflow(t prworkflow.Target) (platforms.PullRequestWorkflow, error) {
	owner, _ := platforms.SplitPlatformID(t.Platform)
	if owner == "" {
		return nil, nil
	}
	p, ok := s.registry.Get(platforms.ID(t.Platform))
	if !ok {
		return nil, platforms.ErrRemoteOffline
	}
	workflow, ok := p.(platforms.PullRequestWorkflow)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	return workflow, nil
}

// RemotePullRequestOperation is invoked only after remote RPC authentication.
func (s *Server) RemotePullRequestOperation(ctx context.Context, platform string, req platforms.PullRequestRequest) platforms.PullRequestResponse {
	return s.prWorkflowModule().Operation(ctx, platform, req)
}

func (s *Server) prTarget(w http.ResponseWriter, r *http.Request, id string) (prworkflow.Target, bool) {
	if owner, _ := platforms.SplitPlatformID(r.URL.Query().Get("platform")); owner != "" {
		if _, ok := s.router().LookupRemote(owner); !ok {
			http.Error(w, "Session owner disconnected", http.StatusServiceUnavailable)
			return prworkflow.Target{}, false
		}
	}
	p := s.resolvePlatformForSession(w, r, id)
	if p == nil {
		return prworkflow.Target{}, false
	}
	detail, err := p.Session(r.Context(), id, 1, 0)
	if err != nil || detail == nil || detail.Session == nil {
		http.Error(w, "Session unavailable", http.StatusNotFound)
		return prworkflow.Target{}, false
	}
	owner, _ := platforms.SplitPlatformID(string(p.ID()))
	if owner == "" {
		owner = "local"
	}
	t := prworkflow.Target{Platform: string(p.ID()), SessionID: id, Directory: detail.Session.Directory, RemoteID: owner}
	if _, err := s.prOwner(r.Context(), t); err != nil {
		http.Error(w, "Session owner disconnected", http.StatusServiceUnavailable)
		return prworkflow.Target{}, false
	}
	return t, true
}

func (s *Server) prOwner(_ context.Context, t prworkflow.Target) (hostsvc.Host, error) {
	owner, _ := platforms.SplitPlatformID(t.Platform)
	if owner == "" {
		if t.RemoteID != "local" {
			return nil, fmt.Errorf("owner mismatch")
		}
		return s.router().Local(), nil
	}
	if owner != t.RemoteID {
		return nil, fmt.Errorf("owner mismatch")
	}
	h, ok := s.router().LookupRemote(owner)
	if !ok {
		return nil, fmt.Errorf("owner disconnected")
	}
	return h, nil
}

func (s *Server) prWatchReady(ctx context.Context, t prworkflow.Target) bool {
	if s.stateDB != nil {
		archived, err := s.stateDB.IsSessionArchived(ctx, t.Platform, t.SessionID)
		if err != nil || archived {
			return false
		}
	}
	route, err := s.prWatchRoute(ctx, t.Platform, t.SessionID)
	if err != nil {
		return false
	}
	p, ok := s.registry.Get(platforms.ID(route))
	if !ok {
		return false
	}
	reader, ok := p.(platforms.LifecycleReader)
	if !ok {
		return false
	}
	life, err := reader.SessionLifecycle(ctx, t.SessionID)
	// Waiting is a normally completed assistant turn, not a running turn.
	// Match the held queue's running gate, including quota-limited turns.
	return err == nil && life != nil && !life.ReadOnly && life.Status != db.StatusBusy && life.Status != db.StatusLimited
}

func (s *Server) linkLaunchedPR(ctx context.Context, platform, id, dir, owner, remote string, number int, pr *forge.PR) {
	if err := s.prWorkflowModule().LinkCreated(ctx, prworkflow.Target{Platform: platform, SessionID: id, Directory: dir, RemoteID: owner}, remote, number, pr); err != nil {
		log.WithError(err).Warn("linking launched PR session")
	}
}
