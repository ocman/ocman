package conversationingest

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

const (
	sequencePrefix = "seq/"
	feedDebounce   = 100 * time.Millisecond
)

// feedState is filled by the watch goroutine and drained by the worker.
type feedState struct {
	mu         sync.Mutex
	pending    map[string]*platforms.HistoryChange
	catchUp    bool
	connected  bool
	messageIDs map[string]map[string]bool
	ids        int
	overflow   bool
}

func feedSupported(ctx context.Context, feed platforms.HistoryFeed) bool {
	_, err := feed.HistorySequences(ctx)
	return !errors.Is(err, platforms.ErrUnsupported)
}

// runFeed keeps a feed source current without polling its content. Live
// changes read only the messages they name. Each (re)connection triggers a
// catch-up that compares the source's per-session event sequences with the
// sequences already applied, so downtime costs one read per changed session.
// Only while the feed is down does the cheap sequence comparison repeat.
func (w *sourceWorker) runFeed(ctx context.Context) {
	w.feedState = feedState{pending: map[string]*platforms.HistoryChange{}, catchUp: true}
	var watcher sync.WaitGroup
	watcher.Go(func() { w.watchFeed(ctx) })
	defer watcher.Wait()
	ticker := time.NewTicker(w.module.deps.PollInterval)
	defer ticker.Stop()
	w.drainFeed(ctx)
	var retry retryBackoff
	if w.lastFailure != "" {
		retry.fail()
	}
	for {
		var retryC <-chan time.Time
		var retryTimer *time.Timer
		if !retry.next.IsZero() {
			retryTimer = time.NewTimer(max(0, time.Until(retry.next)))
			retryC = retryTimer.C
		}
		select {
		case <-ctx.Done():
			if retryTimer != nil {
				retryTimer.Stop()
			}
			return
		case <-retryC:
		case <-ticker.C:
			w.feedState.mu.Lock()
			disconnected := !w.feedState.connected
			w.feedState.catchUp = w.feedState.catchUp || disconnected
			w.feedState.mu.Unlock()
			if !disconnected && w.lastFailure == "" {
				w.readiness(true) // live and current: nothing to read
			}
		case <-w.wake:
			// Live changes are cheap targeted reads; coalesce them briefly
			// so a just-finished turn is readable without a visible delay.
			timer := time.NewTimer(min(w.module.deps.Debounce, feedDebounce))
			select {
			case <-ctx.Done():
				timer.Stop()
				if retryTimer != nil {
					retryTimer.Stop()
				}
				return
			case <-timer.C:
			}
		}
		if retryTimer != nil {
			retryTimer.Stop()
		}
		if !retry.due() {
			continue
		}
		w.drainFeed(ctx)
		if w.lastFailure != "" {
			retry.fail()
		} else {
			retry = retryBackoff{}
		}
	}
}

