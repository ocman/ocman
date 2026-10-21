package platforms

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
)

// SidebarQuery selects compact browsing metadata, never accounting summaries.
type SidebarQuery struct {
	Directory        string
	Since            int64
	Limit, Offset    int
	Origin, OriginID string
	IncludeIDs       []string         `json:",omitempty"`
	UseSeenCutoffs   bool             `json:",omitempty"`
	SeenCutoffs      map[string]int64 `json:",omitempty"`
}

type SidebarReader interface {
	SidebarSessions(context.Context, SidebarQuery) ([]db.Session, error)
}

// SidebarGroupReader combines the owner's enabled local platforms in one query.
type SidebarSource struct{ Owner, Platform, Source, Generation string }
type SidebarGroupReader interface {
	SidebarSource(context.Context) (SidebarSource, error)
	SidebarSessionsFor(context.Context, []SidebarSource, SidebarQuery) ([]db.Session, error)
}

// CachedSessionObserver copies event-maintained observations without RPCs,
// native history reads, directory discovery or lifecycle side effects.
type CachedSessionObserver interface {
	CachedSessionObservations() map[string]LiveState
}

// CachedSessionAvailability reports whether this source can accept a new turn.
// It is separate from the event stream's authority to establish busy status.
type CachedSessionAvailability interface{ CachedSessionAvailable() bool }
