package platforms

import (
	"context"
	"io"
	"net/http"

	"github.com/ocman/ocman/internal/db"
)

// OpenCodeEventClientProvider supplies an owner-local account's authenticated
// streaming transport without exposing its credentials to the composition root.
type OpenCodeEventClientProvider interface {
	OpenCodeEventClient() *http.Client
}

// SessionEventProjector normalizes an integration's owner-global stream before
// application observers see session references. Opaque provider data is retained.
type SessionEventProjector interface {
	SessionEventWriter(context.Context, io.Writer) io.Writer
}

// OpenCodeEventObserver retains instance-generation fencing and native live
// caches behind the canonical adapter. Session IDs follow the outer adapter's
// contract; the owning integration translates before touching native caches.
type OpenCodeEventObserver interface {
	PromptPortGeneration(string) uint64
	StatusPortGeneration(string) uint64
	SeedSessionStatusFromInstance(context.Context, string, uint64, []string) bool
	StartPromptReconciliation(context.Context, string, []string, ...func(LivePrompt)) <-chan bool
	ClearPromptsForPort(string)
	ClearSessionStatusForPort(string)
	ObservePromptAskedFromPort(string, uint64, string, string, LivePrompt)
	ObservePromptResolved(string, string, string, string)
	ObserveSessionStatus(string, uint64, string, string, ...*db.SessionNotice) bool
	SessionStatusOnPort(string, string) (db.SessionStatus, error)
}

// StoredSessionStatusSettler combines canonical stored inference with only the
// integration's live, port-fenced lifecycle cache. It must not read native history.
type StoredSessionStatusSettler interface {
	SettleStoredSessionStatus(id, port string, inferred db.SessionStatus) db.SessionStatus
}

func OpenCodeObserver(p Integration) OpenCodeEventObserver {
	if accessor, ok := p.(interface{ OpenCodeObserver() OpenCodeEventObserver }); ok {
		return accessor.OpenCodeObserver()
	}
	observer, _ := p.(OpenCodeEventObserver)
	return observer
}

type InstancePorts interface {
	DiscoverOpenCodePorts() map[string]string
	DiscoverOpenCodePort(string) string
}

type InstancePortProvider interface {
	InstancePorts() InstancePorts
}

// SessionCacheMaintainer accepts the same session identity as its adapter.
type SessionCacheMaintainer interface {
	MarkSessionDirty(string)
	RefreshSession(context.Context, string) error
}
