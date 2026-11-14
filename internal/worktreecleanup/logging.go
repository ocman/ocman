package worktreecleanup

import (
	"context"
	"errors"
	"time"

	log "github.com/sirupsen/logrus"
)

// cleanupFailure preserves the original error for callers and DEBUG logs while
// giving INFO logs a bounded reason independent of paths, session IDs or RPC text.
type cleanupFailure struct {
	code string
	err  error
}

func (e *cleanupFailure) Error() string { return e.err.Error() }
func (e *cleanupFailure) Unwrap() error { return e.err }

func cleanupError(code string, err error) error { return &cleanupFailure{code: code, err: err} }

func cleanupReason(err error) string {
	if err == nil {
		return ""
	}
	var failure *cleanupFailure
	if errors.As(err, &failure) {
		return failure.code
	}
	return "eligibility_read_failed"
}

type sweepReport struct {
	log     *log.Entry
	started time.Time
	targets int64
	checked int64
	counts  map[string]int64
	reasons map[string]int64
}

func newSweepReport() *sweepReport {
	started := time.Now()
	return &sweepReport{
		log:     log.WithFields(log.Fields{"component": "worktreecleanup", "sweep_id": started.UTC().Format(time.RFC3339Nano)}),
		started: started,
		counts:  map[string]int64{"removed": 0, "retained": 0, "already-removed": 0, "unavailable-owners": 0},
		reasons: map[string]int64{},
	}
}

func (r *sweepReport) checkout(owner, path string, removed bool, err error) {
	outcome := "already-removed"
	if removed {
		outcome = "removed"
	} else if err != nil {
		outcome = "retained"
		r.reasons[cleanupReason(err)]++
	}
	r.checked++
	r.counts[outcome]++
	r.log.WithFields(log.Fields{"owner": owner, "path": path, "outcome": outcome, "reason_code": cleanupReason(err)}).Info("worktree cleanup checkout")
	if err != nil {
		r.log.WithFields(log.Fields{"owner": owner, "path": path}).WithError(err).Debug("worktree cleanup detail")
	}
}

func (r *sweepReport) finish(err error) {
	status := "completed"
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		status = "cancelled"
	} else if err != nil {
		status = "failed"
	}
	fields := log.Fields{"status": status, "targets": r.targets, "checked": r.checked, "duration_ms": time.Since(r.started).Milliseconds(), "retained_by_reason": r.reasons}
	for key, count := range r.counts {
		fields[key] = count
	}
	r.log.WithFields(fields).Info("worktree cleanup sweep")
}

func unavailableOwner(entry *log.Entry, owner, reason string) {
	entry.WithFields(log.Fields{"owner": owner, "reason_code": reason}).Info("worktree cleanup owner unavailable")
}
