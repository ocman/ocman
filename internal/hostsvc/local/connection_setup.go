package local

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
)

var _ hostsvc.ConnectionSetupHost = (*Host)(nil)

func (h *Host) PreparePlatformConnection(ctx context.Context, req hostsvc.ConnectionSetupRequest) (platformconnections.Reservation, error) {
	if h.deps.ConnectionStore == nil {
		return platformconnections.Reservation{}, platforms.ErrUnsupported
	}
	var r platformconnections.Reservation
	var err error
	if req.ReservationID != "" {
		r, err = h.deps.ConnectionStore.Reservation(ctx, req.ReservationID)
		if err != nil {
			return r, err
		}
		if (req.Kind != "" && req.Kind != r.Kind) || (req.Configuration != "" && req.Configuration != r.Configuration) {
			return r, fmt.Errorf("%w: setup choices do not match the reservation", platformconnections.ErrInvalid)
		}
		if r.Status == "canceled" {
			return r, platformconnections.ErrUnavailable
		}
		if r.Status == "saved" {
			c, err := h.deps.ConnectionStore.Get(ctx, r.ConnectionID)
			if err != nil {
				return r, err
			}
			if c.Removed {
				return r, platformconnections.ErrUnavailable
			}
		}
	} else {
		configuration, ok := h.deps.ConnectionConfigurations[req.Kind]
		if !ok || (req.Configuration == "isolated" && !configuration.SeparateConfiguration) {
			return r, platforms.ErrUnsupported
		}
		directory := configuration.ExistingDirectory
		if req.Configuration == "isolated" {
			if !filepath.IsAbs(h.deps.StateDir) {
				return r, fmt.Errorf("connection setup requires an explicit owner state directory")
			}
			directory = filepath.Join(h.deps.StateDir, "platform-connections", uuid.NewString(), "config")
		}
		r, err = h.deps.ConnectionStore.Reserve(ctx, req.Kind, req.Configuration, directory)
		if err != nil {
			return r, err
		}
	}
	configuration, ok := h.deps.ConnectionConfigurations[r.Kind]
	if !ok || (r.Configuration == "isolated" && !configuration.SeparateConfiguration) {
		return r, platforms.ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return r, err
	}
	if r.Configuration == "isolated" {
		// Preserve the receipt even if preparation fails, so retry can use the
		// same directory. Never copy an existing account or native credentials.
		if err := prepareConnectionDirectory(h.deps.StateDir, r.Directory, r.Status == "pending"); err != nil {
			return r, err
		}
	}
	return r, nil
}

func (h *Host) SavePlatformConnection(ctx context.Context, receipt, name string, enabled bool) (platformconnections.Connection, error) {
	// Repeat preparation against this owner's current policy before saving.
	if _, err := h.PreparePlatformConnection(ctx, hostsvc.ConnectionSetupRequest{ReservationID: receipt}); err != nil {
		return platformconnections.Connection{}, err
	}
	c, err := h.deps.ConnectionStore.SaveReservation(ctx, receipt, name, enabled)
	if err == nil && c.Enabled && h.deps.ConnectionLifecycle.Activate != nil {
		err = h.deps.ConnectionLifecycle.Activate(ctx, c)
	}
	return c, err
}

func (h *Host) CancelPlatformConnectionSetup(ctx context.Context, receipt string) error {
	if h.deps.ConnectionStore == nil {
		return platforms.ErrUnsupported
	}
	r, err := h.deps.ConnectionStore.Reservation(ctx, receipt)
	if err != nil {
		return err
	}
	// Dismissing a setup after a lost Save response must not remove the saved
	// account or trap the browser in an editor it can no longer cancel.
	if r.Status == "saved" {
		return nil
	}
	return h.deps.ConnectionStore.CancelReservation(ctx, receipt)
}

// The state directory is a trusted owner-supplied root. Below it, use rooted
// operations and reject symlinks, non-directories and permissive modes. This is
// not a security boundary against another process running as the same user.
func prepareConnectionDirectory(stateDir, directory string, create bool) error {
	if !filepath.IsAbs(stateDir) {
		return fmt.Errorf("connection setup requires an explicit owner state directory")
	}
	rel, err := filepath.Rel(stateDir, directory)
	if err != nil {
		return err
	}
	parts := splitConnectionDirectory(rel)
	if parts == nil {
		return fmt.Errorf("%w: configuration is outside its reserved owner directory", platformconnections.ErrInvalid)
	}
	root, err := os.OpenRoot(stateDir)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for _, part := range parts {
		next, err := privateConnectionDirectory(root, part, create)
		if err != nil {
			return err
		}
		_ = root.Close()
		root = next
	}
	return nil
}

func splitConnectionDirectory(rel string) []string {
	parent, config := filepath.Split(rel)
	parent = filepath.Clean(parent)
	base, id := filepath.Split(parent)
	if filepath.Clean(base) != "platform-connections" || config != "config" {
		return nil
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil
	}
	return []string{"platform-connections", id, "config"}
}

func privateConnectionDirectory(root *os.Root, name string, create bool) (*os.Root, error) {
	if create {
		if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return nil, fmt.Errorf("connection configuration directory must be private and not a symlink")
	}
	next, err := root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := next.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = next.Close()
		return nil, fmt.Errorf("connection configuration directory changed during preparation")
	}
	return next, nil
}
