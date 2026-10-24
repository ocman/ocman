package owned

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// PrepareCore builds canonical adapters and runs their historical cutover,
// as server startup does in two fenced steps.
func PrepareCore(ctx context.Context, core *state.DB, history *statev2.DB, catalogue *sessioncatalog.Store, owner string, native []platforms.Integration) ([]platforms.Platform, error) {
	adapters, err := BuildCore(ctx, core, history, catalogue, owner, native)
	if err != nil {
		return nil, err
	}
	if err := PrepareBuiltCore(ctx, core, history, catalogue, owner, adapters); err != nil {
		return nil, err
	}
	return adapters, nil
}
