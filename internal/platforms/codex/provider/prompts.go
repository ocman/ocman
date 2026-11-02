package codexprovider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/providersupervisor"
)

// question is one field of an ACP elicitation, as the question UI shows it.
type question struct {
	ID       string
	Header   string
	Question string
	IsOther  bool
	Options  []option
}

type option struct {
	Label       string
	Description string
}

// approval and questionAnswers are the decisions handed to an ACP callback.
// questionAnswers maps question IDs to their answers; empty cancels.
type approval struct{ accept bool }

type questionAnswers map[string][]string

// prompt is an unanswered ACP interaction. The supervisor only relays it:
// ocman's rules, judge or user decide (AD-7a).
type prompt struct {
	session   string
	question  bool
	questions []question
	props     map[string]any // the v1 permission.asked / question.asked properties
	answering bool
	reply     *providerprotocol.Event // delivered to Codex, not yet spooled
	deliver   func(any) error         // decision handed to the ACP callback
	recorded  chan struct{}           // ACP waits for durable resolution before continuing
}

// refusal is the answer that lets Codex continue without the prompt.
func (pr *prompt) refusal() any {
	if pr.question {
		return questionAnswers{}
	}
	return approval{}
}

func v1Questions(qs []question) []map[string]any {
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
		return providersupervisor.WithStatus(http.StatusConflict, errors.New("prompt is being answered"))
	}
	props["sessionID"], props["requestID"] = pr.session, id
	data, _ := json.Marshal(props)
	decision := providerprotocol.Event{Type: typ, Properties: data}
	if pr.reply != nil && (pr.reply.Type != typ || !bytes.Equal(pr.reply.Properties, data)) {
		b.mu.Unlock()
		return providersupervisor.WithStatus(http.StatusUnprocessableEntity, errors.New("codex already received a different decision for this prompt; that decision stands"))
	}
	pr.answering = true
	delivered := pr.reply != nil
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		pr.answering = false
		b.mu.Unlock()
	}()
	if !delivered {
		err := pr.deliver(response(pr))
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
		return providersupervisor.WithStatus(http.StatusServiceUnavailable, fmt.Errorf("%w: %w", platforms.ErrReplyUnrecorded, err))
	}
	return nil
}

// record spools a prompt's resolution, which removes the prompt.
func (b *backend) record(id string, pr *prompt, event providerprotocol.Event) error {
	return b.sup.Publish(event, func() {
		b.mu.Lock()
		if b.prompts[id] == pr {
			delete(b.prompts, id)
			if pr.recorded != nil {
				close(pr.recorded)
			}
		}
		b.mu.Unlock()
	})
}

func (b *backend) replyPermission(id string, reply string) error {
	var decision approval
	switch reply {
	case "once", "always": // ocman remembers "always" itself
		decision.accept = true
	case "reject":
	default:
		return providersupervisor.WithStatus(http.StatusBadRequest, errors.New("reply must be once, always or reject"))
	}
	return b.answer(id, false, func(*prompt) any { return decision },
		"permission.replied", map[string]any{"reply": reply})
}

// replyQuestion answers questions in order; Codex keys answers by question id.
func (b *backend) replyQuestion(id string, answers [][]string) error {
	return b.answer(id, true, func(pr *prompt) any {
		out := questionAnswers{}
		for i, q := range pr.questions {
			if i < len(answers) {
				out[q.ID] = answers[i]
			}
		}
		return out
	}, "question.replied", map[string]any{"answers": answers})
}

func (b *backend) rejectQuestion(id string) error {
	return b.answer(id, true, (*prompt).refusal, "question.rejected", map[string]any{})
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
