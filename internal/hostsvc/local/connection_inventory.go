package local

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
)

var _ hostsvc.ConnectionHost = (*Host)(nil)

func (h *Host) inspectConnection(ctx context.Context, c platformconnections.Connection) (hostsvc.ConnectionCheck, error) {
	if h.deps.ConnectionLifecycle.Inspect == nil {
		return hostsvc.ConnectionCheck{Status: "unchecked", Directory: c.Directory, Checks: []hostsvc.ConnectionCheckItem{}}, nil
	}
	check, err := h.deps.ConnectionLifecycle.Inspect(ctx, c)
	if err != nil {
		return hostsvc.ConnectionCheck{}, err
	}
	switch check.Status {
	case "ready", "unchecked", "sign-in-required", "dependencies-missing":
	default:
		return hostsvc.ConnectionCheck{}, fmt.Errorf("invalid connection inspection status")
	}
	check.Directory = c.Directory
	if check.Checks == nil {
		check.Checks = []hostsvc.ConnectionCheckItem{}
	}
	return check, nil
}

func (h *Host) InventoryPlatformConnections(ctx context.Context) (hostsvc.ConnectionInventory, error) {
	result := hostsvc.ConnectionInventory{Connections: []hostsvc.ConnectionView{}, Platforms: []hostsvc.ConnectionPlatform{}}
	if h.deps.ConnectionStore == nil {
		return result, nil
	}
	collection, err := h.deps.ConnectionStore.Read(ctx)
	if err != nil {
		return result, err
	}
	result.Supported = collection.Initialized && h.deps.ConnectionLifecycle.Inspect != nil
	result.OnboardingComplete = collection.OnboardingComplete
	if !result.Supported {
		return result, nil
	}
	for kind, configuration := range h.deps.ConnectionConfigurations {
		label := configuration.Label
		if label == "" {
			label = kind
		}
		result.Platforms = append(result.Platforms, hostsvc.ConnectionPlatform{ID: kind, Label: label, ExistingDirectory: configuration.ExistingDirectory, SupportsIsolation: configuration.SeparateConfiguration, IsolationNote: configuration.IsolationNote})
	}
	sort.Slice(result.Platforms, func(i, j int) bool { return result.Platforms[i].ID < result.Platforms[j].ID })
	for _, c := range collection.Connections {
		check, err := h.inspectConnection(ctx, c)
		if err != nil {
			return result, err
		}
		result.Connections = append(result.Connections, hostsvc.ConnectionView{ID: c.ID, Kind: c.Kind, Name: c.Name, Configuration: c.Configuration, Directory: c.Directory, Enabled: c.Enabled, IsDefault: c.ID == collection.DefaultID, Status: check.Status, Account: check.Account})
	}
	return result, nil
}

func (h *Host) CheckPlatformConnection(ctx context.Context, req hostsvc.ConnectionCheckRequest) (hostsvc.ConnectionCheck, error) {
	if h.deps.ConnectionStore == nil {
		return hostsvc.ConnectionCheck{}, platforms.ErrUnsupported
	}
	if req.ID != "" {
		c, err := h.deps.ConnectionStore.Get(ctx, req.ID)
		if err != nil {
			return hostsvc.ConnectionCheck{}, err
		}
		if c.Removed {
			return hostsvc.ConnectionCheck{}, platformconnections.ErrUnavailable
		}
		return h.inspectConnection(ctx, c)
	}
	if req.Directory != "" {
		configuration, ok := h.deps.ConnectionConfigurations[req.Kind]
		if !ok || req.Configuration != "existing" || filepath.Clean(req.Directory) != configuration.ExistingDirectory {
			return hostsvc.ConnectionCheck{}, platformconnections.ErrInvalid
		}
	}
	r, err := h.PreparePlatformConnection(ctx, hostsvc.ConnectionSetupRequest{Kind: req.Kind, Configuration: req.Configuration, ReservationID: req.ReservationID})
	if err != nil {
		return hostsvc.ConnectionCheck{ReservationID: r.ID}, err
	}
	check, err := h.inspectConnection(ctx, platformconnections.Connection{ID: r.ConnectionID, Kind: r.Kind, Configuration: r.Configuration, Directory: r.Directory})
	check.ReservationID = r.ID
	return check, err
}

func (h *Host) UpdatePlatformConnection(ctx context.Context, req hostsvc.ConnectionUpdate) error {
	if h.deps.ConnectionStore == nil {
		return platforms.ErrUnsupported
	}
	c, err := h.deps.ConnectionStore.Get(ctx, req.ID)
	if err != nil {
		return err
	}
	if c.Removed {
		return platformconnections.ErrUnavailable
	}
	// One mutation per request avoids partially applied multi-field updates.
	count := 0
	if req.Name != nil {
		count++
	}
	if req.Enabled != nil {
		count++
	}
	if req.Default {
		count++
	}
	if count != 1 {
		return platformconnections.ErrInvalid
	}
	if req.Default {
		check, err := h.inspectConnection(ctx, c)
		if err != nil {
			return err
		}
		if check.Status != "ready" {
			return platformconnections.ErrUnavailable
		}
		return h.deps.ConnectionStore.SetDefault(ctx, c.ID)
	}
	if req.Name != nil {
		return h.deps.ConnectionStore.Rename(ctx, c.ID, *req.Name)
	}
	if !*req.Enabled {
		if !c.Enabled {
			return nil
		}
		if h.deps.ConnectionLifecycle.Detach == nil {
			return platforms.ErrUnsupported
		}
		return h.deps.ConnectionLifecycle.Detach(ctx, c, func() error { return h.deps.ConnectionStore.SetEnabled(ctx, c.ID, false) })
	}
	if err := h.deps.ConnectionStore.SetEnabled(ctx, c.ID, *req.Enabled); err != nil {
		return err
	}
	if *req.Enabled && h.deps.ConnectionLifecycle.Activate != nil {
		c.Enabled = true
		return h.deps.ConnectionLifecycle.Activate(ctx, c)
	}
	return nil
}

func (h *Host) RemovePlatformConnection(ctx context.Context, id string) error {
	if h.deps.ConnectionStore == nil || h.deps.ConnectionLifecycle.Detach == nil {
		return platforms.ErrUnsupported
	}
	c, err := h.deps.ConnectionStore.Get(ctx, id)
	if err != nil {
		return err
	}
	if c.Removed {
		return nil
	}
	return h.deps.ConnectionLifecycle.Detach(ctx, c, func() error { return h.deps.ConnectionStore.Remove(ctx, c.ID) })
}

func (h *Host) CompletePlatformConnectionOnboarding(ctx context.Context) error {
	if h.deps.ConnectionStore == nil {
		return platforms.ErrUnsupported
	}
	return h.deps.ConnectionStore.CompleteOnboarding(ctx)
}
