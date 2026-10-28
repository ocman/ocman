// Package platformtest supports tests that hand a native integration to code
// typed against the application-facing platforms.Platform contract.
package platformtest

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// NoBrowsing answers every persisted read with ErrHistoryUnavailable, as a
// canonical adapter does before ingestion. Embed it beside a native
// integration to satisfy platforms.Platform in tests; the integration's
// optional capabilities stay visible.
type NoBrowsing struct{}

func (NoBrowsing) Session(context.Context, string, int, int) (*platforms.SessionDetail, error) {
	return nil, platforms.ErrHistoryUnavailable
}
func (NoBrowsing) Owns(context.Context, string) bool { return false }
func (NoBrowsing) SessionsInactiveBefore(context.Context, int64) ([]db.SessionArchiveCandidate, error) {
	return nil, platforms.ErrHistoryUnavailable
}
func (NoBrowsing) SessionChanges(context.Context, string) (*platforms.SessionChanges, error) {
	return nil, platforms.ErrHistoryUnavailable
}
func (NoBrowsing) SessionInfo(context.Context, string) (*platforms.SessionInfo, error) {
	return nil, platforms.ErrHistoryUnavailable
}
func (NoBrowsing) AgentCatalog(context.Context, string) ([]platforms.AgentCatalogEntry, error) {
	return nil, platforms.ErrHistoryUnavailable
}
func (NoBrowsing) SlashCommands(context.Context, string) ([]platforms.SlashCommandEntry, error) {
	return nil, platforms.ErrHistoryUnavailable
}
func (NoBrowsing) SessionModels(context.Context, string) (*platforms.SessionModelsResponse, error) {
	return nil, platforms.ErrHistoryUnavailable
}
func (NoBrowsing) DirectoryCatalog(context.Context, platforms.DirectoryCatalogRequest) (*platforms.DirectoryCatalog, error) {
	return nil, platforms.ErrHistoryUnavailable
}
