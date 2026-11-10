package platforms

import (
	"context"

	"github.com/ocman/ocman/internal/db"
)

// SidebarQuery selects compact browsing metadata, never accounting summaries.
type SidebarQuery struct {
	Directory        string
	Since            int64
	Limit, Offset    int
	Origin, OriginID string
	IncludeIDs       []string `json:",omitempty"`
}

type SidebarReader interface {
	SidebarSessions(context.Context, SidebarQuery) ([]db.Session, error)
}

// SidebarSessionReader supplies a committed compact row for event fan-out,
// without loading a conversation or an entire session inventory.
type SidebarSessionReader interface {
	SidebarSession(context.Context, string) (*db.Session, error)
}

type RunningSessionCounter interface {
	RunningSessionCount(context.Context, SidebarQuery) (int, error)
}

type RunningSessionGroupCounter interface {
	RunningSessionCountFor(context.Context, []SidebarSource, SidebarQuery) (int, error)
}

// SidebarGroupReader combines the owner's enabled local platforms in one query.
type SidebarSource struct {
	Owner, Platform, Source, Generation string
	Scoped                              bool
	Sources                             []string
}
type SidebarGroupReader interface {
	SidebarSource(context.Context) (SidebarSource, error)
	SidebarSessionsFor(context.Context, []SidebarSource, SidebarQuery) ([]db.Session, error)
}

// CachedSessionObserver copies event-maintained observations without RPCs,
// native history reads, directory discovery or lifecycle side effects.
type CachedSessionObserver interface {
	CachedSessionObservations() map[string]LiveState
}

// SessionObservationReader returns an owner-ID-keyed snapshot for maintenance.
// An incomplete live bootstrap returns an error rather than an empty snapshot.
type SessionObservationReader interface {
	SessionObservations(context.Context, []string) (map[string]LiveState, error)
}

// SessionObservationReadiness fences destructive selection until native status
// and prompt caches have been seeded. The bool reports a live source whose
// canonical background snapshot must also be bootstrapped. It never launches.
type SessionObservationReadiness interface {
	SessionObservationsReady(context.Context) (bool, error)
}

// CachedSessionAvailability reports whether this source can accept a new turn.
// It is separate from the event stream's authority to establish busy status.
type CachedSessionAvailability interface{ CachedSessionAvailable() bool }
