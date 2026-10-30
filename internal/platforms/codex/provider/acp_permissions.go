package codexprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	acp "github.com/coder/acp-go-sdk"
	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/providerprotocol"
)

func (a *acpExecution) permission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	a.mu.Lock()
	active := a.turns[string(req.SessionId)] != nil
	a.mu.Unlock()
	if !active {
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
	id := "per_" + uuid.NewString()
	input, _ := json.Marshal(req.ToolCall.RawInput)
	metadata := arguments(input)
	kind, patterns := "acp_tool", []string{}
	if command, ok := metadata["command"].(string); ok && command != "" {
		kind = "bash"
		patterns = append(patterns, providerprotocol.ExactPattern(command))
	} else if req.ToolCall.Kind != nil && *req.ToolCall.Kind == acp.ToolKindEdit {
		kind = "edit"
		for _, location := range req.ToolCall.Locations {
			patterns = append(patterns, providerprotocol.ExactPattern(location.Path))
		}
	}
	// Unknown tool inputs get their own exact opaque pattern, never a wildcard
	// that could inherit an unrelated command/file grant.
	if len(patterns) == 0 {
		patterns = []string{providerprotocol.ExactPattern(string(req.ToolCall.ToolCallId))}
	}
	metadata["acpOptions"], metadata["acpToolCall"] = req.Options, req.ToolCall
	props := map[string]any{"id": id, "sessionID": string(req.SessionId), "permission": kind,
		"patterns": patterns, "always": patterns, "metadata": metadata, "tool": map[string]any{"callID": req.ToolCall.ToolCallId}}
	responses := make(chan acp.RequestPermissionResponse, 1)
	pr := &prompt{session: string(req.SessionId), props: props, recorded: make(chan struct{})}
	pr.deliver = func(value any) error {
		decision, ok := value.(codexappserver.ApprovalResponse)
		if !ok {
			return errors.New("invalid ACP approval response")
		}
		outcome := acp.NewRequestPermissionOutcomeCancelled()
		want := acp.PermissionOptionKindRejectOnce
		if decision.Decision == codexappserver.DecisionAccept {
			want = acp.PermissionOptionKindAllowOnce
		}
		found := false
		for _, option := range req.Options {
			if option.Kind == want {
				outcome = acp.NewRequestPermissionOutcomeSelected(option.OptionId)
				found = true
				break
			}
		}
		if want == acp.PermissionOptionKindAllowOnce && !found {
			return fmt.Errorf("%w: adapter offers no allow-once option", platforms.ErrUnsupported)
		}
		select {
		case responses <- acp.RequestPermissionResponse{Outcome: outcome}:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	data, _ := json.Marshal(props)
	if err := a.b.sup.Publish(providerprotocol.Event{Type: "permission.asked", Properties: data}, func() {
		a.b.mu.Lock()
		a.b.prompts[id] = pr
		a.b.mu.Unlock()
	}); err != nil {
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
	defer a.b.cancelPrompt(id)
	select {
	case response := <-responses:
		// Codex cannot finish this interaction before its resolution is durable.
		select {
		case <-pr.recorded:
			return response, nil
		case <-ctx.Done():
		}
	case <-ctx.Done():
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
}
