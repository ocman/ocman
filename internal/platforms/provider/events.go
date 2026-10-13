package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func (a *Adapter) SetSessionEventSink(sink platforms.SessionEventSink) {
	a.mu.Lock()
	a.sink = sink
	a.mu.Unlock()
}

// SetEventHandler installs the headless permission/question pipeline entry point.
// Events are delivered once per provider sequence, after updating the caches.
// The handler may call adapter operations but must not consume another stream.
func (a *Adapter) SetEventHandler(handler func(providerprotocol.Event)) {
	a.mu.Lock()
	a.onEvent = handler
	a.mu.Unlock()
}

// WatchEvents keeps status and pending prompts current without a browser. Run
// once per adapter, under the runtime's context. Reconnects replay the last
// successfully applied provider-wide sequence.
func (a *Adapter) WatchEvents(ctx context.Context) error {
	if !a.Capabilities().Events {
		return platforms.ErrUnsupported
	}
	for {
		a.retryAcknowledgments(ctx)
		a.mu.RLock()
		after := a.seq
		a.mu.RUnlock()
		// A draining supervisor's acknowledgment route is versioned: it is
		// replaced, not pruned.
		_ = a.streamURL(ctx, "", after, io.Discard, nil, "/events?consumer=headless&after="+strconv.FormatUint(after, 10), !a.draining)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (a *Adapter) ProxyEvents(ctx context.Context, id string, w io.Writer, flush func()) error {
	if !a.Capabilities().Events || a.draining {
		return platforms.ErrUnsupported
	}
	if a.hello.Capabilities.Replay && id != "" {
		return a.proxySnapshot(ctx, id, w, flush)
	}
	return a.stream(ctx, id, 0, w, flush)
}

func (a *Adapter) stream(ctx context.Context, id string, after uint64, w io.Writer, flush func()) error {
	return a.streamURL(ctx, id, after, w, flush, "/events?after="+strconv.FormatUint(after, 10), false)
}

func (a *Adapter) streamURL(ctx context.Context, id string, after uint64, w io.Writer, flush func(), path string, acknowledge bool, initialLimited ...map[string]bool) error {
	a.mu.RLock()
	observedAtStart := a.seq
	a.mu.RUnlock()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	watchdog := platforms.NewIdleWatchdog(platforms.SSEIdleTimeout, cancel)
	defer watchdog.Stop()
	resp, err := a.request(ctx, "GET", path, nil)
	if err != nil {
		if watchdog.Expired() {
			return platforms.ErrSSEIdleTimeout
		}
		return err
	}
	defer resp.Body.Close()
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return fmt.Errorf("provider /events is not SSE")
	}
	scanner := bufio.NewScanner(watchdog.Reader(resp.Body))
	scanner.Buffer(make([]byte, 4096), providerprotocol.MaxEventBytes)
	var data strings.Builder
	var seq uint64
	limited := make(map[string]bool)
	if acknowledge {
		a.mu.RLock()
		for sessionID, live := range a.live {
			limited[sessionID] = live.Status == string(db.StatusLimited)
		}
		a.mu.RUnlock()
	}
	if len(initialLimited) != 0 {
		for sessionID, value := range initialLimited[0] {
			limited[sessionID] = value
		}
	}
	isolated := a.hello.Capabilities.Replay && !acknowledge
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if data.Len() > 0 {
				if seq <= after {
					return fmt.Errorf("provider event sequence %d is not after %d", seq, after)
				}
				var event providerprotocol.Event
				if err := json.Unmarshal([]byte(data.String()), &event); err != nil {
					return fmt.Errorf("provider event: %w", err)
				}
				if event, err = a.exactEvent(event); err != nil {
					return fmt.Errorf("provider event: %w", err)
				}
				if a.draining && !isPrompt(event.Type) { // only prompt shapes are version-independent
					after = seq
					data.Reset()
					seq = 0
					continue
				}
				var sessionID string
				if isolated {
					var props platforms.LivePrompt
					err = json.Unmarshal(event.Properties, &props)
					sessionID = eventSession(props)
				} else {
					sessionID, err = a.observe(seq, event)
				}
				if err != nil {
					return err
				}
				if event.Type == "session.status" {
					var props struct {
						Status struct {
							Type string `json:"type"`
						} `json:"status"`
					}
					if err := json.Unmarshal(event.Properties, &props); err != nil {
						return err
					}
					limited[sessionID] = props.Status.Type == "limited"
				}
				// A prompt ocman's rules answer is never shown to a browser.
				_, ruled := a.ruleDecision(event)
				if ruled == "" && (id == "" || sessionID == id) {
					// Determine idle validity from this stream's predecessor, never
					// another consumer's newer live state. Old prompts are delivered
					// from the current pending cache, not replayed into the judge.
					oldPrompt := !isolated && seq <= observedAtStart && (strings.HasPrefix(event.Type, "permission.") || strings.HasPrefix(event.Type, "question."))
					if oldPrompt || (event.Type == "session.idle" && limited[sessionID]) {
						after = seq
						data.Reset()
						seq = 0
						continue
					}
					payload, err := json.Marshal(event)
					if err != nil {
						return err
					}
					if _, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", seq, payload); err != nil {
						return err
					}
					if flush != nil {
						flush()
					}
				}
				after = seq
				if acknowledge && a.hello.Capabilities.Replay && event.Type == "session.idle" && !limited[sessionID] {
					if err := a.acknowledge(ctx, sessionID, seq); err != nil {
						return err
					}
				}
			}
			data.Reset()
			seq = 0
			continue
		}
		if strings.HasPrefix(line, "id:") {
			seq, err = strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "id:")), 10, 64)
			if err != nil || seq == 0 {
				return fmt.Errorf("provider event has invalid sequence: %q", line)
			}
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
			if data.Len() > providerprotocol.MaxEventBytes {
				return fmt.Errorf("provider event exceeds size limit")
			}
		}
	}
	if watchdog.Expired() {
		return platforms.ErrSSEIdleTimeout
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return scanner.Err()
}

