package codexprovider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// prompt is an unanswered app-server request. The supervisor only relays it:
// ocman's rules, judge or user decide (AD-7a).
type prompt struct {
	gen       uint64 // the connection that asked; only it can be answered
	rpcID     json.RawMessage
	session   string
	question  bool
	questions []codexappserver.InputQuestion
	props     map[string]any // the v1 permission.asked / question.asked properties
	answering bool
	reply     *providerprotocol.Event // delivered to Codex, not yet spooled
}

// Request turns an app-server request into a v1 prompt. Requests ocman does
// not handle are refused so Codex does not wait for them.
func (e *events) Request(id json.RawMessage, method string, params json.RawMessage) {
	if !e.current() {
		return
	}
	pr := &prompt{gen: e.gen, rpcID: id}
	typ := "permission.asked"
	switch method {
	case codexappserver.RequestCommandApproval:
		var r codexappserver.CommandApprovalRequest
		if json.Unmarshal(params, &r) != nil {
			break
		}
		command := unwrapShell(r.Command)
		pr.session = r.ThreadID
		pr.props = map[string]any{"permission": "bash", "patterns": []string{providerprotocol.ExactPattern(command)},
			"metadata": map[string]any{"tool": "bash", "command": command, "cwd": r.Cwd, "reason": r.Reason}, "tool": map[string]any{"callID": r.ItemID}}
	case codexappserver.RequestFileChangeApproval:
		var r codexappserver.FileChangeApprovalRequest
		if json.Unmarshal(params, &r) != nil {
			break
		}
		pr.session = r.ThreadID
		files := e.b.files(r.ThreadID, r.ItemID)
		patterns := make([]string, 0, len(files))
		for _, f := range files {
			patterns = append(patterns, providerprotocol.ExactPattern(f))
		}
		if len(patterns) == 0 {
			patterns = []string{"*"}
		}
		pr.props = map[string]any{"permission": "edit", "patterns": patterns,
			"metadata": map[string]any{"tool": "apply_patch", "files": files, "reason": r.Reason, "grantRoot": r.GrantRoot}, "tool": map[string]any{"callID": r.ItemID}}
	case codexappserver.RequestUserInput:
		var r codexappserver.UserInputRequest
		if json.Unmarshal(params, &r) != nil {
			break
		}
		pr.session, pr.question, pr.questions, typ = r.ThreadID, true, r.Questions, "question.asked"
		pr.props = map[string]any{"questions": v1Questions(r.Questions), "tool": map[string]any{"callID": r.ItemID}}
	}
	if pr.props == nil {
		c := e.b.clientFor(e.gen)
		if c != nil {
			go func() { _ = c.ReplyError(id, -32601, "ocman does not handle "+method) }()
		}
		return
	}
	prefix := "per_"
	if pr.question {
		prefix = "que_"
	}
	pid := prefix + uuid.NewString()
	pr.props["id"], pr.props["sessionID"] = pid, pr.session
	if pr.props["patterns"] != nil {
		pr.props["always"] = pr.props["patterns"]
	}
	data, _ := json.Marshal(pr.props)
	// Only a spooled prompt exists: one nobody can see is never answerable.
	if err := e.b.sup.Publish(providerprotocol.Event{Type: typ, Properties: data}, func() {
		e.b.mu.Lock()
		e.b.prompts[pid] = pr
		e.b.mu.Unlock()
	}); err != nil {
		log.WithError(err).WithField("sessionID", pr.session).Error("codex: a prompt could not be spooled; declining it")
		if c := e.b.clientFor(e.gen); c != nil {
			go func() { _ = c.Reply(id, pr.refusal()) }()
		}
	}
}

// refusal is the answer that lets Codex continue without the prompt.
func (pr *prompt) refusal() any {
	if pr.question {
		return codexappserver.UserInputResponse{Answers: map[string]codexappserver.InputAnswer{}}
	}
	return codexappserver.ApprovalResponse{Decision: codexappserver.DecisionDecline}
}

func v1Questions(qs []codexappserver.InputQuestion) []map[string]any {
	out := make([]map[string]any, 0, len(qs))
	for _, q := range qs {
		options := make([]map[string]any, 0, len(q.Options))
		for _, o := range q.Options {
			options = append(options, map[string]any{"label": o.Label, "description": o.Description})
		}
		out = append(out, map[string]any{"question": q.Question, "header": q.Header, "options": options, "multiple": false, "custom": q.IsOther || len(q.Options) == 0})
	}
	return out
}

func (b *backend) clientFor(gen uint64) *codexappserver.Client {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.gen != gen {
		return nil
	}
	return b.client
}

