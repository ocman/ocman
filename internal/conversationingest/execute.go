package conversationingest

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

func (w *sourceWorker) execute(ctx context.Context, p pass, report func(tasksvc.Progress) error) error {
	total := int64(len(p.reads) + len(p.deletions))
	progress := tasksvc.Progress{Phase: "reading sessions", Unit: "sessions", Total: &total, Counters: map[string]int64{}}
	if p.deferred > 0 {
		progress.Counters["deferred"] = int64(p.deferred)
	}
	defer func() { w.last = progress }()
	if p.skipped > 0 {
		progress.Counters["skipped"] = p.skipped
	}
	update := func() error {
		if report != nil {
			return report(progress)
		}
		return nil
	}
	if err := update(); err != nil {
		return err
	}
	served := map[string]bool{}
	failed := 0
	for _, entry := range p.reads {
		if err := ctx.Err(); err != nil {
			return err
		}
		release, err := w.beginBackfill(ctx, p.background, served)
		if err != nil {
			return err
		}
		if served[entry.ID] && !p.full && !p.verify {
			// A foreground read may have advanced a feed sequence beyond the
			// planned inventory. Do not overwrite it with the old backfill hint.
			release()
			progress.Completed++
			progress.Counters["skipped"]++
			if err := update(); err != nil {
				return err
			}
			continue
		}
		outcome := ""
		func() {
			defer release()
			hint, hinted := w.versions[entry.ID]
			ticket := w.module.readTicket(w.source.AdapterID, entry.ID)
			result, err := w.importSession(ctx, entry, p.verify || p.full)
			outcome = result.outcome
			cause := ""
			if err != nil {
				failed++
				outcome, cause = "failed", err.Error()
				w.forget(ctx, entry.ID)
				if ctx.Err() == nil {
					w.module.report(w.instance.ID, entry.ID, err)
				}
			} else {
				if p.verify {
					outcome = w.classify(result, hint, hinted, entry.ID)
				}
				if result.outcome != "purged" && result.outcome != "deferred" {
					matched, checkErr := w.usageMatches(ctx, entry.ID, result.usage)
					switch {
					case checkErr != nil:
						failed++
						outcome, cause = "failed", checkErr.Error()
						w.forget(ctx, entry.ID)
						w.module.report(w.instance.ID, entry.ID, checkErr)
					case !matched:
						// Unchanged content is not rewritten, so this does not self-heal.
						// It means a normalization or column mapping bug to investigate.
						progress.Counters["usage-mismatch"]++
						failed++
						cause = "usage mismatch"
						w.forget(ctx, entry.ID)
						log.WithFields(log.Fields{"instance_id": w.instance.ID, "session_id": entry.ID}).
							Warn("conversation history accounting differs from source; investigate the mapping")
					default:
						err := w.approveRead(ctx, entry.ID, result.version, ticket)
						if err == nil && w.feed != nil {
							err = w.advanceSequence(ctx, entry.ID, result.version)
						}
						if err != nil {
							failed++
							outcome, cause = "failed", err.Error()
							w.forget(ctx, entry.ID)
							w.module.report(w.instance.ID, entry.ID, err)
						}
					}
				}
			}
			if cause == "" && result.outcome == "deferred" {
				cause = errDeferred.Error()
			}
			if cause != "" {
				w.failing[entry.ID] = cause
			} else {
				delete(w.failing, entry.ID)
			}
		}()
		progress.Completed++
		progress.Counters[outcome]++
		if err := update(); err != nil {
			return err
		}
	}
	progress.Phase = "retaining removed history"
	if err := update(); err != nil {
		return err
	}
	// Only a successful, complete source inventory authorizes absence markers.
	// Failed snapshots never authorize deletion of any message or part.
	for _, session := range p.deletions {
		release, err := w.beginBackfill(ctx, p.background, served)
		if err != nil {
			return err
		}
		err = func() error {
			defer release()
			return w.markDeleted(ctx, session)
		}()
		if err != nil {
			return err
		}
		w.forget(ctx, session.SessionID)
		delete(w.failing, session.SessionID)
		progress.Completed++
		progress.Counters["source-deleted"]++
		if err := update(); err != nil {
			return err
		}
	}
	if p.verify {
		if err := w.markVerified(ctx); err != nil {
			return err
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d sessions failed; history was not fully verified", failed)
	}
	return nil
}