func (w *sourceWorker) watchFeed(ctx context.Context) {
	for ctx.Err() == nil {
		err := w.feed.WatchHistory(ctx, func() {
			w.feedState.mu.Lock()
			w.feedState.connected, w.feedState.catchUp = true, true
			w.feedState.mu.Unlock()
			w.signal()
		}, w.enqueue)
		w.feedState.mu.Lock()
		w.feedState.connected = false
		w.feedState.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		log.WithError(err).WithField("source", w.source.AdapterID).Debug("conversation history feed disconnected; reconnecting")
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// enqueue merges changes per session; the latest sequence wins.
func (w *sourceWorker) enqueue(c platforms.HistoryChange) {
	w.feedState.mu.Lock()
	defer w.feedState.mu.Unlock()
	defer w.signal()
	if w.feedState.overflow {
		return
	}
	merged := w.feedState.pending[c.SessionID]
	if merged == nil {
		merged = &platforms.HistoryChange{SessionID: c.SessionID}
		w.feedState.pending[c.SessionID] = merged
	}
	merged.Seq = max(merged.Seq, c.Seq)
	merged.Resync = merged.Resync || c.Resync
	merged.Deleted = merged.Deleted || c.Deleted
	if w.feedState.messageIDs == nil {
		w.feedState.messageIDs = map[string]map[string]bool{}
	}
	seen := w.feedState.messageIDs[c.SessionID]
	if seen == nil {
		seen = map[string]bool{}
		w.feedState.messageIDs[c.SessionID] = seen
	}
	for _, id := range c.MessageIDs {
		if !seen[id] {
			seen[id] = true
			merged.MessageIDs = append(merged.MessageIDs, id)
			w.feedState.ids++
		}
		if w.feedState.ids > 16384 {
			break
		}
	}
	if len(w.feedState.pending) > 4096 || w.feedState.ids > 16384 {
		clear(w.feedState.pending)
		clear(w.feedState.messageIDs)
		w.feedState.ids = 0
		w.feedState.overflow, w.feedState.catchUp = true, true
	}
}

func (w *sourceWorker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *sourceWorker) drainFeed(ctx context.Context) {
	w.feedState.mu.Lock()
	catchUp, pending := w.feedState.catchUp, w.feedState.pending
	w.feedState.catchUp, w.feedState.pending = false, map[string]*platforms.HistoryChange{}
	w.feedState.messageIDs, w.feedState.ids, w.feedState.overflow = nil, 0, false
	w.feedState.mu.Unlock()
	w.mu.Lock()
	dirty := w.dirty
	w.dirty = map[string]bool{}
	w.mu.Unlock()
	w.last.Counters = nil
	err := w.ensureInstance(ctx)
	if err == nil && (catchUp || dirty[""]) {
		w.readiness(false)
		err = w.catchUpFeed(ctx, dirty)
	}
	if err == nil {
		err = w.applyChanges(ctx, pending, dirty)
	}
	if err == nil && w.last.Counters["deferred"] > 0 {
		err = errDeferred
	}
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		// A sequence may not move on an explicit repair. Keep invalidations
		// independently of sequence checkpoints, including deferred reads.
		w.mu.Lock()
		for id := range dirty {
			w.dirty[id] = true
		}
		for id := range pending {
			w.dirty[id] = true
		}
		w.mu.Unlock()
		w.feedState.mu.Lock()
		w.feedState.catchUp = true
		w.feedState.mu.Unlock()
		w.readiness(false)
		_ = w.recordFailure(ctx, err, w.last)
		return
	}
	w.lastFailure = ""
	if catchUp || dirty[""] {
		w.readiness(true)
	}
}

// catchUpFeed reads only sessions whose durable sequence differs from the one
// already applied, plus sessions the source no longer lists (retained as
// deleted). The inventory is a list of identities, not conversation content.
func (w *sourceWorker) catchUpFeed(ctx context.Context, dirty map[string]bool) error {
	store := w.module.deps.Store
	readCtx, cancel := context.WithTimeout(ctx, w.module.deps.ReadTimeout)
	defer cancel()
	sequences, err := w.feed.HistorySequences(readCtx)
	sequenceDeferred, err := inventoryDeferrals(err)
	if err != nil {
		return err
	}
	inventory, deferred, err := readInventory(readCtx, w.source.Reader)
	if err != nil {
		return err
	}
	applied, err := store.Checkpoints(ctx, w.instance.ID, sequencePrefix)
	if err != nil {
		return err
	}
	receipts, err := store.SourceState(ctx, w.instance.ID)
	if err != nil {
		return err
	}
	known, err := store.Sessions(ctx, w.instance.ID)
	if err != nil {
		return err
	}
	stored := map[string]statev2.Session{}
	for _, s := range known {
		stored[s.SessionID] = s
	}
	p := pass{deferred: max(sequenceDeferred, deferred)}
	present := map[string]bool{}
	for _, entry := range inventory {
		if entry.ID == "" || present[entry.ID] {
			return errors.New("history inventory contains an empty or duplicate identity")
		}
		present[entry.ID] = true
		seq, ok := sequences[entry.ID]
		version := db.SequenceVersion(seq, ok)
		s, have := stored[entry.ID]
		if have && !s.SourceDeleted && s.ReadFormat == statev2.ConversationReadFormat && applied[entry.ID] == version && receipts["read-ready/"+entry.ID] == version {
			p.skipped++
			continue
		}
		p.reads = append(p.reads, db.HistoryEntry{ID: entry.ID, Version: version})
	}
	for _, s := range known {
		if p.deferred == 0 && !s.SourceDeleted && !present[s.SessionID] {
			p.deletions = append(p.deletions, s)
		}
	}
	for id := range w.failing {
		if p.deferred == 0 && !present[id] {
			delete(w.failing, id)
		}
	}
	for id := range dirty {
		if id != "" && !present[id] {
			delete(dirty, id)
		}
	}
	if len(p.reads) == 0 && len(p.deletions) == 0 && p.deferred == 0 {
		return nil
	}
	return w.requireComplete(w.runPrepared(ctx, p))
}

// applyChanges handles live changes and explicit repair requests.
func (w *sourceWorker) applyChanges(ctx context.Context, pending map[string]*platforms.HistoryChange, dirty map[string]bool) error {
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := w.applyChange(ctx, *pending[id]); err != nil {
			return err
		}
	}
	for id := range dirty {
		if id == "" || pending[id] != nil {
			continue
		}
		if err := w.refreshFeedSession(ctx, id, dirty[id]); err != nil {
			return err
		}
	}
	return nil
}

