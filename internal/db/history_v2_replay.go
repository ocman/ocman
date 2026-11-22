package db

import (
	"encoding/json"
	"fmt"

	ocv2 "github.com/ocman/ocman/internal/platforms/opencode/apiv2"
)

// ReplayV2HistoryMessage converts the raw envelope kept in a stored message's
// extensions. It never opens the OpenCode database or writes to Ocman.
// An unsupported message is distinguished from a missing or expired payload.
func ReplayV2HistoryMessage(sessionID, messageID string, extensions json.RawMessage) (ocv2.V1Message, bool, error) {
	var envelope struct {
		Native struct {
			Type string
			Data json.RawMessage
		}
	}
	if err := json.Unmarshal(extensions, &envelope); err != nil {
		return ocv2.V1Message{}, false, err
	}
	return convertV2HistoryMessage(sessionID, messageID, envelope.Native.Type, envelope.Native.Data)
}

func convertV2HistoryMessage(sessionID, id, kind string, raw json.RawMessage) (ocv2.V1Message, bool, error) {
	var native map[string]any
	if kind == "" || json.Unmarshal(raw, &native) != nil || native == nil {
		return ocv2.V1Message{}, false, fmt.Errorf("OpenCode message %s has no valid raw body", id)
	}
	native["id"], native["type"] = id, kind
	converted, ok := ocv2.ConvertMessage(sessionID, native)
	return converted, ok, nil
}
