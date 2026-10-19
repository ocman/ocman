package platforms

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
)

// HistoryIdentity describes a physical, owner-local history source. It does not
// identify a running supervisor, model provider, or network credential.
type HistoryIdentity struct {
	Platform  string
	SourceKey string
	Name      string
}

// HistoryReader is optional. Unlike UI session lists, its inventory must include
// every retained session, including completed children and hidden helpers. A
// failed or partial inventory must return an error, never a successful empty list.
// Snapshots must be internally consistent and contain all messages and parts.
// These reads must not launch agents, acknowledge turns, or publish lifecycle events.
// A reader is bound to one physical identity for its lifetime; changing source
// configuration requires replacing the reader and its worker, never rebinding it.
// HistoryChange is one durable source change to one session. Seq is the
// source's per-session sequence. MessageIDs names the messages to read; an
// empty list with neither flag set is a metadata-only change.
type HistoryChange struct {
	SessionID  string
	Seq        int64
	MessageIDs []string
	Resync     bool // touched an unknown range of messages; reread the session
	Deleted    bool
}

// HistoryFeed is optional on a HistoryReader whose source publishes durable
// changes. Sequences is a cheap content-free inventory used to catch up after
// downtime; Watch streams live changes until the source disconnects, calling
// connected once the stream is established and before any change it delivers.
type HistoryFeed interface {
	HistorySequences(context.Context) (map[string]int64, error)
	HistoryMessages(ctx context.Context, sessionID string, messageIDs []string) (*db.HistorySnapshot, error)
	WatchHistory(ctx context.Context, connected func(), change func(HistoryChange)) error
}

// HistoryJournal is optional on a HistoryReader whose source has no durable
// feed but whose hooks append transcript-change records (historyjournal) in
// every session. Ingestion follows the journal instead of polling.
type HistoryJournal interface {
	HistoryJournalDir() string
}

type HistoryReader interface {
	HistoryIdentity(context.Context) (HistoryIdentity, error)
	HistoryInventory(context.Context) ([]db.HistoryEntry, error)
	HistorySnapshot(context.Context, string) (*db.HistorySnapshot, error)
}
