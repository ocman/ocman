package claudeprovider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// prompt is an unanswered can_use_tool request from a claude child. The
// supervisor only relays it: ocman's rules, judge or user decide (AD-7a).
type prompt struct {
	proc      *proc
	request   string // claude's request_id
	input     map[string]any
	question  bool
	preflight bool
	resolved  chan struct{}
	props     map[string]any // the v1 permission.asked / question.asked properties
	// Under l.mu. answering: a control_response write or the recording of
	// reply is in flight. reply resolves the prompt (the decision claude
	// received, or its cancellation): once set it never changes and is
	// never sent to claude again; it is spooled, retried until it is, and
	// only that removes the prompt. cancelled: claude no longer waits.
	// exited: the child exited while the decision was being written; the
	// write decides (delivered: the decision; failed: a cancellation).
	answering, cancelled, exited, retrying bool
	reply                                  *providerprotocol.Event
}

// ask turns a control request into a v1 prompt. Subagent requests carry the
// top-level session id, so their prompts surface on the session the user sees.
func (l *live) ask(p *proc, raw []byte) {
	var m struct {
		RequestID string `json:"request_id"`
		Request   struct {
			Subtype        string         `json:"subtype"`
			Preflight      bool           `json:"ocman_preflight"`
			ToolName       string         `json:"tool_name"`
			Input          map[string]any `json:"input"`
			ToolUseID      string         `json:"tool_use_id"`
			AgentID        string         `json:"agent_id"`
			Suggestions    []any          `json:"permission_suggestions"`
			BlockedPath    string         `json:"blocked_path"`
			DecisionReason string         `json:"decision_reason"`
			Title          string         `json:"title"`
			DisplayName    string         `json:"display_name"`
			Description    string         `json:"description"`
		} `json:"request"`
	}
	if json.Unmarshal(raw, &m) != nil {
		return
	}
	req := m.Request
	if req.Subtype != "can_use_tool" {
		_ = l.write(p, map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "error", "request_id": m.RequestID, "error": "unsupported control request: " + req.Subtype}})
		return
	}
	if req.Input == nil {
		req.Input = map[string]any{}
	}
	pr := &prompt{proc: p, request: m.RequestID, input: req.Input, question: req.ToolName == "AskUserQuestion", preflight: req.Preflight, resolved: make(chan struct{})}
	tool := map[string]any{"callID": req.ToolUseID}
	typ := "permission.asked"
	if pr.question {
		pr.props = map[string]any{"id": "que_" + uuid.NewString(), "sessionID": p.id, "questions": v1Questions(req.Input), "tool": tool}
		typ = "question.asked"
	} else {
		permission, patterns := toolPermission(req.ToolName, req.Input)
		metadata := maps.Clone(req.Input)
		metadata["tool"] = req.ToolName
		metadata["ocmanPreflight"] = req.Preflight // reserved; tool input cannot select the permission flow
		for key, value := range map[string]string{"blocked_path": req.BlockedPath, "decision_reason": req.DecisionReason,
			"title": req.Title, "display_name": req.DisplayName, "description": req.Description} {
			if value != "" {
				metadata[key] = value
			}
		}
		if len(req.Suggestions) > 0 {
			metadata["permission_suggestions"] = req.Suggestions
		}
		if req.AgentID != "" {
			metadata["agentID"] = req.AgentID
		}
		pr.props = map[string]any{"id": "per_" + uuid.NewString(), "sessionID": p.id, "permission": permission,
			"patterns": patterns, "always": patterns, "metadata": metadata, "tool": tool}
	}
	id := pr.props["id"].(string)
	data, _ := json.Marshal(pr.props)
	// Only a spooled prompt exists: one no consumer can see is never
	// answerable, and publishFailed stops its child.
	if err := l.sup.Publish(providerprotocol.Event{Type: typ, Properties: data}, func() {
		l.mu.Lock()
		l.prompts[id] = pr
		l.mu.Unlock()
	}); err != nil {
		l.publishFailed(p.id, err)
	}
}

// v1Questions converts AskUserQuestion input to OpenCode's question shape.
func v1Questions(input map[string]any) []map[string]any {
	raw, _ := input["questions"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		q, _ := item.(map[string]any)
		multi, _ := q["multiSelect"].(bool)
		options, _ := q["options"].([]any)
		if options == nil {
			options = []any{}
		}
		out = append(out, map[string]any{"question": q["question"], "header": q["header"], "options": options, "multiple": multi, "custom": true})
	}
	return out
}

