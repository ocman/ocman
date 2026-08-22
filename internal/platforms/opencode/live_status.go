package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// This file holds ocman's live view of which sessions are running a turn.
//
// The source of truth is the OpenCode instance itself, which exposes it two
// ways:
//
//	GET /session/status  ->  {"ses_...":{"type":"busy"}}
//	session.status event ->  {"properties":{"sessionID":"ses_...","status":{"type":"busy"}}}
//
// `SessionStatus` is `{type: "idle"} | {type: "busy"} | {type: "retry", ...}`;
// a session absent from the map has no turn in flight. The watcher seeds the
// registry from the snapshot when it connects to an instance and keeps it
// current from the event stream, so the view survives an ocman restart
// without ocman persisting a copy of state it does not own.
//
// Everything here is keyed by the instance port, so a departing process can
// have its entries dropped wholesale (see ClearSessionStatusForPort) rather
// than leaving sessions pinned "busy" forever.

// statusSnapshotTimeout bounds the seed fetch. The instance is local and the
// handler is a map read; a slow answer means the process is wedged, and the
// next reconnect will retry.
const statusSnapshotTimeout = 2 * time.Second

type liveStatusEntry struct {
	port string
	busy bool
	// seq is the registry sequence at which this entry was written by an
	// event. A snapshot only overwrites entries with a lower seq than the
	// snapshot's token, so an event that raced the in-flight fetch is
	// never clobbered by the older truth it already superseded.
	seq uint64
}

type liveStatusRegistry struct {
	mu      sync.RWMutex
	entries map[string]liveStatusEntry
	// seeded holds ports whose /session/status snapshot has been read.
	// Until a port is seeded, absence from entries proves nothing, so
	// TurnState reports TurnUnobserved rather than TurnSettled — without
	// that distinction a genuinely busy session would blink to "done"
	// every time the watcher reconnects.
	seeded map[string]uint64
	// portGen invalidates in-flight events from a stream whose port was
	// already removed. Mirrors livePromptRegistry.portGen.
	portGen map[string]uint64
	// seq is a monotonic counter stamped onto every event-written entry.
	// Mirrors livePromptRegistry's version/applied pair, for the same
	// reason: reconciling a snapshot against a live stream needs to know
	// which of the two saw the world last.
	seq uint64
}

func newLiveStatusRegistry() *liveStatusRegistry {
	return &liveStatusRegistry{
		entries: make(map[string]liveStatusEntry),
		seeded:  make(map[string]uint64),
		portGen: make(map[string]uint64),
	}
}

// turnRunning reports whether an OpenCode SessionStatus type means a turn is
// in flight. "retry" is a provider backoff *within* a turn, so it counts as
// running; anything else ("idle", or a type added by a future OpenCode) does
// not.
func turnRunning(statusType string) bool {
	return statusType == "busy" || statusType == "retry"
}

// StatusPortGeneration returns (and initializes) the generation token for a
// port's status stream. Events carrying a stale token are dropped.
func (a *Adapter) StatusPortGeneration(port string) uint64 {
	if a == nil || a.turns == nil {
		return 0
	}
	a.turns.mu.Lock()
	defer a.turns.mu.Unlock()
	if a.turns.portGen[port] == 0 {
		a.turns.portGen[port] = 1
	}
	return a.turns.portGen[port]
}

// ObserveSessionStatus records one session.status event from an instance.
func (a *Adapter) ObserveSessionStatus(port string, generation uint64, sessionID, statusType string) {
	if a == nil || a.turns == nil || sessionID == "" {
		return
	}
	a.turns.mu.Lock()
	defer a.turns.mu.Unlock()
	if generation != 0 && a.turns.portGen[port] != generation {
		return
	}
	a.turns.seq++
	// Idle is recorded rather than deleted so the entry keeps naming the
	// port that owns this session: that is what lets TurnState tell
	// "settled on a live instance" apart from "no instance at all".
	a.turns.entries[sessionID] = liveStatusEntry{
		port: port,
		busy: turnRunning(statusType),
		seq:  a.turns.seq,
	}
}

// statusSeq reads the current sequence. Captured before a snapshot fetch so
// SeedSessionStatus can tell which entries an event has since superseded.
func (a *Adapter) statusSeq() uint64 {
	if a == nil || a.turns == nil {
		return 0
	}
	a.turns.mu.RLock()
	defer a.turns.mu.RUnlock()
	return a.turns.seq
}

// SeedSessionStatus replaces a port's view with a full snapshot and marks
// the port seeded.
//
// seq is the sequence captured *before* the snapshot was fetched. Entries an
// event wrote after that point are left alone: the stream is already
// connected when the snapshot is taken, so a buffered event can describe a
// transition the snapshot predates. Without the guard that event would be
// clobbered and the session could read settled while a turn runs, until the
// next transition happened to correct it. Pass 0 to keep every
// event-observed entry and replace only what an earlier snapshot wrote.
func (a *Adapter) SeedSessionStatus(port string, generation, seq uint64, statuses map[string]string) {
	if a == nil || a.turns == nil || port == "" {
		return
	}
	a.turns.mu.Lock()
	defer a.turns.mu.Unlock()
	if generation != 0 && a.turns.portGen[port] != generation {
		return
	}
	superseded := func(sessionID string) bool {
		return a.turns.entries[sessionID].seq > seq
	}
	for sessionID, entry := range a.turns.entries {
		if entry.port == port && !superseded(sessionID) {
			delete(a.turns.entries, sessionID)
		}
	}
	for sessionID, statusType := range statuses {
		if superseded(sessionID) {
			continue
		}
		a.turns.entries[sessionID] = liveStatusEntry{port: port, busy: turnRunning(statusType)}
	}
	a.turns.seeded[port] = a.turns.portGen[port]
}

