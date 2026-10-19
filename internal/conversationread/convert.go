package conversationread

import (
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Convert restores the previously normalized v1 DTOs. Native v2/Claude
// conversion remains in their original converters; opaque rows stay retained
// but do not consume normal conversation paging slots.
func Convert(page *statev2.Detail, head *platforms.ConversationHead, platform string) (*platforms.SessionDetail, error) {
	root := sessionRow(page.Session, platform)
	root.Status = db.SettleSessionStatus(db.TurnUnobserved, false, db.InferSessionStatus(page.Conversation.LatestRole, page.Conversation.LatestFinish, page.Conversation.LatestError, false))
	if head.Session != nil {
		root = *head.Session
		root.Platform = platform
	}
	root.LastTurnCompletedAt = page.Conversation.LastTurnCompletedAt
	root.LastUserPromptAt = page.Conversation.LastUserPromptAt
	root.ActiveDurationMs = page.Conversation.ActiveDurationMs
	root.LastErrorName, root.LastErrorMessage = page.Conversation.LatestErrorName, page.Conversation.LatestErrorMessage
	d := &platforms.SessionDetail{Session: &root, Messages: []db.Message{}, Parts: []db.Part{}, SessionTree: []db.Session{}, TotalMessages: page.TotalMessages,
		ContextTokenCount: page.Conversation.ContextTokens, DefaultModel: head.DefaultModel, Warnings: head.Warnings, ConversationSource: "statev2"}
	if head.DirectoryDefaults {
		d.DefaultAgent, d.DefaultModel = page.Conversation.DefaultAgent, page.Conversation.DefaultModel
		if page.Conversation.UserModel != "" {
			d.DefaultModel = page.Conversation.UserModel
		}
	}
	for _, m := range page.Messages {
		data := m.Extensions
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return nil, err
		}
		if metadata, ok := fields["metadata"]; ok {
			if _, native := fields["native"]; native {
				data = metadata
			}
		}
		d.Messages = append(d.Messages, db.Message{ID: m.ID, SessionID: page.Session.SessionID, TimeCreated: m.CreatedAt, Data: data})
	}
	for _, p := range page.Parts {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(p.Extensions, &fields); err != nil {
			return nil, err
		}
		if fields == nil {
			fields = map[string]json.RawMessage{}
		}
		fields["type"], _ = json.Marshal(p.Kind)
		if p.TextPresent {
			fields["text"], _ = json.Marshal(p.Text)
		}
		data, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		d.Parts = append(d.Parts, db.Part{ID: p.ID, SessionID: page.Session.SessionID, MessageID: p.MessageID, TimeCreated: p.CreatedAt, Data: data})
	}
	for _, s := range page.Tree {
		row := sessionRow(s, platform)
		total := page.ConversationTotals[s.SessionID]
		row.Status = db.SettleSessionStatus(db.TurnUnobserved, false, db.InferSessionStatus(total.Role, total.Finish, total.Error, false))
		if row.ID == root.ID {
			row = root
		}
		applyTotals(&row, page.ConversationTotals[s.SessionID], head)
		d.SessionTree = append(d.SessionTree, row)
	}
	applyTotals(d.Session, page.ConversationTotals[page.Session.SessionID], head)
	return d, nil
}

func applyTotals(row *db.Session, total statev2.ConversationTotals, head *platforms.ConversationHead) {
	row.LastTurnCompletedAt, row.LastUserPromptAt, row.ActiveDurationMs = total.CompletedAt, total.PromptAt, total.ActiveDuration
	row.MessageCount, row.TotalInputTokens, row.TotalOutputTokens, row.TotalCost = total.Messages, total.Input, total.Output, total.Cost
	row.TotalEffectiveCost = total.Cost
	row.TotalEstCost = 0
	for _, m := range total.Models {
		if head.Pricing == nil || m.Model == "" {
			continue
		}
		model := m.Model
		if m.Provider != "" {
			model = m.Provider + "/" + model
		}
		estimate := head.Pricing.CalcCost(model, m.Input, m.Output, m.CacheRead, m.CacheWrite)
		row.TotalEstCost += estimate
		if m.Paid == 0 {
			row.TotalEffectiveCost += estimate - m.Unpaid
		}
	}
}

func sessionRow(s statev2.Session, platform string) db.Session {
	r := db.Session{ID: s.SessionID, Platform: platform, ParentID: s.ParentID, ProjectID: s.ProjectID, Title: s.Title, Directory: s.Directory, TimeCreated: s.CreatedAt, TimeUpdated: s.UpdatedAt, DurationMs: s.UpdatedAt - s.CreatedAt, Status: db.StatusInterrupted}
	var extra struct {
		SummaryAdditions, SummaryDeletions, SummaryFiles *int
		ShareURL                                         *string
	}
	if json.Unmarshal(s.Extensions, &extra) == nil {
		r.SummaryAdditions, r.SummaryDeletions, r.SummaryFiles, r.ShareURL = extra.SummaryAdditions, extra.SummaryDeletions, extra.SummaryFiles, extra.ShareURL
	}
	return r
}