func (w *sourceWorker) applyChange(ctx context.Context, c platforms.HistoryChange) error {
	store := w.module.deps.Store
	key := statev2.Key{InstanceID: w.instance.ID, SessionID: c.SessionID}
	version := db.SequenceVersion(c.Seq, true)
	stored, err := store.SessionState(ctx, key)
	if err != nil {
		return err
	}
	exists := stored.Revision > 0
	if exists && !stored.SourceDeleted && stored.ReadFormat == statev2.ConversationReadFormat {
		applied, err := store.Checkpoint(ctx, w.instance.ID, sequencePrefix+c.SessionID)
		if err != nil {
			return err
		}
		if applied == version && !w.explicitReadPending(c.SessionID) {
			return nil
		}
	}
	switch {
	case c.Deleted:
		if exists && !stored.SourceDeleted {
			if err := w.markDeleted(ctx, stored); err != nil {
				return err
			}
		}
		return w.advanceSequence(ctx, c.SessionID, version)
	case c.Resync || !exists || stored.SourceDeleted || stored.ReadFormat != statev2.ConversationReadFormat:
		return w.requireComplete(w.execute(ctx, pass{reads: []db.HistoryEntry{{ID: c.SessionID, Version: version}}}, nil))
	}
	readCtx, cancel := context.WithTimeout(ctx, w.module.deps.ReadTimeout)
	ticket := w.module.readTicket(w.source.AdapterID, c.SessionID)
	snapshot, err := w.feed.HistoryMessages(readCtx, c.SessionID, uniqueSorted(c.MessageIDs))
	cancel()
	if err != nil {
		return err
	}
	batch, _, err := Normalize(key, snapshot)
	if err != nil {
		return err
	}
	current, err := store.Checkpoint(ctx, w.instance.ID, sequencePrefix+c.SessionID)
	if err != nil {
		return err
	}
	if current == version {
		return w.approveRead(ctx, c.SessionID, version, ticket)
	}
	if _, err := store.ApplyStream(ctx, statev2.StreamUpdate{InstanceID: w.instance.ID,
		Checkpoint: statev2.CheckpointUpdate{Stream: sequencePrefix + c.SessionID, ExpectedCursor: current, Cursor: version},
		Batches:    []statev2.StreamBatch{{Session: &batch.Session, Messages: batch.Messages, Parts: batch.Parts}}}); err != nil {
		return err
	}
	return w.approveRead(ctx, c.SessionID, version, ticket)
}

// advanceSequence records the sequence a session's stored history reflects.
func (w *sourceWorker) advanceSequence(ctx context.Context, sessionID, version string) error {
	store := w.module.deps.Store
	current, err := store.Checkpoint(ctx, w.instance.ID, sequencePrefix+sessionID)
	if err != nil || current == version {
		return err
	}
	_, err = store.ApplyStream(ctx, statev2.StreamUpdate{InstanceID: w.instance.ID,
		Checkpoint: statev2.CheckpointUpdate{Stream: sequencePrefix + sessionID, ExpectedCursor: current, Cursor: version}})
	return err
}

func (w *sourceWorker) sessionVersion(ctx context.Context, id string) (string, error) {
	sequences, err := w.feed.HistorySequences(ctx)
	if err != nil {
		return "", err
	}
	seq, ok := sequences[id]
	return db.SequenceVersion(seq, ok), nil
}

func uniqueSorted(ids []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
