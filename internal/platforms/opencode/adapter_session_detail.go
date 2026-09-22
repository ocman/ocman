package opencode

import (
	"context"
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (s messageStats) activeDurationAt(now int64, running bool) int64 {
	if running && s.inFlightStartedAt > 0 && now > s.inFlightStartedAt {
		return s.activeDurationMs + now - s.inFlightStartedAt
	}
	return s.activeDurationMs
}

func currentConversationModel(messages []db.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		var data db.MessageData
		if err := json.Unmarshal(messages[i].Data, &data); err != nil || data.Role != "user" {
			continue
		}
		providerID, modelID := data.ProviderID, data.ModelID
		if modelID == "" && data.Model != nil {
			providerID, modelID = data.Model.ProviderID, data.Model.ModelID
		}
		if modelID == "" {
			continue
		}
		return formatConversationModel(providerID, modelID)
	}
	return ""
}

func formatConversationModel(providerID, modelID string) string {
	if modelID == "" {
		return ""
	}
	if providerID != "" {
		return providerID + "/" + modelID
	}
	return modelID
}

func (a *Adapter) attachSessionTree(ctx context.Context, id string, detail *platforms.SessionDetail) error {
	tree, err := a.db.GetSessionTree(ctx, id)
	if err != nil {
		return err
	}

	byID := make(map[string]db.Session, len(tree))
	for _, session := range tree {
		byID[session.ID] = session
	}

	ports := discoverOpenCodePorts()
	detail.SessionTree = make([]db.Session, 0, len(byID))
	for _, session := range byID {
		if a.pricing != nil {
			messages, err := a.db.GetSessionMessages(ctx, session.ID)
			if err != nil {
				return err
			}
			_, session.TotalEstCost, session.TotalEffectiveCost = costsFromMessages(messages, a.pricing)
		}
		session.Platform = string(PlatformID)
		session.Status = a.settleStatus(session.ID, session.Directory, session.Status, ports)
		session.LiveConnection = directoryHasLivePort(ports, session.Directory)
		detail.SessionTree = append(detail.SessionTree, session)
	}
	return nil
}

// applySessionDetailMetadataFromMessages fills in aggregates, status
// inference, and error metadata from stored messages. The caller must
// re-settle Status against the live turn signal (see Adapter.settleStatus).
func applySessionDetailMetadataFromMessages(session *db.Session, messages []db.Message) {
	if session == nil {
		return
	}
	session.ActiveDurationMs = 0
	for _, message := range messages {
		var data db.MessageData
		if err := json.Unmarshal(message.Data, &data); err != nil || data.Role != "assistant" || data.Time == nil {
			continue
		}
		if data.Time.Completed > data.Time.Created {
			session.ActiveDurationMs += data.Time.Completed - data.Time.Created
		}
	}
	if len(messages) == 0 {
		session.Status = db.InferSessionStatus("", "", "", false)
		return
	}
	last := messages[len(messages)-1]
	var data struct {
		Role   string `json:"role"`
		Finish string `json:"finish"`
		Error  *struct {
			Name    string `json:"name"`
			Message string `json:"message"`
			Data    *struct {
				Message string `json:"message"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(last.Data, &data); err != nil {
		return
	}
	lastErr := ""
	if data.Error != nil {
		lastErr = "true"
		session.LastErrorName = data.Error.Name
		if data.Error.Data != nil {
			session.LastErrorMessage = data.Error.Data.Message
		} else {
			session.LastErrorMessage = data.Error.Message
		}
		session.LastErrorAt = last.TimeCreated
	}
	session.Status = db.InferSessionStatus(data.Role, data.Finish, lastErr, false)
}
