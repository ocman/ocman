package autoapprove

import (
	"encoding/json"
	"github.com/NoUseFreak/ocman/internal/ocv2"
)

func (t *Tee) dispatchEvent(eventType, dataJSON string) {
	t.dispatchEventInDirectory(eventType, dataJSON, "")
}

func (t *Tee) dispatchEventInDirectory(eventType, dataJSON, directory string) {
	// Global streams wrap a regular event in its owning directory.
	var global struct {
		Directory string          `json:"directory"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &global); err == nil && len(global.Payload) > 0 {
		t.dispatchEventInDirectory("", string(global.Payload), global.Directory)
		return
	}
	var typeOnly struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &typeOnly); err != nil {
		return
	}
	effectiveType := eventType
	if effectiveType == "" {
		effectiveType = typeOnly.Type
	}
	switch effectiveType {
	case "permission.asked", "permission.v2.asked":
		t.dispatchPermissionAsked(directory, dataJSON)
	case "permission.replied", "permission.v2.replied":
		t.dispatchPermissionReplied(directory, dataJSON, "")
	case "permission.rejected":
		t.dispatchPermissionReplied(directory, dataJSON, "reject")
	case "question.asked":
		t.dispatchQuestionAsked(directory, dataJSON)
	case "question.replied":
		t.dispatchQuestionResolved(directory, dataJSON, "replied")
	case "question.rejected":
		t.dispatchQuestionResolved(directory, dataJSON, "rejected")
	case "session.idle":
		t.dispatchSessionIdle(dataJSON)
	case "session.status":
		t.dispatchSessionStatus(dataJSON)
	case "session.created", "session.updated":
		t.dispatchSessionChanged(dataJSON)
	case "message.updated", "message.removed", "message.part.updated", "message.part.removed", "message.part.delta":
		t.dispatchSessionDataChanged(messageEventSessionID(dataJSON))
		if effectiveType == "message.part.updated" {
			t.dispatchTerminalPart(dataJSON)
		}
		if effectiveType == "message.updated" {
			t.dispatchUserPrompt(dataJSON)
		}
	case "session.deleted":
		t.dispatchSessionDataChanged(deletedSessionID(dataJSON))
	case ocv2.QueueChangedEvent:
		if t.OnQueueChanged != nil {
			if sid := messageEventSessionID(dataJSON); sid != "" {
				t.OnQueueChanged(sid)
			}
		}
	}
}

func (t *Tee) dispatchUserPrompt(dataJSON string) {
	if t.OnUserPrompt == nil {
		return
	}
	var event struct {
		Properties struct {
			Info struct {
				SessionID string `json:"sessionID"`
				Role      string `json:"role"`
				Synthetic bool   `json:"synthetic"`
				Time      struct {
					Created int64 `json:"created"`
				} `json:"time"`
			} `json:"info"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(dataJSON), &event); err != nil {
		return
	}
	info := event.Properties.Info
	if info.Role == "user" && !info.Synthetic && info.SessionID != "" && info.Time.Created > 0 {
		t.OnUserPrompt(info.SessionID, info.Time.Created)
	}
}
