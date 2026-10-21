package conversationingest

import (
	"context"
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

const (
	syncTaskKind   = "conversation.history"
	verifyTaskKind = "conversation.history.verify"
)

// runPass records a task only for passes that read or retain something, and
// for new failures. Idle polls stay out of the task list.
func (w *sourceWorker) runPass(ctx context.Context, dirty map[string]bool) error {
	p, err := w.prepare(ctx, dirty)
	if err != nil {
		return w.recordFailure(ctx, err, tasksvc.Progress{})
	}
	return w.runPrepared(ctx, p)
}

func (w *sourceWorker) runPrepared(ctx context.Context, p pass) error {
	p.background = !p.routine
	w.last = tasksvc.Progress{}
	if len(p.reads) == 0 && len(p.deletions) == 0 && !p.verify && p.deferred == 0 {
		w.lastFailure = ""
		return nil
	}
	if w.module.deps.Tasks == nil {
		return w.executePrepared(ctx, p, nil)
	}
	// Retrying only sessions that already failed is not new work: it is
	// recorded again only if the failure changes.
	deferredOnly := p.deferred > 0 && len(p.reads) == 0 && len(p.deletions) == 0 && !p.verify
	if p.routine || deferredOnly || w.retrying(p) {
		err := w.executePrepared(ctx, p, nil)
		if err == nil {
			w.lastFailure = ""
			return nil
		}
		return w.recordFailure(ctx, err, w.last)
	}
	spec := w.taskSpec(syncTaskKind, "Sync conversation history: ")
	if p.verify {
		spec = w.taskSpec(verifyTaskKind, "Verify conversation history: ")
	}
	err := w.module.deps.Tasks.Do(ctx, spec, func(ctx context.Context, reporter *tasksvc.Reporter) error {
		return w.executePrepared(ctx, p, reporter.Report)
	})
	switch {
	case err == nil:
		w.lastFailure = ""
	case ctx.Err() == nil:
		w.lastFailure = w.failureSignature(err)
	}
	return err
}

func (w *sourceWorker) executePrepared(ctx context.Context, p pass, report func(tasksvc.Progress) error) error {
	err := w.execute(ctx, p, report)
	if w.feed != nil || w.journaled || p.deferred > 0 {
		return w.requireComplete(err)
	}
	return err
}

// retrying reports whether a pass only rereads sessions whose last read failed.
func (w *sourceWorker) retrying(p pass) bool {
	if p.verify || len(p.deletions) > 0 || len(p.reads) == 0 {
		return false
	}
	for _, entry := range p.reads {
		if _, ok := w.failing[entry.ID]; !ok {
			return false
		}
	}
	return true
}

// failureSignature identifies a failure by its error and the failing sessions
// with their causes, which the aggregate pass error does not name.
func (w *sourceWorker) failureSignature(err error) string {
	parts := make([]string, 0, len(w.failing)+1)
	for id, cause := range w.failing {
		parts = append(parts, id+": "+cause)
	}
	sort.Strings(parts)
	return strings.Join(append([]string{err.Error()}, parts...), "\n")
}

// recordFailure records a failing pass once per distinct failure, so an
// unavailable source or a broken session does not add a task every poll.
func (w *sourceWorker) recordFailure(ctx context.Context, err error, final tasksvc.Progress) error {
	if err == nil {
		w.lastFailure = ""
		return nil
	}
	signature := w.failureSignature(err)
	if ctx.Err() != nil || signature == w.lastFailure {
		return err
	}
	w.lastFailure = signature
	if w.module.deps.Tasks == nil {
		return err
	}
	_ = w.module.deps.Tasks.Do(ctx, w.taskSpec(syncTaskKind, "Sync conversation history: "),
		func(_ context.Context, reporter *tasksvc.Reporter) error {
			if rerr := reporter.Report(final); rerr != nil {
				return rerr
			}
			return err
		})
	return err
}

func (w *sourceWorker) taskSpec(kind, title string) tasksvc.Spec {
	label := w.source.Label
	if label == "" {
		label = w.source.AdapterID
	}
	return tasksvc.Spec{Kind: kind, Key: w.source.AdapterID, Scope: w.source.AdapterID, Title: title + label}
}

// classify names a verify-pass result. Drift is content that changed while
// the source version hint stayed the same, so normal passes missed it.
func (w *sourceWorker) classify(result importResult, hint string, hinted bool, sessionID string) string {
	if result.outcome != "imported" {
		if result.outcome == "unchanged" {
			return "matched"
		}
		return result.outcome
	}
	switch {
	case !hinted:
		return "missing"
	case hint != result.version:
		return "changed"
	}
	log.WithFields(log.Fields{"instance_id": w.instance.ID, "session_id": sessionID, "version": hint}).
		Warn("conversation history drift: content changed without a new source version; repaired")
	return "drifted"
}
