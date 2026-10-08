package server

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
)

// injectApprovalNotices restores persisted permission footnotes on history reads.
func injectApprovalNotices(ctx context.Context, platform, sessionID string, stateDB interface {
	ListApprovedPermissions(context.Context, string, string) ([]state.ApprovedPermission, error)
}, msgs *[]db.Message, parts *[]db.Part) {
	approved, err := stateDB.ListApprovedPermissions(ctx, platform, sessionID)
	if err != nil {
		return
	}
	for _, p := range approved {
		keyPart := p.PermissionID
		if keyPart == "" {
			keyPart = p.JudgeSessionID
		}
		patterns := p.Patterns
		if patterns == nil {
			patterns = []string{}
		}
		reasoning := p.Reasoning
		if p.UserApproved() {
			reasoning = ""
		}
		partData, _ := json.Marshal(map[string]interface{}{
			"type": "auto-approved", "permission": p.PermissionText, "patterns": patterns,
			"reasoning": reasoning, "approvedBy": p.ApprovedBy, "reply": p.Reply,
			"metadata": p.Metadata, "askedAt": p.AskedAt, "approvedAt": p.ApprovedAt,
		})
		injectHistoryNotice(sessionID, "ocman-notice-"+keyPart, p.ApprovedAt, partData, msgs, parts)
	}
}

func injectHistoryNotice(sessionID, id string, at int64, data json.RawMessage, msgs *[]db.Message, parts *[]db.Part) {
	if slices.ContainsFunc(*msgs, func(m db.Message) bool { return m.ID == id }) {
		return
	}
	*msgs = append(*msgs, db.Message{ID: id, SessionID: sessionID, TimeCreated: at, Data: json.RawMessage(`{"role":"notice"}`)})
	*parts = append(*parts, db.Part{ID: id + "-part", MessageID: id, SessionID: sessionID, TimeCreated: at, Data: data})
	slices.SortStableFunc(*msgs, func(a, b db.Message) int { return cmp.Compare(a.TimeCreated, b.TimeCreated) })
	slices.SortStableFunc(*parts, func(a, b db.Part) int { return cmp.Compare(a.TimeCreated, b.TimeCreated) })
}
