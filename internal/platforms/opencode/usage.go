package opencode

import (
	"context"
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) SessionUsage(ctx context.Context, sessionID string) (map[string]platforms.Usage, error) {
	if a.db == nil {
		return nil, platforms.ErrNotFound
	}
	messages, err := a.db.GetDescendantMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	usage := map[string]platforms.Usage{sessionID: {}}
	for _, message := range messages {
		var data struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(message.Data, &data) != nil || data.Role != "assistant" {
			continue
		}
		batch := []db.Message{message}
		cost, estimate, _ := costsFromMessages(batch, a.pricing)
		total := usage[message.SessionID]
		total.Add(platforms.Usage{Tokens: tokenTotalsFromMessages(batch), Cost: cost, EstCost: estimate})
		usage[message.SessionID] = total
	}
	return usage, nil
}
