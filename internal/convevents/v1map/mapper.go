// Package v1map maps today's v1-shaped session streams (OpenCode v1, the
// ocv2 translation, the Claude provider protocol and ocman's synthetic
// ocman.* frames) to Ocman conversation events. It is a pure, deterministic
// library: one Mapper per session subscription, fed frames in order.
//
// The mapper reports raw lifecycle edges as session.status events (an idle
// edge is a provisional done). Settling terminal status, turn edges, waiting
// derivation, busy inference and prompt routing by ancestry are the hub's.
package v1map

import (
	"container/list"
	"encoding/json"
	"time"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/db"
)

type Options struct {
	// SessionID is the page session the stream is about.
	SessionID string
	// Now supplies Unix ms for values a frame does not carry (prompt
	// askedAt, messages without a creation time). Defaults to the clock.
	Now func() int64
	// Descendant reports whether a session descends from SessionID. Children
	// announced on the stream (session.created) are tracked without it.
	Descendant func(sessionID string) bool
}

// PartRef names a part.
type PartRef struct{ MessageID, PartID string }

// Result is the mapping of one frame.
type Result struct {
	Events []convevents.Event
	// Replayed marks a provider frame replayed after acknowledgment.
	Replayed bool
	// Unanchored lists deltas withheld because the mapper has no base for
	// their part; the caller should refresh the message (design §5.2).
	Unanchored []PartRef
	// QueueChanged asks the caller to read the session queue and publish
	// queue.changed.
	QueueChanged bool
	// Ignored is set for frames of no recognised type.
	Ignored bool
}

type Mapper struct {
	opts      Options
	parts     map[string]*partState
	partOrder list.List
	children  map[string]bool
	// childLevel is each child's last status level, to drop repeats such as
	// the idle twin.
	childLevel map[string]string
	resolved   map[string]bool
	dirtied    map[string]bool
	// idle is set after an idle edge until activity; a second edge is the
	// session.status/session.idle twin.
	idle    bool
	limited bool
}

type partState struct {
	messageID string
	typ       string
	fields    map[string]int
	order     *list.Element
}

func New(opts Options) *Mapper {
	if opts.Now == nil {
		opts.Now = func() int64 { return time.Now().UnixMilli() }
	}
	return &Mapper{opts: opts, parts: map[string]*partState{}, children: map[string]bool{},
		childLevel: map[string]string{}, resolved: map[string]bool{}, dirtied: map[string]bool{}}
}

func (m *Mapper) Map(f Frame) Result {
	// Resolution/edit deduplication is a recent-event window, not an
	// unbounded record of every completed turn on a persistent connection.
	if len(m.resolved) >= maxAnchors {
		clear(m.resolved)
	}
	if len(m.dirtied) >= maxAnchors {
		clear(m.dirtied)
	}
	var r Result
	typ, props, top := normalize(f)
	if top == nil {
		r.Ignored = true
		return r
	}
	r.Replayed = string(top["acknowledged"]) == "true"
	if !m.dispatch(&r, typ, props) {
		r.Ignored = true
	}
	return r
}

func (m *Mapper) emit(r *Result, evs ...convevents.Event) { r.Events = append(r.Events, evs...) }

// dispatch returns false for frames it does not recognise.
func (m *Mapper) dispatch(r *Result, typ string, p obj) bool {
	switch typ {
	case "server.connected":
		m.emit(r, background(p.obj("backgroundTasks").int(m.opts.SessionID),
			p.obj("backgroundTaskDetails").arr(m.opts.SessionID)))
	case "server.heartbeat":
	case "message.created", "message.updated":
		if typ == "message.created" {
			m.idle = false
		}
		m.message(r, p)
	case "message.part.updated":
		m.partUpdated(r, p)
	case "message.part.delta":
		m.delta(r, p)
	case "message.removed":
		m.messageRemoved(r, p)
	case "message.part.removed":
		m.partRemoved(r, p)
	case "session.status":
		m.status(r, p)
	case "session.idle":
		m.sessionIdle(r, p)
	case "session.created", "session.updated", "session.deleted":
		m.session(r, typ, p)
	case "session.compacted":
		if m.own(p.str("sessionID")) {
			m.emit(r, convevents.HistoryRewritten{Reason: convevents.RewriteCompaction})
		}
	case "session.diff":
		if m.own(p.str("sessionID")) {
			m.emit(r, convevents.ChangesDirty{Reason: convevents.DirtyDiff})
		}
	case "session.error":
		m.sessionError(r, p)
	case "permission.asked":
		m.permissionAsked(r, p)
	case "permission.replied":
		m.permissionReplied(r, p)
	case "question.asked":
		m.questionAsked(r, p)
	case "question.replied", "question.rejected":
		m.questionCleared(r, typ, p)
	case "ocman.session.background":
		if !m.own(p.str("sessionID")) {
			return true
		}
		if n, ok := p.num("count"); ok && n >= 0 && n <= 1<<53 && n == float64(int64(n)) {
			m.emit(r, background(int64(n), p.arr("tasks")))
		}
	case "ocman.queue.changed":
		r.QueueChanged = true
	case "ocman.permission.pending", "ocman.permission.checking", "ocman.permission.flagged":
		m.judge(r, typ, p)
	case "ocman.permission.auto-approved", "ocman.permission.approved":
		m.approved(r, p)
	case "ocman.session.changed":
		m.emit(r, convevents.ChangesDirty{Reason: convevents.DirtyCommit})
	case "ocman.history.committed":
		// Stored history caught up; the client may read what the stream missed.
		if n, ok := p.num("revision"); ok && n > 0 && n <= 1<<53 && n == float64(int64(n)) && p.str("sessionID") == m.opts.SessionID {
			m.emit(r, convevents.HistoryCommitted{Revision: int64(n)})
		}
	default:
		// Recover message and part payloads from unknown channels once, here.
		switch {
		case p.obj("info") != nil:
			m.message(r, p)
		case p.obj("part") != nil:
			m.partUpdated(r, p)
		case p.str("id") != "" && p.first("messageID", "messageId") != "":
			m.partUpdated(r, obj{"part": mustMarshal(p)})
		default:
			return false
		}
	}
	return true
}

