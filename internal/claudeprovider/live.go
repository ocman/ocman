package claudeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/providersupervisor"
)

// TestedCLI lists versions with recorded stream-json/control fixtures under
// internal/claudeprovider/testdata/<version>/; it is evidence, not a launch gate.
var TestedCLI = []string{"2.1.220"}

// checkCLI requires an installed CLI without rejecting automatic updates.
func checkCLI(version string) error {
	tested := strings.Join(TestedCLI, ", ")
	if version == "unavailable" {
		return fmt.Errorf("claude CLI not found: install Claude Code %s", tested)
	}
	return nil
}

// owned is a session ocman launched or adopted when continuing its transcript.
type owned struct {
	Directory   string `json:"directory"`
	ProjectRoot string `json:"projectRoot,omitempty"`
	Title       string `json:"title,omitempty"`
	Created     int64  `json:"created"`
	Started     bool   `json:"started,omitempty"` // a turn completed: claude has a transcript to --resume
	ForkOf      string `json:"forkOf,omitempty"`  // first spawn forks this session
	// TitlePending is a rename not yet written to the transcript.
	TitlePending bool `json:"titlePending,omitempty"`
	// Config is the Claude config directory (CLAUDE_CONFIG_DIR) it was
	// launched under: its transcript lives in that configuration's history.
	Config string `json:"config,omitempty"`
}

// foreign reports a session launched under another Claude configuration: its
// transcript is not in this one's history, so it is listed read-only and is
// never resumed here; switching back makes it drivable again. Sessions
// recorded before configurations were tracked carry none and count as this
// configuration's.
func (l *live) foreign(o *owned) bool {
	return o != nil && o.Config != "" && filepath.Clean(o.Config) != filepath.Clean(l.opts.ConfigDir)
}

func (l *live) foreignError(o owned) error {
	return statusError{http.StatusForbidden, fmt.Errorf("this Claude session was started under Claude configuration %s and is unavailable under %s", o.Config, l.opts.ConfigDir)}
}

var errReadOnly = errors.New("this Claude session cannot be continued as an independent conversation")

// live is the interactive half of the provider: owned sessions, their
// claude children and the per-session view served as snapshots.
type live struct {
	sup        *providersupervisor.Supervisor
	idx        *Index
	opts       Options
	versionErr error
	account    *account // subscription and observed rate-limit windows (FR-7)
	cmds       *commands

	mu        sync.Mutex
	slots     *sync.Cond // signalled when a child exits or a turn ends
	owned     map[string]*owned
	procs     map[string]*proc
	status    map[string]db.Session // Status and ResetsAt from the process
	view      map[string][]viewEntry
	prompts   map[string]*prompt              // unanswered can_use_tool requests, by ocman prompt id
	hooks     map[string]hookState            // terminal sessions' hook-reported status (FR-6)
	waits     map[string]map[*waitTicket]bool // turns not yet written to a child
	tails     map[string]chan struct{}        // closed when the session's last turn was written
	subs      map[string][]string             // subagent sessions streamed in a session's turn
	accepted  map[string]uint64               // turns accepted per session (BeginTurn), see settle
	deferred  map[string]db.SessionStatus     // a settle held back only by its own input's wait ticket
	unsettled map[string]settlement           // a turn end the spool could not take, retried (settleLater)
	settling  map[string]bool                 // sessions with a settleLater worker
	// unrecorded sessions have a transcript whose ownership update failed.
	unrecorded map[string]bool
	children   int // claude children started and not yet reaped
	// done closes when the live is stopped (provider shutdown or the test
	// harness tearing it down); the durable-publication retry workers
	// (recordLater, settleLater) end with it instead of outliving it.
	done     chan struct{}
	stopOnce sync.Once
	// ctx ends with done: waits for a slot select on it.
	ctx  context.Context
	stop context.CancelFunc
}

type viewEntry struct {
	key   string
	event providerprotocol.Event
}

