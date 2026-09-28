package server

import (
	"context"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

// continuationPrompt resumes a turn that died on a quota wall. OpenCode
// drops the failed turn from the model-facing context but its file
// writes stay on disk, so the inspect-first instruction is what keeps
// the replacement model from duplicating work it cannot see.
const continuationPrompt = "The previous attempt at this task was interrupted before it finished. " +
	"Files it already changed are still on disk, but its work is not in your context. " +
	"Before redoing any work, inspect the current state of the working tree " +
	"(for example `git status` and `git diff`), then complete the task."

// switchNoticeFor is how long a model-switch notice stays attached.
const switchNoticeFor = 10 * time.Minute

type fallNotice struct {
	notice db.SessionNotice
	until  time.Time
}

func fallKey(platformID, sessionID string) string { return platformID + "\x00" + sessionID }

// continueSession sends the continuation prompt after provider cooled
// down under the session. It reports whether it handled the idle edge,
// in which case the follow-up queue must not also drain on it: a held
// message waits for the continuation's own turn to end. The walk is
// bounded by the cooldown floor: every failure cools its provider, so
// each model is tried at most once before the list is exhausted.
func (s *Server) continueSession(ctx context.Context, platformID, sessionID, provider string) bool {
	if s.sessions == nil || provider == "" {
		return false
	}
	model, recovers, ok := s.sessions.Fallthrough(ctx, platformID, sessionID)
	if !ok {
		return false
	}
	if model == "" {
		s.setExhaustedNotice(platformID, sessionID, recovers)
		return true
	}
	if err := s.sendNow(ctx, platformID, platforms.SendMessageRequest{SessionID: sessionID, Message: continuationPrompt, Model: model}); err != nil {
		log.WithError(err).WithField("sessionID", sessionID).Warn("continuing session on next model")
		return false
	}
	s.setFallNotice(platformID, sessionID, db.SessionNotice{
		Kind:    "model_switch",
		Message: fmt.Sprintf("Switched to %s: %s ran out of quota", model, provider),
	}, time.Now().Add(switchNoticeFor))
	return true
}

// abortParkedRetry stops a turn OpenCode parked on a cooled provider so
// the idle edge can continue it on the next model. Only one abort per
// park: the marker is consumed by that idle edge.
func (s *Server) abortParkedRetry(ctx context.Context, sessionID, provider string) {
	platformID := string(opencode.PlatformID)
	key := fallKey(platformID, sessionID)
	if s.sessions == nil || provider == "" {
		return
	}
	if _, parked := s.fallAborted.LoadOrStore(key, provider); parked {
		return
	}
	model, recovers, ok := s.sessions.Fallthrough(ctx, platformID, sessionID)
	if ok && model == "" {
		s.setExhaustedNotice(platformID, sessionID, recovers)
	}
	if !ok || model == "" {
		s.fallAborted.Delete(key)
		return
	}
	if err := s.sessions.Abort(ctx, platformID, platforms.AbortRequest{SessionID: sessionID}); err != nil {
		s.fallAborted.Delete(key)
		log.WithError(err).WithField("sessionID", sessionID).Warn("aborting parked retry")
	}
}

func (s *Server) setExhaustedNotice(platformID, sessionID string, recovers time.Time) {
	s.setFallNotice(platformID, sessionID, db.SessionNotice{
		Kind:    "models_exhausted",
		Message: "Every model in the project list is cooled down; nothing was sent",
		RetryAt: recovers.UnixMilli(),
	}, recovers)
}

func (s *Server) setFallNotice(platformID, sessionID string, n db.SessionNotice, until time.Time) {
	// ponytail: one entry per switched session, overwritten, never evicted.
	s.fallNotices.Store(fallKey(platformID, sessionID), fallNotice{notice: n, until: until})
	s.broadcastSessionChanged(sessionID)
}

// applyFallNotice replaces a session's notice with a live fallthrough one.
func (s *Server) applyFallNotice(platformID string, sess *db.Session) {
	v, ok := s.fallNotices.Load(fallKey(platformID, sess.ID))
	if !ok {
		return
	}
	if fn := v.(fallNotice); time.Now().Before(fn.until) {
		n := fn.notice
		sess.Notice = &n
	}
}
