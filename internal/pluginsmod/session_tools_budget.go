package pluginsmod

import (
	"context"
	"encoding/json"

	"github.com/ocman/ocman/internal/db"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

// A session has a single user turn. Sum every observed assistant step, rather
// than treating a tool-loop's newest message as the entire cost. A finite cap
// fails closed before dispatch when accounting is absent or incomplete.
func toolBudgetAllows(b *sessionToolBinding, cost *float64) bool {
	return b.params.MaxCostUSD == 0 || cost != nil && *cost < b.params.MaxCostUSD
}

func (m *Module) toolSessionCost(ctx context.Context, b *sessionToolBinding) *float64 {
	if m.deps.Adapter == nil {
		return nil
	}
	adapter, ok := m.deps.Adapter(ctx, b.platform, b.session)
	if !ok {
		return nil
	}
	detail, err := adapter.Session(ctx, b.session, 500, 0)
	if err != nil || detail == nil || detail.TotalMessages > len(detail.Messages) {
		return nil
	}
	usage := new(plugins.SessionUsage)
	known := false
	for _, message := range detail.Messages {
		var data struct {
			db.MessageData
			Cost *float64 `json:"cost"`
		}
		if json.Unmarshal(message.Data, &data) != nil {
			return nil
		}
		if data.Role != "assistant" {
			continue
		}
		if data.Cost == nil || data.UsageAvailable != nil && !*data.UsageAvailable || *data.Cost < 0 {
			return nil
		}
		known = true
		usage.CostUSD += *data.Cost
		if usage.Validate() != nil {
			return nil
		}
	}
	if !known {
		return nil
	}
	return &usage.CostUSD
}
