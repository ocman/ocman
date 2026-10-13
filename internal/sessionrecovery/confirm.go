package sessionrecovery

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

func replacementCandidates(sessions []db.Session, baseline ...map[string]state.SessionInterruption) []state.ReplacementCandidate {
	candidates := make([]state.ReplacementCandidate, 0, len(sessions))
	for _, session := range sessions {
		candidate := state.ReplacementCandidate{SessionID: session.ID, Directory: session.Directory}
		if len(baseline) > 0 {
			if prior, found := baseline[0][session.ID]; found {
				candidate.Prior = &prior
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

// Closure is durable before any reads. Completed read batches are checkpointed under
// a short independent context, so expiration of the host's confirmation budget
// cannot discard work. History is published only when the queue is drained.
func (s *Service) ConfirmOpencodeReplacement(ctx context.Context, root string) error {
	if s.deps.State == nil {
		return nil
	}
	if err := s.deps.State.MarkReplacementStopped(ctx, "opencode", root); err != nil {
		return err
	}
	stopped, err := s.deps.State.ReplacementStopped(ctx, "opencode", root)
	if err != nil || !stopped {
		return err
	}
	adapter, ok := s.deps.Platform("opencode")
	if !ok {
		return nil
	}
	progress, err := s.deps.State.ReplacementReconciliationStatus(ctx, "opencode", root)
	if err != nil {
		return err
	}
	if !progress.Initialized {
		var sessions []db.Session
		if s.deps.Sessions != nil {
			sessions, err = s.deps.Sessions.GetSessionIdentities(ctx)
		} else {
			sessions, err = adapter.Sessions(ctx, "", 0)
		}
		if err != nil {
			return err
		}
		var baseline map[string]state.SessionInterruption
		// Legacy stopped attempts have no seeded queue. Decode their original
		// baseline once; modern attempts already carry it on candidate rows.
		if !progress.Seeded {
			if progress.StopStartedAt > 0 {
				_, baseline, err = s.deps.State.SessionReplacementStopEvidence(ctx, "opencode", root)
			} else {
				baseline, err = s.deps.State.PreparedSessionInterruptions(ctx, "opencode", root)
			}
			if err != nil {
				return err
			}
		}
		if err := s.deps.State.InitializeReplacementReconciliation(ctx, "opencode", root, replacementCandidates(sessions, baseline)); err != nil {
			return err
		}
	}
	// Drain under the host's bounded context, including the final empty check.
	// If its deadline expires, saved work resumes without another Stop.
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		batch, err := s.deps.State.ReplacementReconciliationBatch(ctx, "opencode", root, 64)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			ids, err := s.deps.State.PublishReplacementReconciliation(publishCtx, "opencode", root)
			cancel()
			if err != nil {
				return err
			}
			for _, id := range ids {
				s.deps.BroadcastSessionChanged(id)
			}
			return nil
		}
		if err := s.confirmReplacementBatch(ctx, root, adapter, batch, progress); err != nil {
			return err
		}
	}
}

func (s *Service) confirmReplacementBatch(ctx context.Context, root string, adapter platforms.Platform, batch []state.ReplacementCandidate, progress state.ReplacementReconciliation) (err error) {
	var checkpoints []state.ReplacementCheckpoint
	// Flush completed reads even when the next read fails or cancels the budget.
	defer func() {
		if len(checkpoints) > 0 {
			checkpointCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			err = errors.Join(err, s.deps.State.CheckpointReplacementBatch(checkpointCtx, "opencode", root, checkpoints))
		}
	}()
	members := make(map[string]bool)
	for _, candidate := range batch {
		if err := ctx.Err(); err != nil {
			return err
		}
		member, known := members[candidate.Directory]
		if candidate.Member != nil {
			member, known = *candidate.Member, true
		}
		if !known {
			resolved, err := s.ReplacementMembership(ctx, root, []db.Session{{Directory: candidate.Directory}})
			if err != nil {
				return err
			}
			member = resolved[candidate.Directory]
			membershipCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			err = s.deps.State.CheckpointReplacementMembership(membershipCtx, "opencode", root, candidate.Directory, member)
			cancel()
			if err != nil {
				return err
			}
		}
		members[candidate.Directory] = member
		var notice state.SessionInterruption
		if member {
			var prior state.SessionInterruption
			if candidate.Prior != nil {
				prior = *candidate.Prior
			}
			notice, err = s.replacementCandidateNotice(ctx, adapter, candidate.SessionID, prior, candidate.Prior != nil, progress.StopStartedAt, progress.AdmissionStartedAt)
			if err != nil {
				return err
			}
		}
		checkpoints = append(checkpoints, state.ReplacementCheckpoint{Candidate: candidate, Member: member, Notice: notice})
	}
	return nil
}

func (s *Service) replacementCandidateNotice(ctx context.Context, adapter platforms.Platform, id string, prior state.SessionInterruption, sampled bool, stopAt, admissionAt int64) (state.SessionInterruption, error) {
	active := prior.BaselineStatus == string(db.StatusBusy) || prior.BaselineStatus == string(db.StatusInterrupted)
	var messageID string
	if s.deps.Sessions != nil {
		l, err := s.deps.Sessions.GetSessionLifecycle(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return state.SessionInterruption{}, nil
		}
		if err != nil {
			return state.SessionInterruption{}, err
		}
		if sampled && l.LatestMessageID == prior.MessageID && (replacementAbort(prior.BaselineErrorName) || !active) {
			return state.SessionInterruption{}, nil
		}
		unfinished := l.Status == db.StatusBusy || l.LatestMessageRole == "user" || l.LatestMessageFinish == "tool-calls" || l.LatestMessageFinish == "unknown"
		if !unfinished && !ReplacementAbortDuringStop(l, prior, sampled, stopAt, admissionAt) {
			return state.SessionInterruption{}, nil
		}
		messageID = l.LatestMessageID
	} else {
		l, err := interruptionLifecycle(ctx, adapter, id)
		if errors.Is(err, platforms.ErrNotFound) {
			return state.SessionInterruption{}, nil
		}
		if err != nil {
			return state.SessionInterruption{}, err
		}
		if l == nil || (l.Status != db.StatusBusy && l.Status != db.StatusInterrupted) || (sampled && l.LatestMessageID == prior.MessageID && !active) {
			return state.SessionInterruption{}, nil
		}
		messageID = l.LatestMessageID
	}
	if messageID == "" {
		return state.SessionInterruption{}, nil
	}
	text := prior.Message
	if text == "" {
		text = "The server stopped during this unfinished turn while replacement was being prepared. Send a follow-up to continue."
	}
	return state.SessionInterruption{MessageID: messageID, ObservedAt: time.Now().UnixMilli(), Message: text}, nil
}
