package claudeprovider

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// Runtime details bootstrap ingestion before Claude writes a transcript. These
// are supervisor reads, never a fallback for canonical conversation browsing.
func (l *live) runtimeDetail(s db.Session, r *http.Request) *platforms.SessionDetail {
	l.mu.Lock()
	defer l.mu.Unlock()
	d := &platforms.SessionDetail{Session: &s, Messages: []db.Message{}, Parts: []db.Part{}, SessionTree: []db.Session{s}}
	for _, v := range l.view[s.ID] {
		var frame struct {
			Info json.RawMessage `json:"info"`
			Part json.RawMessage `json:"part"`
		}
		_ = json.Unmarshal(v.event.Properties, &frame)
		var data struct {
			ID        string `json:"id"`
			MessageID string `json:"messageID"`
			Time      struct {
				Created int64 `json:"created"`
				Start   int64 `json:"start"`
			} `json:"time"`
		}
		switch v.event.Type {
		case "message.updated":
			_ = json.Unmarshal(frame.Info, &data)
			d.Messages = append(d.Messages, db.Message{ID: data.ID, SessionID: s.ID, TimeCreated: data.Time.Created, Data: frame.Info})
		case "message.part.updated":
			_ = json.Unmarshal(frame.Part, &data)
			d.Parts = append(d.Parts, db.Part{ID: data.ID, MessageID: data.MessageID, SessionID: s.ID, TimeCreated: data.Time.Start, Data: frame.Part})
		}
	}
	d.TotalMessages = len(d.Messages)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if limit > 0 {
		end := max(d.TotalMessages-max(offset, 0), 0)
		d.Messages = d.Messages[max(end-limit, 0):end]
		selected := map[string]bool{}
		for _, message := range d.Messages {
			selected[message.ID] = true
		}
		parts := d.Parts[:0]
		for _, part := range d.Parts {
			if selected[part.MessageID] {
				parts = append(parts, part)
			}
		}
		d.Parts = parts
	}
	return d
}
