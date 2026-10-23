// Package startup exposes pre-serving progress without opening application
// routes or starting workers against databases being migrated.
package startup

import (
	"context"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Snapshot struct {
	State     string `json:"state"`
	Phase     string `json:"phase"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
	Elapsed   int64  `json:"elapsedSeconds"`
}

type Progress struct {
	mu      sync.Mutex
	status  Snapshot
	started time.Time
	logged  time.Time
}

type progressKey struct{}

type reporterKey struct{}

// WithReporter redirects phase observations to background task progress once
// historical preparation no longer belongs to blocking application startup.
// report must not block or write to databases used by the caller.
func WithReporter(ctx context.Context, report func(string, int, int)) context.Context {
	return context.WithValue(ctx, reporterKey{}, report)
}

func New(ctx context.Context) (context.Context, *Progress) {
	p := &Progress{started: time.Now(), status: Snapshot{State: "starting", Phase: "Opening databases"}}
	return context.WithValue(ctx, progressKey{}, p), p
}

// Report is a no-op outside startup. Counts describe this phase, not an
// estimated overall percentage. Never pass paths, session IDs or raw errors.
func Report(ctx context.Context, phase string, completed, total int) {
	if report, ok := ctx.Value(reporterKey{}).(func(string, int, int)); ok {
		report(phase, completed, total)
		return
	}
	if p, ok := ctx.Value(progressKey{}).(*Progress); ok {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.status.State != "starting" {
			return
		}
		changed := p.status.Phase != phase
		p.status.Phase, p.status.Completed, p.status.Total = phase, completed, total
		if changed || time.Since(p.logged) >= 2*time.Second || (total > 0 && completed == total) {
			p.logLocked()
		}
	}
}

func (p *Progress) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Elapsed = int64(time.Since(p.started).Seconds())
	return s
}

func (p *Progress) finish(state, phase string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status = Snapshot{State: state, Phase: phase}
	p.logLocked()
}

// Heartbeat reports ongoing work even while one expensive SQL statement runs.
func (p *Progress) Heartbeat(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.mu.Lock()
			if p.status.State == "starting" {
				p.logLocked()
			}
			p.mu.Unlock()
		}
	}
}

func (p *Progress) logLocked() {
	p.logged = time.Now()
	log.WithFields(log.Fields{"phase": p.status.Phase, "completed": p.status.Completed,
		"total": p.status.Total, "elapsed": time.Since(p.started).Round(time.Second).String(),
		"state": p.status.State}).Info("startup progress")
}
