package autoapprove

import (
	"context"
	"encoding/json"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// HandleProviderEvent is the headless prompt pipeline for a provider
// adapter's event stream (AD-7a). Prompts ocman's rules settle never get
// here; the rest go to the safe-command cache, the judge, then the user.
// Replies are recorded with their provenance like OpenCode's and broadcast
// as resolved under the adapter's platform.
func (s *Service) HandleProviderEvent(ctx context.Context, adapter platforms.Platform, event providerprotocol.Event) {
	var prompt platforms.LivePrompt
	if json.Unmarshal(event.Properties, &prompt) != nil {
		return
	}
	sessionID, _ := prompt["sessionID"].(string)
	switch event.Type {
	case "permission.asked":
		id, _ := prompt["id"].(string)
		permission, _ := prompt["permission"].(string)
		metadata, _ := prompt["metadata"].(map[string]any)
		s.ObservePermissionPrompt(adapter.ID(), sessionID, prompt)
		s.Ensure(adapter.ID(), adapter, sessionID, id, permission, prompt.StringsField("patterns"), metadata)
	case "permission.replied":
		id, _ := prompt["requestID"].(string)
		reply, _ := prompt["reply"].(string)
		if prompt["reason"] == "cancelled" { // an interrupt, not a user decision
			s.Cancel(sessionID, id)
			s.forgetAsked(sessionID, id)
			return
		}
		s.HandlePermissionReplied(ctx, sessionID, id, reply)
		reason := "replied"
		if reply == "reject" {
			reason = "rejected"
		}
		s.broadcastPermissionResolved(adapter.ID(), sessionID, id, reason)
	case "question.asked":
		s.ObserveQuestionPrompt(adapter.ID(), prompt)
	case "question.replied", "question.rejected":
		id, _ := prompt["requestID"].(string)
		reason := "replied"
		if event.Type == "question.rejected" {
			reason = "rejected"
		}
		s.broadcastQuestionResolved(adapter.ID(), sessionID, id, reason)
	}
}
