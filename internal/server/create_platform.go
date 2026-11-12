package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Named owners select their saved account before interpreting a model namespace.
// Only legacy owners infer an omitted platform from the model prefix.
func (s *Server) createPlatform(ctx context.Context, host hostsvc.Host, requested, model string) (string, string, error) {
	var err error
	if prefix, _ := platforms.SplitModel(model); requested == "" && strings.HasPrefix(string(prefix), "cnx_") {
		requested = string(prefix)
	}
	if requested == "" {
		inventory, named, modeErr := s.ownerConnectionMode(ctx, host)
		if modeErr != nil {
			return "", "", modeErr
		}
		if named {
			requested, err = s.namedDefaultForOwner(ctx, host, inventory)
			if err != nil {
				return "", "", err
			}
		}
	}
	_, baseRequested := platforms.SplitPlatformID(requested)
	if !strings.HasPrefix(baseRequested, "cnx_") {
		requested, err = platforms.PlatformForModel(requested, model)
	}
	if err != nil {
		return "", "", err
	}
	if requested == "" {
		requested, err = s.defaultPlatformFor(ctx, host)
		if err != nil {
			return "", "", err
		}
	}
	owner, base := platforms.SplitPlatformID(requested)
	if owner != "" && owner != host.RemoteID() {
		return "", "", fmt.Errorf("platform does not belong to remoteId")
	}
	id := platformForHost(host, base)
	if strings.HasPrefix(base, "cnx_") && model != "" {
		kind, err := s.connectionModelKind(ctx, host, id, base)
		if err != nil {
			return "", "", err
		}
		if _, err := platforms.ModelForConnection(platforms.ID(id), kind, model); err != nil {
			return "", "", err
		}
	}
	return id, base, nil
}

func (s *Server) connectionModelKind(ctx context.Context, host hostsvc.Host, id, base string) (platforms.ID, error) {
	if (host.RemoteID() == "" || host.RemoteID() == "local") && s.connectionStore != nil {
		c, err := s.connectionStore.Get(ctx, base)
		return platforms.ID(c.Kind), err
	}
	if s.registry != nil {
		if p, ok := s.registry.Get(platforms.ID(id)); ok {
			if native, ok := p.(interface{ NativeKind() platforms.ID }); ok {
				return native.NativeKind(), nil
			}
			kind := p.ID().Base()
			if !strings.HasPrefix(string(kind), "cnx_") {
				return kind, nil
			}
		}
	}
	return "", platforms.ErrPlatformUnreachable
}

func (s *Server) ownerConnectionMode(ctx context.Context, host hostsvc.Host) (hostsvc.ConnectionInventory, bool, error) {
	if host.RemoteID() == "" || host.RemoteID() == "local" {
		return hostsvc.ConnectionInventory{}, s.connections != nil, nil
	}
	if connections, ok := host.(hostsvc.ConnectionHost); ok {
		inventory, err := connections.InventoryPlatformConnections(ctx)
		if errors.Is(err, platforms.ErrUnsupported) || status.Code(err) == codes.Unimplemented {
			return hostsvc.ConnectionInventory{}, false, nil
		}
		if err != nil {
			return hostsvc.ConnectionInventory{}, false, err
		}
		return inventory, inventory.Supported, nil
	}
	return hostsvc.ConnectionInventory{}, false, nil
}

func (s *Server) namedDefaultForOwner(ctx context.Context, host hostsvc.Host, inventory hostsvc.ConnectionInventory) (string, error) {
	if host.RemoteID() == "" || host.RemoteID() == "local" {
		return s.connectionDefault(ctx)
	}
	for _, c := range inventory.Connections {
		if c.IsDefault {
			if !c.Enabled || c.Status != "ready" || c.ID == "" {
				return "", platforms.ErrPlatformUnreachable
			}
			return c.ID, nil
		}
	}
	return "", platforms.ErrPlatformUnreachable
}

// defaultPlatformFor resolves the create default against the platforms the
// owning machine has enabled: the local registry for the hub, the remote's
// advertised adapters for a remote (FR-1/2).
func (s *Server) defaultPlatformFor(ctx context.Context, host hostsvc.Host) (string, error) {
	inventory, named, err := s.ownerConnectionMode(ctx, host)
	if err != nil {
		return "", err
	}
	if named {
		return s.namedDefaultForOwner(ctx, host, inventory)
	}
	return s.legacyDefaultForOwner(ctx, host)
}

func (s *Server) legacyDefaultForOwner(ctx context.Context, host hostsvc.Host) (string, error) {
	preferred, err := s.stateDB.DefaultPlatform(ctx)
	id := host.RemoteID()
	if err != nil || id == "" || id == "local" || s.registry == nil {
		return preferred, err
	}
	var bases []string
	for _, entry := range s.registry.Entries() {
		if owner, base := platforms.SplitPlatformID(string(entry.Key)); owner == id && !strings.HasPrefix(base, "cnx_") {
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
		return platforms.CompoundPlatformID(id, base)
	}
	return base
}
