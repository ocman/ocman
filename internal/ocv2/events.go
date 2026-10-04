package ocv2

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

// QueueChangedEvent tells ocman a session's native follow-up queue (v2
// inbox) changed, so it can push the new list to clients. It is ocman's
// own event type; v1 OpenCode never sends it.
const QueueChangedEvent = "ocman.queue.changed"

func init() {
	handle(http.MethodGet, `/event`, func(c *compat, r *http.Request, _ []string) (*http.Response, error) {
		return c.stream(r, false)
	})
	handle(http.MethodGet, `/global/event`, func(c *compat, r *http.Request, _ []string) (*http.Response, error) {
		return c.stream(r, true)
	})
}

// stream subscribes to v2's single global /api/event feed and re-emits
// it as a v1 stream: per-directory `/event` frames, or `/global/event`
// frames wrapped in {directory, payload}.
func (c *compat) stream(r *http.Request, global bool) (*http.Response, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.base+"/api/event", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Request = r
		return resp, nil
	}
	pr, pw := io.Pipe()
	t := &translator{c: c, ctx: r.Context(), global: global, out: pw, msgs: map[string]map[string]any{}, unseeded: map[string]bool{}}
	if !global {
		t.dir = requestDirectory(r)
	}
	go func() {
		defer resp.Body.Close()
		defer func() {
			// A malformed upstream event must end this stream, not ocman.
			if p := recover(); p != nil {
				pw.CloseWithError(fmt.Errorf("opencode v2 event translation: %v", p))
			}
		}()
		pw.CloseWithError(t.run(resp.Body))
	}()
	h := http.Header{}
	h.Set("Content-Type", "text/event-stream")
	return &http.Response{
		StatusCode: http.StatusOK, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: h, Body: pr, ContentLength: -1, Request: r,
	}, nil
}

// translator turns v2 events into v1 events. It keeps the in-flight
// assistant messages (as v2 objects, projected the way OpenCode does)
// so every emitted part is produced by ConvertMessage and matches what
// the history endpoint and the database views return.
type translator struct {
	c      *compat
	ctx    context.Context
	global bool
	dir    string
	out    io.Writer
	msgs   map[string]map[string]any // assistant message id -> v2 message
	// unseeded marks in-flight messages whose fetch failed, so a missed
	// message costs one GET, not one per streamed delta.
	unseeded map[string]bool
}

type v2Event struct {
	ID       string         `json:"id"`
	Type     string         `json:"type"`
	Created  float64        `json:"created"`
	Data     map[string]any `json:"data"`
	Location map[string]any `json:"location"`
}

func (t *translator) run(body io.Reader) error {
	rd := bufio.NewReaderSize(body, 64<<10)
	var buf strings.Builder
	for {
		line, err := rd.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "data:"):
			if buf.Len() > 0 {
				buf.WriteByte('\n')
			}
			buf.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case strings.HasPrefix(line, ":"):
			if werr := t.emit("", "", "server.heartbeat", map[string]any{}); werr != nil {
				return werr
			}
		case line == "" && buf.Len() > 0:
			var ev v2Event
			if json.Unmarshal([]byte(buf.String()), &ev) == nil {
				if werr := t.handle(ev); werr != nil {
					return werr
				}
			}
			buf.Reset()
		}
		if err != nil {
			return err
		}
	}
}

func (t *translator) emit(sessionID, dir, typ string, props map[string]any) error {
	if !t.global && t.dir != "" && dir != "" && filepath.Clean(dir) != filepath.Clean(t.dir) {
		return nil
	}
	if sessionID != "" {
		if _, ok := props["sessionID"]; !ok {
			props["sessionID"] = sessionID
		}
	}
	var frame any = map[string]any{"type": typ, "properties": props}
	if t.global {
		frame = map[string]any{"directory": dir, "payload": frame}
	}
	b, err := json.Marshal(frame)
	if err != nil {
		return nil
	}
	_, err = t.out.Write(append(append([]byte("data: "), b...), '\n', '\n'))
	return err
}

