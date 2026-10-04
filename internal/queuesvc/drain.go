package queuesvc

import (
	"context"
	"errors"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// completionState is the lifecycle read already made by a guarded sweep.
// The guard generation must still match before it can be used for draining.
type completionState struct {
	messageID string
	createdAt int64
	running   bool
	completed bool
	ok        bool
}

// Flush sends the single oldest queued message on an authoritative idle edge.
// Exactly one message is sent per call. On a send error the head stays queued
// for a later edge. platformID is required: session IDs are owner-scoped.
func (s *Service) Flush(ctx context.Context, platformID, sessionID string) {
	key := sessionKey{Platform: platformID, SessionID: sessionID}
	unlock := s.lockFor(key)
	defer unlock()
	s.clearDrainedSinceIdle(key)
	// Trust the edge regardless of session.status event ordering or a failed
	// status read. Otherwise the queue may be stranded with no second edge.
	s.drainHead(ctx, key, true, nil)
}

// drainHead sends the oldest queued message. The caller holds the session
// lock. Flush trusts its idle edge; enqueue and Sweep gate on turn status.
// A guarded sweep passes its existing read after checking guard generation.
func (s *Service) drainHead(ctx context.Context, key sessionKey, trustIdle bool, prior *completionState) {
	sessionID := key.SessionID
	completion, hasCompletion := s.status.(completionInferer)
	// LatestMessageState reports running too, so it is the only status read
	// when available. Older status readers still gate on TurnRunning.
	if !trustIdle && !hasCompletion {
		if running, ok := s.status.TurnRunning(ctx, key.Platform, sessionID); ok && running {
			return
		}
	}

	head, err := s.store.HeadQueuedMessage(ctx, key.Platform, sessionID)
	if err != nil {
		log.WithError(err).WithField("sessionID", sessionID).
			Warn("queuesvc: reading queue head")
		return
	}
	if head == nil {
		return
	}

	var latest completionState
	if hasCompletion {
		if prior != nil {
			latest = *prior
		} else {
			latest.messageID, latest.createdAt, latest.running, latest.completed, latest.ok = completion.LatestMessageState(ctx, head.Platform, sessionID)
		}
		if !trustIdle && (!latest.ok || latest.running) {
			return
		}
	}
	req := platforms.SendMessageRequest{
		SessionID: head.SessionID,
		Message:   head.Text,
		Images:    decodeImages(head.ImagesJSON),
		Model:     head.Model,
		Agent:     head.Agent,
		Reasoning: head.Reasoning,
	}
	if err := s.sender.SendNow(ctx, head.Platform, req); err != nil {
		if errors.Is(err, ErrDeferred) {
			return // ordering wait, not a delivery failure
		}
		// Count failures so an unsendable head cannot block every later
		// message forever. The head remains queued for retry until set aside.
		blocked, recErr := s.store.RecordQueuedMessageFailure(ctx, head.ID, err.Error())
		if recErr != nil {
			log.WithError(recErr).WithField("messageID", head.ID).
				Warn("queuesvc: recording send failure")
		}
		entry := log.WithError(err).WithField("sessionID", sessionID)
		if blocked {
			entry.WithField("messageID", head.ID).
				Error("queuesvc: queued message set aside after repeated send failures")
			s.fireNotify(ctx, key)
		} else {
			entry.Warn("queuesvc: sending queued message")
		}
		return
	}
	if _, err := s.store.DeleteQueuedMessage(ctx, head.ID); err != nil {
		log.WithError(err).WithField("messageID", head.ID).
			Warn("queuesvc: dequeuing sent message")
		return
	}
	// This send started a turn; block the enqueue fast-path until a real
	// idle edge or a newer completed assistant message confirms it finished.
	s.markDrained(key, latest.messageID, latest.createdAt)
	s.fireNotify(ctx, key)
}
