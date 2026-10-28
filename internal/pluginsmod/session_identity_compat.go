package pluginsmod

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

// Only protocol serialization and opaque receipt keys use this compatibility
// projection. Session execution, queues and persisted mappings retain core IDs.
func pluginSessionReference(ctx context.Context, adapter platforms.Platform, id string) (string, error) {
	if identity.IsPendingReference(id) {
		return "", plugins.ErrUnavailable
	}
	if compatibility, ok := adapter.(platforms.PluginSessionCompatibility); ok {
		return compatibility.PluginSessionReference(ctx, id)
	}
	if platforms.CanonicalAdapter(adapter) {
		return "", plugins.ErrUnavailable
	}
	return id, nil
}

func (m *Module) pluginWireSession(ctx context.Context, platform, id string) (string, error) {
	if identity.IsPendingReference(id) {
		return "", plugins.ErrUnavailable
	}
	if m.deps.Adapter == nil {
		if identity.IsSessionID(id) {
			return "", plugins.ErrUnavailable
		}
		return id, nil
	}
	adapter, ok := m.deps.Adapter(ctx, platform, id)
	if !ok {
		return "", plugins.ErrUnavailable
	}
	return pluginSessionReference(ctx, adapter, id)
}

func (m *Module) completePluginSession(ctx context.Context, completion PluginSessionCompletion, id string) error {
	return m.SessionBroker().Completed(ctx, completion.PluginID, sessionOutboxOperation(completion.ID), plugins.SessionCompleted{SessionID: id, Text: completion.Text, Usage: completion.Usage})
}
