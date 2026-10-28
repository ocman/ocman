package platforms

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/db"
)

// StoredSessionEnricher overlays only current platform observations onto stored
// metadata. Inputs use integration-local IDs. It must not read native history,
// replace persisted facts, or execute lifecycle actions.
type StoredSessionEnricher interface {
	EnrichStoredSessions(context.Context, []db.Session, map[string]string) error
}

// StoredReadConfigurer supplies live configuration, never native session data.
// A non-empty directory also requests current warnings for that directory.
type StoredReadConfigurer interface {
	StoredReadConfig(directory string) *ConversationHead
}

// SessionHaltIdentity scopes persisted halt receipts while the native prompt
// registry retains integration-local IDs in its in-memory observations. Frozen
// pre-cutover keys stay compatible; modern receipts use canonical identities.
type SessionHaltIdentity interface {
	SetSessionHaltIdentity(func(context.Context, string) (string, error), func(context.Context, string) (string, error))
}

// StoredCatalogReader merges ingested recents/defaults with live configuration.
// The integration must not query its native database as a historical fallback.
type StoredCatalogReader interface {
	DirectoryCatalogFromStored(context.Context, DirectoryCatalogRequest, []db.RecentModel, db.SessionDefaults, bool) (*DirectoryCatalog, error)
}

// LiveDirectoryCatalogReader is an explicit configuration-only capability.
// Adapters without it cannot receive a native historical catalogue fallback.
type LiveDirectoryCatalogReader interface {
	LiveDirectoryCatalog(context.Context, DirectoryCatalogRequest) (*DirectoryCatalog, error)
}

// StoredDetailProjector computes platform-specific panels from already-ingested
// v1-compatible DTOs. Live configuration may enrich info; native history may not.
type StoredDetailProjector interface {
	ChangesFromStored(context.Context, *SessionDetail) (*SessionChanges, error)
	InfoFromStored(context.Context, *SessionDetail) (*SessionInfo, error)
}

// StoredHistoryWait bounds a read-your-writes wait for ingestion.
const StoredHistoryWait = 5 * time.Second

// StoredHistoryHooks are wired before serving. Invalidations and bounded
// read-your-writes waits go to ingestion instead of native browsing caches.
type StoredHistoryHooks struct {
	Notify  func(string)
	Await   func(context.Context, string) bool
	Refresh func(context.Context, string) error
}
type StoredHistoryBinder interface{ BindStoredHistory(StoredHistoryHooks) }

// OverlaySessionObservation is the shared event-cache rule for stored reads.
// Persisted message shape alone can never establish that a turn is running.
func OverlaySessionObservation(row *db.Session, live *LiveState, connected bool) {
	inferred := row.Status
	row.LiveConnection = connected
	row.PendingPermission, row.PendingQuestion = false, false
	row.Status = db.SettleSessionStatus(db.TurnUnobserved, false, row.Status)
	if live != nil {
		row.BackgroundTaskCount, row.BackgroundServiceCount = live.BackgroundTaskCount, live.BackgroundServiceCount
	}
	if live == nil || !connected {
		return
	}
	row.PendingPermission, row.PendingQuestion = live.PendingPermission, live.PendingQuestion
	switch db.SessionStatus(live.Status) {
	case "idle":
		row.Status = db.SettleSessionStatus(db.TurnSettled, connected, inferred)
	case db.StatusBusy:
		row.Status = db.SettleSessionStatus(db.TurnRunning, connected, row.Status)
	case db.StatusWaiting, db.StatusDone, db.StatusError, db.StatusInterrupted:
		row.Status = db.SettleSessionStatus(db.TurnSettled, connected, db.SessionStatus(live.Status))
	case db.StatusLimited:
		row.Status = db.SettleSessionStatus(db.TurnLimited, connected, row.Status)
	}
	if row.Status == db.StatusBusy && (row.PendingPermission || row.PendingQuestion) {
		row.Status = db.StatusWaiting
	}
}

// BubblePromptSessions uses only ingested parent evidence. A malformed cycle is
// bounded and cannot borrow another source's similarly named parent.
func BubblePromptSessions(prompted map[string]bool, parents map[string]string) map[string]bool {
	out := map[string]bool{}
	for id, pending := range prompted {
		if !pending {
			continue
		}
		root := id
		seen := map[string]bool{}
		for parents[root] != "" && !seen[root] {
			seen[root] = true
			root = parents[root]
		}
		if seen[root] {
			root = id
		}
		out[root] = true
	}
	return out
}
