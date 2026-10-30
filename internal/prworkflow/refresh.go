package prworkflow

import (
	"time"

	"github.com/ocman/ocman/internal/forge"
)

func (m *Module) now() time.Time {
	if m.d.Now != nil {
		return m.d.Now()
	}
	return time.Now()
}

// updatedAt is a cheap hint, not a complete comment fingerprint. Reread every
// ten minutes even when unchanged, since hosts may not bump it for comment edits.
func feedbackDue(previous *Snapshot, pr forge.PR, now time.Time) bool {
	return previous == nil || pr.UpdatedAt.IsZero() ||
		previous.SHA != pr.HeadSHA || !previous.UpdatedAt.Equal(pr.UpdatedAt) ||
		previous.RemarksReadAt.IsZero() || previous.RemarksReadAt.After(now) ||
		now.Sub(previous.RemarksReadAt) >= 10*time.Minute
}

func quietPR(pr forge.PR, snapshot *Snapshot, now time.Time) bool {
	return !pr.UpdatedAt.IsZero() && now.Sub(pr.UpdatedAt) >= 30*time.Minute && !snapshot.InFlight
}
