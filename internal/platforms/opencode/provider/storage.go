package opencode

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

func (a *Adapter) StorageSizes(ctx context.Context) ([]platforms.StorageSize, error) {
	if a.db == nil {
		return nil, nil
	}
	size, err := a.db.SizeBytes(ctx)
	if err != nil {
		return nil, err
	}
	return []platforms.StorageSize{{Name: "opencode", SizeBytes: size}}, nil
}
