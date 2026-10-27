package codexprovider

import (
	"encoding/json"
	"time"
	"unicode/utf8"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

type turnEnd struct {
	admission uint64
	status    db.SessionStatus
}

// Keep every encoded upsert below the spool limit, including JSON escaping.
// A large structured payload becomes an explicit error instead of disappearing.
func boundedEvent(id, key, typ string, props any) providerprotocol.Event {
	data, _ := json.Marshal(props)
	if len(data) > providerprotocol.MaxEventBytes/2 {
		var object map[string]any
		_ = json.Unmarshal(data, &object)
		boundStrings(object)
		data, _ = json.Marshal(object)
		if len(data) > providerprotocol.MaxEventBytes/2 {
			if part, ok := object["part"].(map[string]any); ok {
				object["part"] = map[string]any{"id": part["id"], "messageID": part["messageID"], "sessionID": id,
					"type": "text", "text": "[Codex output truncated: structured item exceeds event size limit]"}
			} else {
				object["info"] = map[string]any{"id": key[2:], "sessionID": id, "role": "assistant",
					"error": map[string]any{"name": "UnknownError", "data": map[string]any{"message": "Codex output truncated: message exceeds event size limit"}}}
			}
			data, _ = json.Marshal(object)
		}
	}
	return providerprotocol.Event{Type: typ, Properties: data}
}

func boundStrings(value any) {
	const limit = 256 << 10
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			if text, ok := child.(string); ok && len(text) > limit {
				end := limit
				for !utf8.ValidString(text[:end]) {
					end--
				}
				v[k] = text[:end] + "\n[Codex output truncated]"
			} else {
				boundStrings(child)
			}
		}
	case []any:
		for i, child := range v {
			if text, ok := child.(string); ok && len(text) > limit {
				v[i] = "[Codex output truncated]"
			} else {
				boundStrings(child)
			}
		}
	}
}

// emit retains the latest immutable upsert on failure. Later events and idle
// cannot overtake it. Coalescing uses the same keys as the snapshot.
func (b *backend) emit(id, key, typ string, props any) {
	b.publicationMu.Lock()
	defer b.publicationMu.Unlock()
	if b.pending == nil {
		b.pending = map[string][]viewEntry{}
	}
	event := boundedEvent(id, key, typ, props)
	entries := b.pending[id]
	for i := range entries {
		if key != "" && entries[i].key == key {
			entries[i].event = event
			b.pending[id] = entries
			b.flush(id)
			return
		}
	}
	b.pending[id] = append(entries, viewEntry{key: key, event: event})
	b.flush(id)
}

// flush runs under publicationMu. Failed writes leave the immutable payload
// queued for the single recovery worker, with no per-failure goroutines.
func (b *backend) flush(id string) bool {
	for len(b.pending[id]) > 0 {
		entry := b.pending[id][0]
		if err := b.sup.Publish(entry.event, func() { b.apply(id, entry.key, entry.event) }); err != nil {
			return false
		}
		b.pending[id] = b.pending[id][1:]
	}
	delete(b.pending, id)
	return true
}

func (b *backend) settle(id string, status db.SessionStatus, admission uint64) {
	b.publicationMu.Lock()
	defer b.publicationMu.Unlock()
	b.mu.Lock()
	s := b.sessions[id]
	if s == nil || !s.running || (admission != 0 && s.accepted != admission) {
		b.mu.Unlock()
		return
	}
	admission = s.accepted
	b.mu.Unlock()
	if b.ends == nil {
		b.ends = map[string]turnEnd{}
	}
	b.ends[id] = turnEnd{admission: admission, status: status}
	b.finish(id)
}

func (b *backend) finish(id string) {
	if !b.flush(id) {
		return
	}
	end, ok := b.ends[id]
	if !ok {
		return
	}
	var events []providerprotocol.Event
	if end.status != db.StatusDone {
		props, _ := json.Marshal(map[string]any{"sessionID": id, "status": map[string]any{"type": end.status}})
		events = append(events, providerprotocol.Event{Type: "session.status", Properties: props})
	}
	props, _ := json.Marshal(map[string]any{"sessionID": id})
	events = append(events, providerprotocol.Event{Type: "session.idle", Properties: props})
	_, err := b.sup.PublishIf(func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		s := b.sessions[id]
		return s != nil && s.running && s.accepted == end.admission
	}, func(e providerprotocol.Event) { b.apply(id, "", e) }, events...)
	if err == nil {
		delete(b.ends, id)
	} else {
		log.WithError(err).WithField("sessionID", id).Warn("codex: retaining turn end for spool recovery")
	}
}

func (b *backend) recoverPublications() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-b.ctx.Done():
			return
		case <-ticker.C:
			b.publicationMu.Lock()
			for id := range b.pending {
				b.finish(id)
			}
			for id := range b.ends {
				b.finish(id)
			}
			b.publicationMu.Unlock()
		}
	}
}