func newLive(idx *Index, opts Options, version string) *live {
	l := &live{idx: idx, opts: opts, versionErr: checkCLI(version), account: newAccount(opts), owned: map[string]*owned{},
		procs: map[string]*proc{}, status: map[string]db.Session{}, view: map[string][]viewEntry{}, prompts: map[string]*prompt{}, hooks: map[string]hookState{},
		waits: map[string]map[*waitTicket]bool{}, tails: map[string]chan struct{}{}, subs: map[string][]string{}, accepted: map[string]uint64{},
		deferred: map[string]db.SessionStatus{}, unsettled: map[string]settlement{}, settling: map[string]bool{}, unrecorded: map[string]bool{}, done: make(chan struct{})}
	l.cmds = newCommands(opts)
	l.ctx, l.stop = context.WithCancel(context.Background())
	l.slots = sync.NewCond(&l.mu)
	l.syncOwnedLocked()
	return l
}

// emit publishes one v1 event and records it in the session's view. key
// identifies the upsert (message or part id); empty keys are not retained.
func (l *live) emit(id, key, typ string, props any) {
	data, err := json.Marshal(props)
	if err != nil {
		return
	}
	event := providerprotocol.Event{Type: typ, Properties: data}
	if err := l.sup.Publish(event, func() { l.apply(id, key, event) }); err != nil {
		l.publishFailed(id, err)
	}
}

// publishFailed handles an event that could not be made durable: no consumer
// will ever see it, so the session's turn cannot go on (a prompt nobody sees
// would wait forever). The session reads as failed and a running child is
// killed, which cancels its prompts and fails the turn when the spool works.
func (l *live) publishFailed(id string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.publishFailedLocked(id, err)
}

// turnFailed is publishFailed for the end of turn generation gen. Checked
// under l.mu, which BeginTurn's accept takes: once a later turn was
// accepted, the old failure neither marks that turn failed nor kills its
// child (the unsettled retry still publishes or drops the old end).
func (l *live) turnFailed(id string, err error, gen uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.accepted[id] != gen {
		log.WithError(err).WithField("sessionID", id).Warn("claude: an earlier turn's end could not be spooled; a newer turn owns the session")
		return
	}
	l.publishFailedLocked(id, err)
}

func (l *live) publishFailedLocked(id string, err error) {
	root := id
	for parent, children := range l.subs {
		if slices.Contains(children, id) {
			root = parent
		}
	}
	log.WithError(err).WithField("sessionID", root).Error("claude: an event could not be spooled; failing the turn")
	l.status[root] = db.Session{Status: db.StatusError}
	if p := l.procs[root]; p != nil && p.busy {
		_ = p.cmd.Process.Kill()
	}
}

// apply runs under the supervisor's publishing lock, so snapshots and the
// event stream agree. Lock order is always supervisor, then l.mu.
func (l *live) apply(id, key string, event providerprotocol.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch event.Type {
	case "session.status":
		var p struct {
			Status struct {
				Type     string `json:"type"`
				ResetsAt int64  `json:"resetsAt"`
			} `json:"status"`
		}
		_ = json.Unmarshal(event.Properties, &p)
		if p.Status.Type == "busy" {
			l.clearViewLocked(id)
		}
		l.status[id] = db.Session{Status: db.SessionStatus(p.Status.Type), ResetsAt: p.Status.ResetsAt}
	case "session.idle":
		// The idle edge keeps a failed turn's terminal status.
		if st := l.status[id].Status; st != db.StatusError {
			l.status[id] = db.Session{Status: db.StatusDone}
		}
	}
	if key == "" {
		return
	}
	for i, v := range l.view[id] {
		if v.key == key {
			l.view[id][i].event = event
			return
		}
	}
	l.view[id] = append(l.view[id], viewEntry{key: key, event: event})
}

// clearViewLocked drops a finished turn's upserts, its subagents' included:
// they are in the transcripts now.
func (l *live) clearViewLocked(id string) {
	l.view[id] = nil
	for _, child := range l.subs[id] {
		delete(l.view, child)
	}
	delete(l.subs, id)
}

