package statev2

import (
	"encoding/json"
	"slices"
	"time"
)

type RawHistoryRetentionMode int32

const (
	RawHistoryRetentionOff RawHistoryRetentionMode = iota
	RawHistoryRetentionReport
	RawHistoryRetentionApply
)

func (d *DB) SetRawHistoryRetentionMode(mode RawHistoryRetentionMode) {
	d.rawRetention.Store(int32(mode))
}

func (d *DB) RawHistoryRetentionMode() RawHistoryRetentionMode {
	return RawHistoryRetentionMode(d.rawRetention.Load())
}

func rawHistoryCutoff(now time.Time) int64 { return now.Add(-30 * 24 * time.Hour).UnixMilli() }

func expireRawMessages(messages []Message, cutoff int64) []Message {
	var changed []Message
	for i, m := range messages {
		if ext := expireRawHistory(m.Extensions, m.CreatedAt, cutoff); ext != nil {
			if changed == nil {
				changed = slices.Clone(messages)
			}
			changed[i].Extensions = ext
		}
	}
	if changed == nil {
		return messages
	}
	return changed
}

// expireRawHistory recognizes the OpenCode v2 envelope. Keep wrappers and
// all normalized fields: the conversation reader uses the wrapper's presence.
func expireRawHistory(ext json.RawMessage, createdAt, cutoff int64) json.RawMessage {
	if createdAt >= cutoff {
		return nil
	}
	var fields, native map[string]json.RawMessage
	if json.Unmarshal(ext, &fields) != nil || len(fields["metadata"]) == 0 ||
		json.Unmarshal(fields["native"], &native) != nil || len(native["Data"]) == 0 {
		return nil
	}
	var kind string
	if json.Unmarshal(native["Type"], &kind) != nil || kind == "" {
		return nil
	}
	var updatedAt int64
	if updated := native["UpdatedAt"]; len(updated) > 0 && json.Unmarshal(updated, &updatedAt) != nil {
		return nil
	}
	if ageAt := max(createdAt, updatedAt); ageAt <= 0 || ageAt >= cutoff {
		return nil
	}
	delete(native, "Data")
	fields["native"], _ = json.Marshal(native)
	result, _ := json.Marshal(fields)
	return result
}
