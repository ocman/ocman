package autoapprove

import (
	"context"
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// recentUserMessagesFromSession reads the latest user texts of a session
// through its platform adapter, oldest first.
func recentUserMessagesFromSession(ctx context.Context, adapter platforms.Platform, sessionID string) []string {
	if adapter == nil {
		return nil
	}
	detail, err := adapter.Session(ctx, sessionID, 4*recentUserMessagesLimit, 0)
	if err != nil || detail == nil {
		return nil
	}
	user := map[string]bool{}
	var order []string
	for _, m := range detail.Messages {
		var data struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(m.Data, &data) == nil && data.Role == "user" {
			user[m.ID] = true
			order = append(order, m.ID)
		}
	}
	text := map[string]string{}
	for _, p := range detail.Parts {
		var part struct {
			Type      string `json:"type"`
			Text      string `json:"text"`
			Synthetic bool   `json:"synthetic"`
		}
		if user[p.MessageID] && json.Unmarshal(p.Data, &part) == nil && part.Type == "text" && !part.Synthetic {
			text[p.MessageID] += part.Text
		}
	}
	var out []string
	for _, id := range order {
		if text[id] != "" {
			out = append(out, text[id])
		}
	}
	if len(out) > recentUserMessagesLimit {
		out = out[len(out)-recentUserMessagesLimit:]
	}
	return out
}