// answer delivers a decision to Codex, then records it, which removes the
// prompt. A decision Codex received but the spool did not take is reported as
// unrecorded; repeating the same decision records it without resending.
func (b *backend) answer(id string, question bool, response func(*prompt) any, typ string, props map[string]any) error {
	b.mu.Lock()
	pr := b.prompts[id]
	switch {
	case pr == nil || pr.question != question:
		b.mu.Unlock()
		return platforms.ErrNotFound
	case pr.answering:
		b.mu.Unlock()
		return statusError{http.StatusConflict, errors.New("prompt is being answered")}
	}
	props["sessionID"], props["requestID"] = pr.session, id
	data, _ := json.Marshal(props)
	decision := providerprotocol.Event{Type: typ, Properties: data}
	if pr.reply != nil && (pr.reply.Type != typ || !bytes.Equal(pr.reply.Properties, data)) {
		b.mu.Unlock()
		return statusError{http.StatusUnprocessableEntity, errors.New("codex already received a different decision for this prompt; that decision stands")}
	}
	pr.answering = true
	delivered := pr.reply != nil
	c := b.client
	if b.gen != pr.gen {
		c = nil
	}
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		pr.answering = false
		b.mu.Unlock()
	}()
	if !delivered {
		var err error
		if c == nil {
			err = platforms.ErrNotFound // the asking app-server is gone
		} else {
			err = c.Reply(pr.rpcID, response(pr))
		}
		b.mu.Lock()
		if err == nil {
			pr.reply = &decision
		}
		b.mu.Unlock()
		if err != nil {
			return err
		}
	}
	b.mu.Lock()
	reply := *pr.reply
	b.mu.Unlock()
	if err := b.record(id, pr, reply); err != nil {
		return statusError{http.StatusServiceUnavailable, fmt.Errorf("%w: %w", platforms.ErrReplyUnrecorded, err)}
	}
	return nil
}

// record spools a prompt's resolution, which removes the prompt.
func (b *backend) record(id string, pr *prompt, event providerprotocol.Event) error {
	return b.sup.Publish(event, func() {
		b.mu.Lock()
		if b.prompts[id] == pr {
			delete(b.prompts, id)
		}
		b.mu.Unlock()
	})
}

func (b *backend) replyPermission(id string, reply string) error {
	decision := codexappserver.DecisionAccept
	switch reply {
	case "once", "always": // ocman remembers "always" itself
	case "reject":
		decision = codexappserver.DecisionDecline
	default:
		return statusError{http.StatusBadRequest, errors.New("reply must be once, always or reject")}
	}
	return b.answer(id, false, func(*prompt) any { return codexappserver.ApprovalResponse{Decision: decision} },
		"permission.replied", map[string]any{"reply": reply})
}

// replyQuestion answers questions in order; Codex keys answers by question id.
func (b *backend) replyQuestion(id string, answers [][]string) error {
	return b.answer(id, true, func(pr *prompt) any {
		out := map[string]codexappserver.InputAnswer{}
		for i, q := range pr.questions {
			if i < len(answers) {
				out[q.ID] = codexappserver.InputAnswer{Answers: answers[i]}
			}
		}
		return codexappserver.UserInputResponse{Answers: out}
	}, "question.replied", map[string]any{"answers": answers})
}

func (b *backend) rejectQuestion(id string) error {
	return b.answer(id, true, (*prompt).refusal, "question.rejected", map[string]any{})
}

// resolved handles Codex withdrawing a request, such as on interrupt. A
// request ocman answered is already gone or holds its delivered decision.
func (b *backend) resolved(gen uint64, rpcID json.RawMessage) {
	b.mu.Lock()
	var id string
	for pid, pr := range b.prompts {
		if pr.gen == gen && bytes.Equal(bytes.TrimSpace(pr.rpcID), bytes.TrimSpace(rpcID)) && pr.reply == nil && !pr.answering {
			id = pid
		}
	}
	b.mu.Unlock()
	if id != "" {
		b.cancelPrompt(id)
	}
}

// cancelPrompt resolves a prompt Codex no longer waits for.
func (b *backend) cancelPrompt(id string) {
	b.mu.Lock()
	pr := b.prompts[id]
	var delivered *providerprotocol.Event
	if pr != nil {
		delivered = pr.reply
	}
	b.mu.Unlock()
	if pr == nil {
		return
	}
	typ, props := "permission.replied", map[string]any{"sessionID": pr.session, "requestID": id, "reply": "reject", "reason": "cancelled"}
	if pr.question {
		typ, props = "question.rejected", map[string]any{"sessionID": pr.session, "requestID": id}
	}
	data, _ := json.Marshal(props)
	event := providerprotocol.Event{Type: typ, Properties: data}
	if delivered != nil {
		event = *delivered // delivered before the withdrawal: record that
	}
	if err := b.record(id, pr, event); err != nil {
		log.WithError(err).WithField("sessionID", pr.session).Error("codex: a prompt's cancellation could not be spooled")
	}
}

// pendingPrompts lists unanswered prompts for reconciliation.
func (b *backend) pendingPrompts(sessionID string, questions bool) []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []map[string]any{}
	for _, pr := range b.prompts {
		if pr.question == questions && pr.session == sessionID {
			out = append(out, pr.props)
		}
	}
	return out
}

// files lists the paths of a file-change item seen on the running turn.
func (b *backend) files(thread, itemID string) []string {
	c := b.converter(thread)
	if c == nil || c.cur == nil {
		return nil
	}
	for _, p := range c.cur.parts {
		if p["callID"] != itemID {
			continue
		}
		entries, _ := p["state"].(map[string]any)["metadata"].(map[string]any)["files"].([]map[string]any)
		files := make([]string, 0, len(entries))
		for _, e := range entries {
			files = append(files, e["filePath"].(string))
		}
		return files
	}
	return nil
}
