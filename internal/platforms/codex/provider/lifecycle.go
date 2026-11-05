package codexprovider

import (
	"encoding/json"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// StoredLifecycle preserves the converter's native message identity for normal
// lifecycle consumers, including plugin follow-up receipt checks.
func StoredLifecycle(snapshot *db.HistorySnapshot) *platforms.SessionLifecycle {
	out := &platforms.SessionLifecycle{Status: db.SettleSessionStatus(db.TurnUnobserved, false, snapshot.InferredStatus)}
	if n := len(snapshot.Messages); n > 0 {
		last := snapshot.Messages[n-1]
		var info struct {
			Role string `json:"role"`
		}
		_ = json.Unmarshal(last.Data, &info)
		out.LatestMessageID, out.LatestMessageCreated, out.LatestMessageRole = last.ID, last.TimeCreated, info.Role
	}
	return out
}
