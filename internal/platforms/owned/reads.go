package owned

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Persisted session reads come only from statev2. The native integration
// contributes live observations, never a historical fallback.

func (a *Adapter) Owns(ctx context.Context, id string) bool {
	_, err := a.storedIdentity(ctx, id)
	return err == nil
}

func (a *Adapter) Sessions(ctx context.Context, directory string, since int64) ([]db.Session, error) {
	rows, err := a.storedSessions(ctx, directory, "")
	return filterStoredSessions(rows, since), err
}

func (a *Adapter) SessionSummary(ctx context.Context, id string) (*db.Session, error) {
	return a.storedSummary(ctx, id)
}

func (a *Adapter) SessionMetadata(ctx context.Context, id string) (*db.Session, error) {
	return a.storedMetadata(ctx, id)
}

func (a *Adapter) Session(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	return a.storedConversation(ctx, id, limit, offset)
}

func (a *Adapter) SessionChanges(ctx context.Context, id string) (*platforms.SessionChanges, error) {
	return a.storedChanges(ctx, id)
}

func (a *Adapter) SessionInfo(ctx context.Context, id string) (*platforms.SessionInfo, error) {
	return a.storedInfo(ctx, id)
}

func (a *Adapter) SessionUsage(ctx context.Context, id string) (map[string]platforms.Usage, error) {
	return a.storedUsage(ctx, id)
}

func (a *Adapter) UnreadCounts(ctx context.Context, cutoffs map[string]int64) (map[string]int, error) {
	return a.storedUnread(ctx, cutoffs)
}

func (a *Adapter) SessionDescendants(ctx context.Context, id string) ([]string, error) {
	key, err := a.storedKey(ctx, id)
	if err != nil {
		return nil, err
	}
	return a.history.Descendants(ctx, key)
}

func (a *Adapter) SessionsInactiveBefore(ctx context.Context, cutoff int64) ([]db.SessionArchiveCandidate, error) {
	return a.storedInactive(ctx, cutoff)
}

func (a *Adapter) LiveStatus(id string) *platforms.LiveState {
	nativeID, ok := a.byID.Load(id)
	if !ok {
		return nil
	}
	return a.native.LiveStatus(nativeID.(string))
}
