package local

import (
	"context"
	"github.com/ocman/ocman/internal/state"
)

// ProviderState persists supervisor handles within an integration's scope.
type ProviderState interface {
	GetManagedProvider(context.Context, string, string) (state.ManagedInstance, bool, error)
	UpsertManagedProvider(context.Context, string, string, state.ManagedInstance) error
	DeleteManagedProvider(context.Context, string, string) error
	ListManagedProviders(context.Context, string) ([]string, error)
}

var _ ProviderState = (*state.DB)(nil)