func (t *translator) handle(ev v2Event) error {
	d := ev.Data
	sessionID := str(d, "sessionID")
	dir := str(ev.Location, "directory")
	// One v2 server serves the whole machine: skip other directories
	// before doing any work (fetches, projections), not just on output.
	if !t.global && t.dir != "" && dir != "" && filepath.Clean(dir) != filepath.Clean(t.dir) {
		return nil
	}
	emit := func(typ string, props map[string]any) error { return t.emit(sessionID, dir, typ, props) }
	switch ev.Type {
	case "server.connected":
		return emit("server.connected", map[string]any{})
	case "session.execution.started":
		return emit("session.status", map[string]any{"status": map[string]any{"type": "busy"}})
	case "session.execution.succeeded", "session.execution.failed", "session.execution.interrupted":
		for id, m := range t.msgs {
			if m["sessionID"] == sessionID {
				delete(t.msgs, id)
			}
		}
		clear(t.unseeded)
		if err := emit("session.status", map[string]any{"status": map[string]any{"type": "idle"}}); err != nil {
			return err
		}
		return emit("session.idle", map[string]any{})
	case "session.retry.scheduled":
		return emit("session.status", map[string]any{"status": map[string]any{
			"type": "retry", "attempt": d["attempt"], "message": str(obj(d, "error"), "message"), "next": d["at"],
		}})
	case "session.created", "session.renamed", "session.permissions", "session.moved", "session.forked",
		"session.metadata.updated", "session.agent.selected", "session.model.selected",
		"session.revert.staged", "session.revert.cleared", "session.revert.committed":
		s, err := t.c.session(t.ctx, sessionID)
		if err != nil {
			return nil
		}
		typ := "session.updated"
		if ev.Type == "session.created" {
			typ = "session.created"
		}
		return t.emit(sessionID, firstNonEmpty(dir, str(obj(s, "location"), "directory")), typ, map[string]any{"info": V1Session(s)})
	case "session.deleted":
		return emit("session.deleted", map[string]any{"info": map[string]any{"id": sessionID}})
	case "session.inbox.enqueued", "session.inbox.cancelled", "session.inbox.delivery.changed":
		return emit(QueueChangedEvent, map[string]any{})
	case "session.inbox.delivered":
		if err := t.emitRecent(sessionID, dir); err != nil {
			return err
		}
		return emit(QueueChangedEvent, map[string]any{})
	case "session.synthetic", "session.skill.activated",
		"session.shell.started", "session.shell.ended",
		"session.compaction.started", "session.compaction.ended", "session.compaction.failed":
		return t.emitRecent(sessionID, dir)
	case "permission.asked":
		rememberPrompt(str(d, "id"), sessionID)
		return emit("permission.asked", V1Permission(d))
	case "permission.replied":
		return emit("permission.replied", map[string]any{"requestID": str(d, "requestID"), "reply": str(d, "reply")})
	case "form.created":
		form := obj(d, "form")
		if !IsQuestionForm(form) {
			return nil
		}
		rememberPrompt(str(form, "id"), str(form, "sessionID"))
		return t.emit(str(form, "sessionID"), dir, "question.asked", V1Question(form))
	case "form.replied", "form.cancelled":
		typ := "question.replied"
		if ev.Type == "form.cancelled" {
			typ = "question.rejected"
		}
		return emit(typ, map[string]any{"requestID": str(d, "id")})
	}
	if strings.HasPrefix(ev.Type, "session.step.") || strings.HasPrefix(ev.Type, "session.text.") ||
		strings.HasPrefix(ev.Type, "session.reasoning.") || strings.HasPrefix(ev.Type, "session.tool.") {
		return t.assistant(ev, sessionID, dir)
	}
	return nil
}

// emitRecent re-sends the newest messages of a session. Used for events
// whose message id is not carried on the event (shell, compaction,
// delivered prompts); re-sending is idempotent for every consumer.
func (t *translator) emitRecent(sessionID, dir string) error {
	var resp data[[]map[string]any]
	q := url.Values{"order": {"desc"}, "limit": {"4"}}
	if t.c.call(t.ctx, http.MethodGet, "/api/session/"+url.PathEscape(sessionID)+"/message", q, nil, &resp) != nil {
		return nil
	}
	for i := len(resp.Data) - 1; i >= 0; i-- {
		if err := t.emitMessage(sessionID, dir, resp.Data[i], -1, true); err != nil {
			return err
		}
	}
	return nil
}

// emitMessage sends message.updated (when info is set) and then the
// part at index (all parts when index < 0) of one v2 message.
func (t *translator) emitMessage(sessionID, dir string, msg map[string]any, index int, info bool) error {
	v1, ok := ConvertMessage(sessionID, msg)
	if !ok {
		return nil
	}
	if info {
		if err := t.emit(sessionID, dir, "message.updated", map[string]any{"info": v1.Info}); err != nil {
			return err
		}
	}
	for i, p := range v1.Parts {
		if index >= 0 && i != index {
			continue
		}
		if err := t.emit(sessionID, dir, "message.part.updated", map[string]any{"part": p}); err != nil {
			return err
		}
	}
	return nil
}
