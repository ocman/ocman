package claudeprovider

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// result ends the turn unless accepted input is still outstanding. It honours
// is_error/subtype (an API or execution failure is an error), an interrupt
// (aborted: settled, not failed). Usage limits do not override the outcome.
func (l *live) result(p *proc, raw []byte) {
	l.resultFor(p, raw, nil)
}

func (l *live) resultFor(p *proc, raw []byte, expected *uint64) {
	var r struct {
		IsError        bool     `json:"is_error"`
		Subtype        string   `json:"subtype"`
		TerminalReason string   `json:"terminal_reason"`
		Result         string   `json:"result"`
		Errors         []string `json:"errors"`
	}
	_ = json.Unmarshal(raw, &r)
	l.mu.Lock()
	if expected != nil && l.accepted[p.id] != *expected {
		l.mu.Unlock()
		return
	}
	startErr := l.markStartedLocked(p.id)
	if p.startErr != nil { // latched at the echo: recovery since does not undo it
		startErr = p.startErr
	}
	aborted := p.aborted || strings.HasPrefix(r.TerminalReason, "aborted")
	failed := !aborted && (r.IsError || (r.Subtype != "" && r.Subtype != "success"))
	reason := or(strings.Join(r.Errors, "; "), or(r.Result, or(r.TerminalReason, r.Subtype)))
	if startErr != nil && !aborted {
		if !failed {
			reason = ""
		}
		failed, reason = true, strings.TrimPrefix(reason+"; "+startErr.Error(), "; ")
	}
	// A successful turn whose only output is the result text (a local
	// command such as /cost) renders it, once (spike §4).
	resultOnly := !failed && !aborted && r.Result != "" && !p.replied
	if len(p.pending) == 0 && (len(p.tasks) > 0 || (p.state != "" && p.state != "idle")) {
		p.deferredResult = slices.Clone(raw)
		p.deferredGen = l.accepted[p.id]
		l.armRunCeilingLocked(p)
		l.mu.Unlock()
		return
	}
	p.replied = false
	if len(p.pending) > 0 {
		if aborted {
			p.deferredResult = slices.Clone(raw) // interrupt acknowledgment decides which inputs survived
			p.deferredGen = l.accepted[p.id]
		}
		l.mu.Unlock()
		if failed {
			l.errorMessage(p.id, reason)
		} else if resultOnly {
			l.resultMessage(p.id, r.Result)
		}
		return
	}
	p.deferredResult = nil
	l.stopRunCeilingLocked(p)
	p.busy, p.aborted = false, false
	p.idle = time.AfterFunc(l.opts.IdleTimeout, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.procs[p.id] == p && !p.busy {
			l.stopLocked(p)
		}
	})
	gen := l.accepted[p.id]
	l.slots.Broadcast()
	l.mu.Unlock()
	if err := l.flushTitle(context.Background(), p.id); err != nil {
		log.WithError(err).Warn("writing claude session title")
	}
	if resultOnly {
		l.resultMessage(p.id, r.Result)
	}
	switch {
	case failed:
		l.fail(p.id, errors.New(reason), gen)
	default:
		l.settle(p.id, db.StatusDone, gen)
	}
}

// settle ends the open turn: a non-done terminal status, then the idle edge.
// gen is l.accepted when the caller decided the turn was over. Both events
// are published only if, under the publish lock that BeginTurn takes, no
// send was accepted since, none waits to be written and no child is busy:
// otherwise that work owns the idle edge and a stale one would let the
// queue drain or a routine complete with work outstanding.
//
// An edge the spool cannot take is not lost: the session reads as failed
// and one retry per session publishes the error status and idle edge once
// the spool works again (unless a later turn superseded it), so the
// supervisor's active turn, the queue, routines and /drain all settle.
func (l *live) settle(id string, status db.SessionStatus, gen uint64) {
	err := l.trySettle(id, status, gen, true)
	l.mu.Lock()
	pending, has := l.unsettled[id]
	switch {
	case err == nil && has && pending.gen <= gen:
		delete(l.unsettled, id) // published, or superseded by this later turn
	case err != nil && (!has || pending.gen <= gen):
		l.unsettled[id] = settlement{db.StatusError, gen}
	}
	// One worker per session retries whatever generation is unsettled.
	start := err != nil && !l.settling[id]
	if start {
		l.settling[id] = true
	}
	l.mu.Unlock()
	if err != nil {
		testHook("failed", id)
		l.turnFailed(id, err, gen)
		if start {
			go l.settleLater(id)
		}
	}
}

type settlement struct {
	status db.SessionStatus
	gen    uint64
}

// settleLater retries the session's unsettled turn end until none is left.
// It owns every generation: one that replaced the entry it was retrying
// (a newer turn that also failed to publish) is retried next, never
// abandoned, and only the entry it settled is removed.
func (l *live) settleLater(id string) {
	delay := time.Duration(settleRetry.Load())
	for {
		time.Sleep(delay)
		l.mu.Lock()
		s, ok := l.unsettled[id]
		if !ok {
			delete(l.settling, id)
			l.mu.Unlock()
			return
		}
		l.mu.Unlock()
		testHook("retry", id)
		if l.trySettle(id, s.status, s.gen, false) != nil {
			delay = min(2*delay, time.Minute)
			continue
		}
		l.mu.Lock()
		if l.unsettled[id] == s {
			delete(l.unsettled, id)
		}
		l.mu.Unlock()
		delay = time.Duration(settleRetry.Load())
	}
}

