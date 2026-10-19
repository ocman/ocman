// Package conversationread serves validated, settled conversation snapshots.
// Platform adapters retain all live, mutation and streaming authority.
package conversationread

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type Service struct {
	Store   *statev2.DB
	OwnerID string
	Notify  func(string, string)
	// Await asks ingestion to apply one session and waits, bounded by ctx,
	// until its stored history covers version. A turn that just ended is
	// read before its final events are applied; waiting is read-your-writes,
	// not a fallback to the platform's transcript.
	Await func(ctx context.Context, platformID, sessionID, version string) bool
}

// AwaitTimeout bounds how long a settled read waits for ingestion.
const AwaitTimeout = 5 * time.Second

func (s *Service) ReadConversation(ctx context.Context, p platforms.Platform, id string, limit, offset int) (out *platforms.SessionDetail, handled bool, readErr error) {
	defer func() {
		if ctx.Err() != nil {
			out, handled, readErr = nil, true, ctx.Err()
		}
	}()
	if ctx.Err() != nil {
		return nil, true, ctx.Err()
	}
	if owner, _ := platforms.SplitPlatformID(string(p.ID())); owner != "" {
		return nil, false, nil
	}
	if active(p.LiveStatus(id)) {
		return nil, false, nil
	}
	probe, ok := p.(platforms.ConversationHeadReader)
	if !ok {
		return nil, true, s.unavailable(p, id, "platform history metadata is unavailable")
	}
	head, err := probe.ConversationHead(ctx, id)
	if err != nil && !errors.Is(err, platforms.ErrNotFound) {
		return nil, true, s.unavailable(p, id, "source metadata is unavailable")
	}
	if head == nil {
		return nil, true, s.unavailable(p, id, "source metadata is unavailable")
	}
	if (err == nil && head.Session == nil) || (head.Session != nil && head.Session.ID != id) {
		return nil, true, s.unavailable(p, id, "source metadata identity mismatch")
	}
	if err == nil && (head.LiveOnly || platformRead(head.Session) || active(p.LiveStatus(id))) {
		return nil, false, nil
	}
	if err == nil && !settled(head.Session) {
		return nil, true, s.unavailable(p, id, "source lifecycle is unknown")
	}
	if errors.Is(err, platforms.ErrNotFound) && (s.Store == nil || s.OwnerID == "") {
		return nil, true, platforms.ErrNotFound
	}
	if s.Store == nil || s.OwnerID == "" {
		return nil, true, s.unavailable(p, id, "history ingestion is unavailable")
	}
	page, reason, lookupErr := s.initialPage(ctx, head, id, limit, offset, errors.Is(err, platforms.ErrNotFound))
	// A newly created session can reach its first read before even its source
	// registration or validation receipt exists. Give ingestion the same bounded
	// read-your-writes wait as an existing, stale snapshot.
	if reason != "" && err == nil && head.Version != "" && s.Await != nil {
		if active(p.LiveStatus(id)) {
			return nil, false, nil
		}
		waitCtx, cancel := context.WithTimeout(ctx, AwaitTimeout)
		caughtUp := s.Await(waitCtx, string(p.ID()), id, head.Version)
		cancel()
		if active(p.LiveStatus(id)) {
			return nil, false, nil
		}
		if caughtUp {
			page, reason, lookupErr = s.initialPage(ctx, head, id, limit, offset, false)
		}
	}
	if reason != "" {
		return nil, true, s.unavailable(p, id, reason)
	}
	if lookupErr != nil {
		return nil, true, lookupErr
	}
	key := page.Session.Key
	retained := errors.Is(err, platforms.ErrNotFound) && page.Session.SourceDeleted
	if !retained && head.Version != "" && !page.Session.SourceDeleted && !covers(page.ReadyVersion, head.Version) && s.Await != nil {
		if active(p.LiveStatus(id)) {
			return nil, false, nil
		}
		waitCtx, cancel := context.WithTimeout(ctx, AwaitTimeout)
		caughtUp := s.Await(waitCtx, string(p.ID()), id, head.Version)
		cancel()
		if active(p.LiveStatus(id)) {
			return nil, false, nil
		}
		if caughtUp {
			if page, readErr = s.Store.ReadConversation(ctx, key, limit, offset); readErr != nil {
				return nil, true, s.unavailable(p, id, "conversation import is pending")
			}
		}
	}
	if !retained && (page.Session.SourceDeleted || head.Version == "" || !covers(page.ReadyVersion, head.Version)) {
		return nil, true, s.unavailable(p, id, "conversation import has not caught up")
	}
	if !retained {
		after, err := probe.ConversationHead(ctx, id)
		if err != nil || after == nil || after.Session == nil || after.Session.ID != id || after.Identity.Platform != head.Identity.Platform || after.Identity.SourceKey != head.Identity.SourceKey {
			return nil, true, s.unavailable(p, id, "source changed during the read")
		}
		// A settled session still receives trailing events, such as a generated
		// title. Accept a newer head the stored history already covers, else
		// wait for it once more before failing closed.
		if platformRead(after.Session) || active(p.LiveStatus(id)) {
			return nil, false, nil
		}
		if !covers(page.ReadyVersion, after.Version) && s.Await != nil {
			waitCtx, cancel := context.WithTimeout(ctx, AwaitTimeout)
			caughtUp := s.Await(waitCtx, string(p.ID()), id, after.Version)
			cancel()
			if active(p.LiveStatus(id)) {
				return nil, false, nil
			}
			if caughtUp {
				if page, readErr = s.Store.ReadConversation(ctx, key, limit, offset); readErr != nil {
					return nil, true, s.unavailable(p, id, "conversation import is pending")
				}
			}
		}
		if !covers(page.ReadyVersion, after.Version) {
			return nil, true, s.unavailable(p, id, "source changed during the read")
		}
		if platformRead(after.Session) || active(p.LiveStatus(id)) {
			return nil, false, nil
		}
		if !settled(after.Session) {
			return nil, true, s.unavailable(p, id, "source lifecycle is unknown")
		}
		head = after
	}
	detail, err := Convert(page, head, string(p.ID()))
	if err != nil {
		return nil, true, s.unavailable(p, id, "stored conversation could not be decoded")
	}
	// Tree rows carry current, bounded lifecycle metadata when available. A
	// missing owner never turns stored-message inference into a running signal.
	for i := range detail.SessionTree {
		row := &detail.SessionTree[i]
		if row.ID == id {
			continue
		}
		other, err := probe.ConversationHead(ctx, row.ID)
		if err == nil && other != nil && other.Session != nil && other.Session.ID == row.ID && other.Identity.Platform == head.Identity.Platform && other.Identity.SourceKey == head.Identity.SourceKey {
			*row = *other.Session
			row.Platform = string(p.ID())
			applyTotals(row, page.ConversationTotals[row.ID], head)
		}
	}
	return detail, true, nil
}

var covers = db.VersionCovers

func (s *Service) unavailable(p platforms.Platform, id, reason string) error {
	if s.Notify != nil {
		s.Notify(string(p.ID()), id)
	}
	return fmt.Errorf("%w: %s; background ingestion will retry", platforms.ErrHistoryUnavailable, reason)
}

func active(live *platforms.LiveState) bool {
	return live != nil && (live.Status == string(db.StatusBusy) || live.Status == string(db.StatusLimited) || live.PendingPermission || live.PendingQuestion)
}

func settled(row *db.Session) bool {
	if row == nil || row.PendingPermission || row.PendingQuestion {
		return false
	}
	return row.Status == db.StatusDone || row.Status == db.StatusWaiting || row.Status == db.StatusError || row.Status == db.StatusInterrupted
}

func platformRead(row *db.Session) bool {
	return row != nil && (row.Status == db.StatusBusy || row.Status == db.StatusLimited || row.PendingPermission || row.PendingQuestion)
}
