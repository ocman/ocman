package conversationingest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
	"github.com/ocman/ocman/internal/tasksvc"
)

const (
	versionKey    = "version/"
	verifiedAtKey = "verified-at"
)

type sourceWorker struct {
	module   *Module
	source   Source
	instance statev2.Instance
	wake     chan struct{}
	mu       sync.Mutex
	dirty    map[string]bool
	priority map[string]bool // requested conversation reads, served before backfill
	// versions holds the source version hint of each stored session. It is
	// persisted, so a restart reads only sessions that changed meanwhile.
	versions    map[string]string
	last        tasksvc.Progress // progress of the most recent pass, for failure receipts
	loaded      bool
	verifiedAt  time.Time
	lastFailure string
	// failing maps each session whose last read failed to its error. It is
	// independent of version hints, which a failure clears to force a retry,
	// so retries of known failures do not record a new task each poll.
	failing map[string]string
	// feed is set for sources with a durable change feed; see feed.go.
	feed      platforms.HistoryFeed
	feedState feedState
	// journaled sources follow hook records (journal_mode.go). Full
	// verification is then a manual repair, never a periodic pass.
	journaled bool
}

// pass is one planned scan of a source: the sessions to read and the stored
// sessions the source no longer lists.
type pass struct {
	verify     bool
	full       bool // explicit source-wide repair bypasses transcript cursors
	background bool // historical work shares the module's resource budget
	routine    bool // one already stored session; suppress routine task records
	reads      []db.HistoryEntry
	deletions  []statev2.Session
	skipped    int64
	deferred   int // unresolved creation identities; never authorize absence markers
}

func (w *sourceWorker) run(ctx context.Context) {
	if feed, ok := w.source.Reader.(platforms.HistoryFeed); ok && feedSupported(ctx, feed) {
		w.feed = feed
		w.runFeed(ctx)
		return
	}
	if dir := journalDir(w.source.Reader); dir != "" {
		w.runJournal(ctx, dir)
		return
	}
	ticker := time.NewTicker(w.module.deps.PollInterval)
	defer ticker.Stop()
	w.sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.wake:
			timer := time.NewTimer(w.module.deps.Debounce)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		w.sweep(ctx)
	}
}

func (w *sourceWorker) sweep(ctx context.Context) { _ = w.sweepChecked(ctx) }

func (w *sourceWorker) sweepChecked(ctx context.Context) error {
	w.readiness(false)
	w.last = tasksvc.Progress{}
	w.mu.Lock()
	dirty := w.dirty
	w.dirty = map[string]bool{}
	w.mu.Unlock()
	var err error
	if w.journaled && w.loaded && len(dirty) > 0 {
		err = w.runJournalPass(ctx, dirty)
	} else {
		err = w.runPass(ctx, dirty)
	}
	w.readiness(err == nil && ctx.Err() == nil && w.last.Counters["deferred"] == 0 && w.last.Counters["usage-mismatch"] == 0)
	if ctx.Err() == nil {
		w.module.report(w.instance.ID, "", err)
	}
	if err != nil {
		// Keep explicit invalidations for the next poll, without a busy loop.
		w.mu.Lock()
		for id := range dirty {
			w.dirty[id] = true
		}
		w.mu.Unlock()
	}
	return err
}

// prepare resolves the source identity and compares its inventory with the
// stored sessions. A normal pass reads only invalidated, new or changed
// sessions. A verify pass rereads everything to measure missed changes.
func (w *sourceWorker) prepare(ctx context.Context, dirty map[string]bool) (pass, error) {
	var p pass
	p.full = dirty[""]
	deps := w.module.deps
	if err := w.ensureInstance(ctx); err != nil {
		return p, err
	}
	readCtx, cancel := context.WithTimeout(ctx, deps.ReadTimeout)
	entries, deferred, err := readInventory(readCtx, w.source.Reader)
	cancel()
	if err != nil {
		return p, err
	}
	p.deferred = deferred
	present := map[string]bool{}
	for _, entry := range entries {
		if entry.ID == "" || present[entry.ID] {
			return p, fmt.Errorf("history inventory contains an empty or duplicate identity")
		}
		present[entry.ID] = true
	}
	for id := range w.failing {
		if deferred == 0 && !present[id] {
			delete(w.failing, id)
		}
	}
	known, err := deps.Store.Sessions(ctx, w.instance.ID)
	if err != nil {
		return p, err
	}
	deleted := map[string]bool{}
	refresh := conversationRefreshes(known)
	existing := map[string]bool{}
	for _, session := range known {
		existing[session.SessionID] = !session.SourceDeleted
		if session.SourceDeleted {
			deleted[session.SessionID] = true
		} else if deferred == 0 && !present[session.SessionID] {
			p.deletions = append(p.deletions, session)
		}
	}
	p.verify = deferred == 0 && !w.journaled && time.Since(w.verifiedAt) >= deps.VerifyInterval
	for _, entry := range entries {
		version, seen := w.versions[entry.ID]
		_, touched := dirty[entry.ID]
		// dirty[""] is the queue-overflow sentinel: every session is invalidated.
		if p.verify || dirty[""] || touched || refresh[entry.ID] || !seen || version != entry.Version || deleted[entry.ID] {
			p.reads = append(p.reads, entry)
		} else {
			p.skipped++
		}
	}
	p.routine = !p.verify && len(p.reads) == 1 && len(p.deletions) == 0 && existing[p.reads[0].ID]
	return p, nil
}

