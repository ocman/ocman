package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	acpexec "github.com/ocman/ocman/internal/platforms/acp"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
)

func (a *acpExecution) send(ctx context.Context, id string, req platforms.SendMessageRequest) error {
	// The generic composer can fall through to SendMessage when ShellExec is
	// false. Never reinterpret its reserved !command syntax as an LLM prompt.
	if strings.HasPrefix(strings.TrimSpace(req.Message), "!") {
		return platforms.ErrUnsupported
	}
	input, err := acpInput(req)
	if err != nil {
		return statusError{http.StatusBadRequest, err}
	}
	a.b.mu.Lock()
	s := a.b.sessionLocked(id)
	a.b.mu.Unlock()
	if !s.sendMu.TryLock() {
		return platforms.ErrBusy
	}
	defer s.sendMu.Unlock()
	c, err := a.conn(ctx)
	if err != nil {
		return err
	}
	a.b.mu.Lock()
	busy := s.running
	a.b.mu.Unlock()
	if busy {
		return a.steer(ctx, c, id, input)
	}
	if err := a.resume(ctx, c, id); err != nil {
		return err
	}
	if err := a.configure(ctx, c, id, req); err != nil {
		return err
	}
	native, _, err := a.b.conn(ctx)
	if err != nil {
		return err
	}
	// Capture the preceding user's identity before admitting this prompt. Never
	// fabricate a user item ID that native ingestion cannot later confirm.
	turn := &acpTurn{items: map[string]*acpItem{}, started: now(), command: strings.HasPrefix(req.Message, "/")}
	a.mu.Lock()
	fresh := a.fresh[id]
	a.mu.Unlock()
	if !fresh {
		before, err := native.ListItems(ctx, id)
		if err != nil {
			return err
		}
		for _, entry := range before {
			if entry.Item.Type == codexappserver.ItemUserMessage {
				turn.baseUser = entry.Item.ID
			}
		}
		turns, err := native.ListTurns(ctx, id, "notLoaded")
		if err != nil {
			return err
		}
		if len(turns) > 0 {
			turn.baseTurn = turns[len(turns)-1].ID
		}
	}
	err = a.b.sup.BeginTurn(id, func() {
		a.b.mu.Lock()
		defer a.b.mu.Unlock()
		s.accepted++
		s.running, s.status, s.view = true, db.StatusBusy, nil
		turn.admission = s.accepted
		turn.conv = &Converter{SessionID: id, ProviderID: s.provider, ModelID: s.model}
		if fresh {
			turn.title = s.title
		}
	})
	if err != nil {
		return statusError{http.StatusConflict, err}
	}
	a.mu.Lock()
	a.turns[id] = turn
	a.mu.Unlock()
	// The HTTP send acknowledges admission. The supervisor lifetime owns the
	// admitted prompt, not the short request deadline or browser connection.
	go a.run(c, id, input, turn)
	return nil
}

func (a *acpExecution) run(c *acpexec.Client, id string, input []acp.ContentBlock, turn *acpTurn) {
	response, err := c.Prompt(a.b.ctx, acp.PromptRequest{SessionId: acp.SessionId(id), Prompt: input})
	if a.b.ctx.Err() != nil {
		return // supervisor shutdown never publishes a false completion
	}
	if !a.finishInteractions(id) {
		return
	}
	turn.mu.Lock()
	if err == nil {
		err = a.reconcile(a.b.ctx, id, turn, response.StopReason == acp.StopReasonCancelled)
	}
	turn.mu.Unlock()
	a.settle(id, turn, err, response.StopReason == acp.StopReasonCancelled)
	a.mu.Lock()
	delete(a.fresh, id)
	if a.turns[id] == turn {
		delete(a.turns, id)
	}
	a.mu.Unlock()
}

// On disconnect the SDK releases the prompt waiter before all cancelled
// callbacks finish. Persist their resolutions before publishing idle. A failed
// spool keeps admission held until persistence recovers or the supervisor stops.
func (a *acpExecution) finishInteractions(sessionID string) bool {
	for {
		a.b.mu.Lock()
		var pending []string
		for id, prompt := range a.b.prompts {
			if prompt.session == sessionID && prompt.deliver != nil {
				pending = append(pending, id)
			}
		}
		a.b.mu.Unlock()
		if len(pending) == 0 {
			return true
		}
		for _, id := range pending {
			a.b.cancelPrompt(id)
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-a.b.ctx.Done():
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

func (a *acpExecution) configure(ctx context.Context, c *acpexec.Client, id string, req platforms.SendMessageRequest) error {
	model := req.Model
	if _, slug, ok := strings.Cut(model, "/"); ok {
		model = slug
	}
	if model != "" {
		native, _, err := a.b.conn(ctx)
		if err != nil {
			return err
		}
		models, err := native.ListModels(ctx, false)
		if err != nil {
			return err
		}
		choice := model
		for _, entry := range models {
			if entry.Model == model {
				choice = entry.ID
				break
			}
		}
		if _, err := c.SetConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{SessionId: acp.SessionId(id), ConfigId: "model", Value: acp.SessionConfigValueId(choice)}}); err != nil {
			return err
		}
	}
	if req.Reasoning != "" {
		if _, err := c.SetConfigOption(ctx, acp.SetSessionConfigOptionRequest{ValueId: &acp.SetSessionConfigOptionValueId{SessionId: acp.SessionId(id), ConfigId: "reasoning_effort", Value: acp.SessionConfigValueId(req.Reasoning)}}); err != nil {
			return err
		}
	}
	if model != "" {
		a.b.mu.Lock()
		a.b.sessionLocked(id).model = model
		a.b.mu.Unlock()
	}
	return nil
}

func (a *acpExecution) steer(ctx context.Context, c *acpexec.Client, id string, input []acp.ContentBlock) error {
	meta, _ := c.Info().Meta["steering"].(map[string]any)
	if meta["supported"] != true {
		return platforms.ErrBusy
	}
	raw, err := c.CallExtension(ctx, "_session/steering", map[string]any{
		"sessionId": id, "prompt": input, "_meta": map[string]any{"steering": map[string]any{"idleBehavior": "promptRequired"}},
	})
	if err != nil {
		return err // uncertain delivery: never try session/prompt instead
	}
	var outcome struct {
		Outcome string `json:"outcome"`
	}
	if err := json.Unmarshal(raw, &outcome); err != nil {
		return err
	}
	if outcome.Outcome != "injected" {
		return fmt.Errorf("%w: Codex ACP steering returned %q", platforms.ErrBusy, outcome.Outcome)
	}
	return nil
}

func acpInput(req platforms.SendMessageRequest) ([]acp.ContentBlock, error) {
	var input []acp.ContentBlock
	for _, image := range req.Images {
		header, data, ok := strings.Cut(image.URL, ",")
		if !ok || !strings.HasPrefix(header, "data:image/") || !strings.HasSuffix(header, ";base64") {
			return nil, errors.New("ACP images must be base64 image data URLs")
		}
		mime := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
		input = append(input, acp.ImageBlock(data, mime))
	}
	if strings.TrimSpace(req.Message) != "" {
		input = append(input, acp.TextBlock(req.Message))
	}
	if len(input) == 0 {
		return nil, errors.New("message is empty")
	}
	return input, nil
}
