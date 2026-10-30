package server

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/ocman/ocman/internal/forge"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/prworkflow"
	"github.com/ocman/ocman/internal/sourcecontrol"
	"github.com/ocman/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

func (s *Server) sourceControlModule() *sourcecontrol.Module {
	return s.sourceControlMod.get(func() *sourcecontrol.Module {
		return sourcecontrol.New(sourcecontrol.Deps{DB: s.stateDB, Discover: func() []sourcecontrol.Discovered { return forgeConnections(s.integrations) }, Rediscover: func() []sourcecontrol.Discovered { return forgeConnections(discoverForgeClients()) }})
	})
}

func forgeConnections(integrations *forgeClients) []sourcecontrol.Discovered {
	connections := []sourcecontrol.Discovered{}
	if integrations == nil {
		return connections
	}
	if c := integrations.GitHub; c != nil {
		source := "anonymous"
		if c.Token() != "" {
			source = "GitHub CLI"
		}
		if os.Getenv("GITHUB_TOKEN") != "" {
			source = "GITHUB_TOKEN"
		} else if os.Getenv("GH_TOKEN") != "" {
			source = "GH_TOKEN"
		}
		connections = append(connections, sourcecontrol.Discovered{Connection: sourcecontrol.Connection{Host: "github.com", Provider: forge.RemoteTypeGitHub, Source: source}, Client: c})
	}
	if reg := integrations.Forgejo; reg != nil {
		for _, host := range reg.Hosts() {
			source := "tea login"
			if os.Getenv("FORGEJO_TOKEN") != "" {
				source = "FORGEJO_TOKEN"
			} else if os.Getenv("GITEA_TOKEN") != "" {
				source = "GITEA_TOKEN"
			}
			connections = append(connections, sourcecontrol.Discovered{Connection: sourcecontrol.Connection{Host: host, Provider: forge.RemoteTypeForgejo, Source: source}, Client: reg.ForHost(host)})
		}
	}
	return connections
}

func (s *Server) prWorkflowModule() *prworkflow.Module {
	return s.prWorkflowMod.get(func() *prworkflow.Module {
		return prworkflow.New(prworkflow.Deps{
			ProjectHandler: s.projectForgeModule().PRToolHandler(),
			DB:             s.stateDB, Target: s.prTarget, Owner: s.prOwner, ResolveForge: s.resolveForge, Ready: s.prWatchReady,
			Enqueue: func(ctx context.Context, platform string, req platforms.SendMessageRequest) error {
				return s.queueSvc().Enqueue(ctx, platform, true, req)
			},
		})
	})
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
		archived, err := s.stateDB.ArchivedSessions(ctx)
		if err != nil {
			return false
		}
		if _, ok := archived[state.Key{Platform: t.Platform, SessionID: t.SessionID}]; ok {
			return false
		}
	}
	p, ok := s.registry.Get(platforms.ID(t.Platform))
	if !ok {
		return false
	}
	reader, ok := p.(platforms.LifecycleReader)
	if !ok {
		return false
	}
	life, err := reader.SessionLifecycle(ctx, t.SessionID)
	return err == nil && life != nil && !life.ReadOnly && life.Status != "busy" && life.Status != "waiting"
}

func (s *Server) linkLaunchedPR(ctx context.Context, platform, id, dir, owner, remote string, number int, pr *forge.PR) {
	if err := s.prWorkflowModule().LinkCreated(ctx, prworkflow.Target{Platform: platform, SessionID: id, Directory: dir, RemoteID: owner}, remote, number, pr); err != nil {
		log.WithError(err).Warn("linking launched PR session")
	}
}
