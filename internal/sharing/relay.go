package sharing

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/share"
	"github.com/ocman/ocman/internal/state"
)

type relayChunk struct {
	Session  *db.Session  `json:"session,omitempty"`
	Messages []db.Message `json:"messages"`
	Parts    []db.Part    `json:"parts"`
	ReadOnly bool         `json:"readOnly"`
}

// createRelayShare allocates a blind relay record and uploads a complete
// encrypted snapshot as chunk zero. If any step fails the local share is
// left local-only rather than returning a dead cross-machine URL.
func (m *Module) createRelayShare(ctx context.Context, link state.ShareLink, adapter platforms.Platform) (state.ShareLink, error) {
	if m.RelayURL() == "" {
		return link, nil
	}
	client := share.NewRelayClient(m.RelayURL())
	allocation, err := client.Create(ctx)
	if err != nil {
		return link, err
	}
	key := share.NewKey()
	detail, err := adapter.Session(ctx, link.SessionID, exportFetchLimit, 0)
	if err != nil {
		_ = client.Delete(ctx, allocation)
		return link, err
	}
	chunks, err := splitShareSnapshot(
		detail.Session,
		detail.Messages,
		truncateShareParts(detail.Parts, sharePartTextLimit),
		shareChunkBudget(allocation.MaxChunkBytes),
	)
	if err != nil {
		_ = client.Delete(ctx, allocation)
		return link, err
	}
	lastSeq, err := m.uploadChunks(ctx, client, allocation, key, 0, chunks)
	if err != nil {
		_ = client.Delete(ctx, allocation)
		return link, err
	}
	if err := m.Store.SetShareRelay(ctx, link.Token, m.RelayURL(), allocation.ID, key.String(), allocation.DeleteToken); err != nil {
		_ = client.Delete(ctx, allocation)
		return link, err
	}
	if err := m.Store.SetShareRelaySeq(ctx, link.Token, lastSeq); err != nil {
		return link, err
	}
	link.RelayURL = m.RelayURL()
	link.RelayID = allocation.ID
	link.RelayKey = key.String()
	link.RelayDeleteToken = allocation.DeleteToken
	link.RelayLastSeq = lastSeq
	return link, nil
}

// uploadChunks seals and uploads chunks starting at firstSeq, returning
// the last sequence number written.
func (m *Module) uploadChunks(
	ctx context.Context,
	client share.RelayClient,
	allocation share.RelayAllocation,
	key share.Key,
	firstSeq uint64,
	chunks []relayChunk,
) (int64, error) {
	if allocation.MaxChunks > 0 && (firstSeq >= uint64(allocation.MaxChunks) || len(chunks) > allocation.MaxChunks-int(firstSeq)) {
		return int64(firstSeq) - 1, share.TooLarge("share has too many chunks")
	}

	lastSeq := int64(firstSeq) - 1
	var totalBytes int64
	for i, chunk := range chunks {
		payload, err := json.Marshal(chunk)
		if err != nil {
			return lastSeq, fmt.Errorf("encoding relay chunk: %w", err)
		}
		seq := firstSeq + uint64(i)
		sealed, err := share.Seal(key, allocation.ID, seq, payload)
		if err != nil {
			return lastSeq, err
		}
		if allocation.MaxChunkBytes > 0 && int64(len(sealed)) > allocation.MaxChunkBytes {
			return lastSeq, share.TooLarge("chunk is too large after encryption")
		}
		totalBytes += int64(len(sealed))
		if allocation.MaxShareBytes > 0 && totalBytes > allocation.MaxShareBytes {
			return lastSeq, share.TooLarge("share is too large")
		}
		if err := client.Put(ctx, allocation, seq, sealed); err != nil {
			return lastSeq, err
		}
		lastSeq = int64(seq)
	}
	return lastSeq, nil
}

// PublishCompletedTurn appends only the latest completed user/assistant
// turn to each active relay share. Rows are upserts in the viewer, so the
// immutable completed turn is stored once and no streaming part rewrites
// or compaction are needed.
func (m *Module) PublishCompletedTurn(ctx context.Context, adapter platforms.Platform, sessionID string) error {
	links, err := m.Store.ListActiveShareLinks(ctx, string(adapter.ID()), sessionID)
	if err != nil {
		return err
	}
	if len(links) == 0 {
		// Unshared sessions need no transcript, including deleted helpers.
		return nil
	}
	detail, err := adapter.Session(ctx, sessionID, exportFetchLimit, 0)
	if err != nil {
		return err
	}
	turn := latestCompletedTurn(detail.Session, detail.Messages, truncateShareParts(detail.Parts, sharePartTextLimit))

	for _, link := range links {
		if link.RelayID == "" {
			continue
		}
		key, err := share.ParseKey(link.RelayKey)
		if err != nil {
			return err
		}
		// A single turn can exceed the chunk limit on its own (many tool
		// calls), so it is split the same way the initial snapshot is.
		chunks, err := splitShareSnapshot(turn.Session, turn.Messages, turn.Parts, shareChunkBudget(0))
		if err != nil {
			return err
		}
		allocation := share.RelayAllocation{
			ID: link.RelayID, DeleteToken: link.RelayDeleteToken,
			// Existing shares predate negotiated limits; retain the relay
			// defaults as a safe fallback until they are recreated.
			MaxChunkBytes: 1 << 20,
		}
		lastSeq, err := m.uploadChunks(ctx, share.NewRelayClient(link.RelayURL), allocation, key, uint64(link.RelayLastSeq+1), chunks)
		if err != nil {
			return err
		}
		if lastSeq > link.RelayLastSeq {
			if err := m.Store.SetShareRelaySeq(ctx, link.Token, lastSeq); err != nil {
				return err
			}
		}
	}
	return nil
}

func latestCompletedTurn(session *db.Session, messages []db.Message, parts []db.Part) relayChunk {
	if len(messages) == 0 {
		return relayChunk{Session: session, Messages: []db.Message{}, Parts: []db.Part{}, ReadOnly: true}
	}
	start := len(messages) - 1
	for start > 0 {
		var data db.MessageData
		if json.Unmarshal(messages[start].Data, &data) == nil && data.Role == "user" {
			break
		}
		start--
	}
	selected := messages[start:]
	ids := make(map[string]bool, len(selected))
	for _, message := range selected {
		ids[message.ID] = true
	}
	selectedParts := make([]db.Part, 0)
	for _, part := range parts {
		if ids[part.MessageID] {
			selectedParts = append(selectedParts, part)
		}
	}
	return relayChunk{Session: session, Messages: selected, Parts: selectedParts, ReadOnly: true}
}
