package claudeprovider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// HistoryConfirmation describes content actually committed by ingestion. Native
// timestamps and live-only tool metadata may differ, but text and tool outcomes
// must match. Claude intentionally omits some streamed reasoning from history.
func HistoryConfirmation(snapshot *db.HistorySnapshot) providerprotocol.HistoryConfirmation {
	r := providerprotocol.HistoryConfirmation{Entries: map[string]string{}}
	for _, m := range snapshot.Messages {
		r.Entries["m:"+m.ID] = "present"
	}
	for _, p := range snapshot.Parts {
		if digest := partContentDigest(p.Data); digest != "" {
			r.Entries["p:"+p.ID] = digest
		}
	}
	return r
}

func partContentDigest(raw json.RawMessage) string {
	var p map[string]any
	if json.Unmarshal(raw, &p) != nil || p["type"] == "reasoning" {
		return ""
	}
	for _, key := range []string{"id", "messageID", "sessionID", "time"} {
		delete(p, key)
	}
	if state, ok := p["state"].(map[string]any); ok {
		delete(state, "time")
		delete(state, "metadata")
	}
	data, _ := json.Marshal(p)
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (l *live) viewContentLocked(id string) map[string]string {
	entries := map[string]string{}
	for _, v := range l.view[id] {
		var p struct {
			Info json.RawMessage `json:"info"`
			Part json.RawMessage `json:"part"`
		}
		_ = json.Unmarshal(v.event.Properties, &p)
		var identity struct {
			ID string `json:"id"`
		}
		switch v.event.Type {
		case "message.updated":
			if json.Unmarshal(p.Info, &identity) == nil {
				entries["m:"+identity.ID] = "present"
			}
		case "message.part.updated":
			if json.Unmarshal(p.Part, &identity) == nil {
				if digest := partContentDigest(p.Part); digest != "" {
					entries["p:"+identity.ID] = digest
				}
			}
		}
	}
	return entries
}

func (l *live) confirmHistory(id string, receipt providerprotocol.HistoryConfirmation) {
	l.mu.Lock()
	defer l.mu.Unlock()
	child, known := l.subagents[id]
	if _, retained := l.view[id]; !known || !retained {
		return
	}
	if child.confirmed == nil {
		child.confirmed = map[string]string{}
	}
	for key, digest := range l.viewContentLocked(id) {
		if receipt.Entries[key] == digest {
			child.confirmed[key] = digest
		}
	}
	l.subagents[id] = child
	l.reclaimChildLocked(id)
}

func (l *live) confirmHistoryRequest(r *http.Request, id string) error {
	var receipt providerprotocol.HistoryConfirmation
	if err := decode(r, &receipt); err != nil {
		return err
	}
	l.confirmHistory(id, receipt)
	return nil
}

func (l *live) reclaimChildLocked(id string) {
	child, known := l.subagents[id]
	if !known || child.confirmed == nil {
		return
	}
	switch l.status[id].Status {
	case db.StatusDone, db.StatusError, db.StatusInterrupted:
	default:
		return
	}
	for key, digest := range l.viewContentLocked(id) {
		if child.confirmed[key] != digest {
			return
		}
	}
	delete(l.view, id)
	child.confirmed = nil
	l.subagents[id] = child
}