// ClearSessionStatusForPort forgets everything learned from an instance that
// is no longer discoverable. Sessions it owned fall back to TurnUnobserved,
// which is what turns an unfinished turn into "interrupted".
func (a *Adapter) ClearSessionStatusForPort(port string) {
	if a == nil || a.turns == nil || port == "" {
		return
	}
	a.turns.mu.Lock()
	defer a.turns.mu.Unlock()
	a.turns.portGen[port]++
	delete(a.turns.seeded, port)
	for sessionID, entry := range a.turns.entries {
		if entry.port == port {
			delete(a.turns.entries, sessionID)
		}
	}
}

// turnState resolves the live turn state for one session. ports is the
// discovery map (directory -> port) so a worktree session folds onto the
// project instance that actually serves it.
func (r *liveStatusRegistry) turnState(sessionID, directory string, ports map[string]string) db.TurnState {
	return r.turnStateForPort(sessionID, portForDirectory(ports, directory))
}

// turnStateForPort is turnState for callers that already resolved the
// instance port (the live detail path). An empty port means nothing live
// serves the session.
func (r *liveStatusRegistry) turnStateForPort(sessionID, port string) db.TurnState {
	if port == "" {
		return db.TurnUnobserved
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if entry, ok := r.entries[sessionID]; ok && entry.port == port {
		if entry.busy {
			return db.TurnRunning
		}
		return db.TurnSettled
	}
	// The instance is up and we have read its snapshot, so a session it
	// does not list is not running.
	if _, ok := r.seeded[port]; ok {
		return db.TurnSettled
	}
	return db.TurnUnobserved
}

// settleStatus applies the live turn signal to an inferred status. A nil
// registry (test-constructed adapters) leaves the inference untouched.
func (a *Adapter) settleStatus(sessionID, directory string, inferred db.SessionStatus, ports map[string]string) db.SessionStatus {
	if a == nil || a.turns == nil {
		return inferred
	}
	return db.SettleSessionStatus(
		a.turns.turnState(sessionID, directory, ports),
		directoryHasLivePort(ports, directory),
		inferred,
	)
}

// settleStatusOnPort is settleStatus for the live detail path, which has
// already resolved the instance port and therefore knows it is reachable.
func (a *Adapter) settleStatusOnPort(sessionID, port string, inferred db.SessionStatus) db.SessionStatus {
	if a == nil || a.turns == nil {
		return inferred
	}
	return db.SettleSessionStatus(a.turns.turnStateForPort(sessionID, port), port != "", inferred)
}

// SessionStatusOnPort returns the current settled status without fetching a
// full session detail. It is used to push idle transitions to the sidebar.
func (a *Adapter) SessionStatusOnPort(sessionID, port string) (db.SessionStatus, error) {
	ctx := context.Background()
	session, err := a.db.GetSession(ctx, sessionID)
	if err != nil {
		return "", err
	}
	messages, err := a.db.GetSessionMessages(ctx, sessionID)
	if err != nil {
		return "", err
	}
	applySessionDetailMetadataFromMessages(session, messages)
	return a.settleStatusOnPort(sessionID, port, session.Status), nil
}

// portForDirectory returns the instance port serving a session directory,
// folding a worktree back to its project root (worktree sessions run on the
// project's shared instance). Empty when nothing live serves it.
func portForDirectory(ports map[string]string, directory string) string {
	if port, ok := ports[normalizePortDirectory(directory)]; ok {
		return port
	}
	root := foldWorktreeToProjectRoot(directory)
	if root == directory {
		return ""
	}
	return ports[normalizePortDirectory(root)]
}

// fetchSessionStatusSnapshot reads GET /session/status from one instance and
// flattens it to sessionID -> status type. The bool reports whether the
// snapshot can be trusted; on false the caller must not mark the port
// seeded, or every session on it would read as settled.
func fetchSessionStatusSnapshot(ctx context.Context, port string) (map[string]string, bool) {
	requestCtx, cancel := context.WithTimeout(ctx, statusSnapshotTimeout)
	defer cancel()
	endpoint := fmt.Sprintf("http://127.0.0.1:%s/session/status", port)
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, false
	}
	resp, err := openCodeClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var raw map[string]struct {
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, false
	}
	out := make(map[string]string, len(raw))
	for sessionID, status := range raw {
		out[sessionID] = status.Type
	}
	return out, true
}

// SeedSessionStatusFromInstance fetches and applies one instance's status
// snapshot. Called by the event watcher each time it connects to a port.
func (a *Adapter) SeedSessionStatusFromInstance(ctx context.Context, port string, generation uint64) bool {
	if a == nil || a.turns == nil || port == "" {
		return false
	}
	// Capture the sequence first: everything an event writes from here on
	// is newer than the snapshot we are about to read.
	seq := a.statusSeq()
	statuses, ok := fetchSessionStatusSnapshot(ctx, port)
	if !ok {
		return false
	}
	a.SeedSessionStatus(port, generation, seq, statuses)
	return true
}
