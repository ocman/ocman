package opencode

import (
	"context"
	"testing"

	ocapi "github.com/ocman/ocman/internal/platforms/opencode/api"
)

func TestStorageSizes(t *testing.T) {
	database := newTestDBWithSessions(t, nil)
	a := New(database, nil, nil, ocapi.Auth{})
	items, err := a.StorageSizes(t.Context())
	if err != nil || len(items) != 1 || items[0].Name != "opencode" || items[0].SizeBytes <= 0 {
		t.Fatalf("sizes = %+v, %v", items, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.StorageSizes(ctx); err == nil {
		t.Fatal("cancellation ignored")
	}
	if items, err := New(nil, nil, nil, ocapi.Auth{}).StorageSizes(t.Context()); err != nil || len(items) != 0 {
		t.Fatalf("absent source = %+v, %v", items, err)
	}
}
