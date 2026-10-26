package platforms

import "context"

// StorageSize describes one independently sampled storage item. Name is a
// stable, globally unique series key, conventionally platform or platform:item.
// Sizes are logical bytes, not filesystem allocation or SQLite WAL overhead.
type StorageSize struct {
	Name      string
	SizeBytes int64
}

// StorageSizer is an optional platform method for owner-local storage metrics.
// A platform may return multiple items and partial results alongside an error.
// It must not launch processes or depend on conversation ingestion readiness.
type StorageSizer interface {
	StorageSizes(context.Context) ([]StorageSize, error)
}
