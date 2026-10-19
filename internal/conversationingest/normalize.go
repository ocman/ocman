// Package conversationingest imports complete owner-local history snapshots
// into statev2 without changing readers or executing historical lifecycle actions.
package conversationingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Normalize consumes the adapters' existing v1-compatible read DTOs. Native v2
// and Claude conversion stays in their existing converters, not duplicated here.
func Normalize(key statev2.Key, snapshot *db.HistorySnapshot) (statev2.Batch, string, error) {
	var batch statev2.Batch
	if snapshot == nil || snapshot.Session.ID != key.SessionID || key.InstanceID == "" || key.SessionID == "" {
		return batch, "", fmt.Errorf("history snapshot identity mismatch")
	}
	s := snapshot.Session
	ext, err := json.Marshal(struct {
		ProjectID        string
		ParentID         string
		SummaryAdditions *int
		SummaryDeletions *int
		SummaryFiles     *int
		ShareURL         *string
	}{s.ProjectID, s.ParentID, s.SummaryAdditions, s.SummaryDeletions, s.SummaryFiles, s.ShareURL})
	if err != nil {
		return batch, "", err
	}
	batch = statev2.Batch{Complete: true, Session: statev2.Session{Key: key, Title: s.Title, Directory: s.Directory,
		ParentID: s.ParentID, ProjectID: s.ProjectID, CreatedAt: s.TimeCreated, UpdatedAt: s.TimeUpdated, Extensions: ext, ReadFormat: statev2.ConversationReadFormat,
		Subagent: snapshot.Subagent}}
	messages := map[string]bool{}
	for i, m := range snapshot.Messages {
		if m.ID == "" || m.SessionID != key.SessionID || messages[m.ID] {
			return batch, "", fmt.Errorf("invalid or duplicate history message identity")
		}
		messages[m.ID] = true
		var data db.MessageData
		if err := json.Unmarshal(m.Data, &data); err != nil || data.Role == "" {
			return batch, "", fmt.Errorf("invalid history message metadata for %s", m.ID)
		}
		ext, err := canonical(m.Data)
		if err != nil {
			return batch, "", err
		}
		if native, ok := snapshot.NativeMessages[m.ID]; ok {
			raw, err := json.Marshal(map[string]json.RawMessage{"metadata": ext, "native": native})
			if err != nil {
				return batch, "", err
			}
			ext, err = canonical(raw)
			if err != nil {
				return batch, "", err
			}
		}
		model, provider := data.ModelID, data.ProviderID
		if model == "" && data.Model != nil {
			model, provider = data.Model.ModelID, data.Model.ProviderID
		}
		if provider != "" && model != "" {
			model = provider + "/" + model
		}
		message := statev2.Message{ID: m.ID, Position: int64(i), Role: data.Role, Agent: data.Agent, Provider: provider, Model: model,
			CreatedAt: m.TimeCreated, Cost: data.Cost, Finish: data.Finish, Extensions: ext}
		_, native := snapshot.NativeMessages[m.ID]
		message.ConversationVisible = !native || data.Role != "unknown"
		if data.Time != nil {
			message.CompletedAt = data.Time.Completed
		}
		if data.Tokens != nil {
			message.TokensInput, message.TokensOutput, message.TokensReasoning = data.Tokens.Input, data.Tokens.Output, data.Tokens.Reasoning
			if data.Tokens.Cache != nil {
				message.TokensCacheRead, message.TokensCacheWrite = data.Tokens.Cache.Read, data.Tokens.Cache.Write
			}
		}
		if len(data.Error) > 0 && string(data.Error) != "null" {
			message.HasError = true
			var errorInfo struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(data.Error, &errorInfo) == nil {
				message.ErrorName = errorInfo.Name
			}
		}
		batch.Messages = append(batch.Messages, message)
	}
	for id := range snapshot.NativeMessages {
		if !messages[id] {
			return batch, "", fmt.Errorf("native history payload has no message identity")
		}
	}
	positions := map[string]int64{}
	parts := map[[2]string]bool{}
	for _, p := range snapshot.Parts {
		partKey := [2]string{p.MessageID, p.ID}
		if p.ID == "" || p.SessionID != key.SessionID || !messages[p.MessageID] || parts[partKey] {
			return batch, "", fmt.Errorf("invalid or duplicate history part identity")
		}
		parts[partKey] = true
		var data struct{ Type, Text string }
		if err := json.Unmarshal(p.Data, &data); err != nil || data.Type == "" {
			return batch, "", fmt.Errorf("invalid history part metadata for %s", p.ID)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(p.Data, &fields); err != nil {
			return batch, "", err
		}
		// Text is normalized separately. Retain tool arguments, results,
		// attachments, usage and unknown extension fields without duplicating text.
		textRaw, textPresent := fields["text"]
		textPresent = textPresent && string(textRaw) != "null"
		if textPresent {
			delete(fields, "text")
		}
		delete(fields, "type")
		raw, err := json.Marshal(fields)
		if err != nil {
			return batch, "", err
		}
		ext, err := canonical(raw)
		if err != nil {
			return batch, "", err
		}
		batch.Parts = append(batch.Parts, statev2.Part{ID: p.ID, MessageID: p.MessageID, Position: positions[p.MessageID],
			Kind: data.Type, Text: data.Text, Extensions: ext, CreatedAt: p.TimeCreated, TextPresent: textPresent})
		positions[p.MessageID]++
	}
	raw, err := json.Marshal(batch)
	if err != nil {
		return batch, "", err
	}
	digest := sha256.Sum256(raw)
	return batch, hex.EncodeToString(digest[:]), nil
}

func canonical(raw []byte) (json.RawMessage, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber() // costs, token counts and native numeric IDs must not round through float64
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
