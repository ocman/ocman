package routines

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
)

func (s *Service) resolveTargetPlatform(ctx context.Context, input *Input) (string, error) {
	if s.resolvePlatform != nil {
		return s.resolvePlatform(ctx, input.RemoteID, input.Platform, input.Model)
	}
	return platforms.PlatformForModel(input.Platform, input.Model)
}

// Resolve defaults before allocating a session. Existing sessions retain their
// own selections; an omitted routine model is not a request to reset them.
func (s *Service) prepareLaunchSelections(ctx context.Context, platformID string, run *state.RoutineRun) error {
	if run.TargetSessionID != "" {
		return nil
	}
	catalog, _, err := s.sessions.DirectoryCatalog(ctx, platformID, platforms.DirectoryCatalogRequest{Directory: run.Directory})
	if errors.Is(err, platforms.ErrUnsupported) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading routine launch catalog: %w", err)
	}
	if catalog == nil {
		return nil
	}
	if run.Agent == "" {
		run.Agent = catalog.DefaultAgent
	}
	if run.Model == "" {
		run.Model = catalog.DefaultModel
		if run.Model == "" && catalog.Models != nil {
			run.Model = catalog.Models.SessionDefault
		}
	}
	if run.Model == "" || catalog.Models == nil || !catalog.Models.HasProviders {
		return nil // Keep native defaults when absent; history cannot prove availability.
	}
	kind, err := s.sessions.PlatformKind(ctx, platformID)
	if err != nil {
		return err
	}
	model, err := platforms.ModelForConnection(platforms.ID(platformID), kind, run.Model)
	if err != nil {
		return err
	}
	model, _, _ = strings.Cut(model, "#")
	for _, entry := range catalog.Models.Models {
		if entry.Provider+"/"+entry.Model == model && entry.IsAvailable {
			return nil
		}
	}
	return fmt.Errorf("routine model %q is unavailable; select an available model on connection %q", run.Model, platformID)
}