func isPrompt(typ string) bool {
	return strings.HasPrefix(typ, "permission.") || strings.HasPrefix(typ, "question.")
}

func eventSession(p platforms.LivePrompt) string {
	if id, _ := p["sessionID"].(string); id != "" {
		return id
	}
	for _, key := range []string{"info", "part"} {
		if nested, ok := p[key].(map[string]any); ok {
			if id, _ := nested["sessionID"].(string); id != "" {
				return id
			}
		}
	}
	return ""
}

func (a *Adapter) observe(seq uint64, event providerprotocol.Event) (string, error) {
	a.observeMu.Lock()
	defer a.observeMu.Unlock()
	var p platforms.LivePrompt
	if err := json.Unmarshal(event.Properties, &p); err != nil {
		return "", fmt.Errorf("provider event properties: %w", err)
	}
	id := eventSession(p)
	if event.Type == "permission.replied" {
		// The effective reply decides a tentative Allow-always before the
		// cursor moves past its evidence: a failure ends the stream here.
		pid, _ := p["requestID"].(string)
		if err := a.settleAlways(pid, p["reply"] == "always"); err != nil {
			return "", err
		}
	}
	a.mu.Lock()
	if seq <= a.seq {
		a.mu.Unlock()
		return id, nil
	}
	if id != "" {
		s := a.sessions[id]
		s.ID = id
		s.Platform = string(a.ID())
		a.sessions[id] = s
	}
	var status db.SessionStatus
	var resetsAt int64
	idle := false
	switch event.Type {
	case "permission.asked", "question.asked":
		pid, _ := p["id"].(string)
		if pid == "" || id == "" {
			a.mu.Unlock()
			return "", fmt.Errorf("provider prompt missing id or sessionID")
		}
		if a.rules != nil && event.Type == "permission.asked" {
			if reply := a.rules.Decide(p); reply != "" {
				a.seq = seq
				a.ruled[pid] = true
				a.mu.Unlock()
				a.answerByRule(p, reply)
				return id, nil
			}
		}
		if event.Type == "permission.asked" {
			a.permissions[pid] = p
		} else {
			a.questions[pid] = p
		}
		a.promptVersion++
	case "permission.replied", "question.replied", "question.rejected":
		key := "requestID"
		pid, _ := p[key].(string)
		if pid == "" {
			a.mu.Unlock()
			return "", fmt.Errorf("provider reply missing %s", key)
		}
		if event.Type == "permission.replied" {
			delete(a.permissions, pid)
			if a.ruled[pid] {
				// ocman's rules answered it: not a user reply, nothing to record.
				delete(a.ruled, pid)
				a.seq = seq
				a.mu.Unlock()
				return id, nil
			}
		} else {
			delete(a.questions, pid)
		}
		a.promptVersion++
	case "session.status":
		value, _ := p["status"].(map[string]any)
		kind, _ := value["type"].(string)
		switch kind {
		case "busy", "retry":
			status = db.StatusBusy
		case "idle", "done":
			status = db.StatusDone
		case "waiting":
			status = db.StatusWaiting
		case "limited":
			status = db.StatusLimited
		case "error":
			status = db.StatusError
		case "interrupted":
			status = db.StatusInterrupted
		default:
			a.mu.Unlock()
			return "", fmt.Errorf("provider sent unknown status %q", kind)
		}
		if v, ok := value["resetsAt"].(float64); ok {
			resetsAt = int64(v)
		}
	case "session.idle":
		// A stale idle edge cannot settle a rate-limited turn, and keeps a
		// failed turn's error status.
		switch a.live[id].Status {
		case string(db.StatusLimited):
		case string(db.StatusError):
			idle = true
		default:
			status = db.StatusDone
			idle = true
		}
	}
	if status != "" {
		if id == "" {
			a.mu.Unlock()
			return "", fmt.Errorf("provider status missing sessionID")
		}
		live := a.live[id]
		live.Status = string(status)
		live.LastEventAt = time.Now()
		a.live[id] = live
		s := a.sessions[id]
		s.Status = status
		s.ResetsAt = resetsAt
		a.sessions[id] = s
	}
	a.seq = seq
	sink := a.sink
	handler := a.onEvent
	a.mu.Unlock()
	if event.Acknowledged {
		// A turn end already acknowledged (and acted on) before this replay:
		// state only, never the queue, routines or the prompt pipeline.
		return id, nil
	}
	if status != "" && sink.OnSessionStatus != nil {
		sink.OnSessionStatus(string(a.ID()), id, status, resetsAt)
	}
	if idle && sink.OnSessionIdle != nil {
		sink.OnSessionIdle(string(a.ID()), id)
	}
	if handler != nil && (event.Type != "session.idle" || idle) {
		event.Properties = append(json.RawMessage(nil), event.Properties...)
		handler(event)
	}
	return id, nil
}