// answer writes the control_response for an unanswered prompt and, only
// once claude has it, records the reply event, which removes the prompt. A
// failed write leaves the prompt pending, so the caller may retry and an
// exiting child still cancels it; a second answer finds nothing (or 409
// mid-write). A decision claude received whose event cannot be spooled is
// an error, not success: the prompt stays listed and its recorded reply is
// retried in the background, never re-sent. Repeating that decision records
// it; a different one is refused (422), so no caller is told a decision
// took effect that claude never received. A prompt claude cancels while the
// write is in flight resolves as cancelled.
func (l *live) answer(id string, question bool, respond func(*prompt) map[string]any, typ string, props func(*prompt) map[string]any) error {
	l.mu.Lock()
	pr := l.prompts[id]
	switch {
	case pr == nil || pr.question != question || pr.cancelled:
		l.mu.Unlock()
		return platforms.ErrNotFound
	case pr.answering:
		l.mu.Unlock()
		return statusError{http.StatusConflict, errors.New("prompt is being answered")}
	}
	data, _ := json.Marshal(props(pr))
	decision := providerprotocol.Event{Type: typ, Properties: data}
	delivered := pr.reply != nil
	if delivered && (pr.reply.Type != decision.Type || !bytes.Equal(pr.reply.Properties, decision.Properties)) {
		l.mu.Unlock()
		return statusError{http.StatusUnprocessableEntity, errors.New("claude already received a different decision for this prompt; that decision stands")}
	}
	pr.answering = true
	l.mu.Unlock()
	if !delivered {
		err := l.write(pr.proc, map[string]any{"type": "control_response", "response": map[string]any{
			"subtype": "success", "request_id": pr.request, "response": respond(pr)}})
		l.mu.Lock()
		// Claude withdrawing the prompt cancels it; an exit only does when
		// the decision never reached claude.
		cancelled := pr.cancelled || (err != nil && pr.exited)
		switch {
		case cancelled:
			pr.cancelled, pr.reply = true, cancellation(id, pr)
		case err != nil:
			pr.answering = false
			l.mu.Unlock()
			return err
		default:
			pr.reply = &decision
		}
		l.mu.Unlock()
		if cancelled {
			if l.record(id, pr) != nil {
				l.recordLater(id, pr)
			}
			return platforms.ErrNotFound
		}
	}
	if err := l.record(id, pr); err != nil {
		l.recordLater(id, pr)
		return statusError{http.StatusServiceUnavailable, fmt.Errorf("%w: %w", platforms.ErrReplyUnrecorded, err)}
	}
	return nil
}

// cancellation is the event resolving a prompt claude withdrew.
func cancellation(id string, pr *prompt) *providerprotocol.Event {
	typ, props := "permission.replied", map[string]any{"sessionID": pr.proc.id, "requestID": id, "reply": "reject", "reason": "cancelled"}
	if pr.question {
		typ, props = "question.rejected", map[string]any{"sessionID": pr.proc.id, "requestID": id}
	}
	data, _ := json.Marshal(props)
	return &providerprotocol.Event{Type: typ, Properties: data}
}

// record spools a prompt's resolution, which removes the prompt; the caller
// holds pr.answering, which record releases.
func (l *live) record(id string, pr *prompt) error {
	l.mu.Lock()
	event := *pr.reply
	l.mu.Unlock()
	err := l.sup.Publish(event, func() {
		l.mu.Lock()
		if l.prompts[id] == pr {
			delete(l.prompts, id)
			if pr.resolved != nil {
				close(pr.resolved)
			}
		}
		l.mu.Unlock()
	})
	l.mu.Lock()
	pr.answering = false
	l.mu.Unlock()
	return err
}

// recordLater retries an unrecorded resolution until it is spooled, so the
// supervisor's pending prompt never outlives the decision (drain, prune).
// One retry runs per prompt.
func (l *live) recordLater(id string, pr *prompt) {
	l.mu.Lock()
	running := pr.retrying
	pr.retrying = true
	l.mu.Unlock()
	if running {
		return
	}
	go func() {
		for delay := time.Duration(recordRetry.Load()); ; delay = min(2*delay, time.Minute) {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-l.done: // the live is gone: nothing will spool this again
				timer.Stop()
				l.mu.Lock()
				pr.retrying = false
				l.mu.Unlock()
				return
			}
			l.mu.Lock()
			gone, busy := l.prompts[id] != pr, pr.answering
			if !gone && !busy {
				pr.answering = true
			}
			l.mu.Unlock()
			if gone {
				return
			}
			if !busy {
				testHook("record", id)
			}
			if !busy && l.record(id, pr) == nil {
				return
			}
		}
	}()
}

