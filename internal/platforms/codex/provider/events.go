package codexprovider

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	"github.com/ocman/ocman/internal/db"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

func now() int64 { return time.Now().UnixMilli() }

// codexVersion reports the installed CLI version without requiring one.
func codexVersion(cli string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cli, "--version").Output()
	if fields := strings.Fields(string(out)); err == nil && len(fields) > 0 {
		return fields[len(fields)-1] // "codex-cli 0.164.0"
	}
	return "unavailable"
}

// events receives one app-server connection's traffic. It runs on the
// connection's reader goroutine, in order; it publishes to the spool but never
// calls the connection, which answers prompts later.
type events struct {
	b   *backend
	gen uint64
}

func (e *events) current() bool {
	e.b.mu.Lock()
	defer e.b.mu.Unlock()
	return e.b.gen == e.gen
}

func (e *events) Notification(method string, params json.RawMessage) {
	if !e.current() {
		return
	}
	b := e.b
	switch method {
	case codexappserver.NotifyTurnStarted:
		var n codexappserver.TurnNotification
		if json.Unmarshal(params, &n) == nil {
			b.turnStarted(n.ThreadID, n.Turn.ID)
		}
	case codexappserver.NotifyItemStarted, codexappserver.NotifyItemCompleted:
		var n struct {
			codexappserver.ItemNotification
			StartedAtMs   int64 `json:"startedAtMs"`
			CompletedAtMs int64 `json:"completedAtMs"`
		}
		if json.Unmarshal(params, &n) != nil {
			return
		}
		it := n.Item
		if it.StartedAtMs == 0 {
			it.StartedAtMs = n.StartedAtMs
		}
		if it.CompletedAtMs == 0 {
			it.CompletedAtMs = n.CompletedAtMs
		}
		b.item(n.ThreadID, it)
	case codexappserver.NotifyAgentMessageDelta, codexappserver.NotifyReasoningSummary, codexappserver.NotifyReasoningText,
		codexappserver.NotifyCommandOutputDelta:
		var n codexappserver.DeltaNotification
		if json.Unmarshal(params, &n) == nil {
			b.delta(method, n)
		}
	case codexappserver.NotifyTokenUsageUpdated:
		var n codexappserver.TokenUsageNotification
		if json.Unmarshal(params, &n) == nil {
			b.usage(n.ThreadID, n.TokenUsage)
		}
	case codexappserver.NotifyTurnCompleted:
		var n codexappserver.TurnNotification
		if json.Unmarshal(params, &n) == nil {
			b.turnCompleted(n.ThreadID, &n.Turn)
		}
	case codexappserver.NotifyServerRequestDone:
		var n codexappserver.ServerRequestResolved
		if json.Unmarshal(params, &n) == nil {
			b.resolved(e.gen, n.RequestID)
		}
	case codexappserver.NotifyThreadNameUpdated:
		var n codexappserver.ThreadNameNotification
		if json.Unmarshal(params, &n) == nil {
			b.mu.Lock()
			b.sessionLocked(n.ThreadID).title = n.ThreadName
			b.mu.Unlock()
		}
	}
}

// turnStarted records Codex's turn id for interrupts. A turn ocman did not
// start (another client of a shared thread) is not followed.
func (b *backend) turnStarted(thread, turn string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if s := b.sessions[thread]; s != nil && s.running && s.turn == "" {
		s.turn = turn
	}
}

// converter returns the running turn's converter, nil when the session runs
// no turn here.
func (b *backend) converter(thread string) *Converter {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.sessions[thread]
	if s == nil || !s.running {
		return nil
	}
	return s.conv
}

func (b *backend) item(thread string, it codexappserver.Item) {
	c := b.converter(thread)
	if c == nil {
		return
	}
	c.AddItem(it, now())
	b.mu.Lock()
	used := b.sessions[thread].used
	b.mu.Unlock()
	// Usage reported before the reply opened applies once it has.
	if used != nil {
		c.SetUsage(*used)
	}
	info, parts := c.Live(it.ID)
	b.publishMessage(thread, info, parts)
}

func (b *backend) delta(method string, d codexappserver.DeltaNotification) {
	c := b.converter(d.ThreadID)
	if c == nil {
		return
	}
	if part := c.Delta(method, d); part != nil {
		b.publishMessage(d.ThreadID, nil, []map[string]any{part})
	}
}

// usage records the turn's own usage: thread totals minus the totals before
// the turn's first model request.
func (b *backend) usage(thread string, u codexappserver.ThreadTokenUsage) {
	c := b.converter(thread)
	if c == nil {
		return
	}
	b.mu.Lock()
	s := b.sessions[thread]
	if s.base == nil {
		base := Sub(u.Total, u.Last)
		s.base = &base
	}
	turn := TurnUsage{Tokens: Sub(u.Total, *s.base), Context: u.Last.InputTokens + u.Last.OutputTokens}
	if u.ModelContextWindow != nil {
		turn.Window = *u.ModelContextWindow
	}
	s.used = &turn
	b.mu.Unlock()
	if info := c.SetUsage(turn); info != nil {
		b.publishMessage(thread, info, nil)
	}
}

func (b *backend) turnCompleted(thread string, turn *codexappserver.Turn) {
	c := b.converter(thread)
	if c == nil {
		return
	}
	info := c.Settle(turn)
	if info != nil {
		b.publishMessage(thread, info, nil)
	}
	status := db.StatusDone
	switch turn.Status {
	case codexappserver.TurnFailed:
		status = db.StatusError
	case codexappserver.TurnInterrupted:
		status = db.StatusInterrupted
	}
	b.settle(thread, status, 0)
}
