package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/share"
	"github.com/NoUseFreak/ocman/internal/state"
)

const relayRequestTimeout = 30 * time.Second

type relayChunk struct {
	Session  *db.Session  `json:"session,omitempty"`
	Messages []db.Message `json:"messages"`
	Parts    []db.Part    `json:"parts"`
	ReadOnly bool         `json:"readOnly"`
}

func (s *Server) relayClient(baseURL string) share.RelayClient {
	return share.RelayClient{BaseURL: baseURL, HTTP: &http.Client{Timeout: relayRequestTimeout}}
}

// createRelayShare allocates a blind relay record and uploads a complete
// encrypted snapshot as chunk zero. If any step fails the local share is
// left local-only rather than returning a dead cross-machine URL.
func (s *Server) createRelayShare(ctx context.Context, link state.ShareLink, adapter platforms.Platform) (state.ShareLink, error) {
	if s.relayURL == "" {
		return link, nil
	}
	client := s.relayClient(s.relayURL)
	allocation, err := client.Create(ctx)
	if err != nil {
		return link, err
	}
	key, err := share.NewKey()
	if err != nil {
		_ = client.Delete(ctx, allocation)
		return link, err
	}
	detail, err := adapter.Session(ctx, link.SessionID, exportFetchLimit, 0)
	if err != nil {
		_ = client.Delete(ctx, allocation)
		return link, err
	}
	payload, err := json.Marshal(relayChunk{Session: detail.Session, Messages: detail.Messages, Parts: detail.Parts, ReadOnly: true})
	if err != nil {
		_ = client.Delete(ctx, allocation)
		return link, fmt.Errorf("encoding relay snapshot: %w", err)
	}
	sealed, err := share.Seal(key, allocation.ID, 0, payload)
	if err != nil {
		_ = client.Delete(ctx, allocation)
		return link, err
	}
	if err := client.Put(ctx, allocation, 0, sealed); err != nil {
		_ = client.Delete(ctx, allocation)
		return link, err
	}
	if err := s.stateDB.SetShareRelay(link.Token, s.relayURL, allocation.ID, key.String(), allocation.DeleteToken); err != nil {
		_ = client.Delete(ctx, allocation)
		return link, err
	}
	if err := s.stateDB.SetShareRelaySeq(link.Token, 0); err != nil {
		return link, err
	}
	link.RelayURL = s.relayURL
	link.RelayID = allocation.ID
	link.RelayKey = key.String()
	link.RelayDeleteToken = allocation.DeleteToken
	link.RelayLastSeq = 0
	return link, nil
}

// publishCompletedTurn appends only the latest completed user/assistant
// turn to each active relay share. Rows are upserts in the viewer, so the
// immutable completed turn is stored once and no streaming part rewrites
// or compaction are needed.
func (s *Server) publishCompletedTurn(ctx context.Context, adapter platforms.Platform, sessionID string) error {
	links, err := s.stateDB.ListActiveShareLinks(string(adapter.ID()), sessionID)
	if err != nil {
		return err
	}
	detail, err := adapter.Session(ctx, sessionID, exportFetchLimit, 0)
	if err != nil {
		return err
	}
	chunk := latestCompletedTurn(detail.Session, detail.Messages, detail.Parts)
	payload, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	for _, link := range links {
		if link.RelayID == "" {
			continue
		}
		key, err := share.ParseKey(link.RelayKey)
		if err != nil {
			return err
		}
		seq := uint64(link.RelayLastSeq + 1)
		sealed, err := share.Seal(key, link.RelayID, seq, payload)
		if err != nil {
			return err
		}
		allocation := share.RelayAllocation{ID: link.RelayID, DeleteToken: link.RelayDeleteToken}
		if err := s.relayClient(link.RelayURL).Put(ctx, allocation, seq, sealed); err != nil {
			return err
		}
		if err := s.stateDB.SetShareRelaySeq(link.Token, int64(seq)); err != nil {
			return err
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
