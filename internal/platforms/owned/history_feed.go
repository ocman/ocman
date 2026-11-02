package owned

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func (a *Adapter) HistorySequences(ctx context.Context) (map[string]int64, error) {
	feed, ok := a.native.(platforms.HistoryFeed)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	if err := a.checkSource(ctx); err != nil {
		return nil, err
	}
	sequences, err := feed.HistorySequences(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(sequences))
	deferred := 0
	for nativeID, seq := range sequences {
		row, err := a.historyIdentity(ctx, nativeID)
		if errors.Is(err, sessioncatalog.ErrCreating) {
			deferred++
			continue
		}
		if err != nil {
			return nil, err
		}
		if !row.Purged {
			out[row.ID] = seq
		}
	}
	if err := a.checkSource(ctx); err != nil {
		return nil, err
	}
	if deferred > 0 {
		return out, &platforms.HistoryDeferred{Count: deferred}
	}
	return out, nil
}

// HistorySequence resolves only the requested session. Unknown and purged
// sessions report no sequence, as they are absent from HistorySequences.
func (a *Adapter) HistorySequence(ctx context.Context, id string) (int64, bool, error) {
	feed, ok := a.native.(platforms.HistoryFeed)
	if !ok {
		return 0, false, platforms.ErrUnsupported
	}
	nativeID, err := a.nativeID(ctx, id)
	switch {
	case errors.Is(err, platforms.ErrNotFound):
		return 0, false, nil
	case errors.Is(err, sessioncatalog.ErrCreating), errors.Is(err, sessioncatalog.ErrCreationFailed):
		return 0, false, &platforms.HistoryDeferred{Count: 1}
	case err != nil:
		return 0, false, err
	}
	var seq int64
	var found bool
	if reader, single := feed.(platforms.HistorySequenceReader); single {
		seq, found, err = reader.HistorySequence(ctx, nativeID)
	} else {
		var sequences map[string]int64
		sequences, err = feed.HistorySequences(ctx)
		seq, found = sequences[nativeID]
	}
	if err != nil {
		return 0, false, err
	}
	if err := a.checkSource(ctx); err != nil {
		return 0, false, err
	}
	return seq, found, nil
}

func (a *Adapter) HistoryMessages(ctx context.Context, id string, messageIDs []string) (*db.HistorySnapshot, error) {
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	feed, ok := a.native.(platforms.HistoryFeed)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	snapshot, err := feed.HistoryMessages(ctx, nativeID, messageIDs)
	if err != nil {
		return nil, err
	}
	return a.historySnapshot(ctx, nativeID, snapshot)
}

func (a *Adapter) WatchHistory(ctx context.Context, connected func(), change func(platforms.HistoryChange)) error {
	feed, ok := a.native.(platforms.HistoryFeed)
	if !ok {
		return platforms.ErrUnsupported
	}
	if err := a.checkSource(ctx); err != nil {
		return err
	}
	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var projectionErr error
	err := feed.WatchHistory(watchCtx, connected, func(event platforms.HistoryChange) {
		if projectionErr != nil {
			return
		}
		row, err := a.historyIdentity(watchCtx, event.SessionID)
		if err != nil {
			projectionErr = err
			cancel()
			return
		}
		if row.Purged {
			return
		}
		event.SessionID = row.ID
		change(event)
	})
	if projectionErr != nil {
		return projectionErr
	}
	return err
}
