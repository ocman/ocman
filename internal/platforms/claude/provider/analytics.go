package claudeprovider

import (
	"encoding/json"

	"github.com/ocman/ocman/internal/db"
)

// analyticsRows projects converted messages for the transcript index:
// assistant JSON as converted, every other role as
// {"role": ...}. Cost stays 0, so analytics price the tokens at API rates.
func analyticsRows(messages []db.Message, tools map[string][]string) []db.AnalyticsMessage {
	out := make([]db.AnalyticsMessage, 0, len(messages))
	for _, m := range messages {
		var info struct {
			Role   string          `json:"role"`
			Finish string          `json:"finish"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(m.Data, &info) != nil {
			continue
		}
		row := db.AnalyticsMessage{ID: m.ID, SessionID: m.SessionID, TimeCreated: m.TimeCreated, Settled: true, Data: string(m.Data)}
		if info.Role != "assistant" {
			data, _ := json.Marshal(map[string]string{"role": info.Role})
			row.Data = string(data)
		} else {
			row.Settled = info.Finish != "" || len(info.Error) > 0
			row.ToolTimes = tools[m.ID]
		}
		out = append(out, row)
	}
	return out
}

// toolTimes records each converted tool part's state.time JSON under its message.
func toolTimes(parts []db.Part, into map[string][]string) {
	for _, p := range parts {
		var part struct {
			Type  string `json:"type"`
			State struct {
				Time json.RawMessage `json:"time"`
			} `json:"state"`
		}
		if json.Unmarshal(p.Data, &part) != nil || part.Type != "tool" {
			continue
		}
		into[p.MessageID] = append(into[p.MessageID], string(part.State.Time))
	}
}
