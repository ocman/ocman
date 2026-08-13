package server

import (
	"encoding/json"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
)

// sharePartTextLimit is the longest string kept inside a shared part.
//
// Tool output dominates the byte count of a coding conversation (file
// reads, diffs, bash output), and the shared viewer already treats it as
// secondary detail. Truncating it is also a privacy measure: tool output
// is exactly where secrets leak into a transcript, so a shared link
// discloses far less with a cap than without one.
const sharePartTextLimit = 8 << 10

// shareChunkSafetyMargin leaves room for the AEAD tag and any encoding
// overhead so a chunk sized against the relay's limit still fits it.
const shareChunkSafetyMargin = 4 << 10

// truncationMarker is appended to a shortened string.
const truncationMarker = "\n\n[ocman: truncated for sharing, %d bytes elided]"

// truncateShareParts returns parts with over-long strings shortened.
//
// The structure is preserved — type, tool name, status, file path all
// survive — because the viewer renders from those fields; only long
// string values are cut, so a part still displays as itself.
func truncateShareParts(parts []db.Part, limit int) []db.Part {
	out := make([]db.Part, len(parts))
	for i, part := range parts {
		out[i] = part
		trimmed, err := truncateJSONStrings(part.Data, limit)
		if err != nil {
			// Unparseable payloads are passed through untouched: the
			// size guard below still bounds the upload, and mangling
			// opaque data would be worse than shipping it.
			continue
		}
		out[i].Data = trimmed
	}
	return out
}

// truncateJSONStrings shortens every string in a JSON document that is
// longer than limit, preserving the document's shape.
func truncateJSONStrings(raw json.RawMessage, limit int) (json.RawMessage, error) {
	if len(raw) == 0 {
		return raw, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(truncateValue(decoded, limit))
}

// truncateValue walks a decoded JSON value, shortening long strings.
func truncateValue(value any, limit int) any {
	switch typed := value.(type) {
	case string:
		if len(typed) <= limit {
			return typed
		}
		return typed[:limit] + fmt.Sprintf(truncationMarker, len(typed)-limit)
	case map[string]any:
		for key, nested := range typed {
			typed[key] = truncateValue(nested, limit)
		}
		return typed
	case []any:
		for i, nested := range typed {
			typed[i] = truncateValue(nested, limit)
		}
		return typed
	default:
		return value
	}
}

// splitShareSnapshot breaks a conversation into chunks that each fit the
// relay's per-chunk limit.
//
// Truncation alone cannot guarantee a fit: a long enough conversation
// exceeds any single-chunk budget on row count alone. Splitting works
// because every chunk is an upsert — the viewer merges them by id, so a
// snapshot spread over several chunks reconstructs exactly the same
// state as one oversized chunk would have.
//
// Chunk 0 always carries the session so a reader has it immediately.
func splitShareSnapshot(session *db.Session, messages []db.Message, parts []db.Part, budget int) ([]relayChunk, error) {
	if budget <= 0 {
		return nil, fmt.Errorf("share snapshot: non-positive chunk budget %d", budget)
	}

	current := relayChunk{Session: session, Messages: []db.Message{}, Parts: []db.Part{}, ReadOnly: true}
	used, err := jsonSize(current)
	if err != nil {
		return nil, err
	}
	chunks := []relayChunk{}

	flush := func() error {
		chunks = append(chunks, current)
		current = relayChunk{Messages: []db.Message{}, Parts: []db.Part{}, ReadOnly: true}
		size, err := jsonSize(current)
		if err != nil {
			return err
		}
		used = size
		return nil
	}

	for _, message := range messages {
		size, err := jsonSize(message)
		if err != nil {
			return nil, err
		}
		if used+size > budget && (len(current.Messages) > 0 || len(current.Parts) > 0) {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		current.Messages = append(current.Messages, message)
		used += size
	}

	for _, part := range parts {
		size, err := jsonSize(part)
		if err != nil {
			return nil, err
		}
		if used+size > budget && (len(current.Messages) > 0 || len(current.Parts) > 0) {
			if err := flush(); err != nil {
				return nil, err
			}
		}
		current.Parts = append(current.Parts, part)
		used += size
	}

	if err := flush(); err != nil {
		return nil, err
	}
	return chunks, nil
}

// jsonSize reports the encoded byte length of a value, plus a comma.
func jsonSize(value any) (int, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return 0, fmt.Errorf("share snapshot: measuring payload: %w", err)
	}
	return len(encoded) + 1, nil
}

// shareChunkBudget is the plaintext budget for one chunk, derived from
// the relay's advertised limit rather than a hardcoded assumption.
func shareChunkBudget(maxChunkBytes int64) int {
	if maxChunkBytes <= 0 {
		maxChunkBytes = 1 << 20
	}
	budget := int(maxChunkBytes) - shareChunkSafetyMargin
	if budget < 16<<10 {
		budget = 16 << 10
	}
	return budget
}
