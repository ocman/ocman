package server

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/state"
)

// createPlatform resolves a default and qualifies it with the authoritative owner.
func (s *Server) createPlatform(ctx context.Context, host hostsvc.Host, requested string) (string, string, error) {
	if requested == "" {
		var err error
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