// own reports whether a frame's session is the page session. Frames
// without a session belong to it.
func (m *Mapper) own(sessionID string) bool {
	return sessionID == "" || sessionID == m.opts.SessionID
}

func (m *Mapper) isChild(id string) bool {
	return id != "" && id != m.opts.SessionID &&
		(m.children[id] || (m.opts.Descendant != nil && m.opts.Descendant(id)))
}

// status maps v1 session.status. Its argument is an object {type, ...} or a
// bare string.
func (m *Mapper) status(r *Result, p obj) {
	st := p.obj("status")
	kind := st.str("type")
	if st == nil {
		kind = p.str("status")
	}
	sid := p.str("sessionID")
	if !m.own(sid) {
		if m.isChild(sid) {
			if status, ok := statusKind(kind); ok {
				ev := convevents.ChildStatus{ChildID: sid, Status: status}
				if notice := retryNotice(kind, st); notice != nil {
					ev.Notice = convevents.Some(*notice)
				}
				m.childStatus(r, ev)
			}
		}
		return
	}
	if kind == "idle" {
		m.limited = false
		m.idleEdge(r)
		return
	}
	status, ok := statusKind(kind)
	if !ok {
		return
	}
	m.limited = status == db.StatusLimited
	ev := convevents.SessionStatusEvent{Status: status}
	switch {
	case kind == "retry":
		m.idle = false
		ev.Notice = convevents.Some(*retryNotice(kind, st))
	case kind == "busy":
		m.idle = false
		ev.Notice = convevents.Null[db.SessionNotice]()
	case kind == "done":
		ev.Notice = convevents.Null[db.SessionNotice]()
	case status == db.StatusLimited:
		ev.ResetsAt = st.int("resetsAt")
	}
	m.emit(r, ev)
}

func (m *Mapper) sessionIdle(r *Result, p obj) {
	sid := p.str("sessionID")
	if !m.own(sid) {
		if m.isChild(sid) {
			var previous convevents.ChildStatus
			_ = json.Unmarshal([]byte(m.childLevel[sid]), &previous)
			if previous.Status == db.StatusError || previous.Status == db.StatusInterrupted {
				return
			}
			m.childStatus(r, convevents.ChildStatus{ChildID: sid, Status: db.StatusDone})
		}
		return
	}
	// The provider withholds a limited turn's idle edge; so does the mapper.
	if !m.limited {
		m.idleEdge(r)
	}
}

func (m *Mapper) childStatus(r *Result, ev convevents.ChildStatus) {
	level := string(mustMarshal(ev))
	if m.childLevel[ev.ChildID] == level {
		return
	}
	m.childLevel[ev.ChildID] = level
	m.emit(r, ev)
}

func (m *Mapper) idleEdge(r *Result) {
	if m.idle {
		return
	}
	m.idle = true
	m.emit(r, convevents.SessionStatusEvent{Status: db.StatusDone, Provisional: true, Notice: convevents.Null[db.SessionNotice]()})
}

func statusKind(kind string) (db.SessionStatus, bool) {
	switch kind {
	case "busy", "retry":
		return db.StatusBusy, true
	case "idle", "done":
		return db.StatusDone, true
	}
	for _, s := range []db.SessionStatus{db.StatusWaiting, db.StatusLimited, db.StatusError, db.StatusInterrupted} {
		if kind == string(s) {
			return s, true
		}
	}
	return "", false
}

func retryNotice(kind string, st obj) *db.SessionNotice {
	if kind != "retry" {
		return nil
	}
	return &db.SessionNotice{Kind: "retry", Message: st.str("message"), RetryAt: st.int("next"), Attempt: int(st.int("attempt"))}
}

// background keeps tasks with a string id and description, first id wins,
// matching reduceBackgroundTaskCount.
func background(count int64, raw []json.RawMessage) convevents.BackgroundChanged {
	ev := convevents.BackgroundChanged{Count: int(count), Tasks: []convevents.BackgroundTask{}}
	if count <= 0 {
		return ev
	}
	seen := map[string]bool{}
	for _, item := range raw {
		var t struct {
			ID          *string `json:"id"`
			Description *string `json:"description"`
			Kind        string  `json:"kind"`
		}
		if json.Unmarshal(item, &t) != nil || t.ID == nil || *t.ID == "" || t.Description == nil || seen[*t.ID] {
			continue
		}
		seen[*t.ID] = true
		if t.Kind != "agent" {
			t.Kind = ""
		}
		ev.Tasks = append(ev.Tasks, convevents.BackgroundTask{ID: *t.ID, Description: *t.Description, Kind: t.Kind})
	}
	return ev
}
