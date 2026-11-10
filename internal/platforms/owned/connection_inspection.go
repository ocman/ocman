package owned

import (
	"context"
	"github.com/ocman/ocman/internal/platforms"
)

func (a *Adapter) InspectConnection(ctx context.Context) (platforms.ConnectionInspection, error) {
	if a.unavailable != nil {
		return platforms.ConnectionInspection{}, a.unavailable
	}
	inspector, ok := a.native.(platforms.ConnectionInspector)
	if !ok {
		return platforms.ConnectionInspection{}, platforms.ErrUnsupported
	}
	return inspector.InspectConnection(ctx)
}

func (a *Adapter) ExecutionReady(ctx context.Context) error {
	if err := a.checkSource(ctx); err != nil {
		return err
	}
	if !a.native.Available(ctx) {
		return platforms.ErrPlatformUnreachable
	}
	if a.legacyReadiness {
		return nil
	}
	if inspector, ok := a.native.(platforms.ConnectionInspector); ok {
		result, err := inspector.InspectConnection(ctx)
		if err != nil {
			return err
		}
		if !result.Ready {
			return platforms.ErrPlatformUnreachable
		}
	}
	return nil
}

// WithLegacyReadiness preserves the original machine account's native policy.
// Composition may set this only from the immutable migrated LegacyKind mapping,
// never from a new connection's user-selectable existing configuration mode.
func (a *Adapter) WithLegacyReadiness() *Adapter { a.legacyReadiness = true; return a }
