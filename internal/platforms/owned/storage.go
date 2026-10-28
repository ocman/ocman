package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

// StorageSizes samples the native source independently of history readiness.
func (a *Adapter) StorageSizes(ctx context.Context) ([]platforms.StorageSize, error) {
	if sizer, ok := a.native.(platforms.StorageSizer); ok {
		return sizer.StorageSizes(ctx)
	}
	return nil, nil
}
