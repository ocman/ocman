package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

type storageNative struct{ platforms.Integration }

func (storageNative) StorageSizes(context.Context) ([]platforms.StorageSize, error) {
	return []platforms.StorageSize{{Name: "test:raw", SizeBytes: 42}}, context.DeadlineExceeded
}

func TestStorageSizesForwardsWithoutHistory(t *testing.T) {
	a := &Adapter{native: storageNative{}, unavailable: platforms.ErrHistoryUnavailable}
	items, err := a.StorageSizes(t.Context())
	if !errors.Is(err, context.DeadlineExceeded) || len(items) != 1 || items[0].SizeBytes != 42 {
		t.Fatalf("sizes = %+v, %v", items, err)
	}
	a.native = nil
	if items, err := a.StorageSizes(t.Context()); err != nil || len(items) != 0 {
		t.Fatalf("unsupported sizes = %+v, %v", items, err)
	}
}