// recordRetry and settleRetry are the first delays (nanoseconds) of the
// durable-publication retries, doubling to a minute.
var recordRetry, settleRetry atomic.Int64

func init() {
	recordRetry.Store(int64(time.Second))
	settleRetry.Store(int64(time.Second))
}

func (l *live) replyPermission(id string, req platforms.RespondPermissionRequest) error {
	if req.Reply != "once" && req.Reply != "always" && req.Reply != "reject" {
		return statusError{http.StatusBadRequest, errors.New("reply must be once, always or reject")}
	}
	// "always" is remembered by ocman, never written into Claude's settings.
	return l.answer(id, false, func(pr *prompt) map[string]any {
		if pr.preflight {
			return preflightReply(req.Reply)
		}
		if req.Reply == "reject" {
			return map[string]any{"behavior": "deny", "message": "The user rejected this tool call."}
		}
		return map[string]any{"behavior": "allow", "updatedInput": pr.input}
	}, "permission.replied", func(pr *prompt) map[string]any {
		return map[string]any{"sessionID": pr.proc.id, "requestID": id, "reply": req.Reply}
	})
}

func (l *live) replyQuestion(id string, answers [][]string) error {
	// Claude keys answers by question text and needs the original questions.
	return l.answer(id, true, func(pr *prompt) map[string]any {
		byText := map[string]any{}
		questions, _ := pr.input["questions"].([]any)
		for i, item := range questions {
			q, _ := item.(map[string]any)
			if text, _ := q["question"].(string); text != "" && i < len(answers) {
				byText[text] = strings.Join(answers[i], ", ")
			}
		}
		input := maps.Clone(pr.input)
		input["answers"] = byText
		return map[string]any{"behavior": "allow", "updatedInput": input}
	}, "question.replied", func(pr *prompt) map[string]any {
		return map[string]any{"sessionID": pr.proc.id, "requestID": id, "answers": answers}
	})
}

func (l *live) rejectQuestion(id string) error {
	return l.answer(id, true, func(*prompt) map[string]any {
		return map[string]any{"behavior": "deny", "message": "The user dismissed the question."}
	}, "question.rejected", func(pr *prompt) map[string]any {
		return map[string]any{"sessionID": pr.proc.id, "requestID": id}
	})
}

// cancel resolves prompts claude no longer waits for: a
// control_cancel_request (interrupt) names one, an exited child (request "")
// all of its own. Each keeps its pending state until its cancellation is
// spooled (retried when it cannot be); a decision already delivered is
// recorded instead. One being written resolves when its write returns: as
// cancelled after a withdrawal, else as the decision if the write delivered it.
func (l *live) cancel(p *proc, request string) {
	l.mu.Lock()
	var ids []string
	for id, pr := range l.prompts {
		if pr.proc != p || pr.cancelled || pr.reply != nil || (request != "" && pr.request != request) {
			continue
		}
		if request == "" && pr.answering {
			// An exit is not a withdrawal: a decision being written may
			// already be with claude. The write's result resolves it.
			pr.exited = true
			continue
		}
		pr.cancelled = true
		if !pr.answering {
			pr.reply, pr.answering = cancellation(id, pr), true
			ids = append(ids, id)
		}
	}
	prompts := make([]*prompt, len(ids))
	sort.Strings(ids)
	for i, id := range ids {
		prompts[i] = l.prompts[id]
	}
	l.mu.Unlock()
	for i, id := range ids {
		if l.record(id, prompts[i]) != nil {
			l.recordLater(id, prompts[i])
		}
	}
}

// pendingPrompts lists unanswered prompts for reconciliation (RefreshPermissions).
func (l *live) pendingPrompts(sessionID string, questions bool) []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []map[string]any{}
	for _, pr := range l.prompts {
		if pr.question == questions && pr.proc.id == sessionID && !pr.cancelled {
			out = append(out, pr.props)
		}
	}
	return out
}
