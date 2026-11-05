package server

import (
	"context"
	"encoding/json"

	"github.com/ocman/ocman/internal/platforms"
)

// StreamRemoteSessionChanges exposes invalidations, never conversation data or
// another owner's events. The broadcast hub coalesces slow consumers without
// blocking persistence callers, including the PR workflow mutex.
func (s *Server) StreamRemoteSessionChanges(ctx context.Context, send func(string, string) error) error {
	sub, unsub := s.broadcastHub.subscribe()
	defer unsub()
	if err := send("", ""); err != nil {
		return err
	}
	forward := func(ev broadcastEvent) error {
		if ev.event != "ocman.session.changed" {
			return nil
		}
		var change struct {
			Platform  string `json:"platform"`
			SessionID string `json:"sessionID"`
		}
		if json.Unmarshal(ev.data, &change) != nil || change.Platform == "" || change.SessionID == "" {
			return nil
		}
		if owner, _ := platforms.SplitPlatformID(change.Platform); owner != "" {
			return nil
		}
		return send(change.Platform, change.SessionID)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-sub.ch:
			if err := forward(ev); err != nil {
				return err
			}
		case <-sub.wake:
			for _, ev := range sub.takeBatch() {
				if err := forward(ev); err != nil {
					return err
				}
			}
		}
	}
}