// ensureInstance resolves the physical source and registers it once. A source
// whose identity changes is a configuration change, never a rebinding.
func (w *sourceWorker) ensureInstance(ctx context.Context) error {
	deps := w.module.deps
	identityCtx, stopIdentity := context.WithTimeout(ctx, deps.ReadTimeout)
	identity, err := w.source.Reader.HistoryIdentity(identityCtx)
	stopIdentity()
	if err != nil {
		return err
	}
	if w.instance.ID != "" && (identity.Platform != w.instance.Platform || identity.SourceKey != w.instance.SourceKey) {
		return statev2.ErrIdentityConflict
	}
	if w.instance.ID == "" {
		instance := statev2.Instance{ID: instanceID(deps.OwnerID, identity), OwnerID: deps.OwnerID,
			Platform: identity.Platform, SourceKey: identity.SourceKey, Name: identity.Name}
		if err := deps.Store.RegisterInstance(ctx, instance); err != nil {
			return err
		}
		w.instance = instance
		w.module.setInstance(w.source.AdapterID, instance.ID)
	}
	return w.load(ctx)
}

// load reads persisted hints once per worker. A source without a recorded
// verification starts its verification interval now, after its first import.
func (w *sourceWorker) load(ctx context.Context) error {
	if w.loaded {
		return nil
	}
	saved, err := w.module.deps.Store.SourceState(ctx, w.instance.ID)
	if err != nil {
		return err
	}
	for key, value := range saved {
		if id, ok := strings.CutPrefix(key, versionKey); ok {
			w.versions[id] = value
		}
	}
	if ms, err := strconv.ParseInt(saved[verifiedAtKey], 10, 64); err == nil {
		w.verifiedAt = time.UnixMilli(ms)
	} else if err := w.markVerified(ctx); err != nil {
		return err
	}
	w.loaded = true
	return nil
}

func (w *sourceWorker) markVerified(ctx context.Context) error {
	w.verifiedAt = time.Now()
	return w.module.deps.Store.SetSourceState(ctx, w.instance.ID, verifiedAtKey, strconv.FormatInt(w.verifiedAt.UnixMilli(), 10))
}

func (w *sourceWorker) forget(ctx context.Context, id string) {
	delete(w.versions, id)
	// Best effort: a stale hint is only cleared to force a reread, and the
	// failure that led here is already reported.
	_ = w.module.deps.Store.SetSourceState(ctx, w.instance.ID, versionKey+id, "")
}

func snapshotStream(id string) string { return "history/snapshot-v1/" + id }

const snapshotAttempts = 3

// snapshot retries a session that changed while it was being read. A live
// session is written continuously, so one unlucky read is not a failure.
func (w *sourceWorker) snapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	for attempt := 1; ; attempt++ {
		readCtx, cancel := context.WithTimeout(ctx, w.module.deps.ReadTimeout)
		snapshot, err := w.source.Reader.HistorySnapshot(readCtx, id)
		cancel()
		if !errors.Is(err, db.ErrHistoryChanged) || attempt == snapshotAttempts {
			return snapshot, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * 100 * time.Millisecond):
		}
	}
}

func (w *sourceWorker) remember(ctx context.Context, id string, result importResult) (importResult, error) {
	if err := w.module.deps.Store.SetSourceState(ctx, w.instance.ID, versionKey+id, result.version); err != nil {
		return importResult{}, err
	}
	w.versions[id] = result.version
	return result, nil
}

func (w *sourceWorker) markDeleted(ctx context.Context, session statev2.Session) error {
	store := w.module.deps.Store
	cursor, err := store.Checkpoint(ctx, session.InstanceID, snapshotStream(session.SessionID))
	if err != nil {
		return err
	}
	session.SourceDeleted = true
	_, err = store.Commit(ctx, statev2.Batch{Session: session, ExpectedRevision: session.Revision,
		Checkpoint: &statev2.CheckpointUpdate{Stream: snapshotStream(session.SessionID), ExpectedCursor: cursor, Cursor: "deleted:" + cursor}})
	if err == nil {
		w.module.publishReceipt(w.source.AdapterID, session.SessionID)
	}
	return err
}
