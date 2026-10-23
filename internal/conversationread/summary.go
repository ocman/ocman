package conversationread

import (
	"encoding/json"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Metadata restores only stored identity/display fields, with no accounting or
// current runtime authority. It is safe for notifications and owner routing.
func Metadata(s statev2.Session, platform string) db.Session { return sessionRow(s, platform) }

// Summary restores the shared session DTO exclusively from persisted facts.
// Platforms may overlay event-derived status, but inference never means running.
func Summary(s statev2.SessionSummary, platform string, head *platforms.ConversationHead) db.Session {
	row := sessionRow(s.Session, platform)
	row.SourceChild = statev2.SourceChild(s.Session)
	row.Status = db.SettleSessionStatus(db.TurnUnobserved, false, InferredStatus(s))
	row.LastErrorName, row.LastErrorMessage, row.LastErrorAt = s.Totals.Error, s.ErrorMessage, s.LatestMessageCreated
	applyTotals(&row, s.Totals, head)
	return row
}

// InferredStatus retains platform-specific static transcript rules without
// teaching application readers platform identities or allowing stored busy to
// establish running authority.
func InferredStatus(s statev2.SessionSummary) db.SessionStatus {
	var extra struct{ InferredStatus db.SessionStatus }
	if json.Unmarshal(s.Extensions, &extra) == nil {
		switch extra.InferredStatus {
		case db.StatusBusy, db.StatusWaiting, db.StatusDone, db.StatusError, db.StatusInterrupted:
			return extra.InferredStatus
		}
	}
	return db.InferStoredSessionStatus(s.Totals.Role, s.Totals.Finish, s.Totals.Error)
}