// snapshot returns the current turn's upserts and status (Backend.Snapshot).
func (l *live) snapshot(id string) []providerprotocol.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	events := make([]providerprotocol.Event, 0, len(l.view[id])+1)
	for _, v := range l.view[id] {
		events = append(events, v.event)
	}
	s, ok := l.status[id]
	if ok && s.Status != db.StatusDone {
		props, _ := json.Marshal(map[string]any{"sessionID": id, "status": map[string]any{"type": s.Status, "resetsAt": s.ResetsAt}})
		events = append(events, providerprotocol.Event{Type: "session.status", Properties: props})
	}
	// A settled turn's idle edge precedes the snapshot's cursor: a browser
	// that read busy before it ended learns the turn is over from here.
	p := l.procs[id]
	count := 0
	if p != nil && p.busy {
		count = len(p.tasks)
	}
	events = append(events, backgroundCountEvent(id, count))
	if ok && (s.Status == db.StatusDone || s.Status == db.StatusError) && len(l.waits[id]) == 0 && (p == nil || !p.busy) {
		props, _ := json.Marshal(map[string]any{"sessionID": id})
		events = append(events, providerprotocol.Event{Type: "session.idle", Properties: props})
	}
	return events
}

// overlay applies the process's status to an indexed session (AD-10).
func (l *live) overlay(s *db.Session) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syncOwnedLocked()
	l.overlayLocked(s)
}

func (l *live) overlayLocked(s *db.Session) {
	// The process settled the turn, so its status is authoritative, done
	// included: an aborted turn's unfinished transcript is not interrupted.
	if st, ok := l.status[s.ID]; ok && st.Status != "" {
		s.Status, s.ResetsAt = st.Status, st.ResetsAt
	}
	l.overlayHookLocked(s)
	o := l.owned[s.ID]
	// Existing root transcripts can be adopted on the next send. Another
	// configuration's managed sessions still cannot be resumed here.
	s.LiveConnection = !l.foreign(o) && (o != nil || l.resumable(s))
	// ocman's title (create or rename) wins over the transcript's first
	// prompt, so a rename made before claude wrote the transcript holds.
	if o != nil && o.Title != "" {
		s.Title = o.Title
	}
}

// pending lists owned sessions with no transcript yet: a new session is
// visible before claude writes its first line.
func (l *live) pending(indexed map[string]bool, dir string) []db.Session {
	return l.overlayAndPending(nil, indexed, dir)
}

func (l *live) overlayAndPending(sessions []db.Session, indexed map[string]bool, dir string) []db.Session {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syncOwnedLocked()
	for i := range sessions {
		l.overlayLocked(&sessions[i])
	}
	for id, o := range l.owned {
		// Another configuration's sessions have their history there.
		if !indexed[id] && !l.foreign(o) && (dir == "" || dir == o.Directory) {
			s := db.Session{ID: id, Title: o.Title, Directory: o.Directory, TimeCreated: o.Created, TimeUpdated: o.Created, Status: db.StatusDone}
			l.overlayLocked(&s)
			sessions = append(sessions, s)
		}
	}
	return sessions
}

func (l *live) ownedSession(id string) (owned, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.syncOwnedLocked()
	o := l.owned[id]
	if o == nil {
		return owned{}, false
	}
	return *o, true
}

func now() int64 { return time.Now().UnixMilli() }

// statuses lists the sessions whose status this process settled
// (GET /sessions?live=1). The dashboard overlays
// them on the browsing list: they survive a dashboard restart, while the
// adapter's event-fed cache only holds turns replayed since it connected.
func (l *live) statuses() []db.Session {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]db.Session, 0, len(l.status))
	for id, s := range l.status {
		out = append(out, db.Session{ID: id, Status: s.Status, ResetsAt: s.ResetsAt})
	}
	return out
}
