package server

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
)

// createPlatform picks a new session's platform (requested, else the model
// reference's prefix, else the owner's default) and qualifies it with the
// authoritative owner.
func (s *Server) createPlatform(ctx context.Context, host hostsvc.Host, requested, model string) (string, string, error) {
	requested, err := platforms.PlatformForModel(requested, model)
	if err != nil {
		return "", "", err
	}
	if requested == "" {
		requested, err = s.defaultPlatformFor(ctx, host)
		if err != nil {
			return "", "", err
		}
	}
	owner, base := remote.SplitPlatformID(requested)
	if owner != "" && owner != host.RemoteID() {
		return "", "", fmt.Errorf("platform does not belong to remoteId")
	}
	id := platformForHost(host, base)
	return id, base, nil
}

// defaultPlatformFor resolves the create default against the platforms the
// owning machine has enabled: the local registry for the hub, the remote's
// advertised adapters for a remote (FR-1/2).
func (s *Server) defaultPlatformFor(ctx context.Context, host hostsvc.Host) (string, error) {
	preferred, err := s.stateDB.DefaultPlatform(ctx)
	id := host.RemoteID()
	if err != nil || id == "" || id == "local" || s.registry == nil {
		return preferred, err
	}
	var bases []string
	for _, p := range s.registry.Platforms() {
		if owner, base := remote.SplitPlatformID(string(p.ID())); owner == id {
			bases = append(bases, base)
		}
	}
	// The hub's saved setting may name a platform only the hub filtered out.
	if s.stateDB != nil {
		if saved, _, err := s.stateDB.GetSetting(ctx, state.DefaultPlatformSetting); err == nil && saved != "" {
			preferred = saved
		}
	}
	return state.PickEnabledPlatform(preferred, bases), nil
}

func platformForHost(host hostsvc.Host, base string) string {
	if id := host.RemoteID(); id != "" && id != "local" {
		return remote.CompoundPlatformID(id, base)
	}
	return base
}
