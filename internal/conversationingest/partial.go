package conversationingest

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

func (w *sourceWorker) rememberSnapshot(ctx context.Context, key statev2.Key, result importResult, partial bool) (importResult, error) {
	if partial {
		// Runtime bootstrap cannot validate absent content. Use the committed
		// total, preserving all previously retained messages and accounting.
		var err error
		result.usage, err = w.module.deps.Store.Usage(ctx, key)
		if err != nil {
			return importResult{}, err
		}
	}
	return w.remember(ctx, key.SessionID, result)
}