// trySettle publishes the turn's end; nil when published or superseded.
func (l *live) trySettle(id string, status db.SessionStatus, gen uint64, first bool) error {
	// The turn's subagent sessions end with it: their idle edges let the
	// headless watcher acknowledge and the spool truncate their events too.
	l.mu.Lock()
	children := slices.Clone(l.subs[id])
	l.mu.Unlock()
	events := make([]providerprotocol.Event, 0, len(children)+2)
	for _, child := range children {
		props, _ := json.Marshal(map[string]any{"sessionID": child})
		events = append(events, providerprotocol.Event{Type: "session.idle", Properties: props})
	}
	if status != db.StatusDone {
		props, _ := json.Marshal(map[string]any{"sessionID": id, "status": map[string]any{"type": status}})
		events = append(events, providerprotocol.Event{Type: "session.status", Properties: props})
	}
	props, _ := json.Marshal(map[string]any{"sessionID": id})
	events = append(events, providerprotocol.Event{Type: "session.idle", Properties: props})
	if first {
		testHook("settle", id)
	}
	_, err := l.sup.PublishIf(func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		p := l.procs[id]
		current, idle := l.accepted[id] == gen, p == nil || !p.busy
		if !first && l.unsettled[id] != (settlement{status, gen}) {
			return false // settled meanwhile, or another generation's
		}
		if current && idle && len(l.waits[id]) > 0 {
			// The result beat its sender's ticket cleanup (or a write still
			// in flight): that cleanup publishes this outcome (turn).
			l.deferred[id] = status
		}
		return current && idle && len(l.waits[id]) == 0
	}, func(e providerprotocol.Event) {
		var p struct {
			SessionID string `json:"sessionID"`
		}
		_ = json.Unmarshal(e.Properties, &p)
		l.apply(p.SessionID, "", e)
	}, events...)
	return err
}

func (l *live) errorMessage(id, text string) {
	ts := now()
	info := map[string]any{"id": "msg_err_" + uuid.NewString(), "sessionID": id, "role": "assistant",
		"time":  map[string]any{"created": ts, "completed": ts},
		"error": map[string]any{"name": "UnknownError", "data": map[string]any{"message": text}}}
	l.emit(id, "m:"+info["id"].(string), "message.updated", map[string]any{"info": info})
}

// resultMessage shows a successful turn's result text when the CLI sent no
// assistant message for it. ponytail: like errorMessage it lives in the
// turn's view only; the transcript is authoritative once re-read.
func (l *live) resultMessage(id, text string) {
	ts, msg := now(), "msg_res_"+uuid.NewString()
	info := map[string]any{"id": msg, "sessionID": id, "role": "assistant", "time": map[string]any{"created": ts, "completed": ts}}
	l.publish(id, info, []map[string]any{{"id": "prt_res_" + uuid.NewString(), "messageID": msg, "sessionID": id,
		"type": "text", "text": text, "time": map[string]any{"start": ts, "end": ts}}})
}

// fail ends a turn with a visible error message and the error status.
func (l *live) fail(id string, err error, gen uint64) {
	l.errorMessage(id, err.Error())
	l.settle(id, db.StatusError, gen)
}

// testHook lets tests act inside the windows settle closes (a send landing
// between deciding a turn is over and publishing its idle edge).
// Atomic: a background retry may outlive the test that installed it.
var testHooks atomic.Pointer[func(point, id string)]

func testHook(point, id string) {
	if h := testHooks.Load(); h != nil {
		(*h)(point, id)
	}
}

// abort interrupts the running turn through the control protocol (spike §5)
// and cancels turns still waiting for a slot.
func (l *live) abort(ctx context.Context, id string) error {
	l.mu.Lock()
	for w := range l.waits[id] {
		w.cancelled = true
	}
	l.slots.Broadcast()
	p := l.procs[id]
	if p == nil || !p.busy {
		l.mu.Unlock()
		return nil
	}
	p.aborted = true
	l.mu.Unlock()
	data, err := l.control(ctx, p, "", map[string]any{"subtype": "interrupt"})
	var response struct {
		Queued []json.RawMessage `json:"still_queued"`
	}
	if err == nil && len(data) > 0 {
		err = json.Unmarshal(data, &response)
	}
	l.mu.Lock()
	if err != nil {
		p.aborted = false
	} else {
		remaining := map[string]bool{}
		for _, raw := range response.Queued {
			var id string
			if json.Unmarshal(raw, &id) != nil {
				var input struct {
					UUID string `json:"uuid"`
				}
				_ = json.Unmarshal(raw, &input)
				id = input.UUID
			}
			if p.pending[id] {
				remaining[id] = true
			}
		}
		p.pending = remaining
	}
	l.mu.Unlock()
	l.finishDeferredResult(p)
	return err
}
