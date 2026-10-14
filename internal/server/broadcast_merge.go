package server

import "encoding/json"

// sessionChange is a decoded ocman.session.changed payload; patch is nil
// for an identity-only change (a refetch request).
type sessionChange struct {
	fields map[string]json.RawMessage // without "patch"
	patch  map[string]json.RawMessage
}

func decodeSessionChange(data []byte) *sessionChange {
	var c sessionChange
	if json.Unmarshal(data, &c.fields) != nil || c.fields == nil {
		return nil
	}
	if raw, ok := c.fields["patch"]; ok {
		if json.Unmarshal(raw, &c.patch) != nil {
			return nil
		}
		delete(c.fields, "patch")
	}
	return &c
}

// mergeSessionChanges folds an older pending session change into a newer
// one for a slow subscriber, so nothing the older one asked for is lost:
// two patches merge field by field (a status patch keeps a pending title),
// and if either side is identity-only the result stays identity-only, since
// that asks the consumer to refetch and a refetch subsumes any patch.
// Runs under the subscriber lock, so it only copies maps; the inputs are
// shared with other subscribers and never mutated.
func mergeSessionChanges(older, newer broadcastEvent) broadcastEvent {
	if older.change == nil || newer.change == nil {
		return newer
	}
	merged := &sessionChange{fields: newer.change.fields}
	if older.change.patch != nil && newer.change.patch != nil {
		merged.patch = make(map[string]json.RawMessage, len(older.change.patch)+len(newer.change.patch))
		for k, v := range older.change.patch {
			merged.patch[k] = v
		}
		for k, v := range newer.change.patch {
			merged.patch[k] = v
		}
	}
	newer.change, newer.data = merged, nil
	return newer
}

// encode fills data for a merged change. Called outside the subscriber lock.
func (ev *broadcastEvent) encode() {
	if ev.data != nil || ev.change == nil {
		return
	}
	out := make(map[string]json.RawMessage, len(ev.change.fields)+1)
	for k, v := range ev.change.fields {
		out[k] = v
	}
	if ev.change.patch != nil {
		patch, err := json.Marshal(ev.change.patch)
		if err != nil {
			return
		}
		out["patch"] = patch
	}
	ev.data, _ = json.Marshal(out)
}

// encodeAll re-encodes merged changes once the batch left the lock.
func encodeAll(batch []broadcastEvent) []broadcastEvent {
	for i := range batch {
		batch[i].encode()
	}
	return batch
}
