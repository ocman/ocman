package opencode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/historyparity"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// CompareHistory is an explicit integration diagnostic, not an application
// analytics source or background fallback. Native access stays with its owner.
func (a *Adapter) CompareHistory(ctx context.Context, store *statev2.DB, owner string) (historyparity.Report, error) {
	if a.db == nil {
		return historyparity.Report{}, platforms.ErrHistoryUnavailable
	}
	source, ok := a.db.OpenCodeOnly()
	if !ok {
		return historyparity.Report{}, platforms.ErrHistoryUnavailable
	}
	return historyparity.Compare(ctx, historyparity.Deps{Source: source, Store: store, OwnerID: owner})
}
