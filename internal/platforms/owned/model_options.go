package owned

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) ModelOptions(ctx context.Context) []string {
	if reader, ok := a.native.(platforms.ModelOptionsReader); ok {
		return reader.ModelOptions(ctx)
	}
	return nil
}
