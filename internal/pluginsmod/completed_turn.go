package pluginsmod

import (
	"context"
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
)

// completedTurnRetries bound how long a finished turn waits for its stored
// transcript before only the reconciliation sweep is left to record it.
var completedTurnRetries = []time.Duration{
	250 * time.Millisecond, 500 * time.Millisecond, time.Second,
	2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
}

// completedTurnAttemptTimeout bounds one attempt, including its history
// refresh (platforms.StoredHistoryWait) and the reread after it.
const completedTurnAttemptTimeout = 15 * time.Second

type completedTurnKey struct{ kind, platform, session string }

// completedTurnRetry is one pending schedule. rearm restarts its backoff when
// another edge arrives for the session while it is pending.
type completedTurnRetry struct{ rearm bool }

// readCompletedTurn reads the transcript of a turn the live lifecycle reports
// finished with message want. Stored history is ingested from a separate
// provider stream, so the idle edge can arrive before it holds that turn. A
// stale read requests one read-your-writes refresh and reads again; behind
// reports a transcript that still trails the live turn. An empty want (no live
// lifecycle) accepts the transcript as read.
func readCompletedTurn(ctx context.Context, adapter platforms.Platform, sessionID, want string) (*platforms.SessionDetail, bool, error) {
	detail, err := adapter.Session(ctx, sessionID, conversationFetchLimit, 0)
	if err != nil || want == "" || storedTurnCurrent(detail, want) {
		return detail, false, err
	}
	refresher, ok := adapter.(platforms.SessionCacheMaintainer)
	if !ok {
		return detail, true, nil
	}
	if err := refresher.RefreshSession(ctx, sessionID); err != nil && ctx.Err() == nil {
		log.WithError(err).WithField("session_id", sessionID).Debug("refreshing stored history for a completed turn")
	}
	detail, err = adapter.Session(ctx, sessionID, conversationFetchLimit, 0)
	if err != nil {
		return nil, false, err
	}
	return detail, !storedTurnCurrent(detail, want), nil
}

// storedTurnCurrent reports whether the transcript holds message want as a
// finished message. A live idle edge can settle the stored status while the
// stored message is still the streamed prefix, so status alone is not enough.
func storedTurnCurrent(detail *platforms.SessionDetail, want string) bool {
	if detail == nil || detail.Session == nil || !conversationReplyReady(detail.Session.Status) {
		return false
	}
	for _, message := range detail.Messages {
		if message.ID == want {
			return storedMessageFinished(message)
		}
	}
	return false
}

// storedMessageFinished mirrors the lifecycle inference: a finish reason or an
// error ends a message; a completion time ends a synthesized envelope.
func storedMessageFinished(message db.Message) bool {
	var data struct {
		Finish string          `json:"finish"`
		Error  json.RawMessage `json:"error"`
		Time   struct {
			Completed int64 `json:"completed"`
		} `json:"time"`
	}
	if json.Unmarshal(message.Data, &data) != nil {
		return false
	}
	return data.Finish != "" || (len(data.Error) > 0 && string(data.Error) != "null") || data.Time.Completed > 0
}

// retryCompletedTurn re-runs reply with backoff while stored history trails a
// finished turn, one schedule per session and kind. Nothing else re-drives a
// completion once ingestion catches up, so without this the turn would wait
// for the reconciliation sweep. An edge arriving while a schedule is pending
// restarts its backoff instead of starting a second one.
func (m *Module) retryCompletedTurn(kind, platformID, sessionID string, reply func(context.Context, string, string) bool) {
	key := completedTurnKey{kind, platformID, sessionID}
	m.turnMu.Lock()
	if pending := m.turnRetries[key]; pending != nil {
		pending.rearm = true
		m.turnMu.Unlock()
		return
	}
	if m.turnRetries == nil {
		m.turnRetries = map[completedTurnKey]*completedTurnRetry{}
	}
	retry := &completedTurnRetry{}
	m.turnRetries[key] = retry
	m.turnMu.Unlock()
	started := m.workers.Go(func(ctx context.Context) {
		defer m.endCompletedTurnRetry(key, retry)
		for attempt := 0; ; attempt++ {
			if attempt == len(completedTurnRetries) {
				if !m.rearmCompletedTurnRetry(key, retry, true) {
					log.WithFields(log.Fields{"kind": kind, "platform": platformID, "session_id": sessionID}).
						Warn("stored history still trails a completed turn; leaving it to reconciliation")
					return
				}
				attempt = 0
			}
			timer := time.NewTimer(completedTurnRetries[attempt])
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			behind := true
			module.RunWithRecover("plugin-"+kind+"-completed-turn-retry", func() {
				attemptCtx, cancel := context.WithTimeout(ctx, completedTurnAttemptTimeout)
				defer cancel()
				behind = reply(attemptCtx, platformID, sessionID)
			})
			if m.rearmCompletedTurnRetry(key, retry, !behind) {
				attempt = -1
			} else if !behind {
				return
			}
		}
	})
	if !started {
		m.endCompletedTurnRetry(key, retry)
	}
}

// rearmCompletedTurnRetry consumes a pending restart request. With finish set
// and no request pending, it also ends the schedule in the same critical
// section, so a request cannot land on a schedule that is about to exit.
func (m *Module) rearmCompletedTurnRetry(key completedTurnKey, retry *completedTurnRetry, finish bool) bool {
	m.turnMu.Lock()
	defer m.turnMu.Unlock()
	if retry.rearm {
		retry.rearm = false
		return true
	}
	if finish && m.turnRetries[key] == retry {
		delete(m.turnRetries, key)
	}
	return false
}

func (m *Module) endCompletedTurnRetry(key completedTurnKey, retry *completedTurnRetry) {
	m.turnMu.Lock()
	if m.turnRetries[key] == retry {
		delete(m.turnRetries, key)
	}
	m.turnMu.Unlock()
}
