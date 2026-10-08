package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// beginOpencodeReplacementStop is the last callback before runtime.Stop. The
// earlier preparation is not evidence that a turn was still active at Stop.
func (s *Server) beginOpencodeReplacementStop(ctx context.Context, root string) error {
	var inst *ocruntime.Instance
	if s.stateDB != nil {
		stored, found, err := s.stateDB.GetManagedOpencode(ctx, root)
		if err != nil {
			return err
		}
		if found {
			inst = &ocruntime.Instance{Endpoint: stored.Endpoint, Kind: stored.Kind, ID: stored.RuntimeID, PID: stored.PID, RepoRoot: root}
		}
	}
	return s.beginOpencodeReplacementStopWithInstance(ctx, root, inst)
}

// The host should pass the actual cleanup handle, including adopted instances
// that have no managed_opencode row. The two-argument wrapper supports older
// callback wiring, but recovery fails closed if it could not capture a handle.
func (s *Server) beginOpencodeReplacementStopWithInstance(ctx context.Context, root string, inst *ocruntime.Instance) error {
	adapter, ok := s.registry.Get("opencode")
	if !ok || s.stateDB == nil {
		return nil
	}
	stopped, err := s.stateDB.ReplacementStopped(ctx, "opencode", root)
	if err != nil || stopped {
		return err
	}
	if pending, err := s.opencodeReplacementStopping(ctx, root); err != nil {
		return err
	} else if pending != nil {
		return state.ErrReplacementStopping
	}
	admissionStartedAt := time.Now().UnixMilli()
	prepared, err := s.stateDB.PreparedSessionInterruptions(ctx, "opencode", root)
	if err != nil {
		return err
	}
	var sessions []db.Session
	if s.db != nil {
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
	baseline := make(map[string]state.SessionInterruption)
	for _, session := range sessions {
		if !members[session.Directory] {
			continue
		}
		l, persisted, err := s.stopLifecycleSnapshot(ctx, adapter, session.ID)
		if errors.Is(err, platforms.ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("revalidating stop lifecycle for %q: %w", session.ID, err)
		}
		if l == nil {
			continue
		}
		prior := prepared[session.ID]
		prior.MessageID, prior.BaselineStatus = l.LatestMessageID, string(l.Status)
		prior.BaselineMessageCreated = l.LatestMessageCreated
		if s.db != nil {
			prior.BaselineMessageCreated = persisted.LatestMessageCreated
			prior.BaselineErrorName = persisted.LatestErrorName
		}
		baseline[session.ID] = prior
	}
	// Take this after revalidation, not at the start of a potentially slow scan.
	var handle state.ReplacementRuntime
	if inst != nil {
		if inst.RepoRoot != "" && inst.RepoRoot != root {
			return errors.New("replacement runtime root does not match stop scope")
		}
		handle = state.ReplacementRuntime{ManagedInstance: state.ManagedInstance{Endpoint: inst.Endpoint, Kind: inst.Kind, RuntimeID: inst.ID, PID: inst.PID}, RepoRoot: inst.RepoRoot}
	}
	return s.stateDB.BeginSessionReplacementStopSnapshot(ctx, "opencode", root, state.ReplacementStopSnapshot{
		Baseline: baseline, StartedAt: time.Now().UnixMilli(), AdmissionStartedAt: admissionStartedAt,
		Runtime: handle, Membership: members, Candidates: replacementCandidates(sessions),
	})
}

// The raw read supplies persisted abort metadata, never settled status. If a
// turn advances between reads, retry the identity/status pair together. A
// continuously changing turn fails the callback rather than guessing status.
func (s *Server) stopLifecycleSnapshot(ctx context.Context, adapter platforms.Platform, sessionID string) (*platforms.SessionLifecycle, db.SessionLifecycle, error) {
	for attempt := 0; attempt < 3; attempt++ {
		l, err := interruptionLifecycle(ctx, adapter, sessionID)
		if err != nil || l == nil || s.db == nil {
			return l, db.SessionLifecycle{}, err
		}
		persisted, err := s.db.GetSessionLifecycle(ctx, sessionID)
		if err != nil {
			return nil, db.SessionLifecycle{}, err
		}
		if persisted.LatestMessageID == l.LatestMessageID {
			return l, persisted, nil
		}
	}
	return nil, db.SessionLifecycle{}, fmt.Errorf("session %q advanced during stop revalidation; retry replacement", sessionID)
}

func replacementAbortDuringStop(l db.SessionLifecycle, prior state.SessionInterruption, sampled bool, stopAt int64, admissionAt ...int64) bool {
	if stopAt == 0 || !replacementAbort(l.LatestErrorName) || l.LatestCompleted < stopAt {
		return false
	}
	if sampled && l.LatestMessageID == prior.MessageID {
		return !replacementAbort(prior.BaselineErrorName) &&
			(prior.BaselineStatus == string(db.StatusBusy) || prior.BaselineStatus == string(db.StatusInterrupted))
	}
	// A user prompt can advance to an assistant envelope; a running assistant
	// can advance to its next step. New sessions/turns admitted during Stop also
	// belong to this attempt, rather than to an ancient session-level timestamp.
	active := sampled && (prior.BaselineStatus == string(db.StatusBusy) || prior.BaselineStatus == string(db.StatusInterrupted))
	admission := stopAt
	if len(admissionAt) > 0 && admissionAt[0] > 0 {
		admission = admissionAt[0]
	}
	return (active && l.LatestMessageCreated >= prior.BaselineMessageCreated) || l.LatestMessageCreated >= admission
}

func replacementAbort(name string) bool { return name == "MessageAbortedError" || name == "AbortError" }
