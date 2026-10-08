package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

// Runs on the owner, before stopping either a project or machine-wide server.
func (s *Server) recordOpencodeReplacement(ctx context.Context, root, reason string) error {
	adapter, ok := s.registry.Get("opencode")
	if !ok || s.stateDB == nil {
		return nil
	}
	// AfterStop persisted closure before reconciliation failed. Retrying must
	// not overwrite that stopped attempt with the now-settled live view.
	stopped, err := s.stateDB.ReplacementStopped(ctx, "opencode", root)
	if err != nil || stopped {
		return err
	}
	if pending, err := s.opencodeReplacementStopping(ctx, root); err != nil {
		return err
	} else if pending != nil {
		return state.ErrReplacementStopping
	}
	var sessions []db.Session
	if s.db != nil {
		// The display list can omit inactive children or newly-created sessions.
		sessions, err = s.db.GetSessionIdentities(ctx)
	} else {
		sessions, err = adapter.Sessions(ctx, "", 0)
	}
	if err != nil {
		return err
	}
	members, err := s.replacementMembership(ctx, root, sessions)
	if err != nil {
		return err
	}
	pending := make(map[string]state.SessionInterruption)
	at := time.Now().UnixMilli()
	for _, session := range sessions {
		if !members[session.Directory] {
			continue
		}
		lifecycle, err := interruptionLifecycle(ctx, adapter, session.ID)
		if errors.Is(err, platforms.ErrNotFound) {
			continue // A transient judge/session can disappear during enumeration.
		}
		if err != nil {
			return fmt.Errorf("reading interruption lifecycle for %q: %w", session.ID, err)
		}
		if lifecycle == nil {
			continue
		}
		text := "The server handling this unfinished turn was confirmed stopped during replacement (" + reason + "). Send a follow-up to continue."
		pending[session.ID] = state.SessionInterruption{MessageID: lifecycle.LatestMessageID, ObservedAt: at, Message: text, BaselineStatus: string(lifecycle.Status), BaselineMessageCreated: lifecycle.LatestMessageCreated}
	}
	if err := s.stateDB.PrepareSessionInterruptions(ctx, "opencode", root, pending, members); err != nil {
		return fmt.Errorf("preparing interruption history: %w", err)
	}
	return nil
}

// Always read the latest owner-local turn, independently of a history page.
func interruptionLifecycle(ctx context.Context, adapter platforms.Platform, sessionID string) (*platforms.SessionLifecycle, error) {
	if reader, ok := adapter.(platforms.LifecycleReader); ok {
		lifecycle, err := reader.SessionLifecycle(ctx, sessionID)
		if !errors.Is(err, platforms.ErrUnsupported) {
			return lifecycle, err
		}
	}
	detail, err := adapter.Session(ctx, sessionID, 1, 0)
	if err != nil || detail == nil || detail.Session == nil {
		return nil, err
	}
	return &platforms.SessionLifecycle{Status: detail.Session.Status, LatestMessageID: latestHistoryMessage(detail.Messages)}, nil
}

func latestHistoryMessage(messages []db.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		var data struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(messages[i].Data, &data) == nil && (data.Role == "assistant" || data.Role == "user") {
			return messages[i].ID
		}
	}
	return ""
}

func (s *Server) enrichInterruptionHistory(ctx context.Context, platform, sessionID string, detail *platforms.SessionDetail) {
	// A dead process may never save an error. Keep the first observation durable.
	if detail.Session != nil && detail.Session.Status == db.StatusInterrupted {
		if adapter, ok := s.registry.Get(platforms.ID(platform)); ok {
			lifecycle, err := interruptionLifecycle(ctx, adapter, sessionID)
			if err != nil {
				log.WithError(err).Warn("reading interrupted session lifecycle")
			} else if lifecycle != nil && lifecycle.Status == db.StatusInterrupted && lifecycle.LatestMessageID != "" {
				err := s.stateDB.RecordSessionInterruption(ctx, platform, sessionID, state.SessionInterruption{
					MessageID: lifecycle.LatestMessageID, ObservedAt: time.Now().UnixMilli(),
					Message: "This unfinished turn was interrupted: ocman no longer has a live agent connection. Send a follow-up to continue.",
				})
				if err != nil {
					log.WithError(err).Warn("persisting session interruption")
				}
			}
		}
	}
	notices, err := s.stateDB.SessionInterruptions(ctx, platform, sessionID)
	if err != nil {
		log.WithError(err).Warn("loading session interruptions")
		return
	}
	for _, notice := range notices {
		data, _ := json.Marshal(map[string]string{"type": "text", "text": notice.Message})
		injectHistoryNotice(sessionID, "ocman-interruption-"+notice.MessageID, notice.ObservedAt, data, &detail.Messages, &detail.Parts)
	}
}
