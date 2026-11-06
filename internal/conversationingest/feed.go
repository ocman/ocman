package conversationingest

import (
	"context"
	"errors"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
	"github.com/ocman/ocman/internal/telemetry"
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
		w.serveFeedPriority(ctx, retry)
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
	var retry reconnectBackoff
	for ctx.Err() == nil {
		// Demand already satisfied by this attempt must not shorten its retry.
		select {
		case <-w.reconnect:
		default:
		}
		var connectedAt time.Time // guarded by feedState.mu with connection state
		finish := telemetry.ConversationWork(ctx, telemetry.ConversationWatch)
		err := w.feed.WatchHistory(ctx, func() {
			w.feedState.mu.Lock()
			connectedAt = time.Now()
			w.feedState.connected, w.feedState.catchUp = true, true
			w.feedState.mu.Unlock()
			w.signal()
		}, w.enqueue)
		finish(err, 1)
		w.feedState.mu.Lock()
		w.feedState.connected = false
		var uptime time.Duration
		if !connectedAt.IsZero() {
			uptime = time.Since(connectedAt)
		}
		w.feedState.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		log.WithError(err).WithField("source", w.source.AdapterID).Debug("conversation history feed disconnected; reconnecting")
		if !waitReconnect(ctx, w.reconnect, retry.after(uptime)) {
			return
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
	finish := telemetry.ConversationWork(ctx, telemetry.ConversationDrain)
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
	defer func() { finish(err, int64(len(pending)+len(dirty))) }()
	if err == nil && (catchUp || dirty[""]) {
		w.readiness(false)
		err = w.catchUpFeed(ctx, dirty, pending)
	}
	if err == nil {
		err = w.applyChanges(ctx, pending, dirty)
	}
	if err == nil && w.last.Counters["deferred"] > 0 {
		err = errDeferred
	}
	if err == nil && (catchUp || dirty[""]) {
		err = w.completeInitialImport(ctx)
	}
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		// A sequence may not move on an explicit repair. Keep invalidations
		// independently of sequence checkpoints, including deferred reads.
		w.mu.Lock()
		for id := range dirty {
			w.dirty[id] = w.dirty[id] || dirty[id]
		}
		w.mu.Unlock()
		for _, change := range pending {
			w.enqueue(*change)
		}
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
func (w *sourceWorker) catchUpFeed(ctx context.Context, dirty map[string]bool, pending map[string]*platforms.HistoryChange) error {
	store := w.module.deps.Store
	readCtx, cancel := context.WithTimeout(ctx, w.module.deps.ReadTimeout)
	defer cancel()
	tickets := w.module.readTickets(w.source.AdapterID)
	known, err := store.Sessions(ctx, w.instance.ID)
	if err != nil {
		return err
	}
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
	stored := map[string]statev2.Session{}
	for _, s := range known {
		stored[s.SessionID] = s
	}
	p := pass{deferred: max(sequenceDeferred, deferred), absenceTickets: tickets}
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
	var passErr error
	if len(p.reads) > 0 || len(p.deletions) > 0 || p.deferred > 0 {
		passErr = w.requireComplete(w.runPrepared(ctx, p))
	}
	if p.deferred == 0 {
		for id, change := range pending {
			if !present[id] && !change.Deleted {
				if _, ok := tickets[id]; !ok {
					tickets[id] = 0
				}
				delete(pending, id)
			}
		}
		for id := range dirty {
			if id != "" && !present[id] {
				if _, ok := tickets[id]; !ok {
					tickets[id] = 0 // include unticketed repair hints
				}
				delete(dirty, id)
			}
		}
		w.resolveAbsent(ctx, present, tickets, stored)
	}
	return passErr
}
