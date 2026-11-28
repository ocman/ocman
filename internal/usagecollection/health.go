package usagecollection

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/usagewire"
)

type healthDay struct {
	id   string
	rows map[string]*usagewire.Health
}

// BeginHealthTurn returns a single-use settlement callback for a live turn.
// Disabling reporting invalidates callbacks for turns that are still running.
func (m *Module) BeginHealthTurn(ctx context.Context, platform string) func(string, time.Duration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.healthEnabled(ctx, platform) {
		return nil
	}
	m.healthRow(platform).Turns.Started++
	epoch := m.healthEpoch
	finished := false
	return func(outcome string, latency time.Duration, sawResponse bool) {
		m.mu.Lock()
		defer m.mu.Unlock()
		if finished || epoch != m.healthEpoch || !m.healthEnabled(ctx, platform) {
			return
		}
		counts := &m.healthRow(platform).Turns
		switch outcome {
		case convevents.OutcomeDone:
			counts.Completed++
		case convevents.OutcomeError:
			counts.Failed++
		case convevents.OutcomeInterrupted:
			counts.Interrupted++
		case "canceled":
			counts.Canceled++
		default:
			return
		}
		finished = true
		if !sawResponse {
			counts.NoResponse++
			return
		}
		if counts.FirstResponse == nil {
			counts.FirstResponse = make([]int64, usagewire.ResponseBucketCount)
		}
		counts.FirstResponse[responseBucket(latency)]++
	}
}

func responseBucket(d time.Duration) int {
	for i, limit := range [...]time.Duration{1, 2, 5, 10, 30, 60} {
		if d < limit*time.Second {
			return i
		}
	}
	return usagewire.ResponseBucketCount - 1
}

// RecordStart records an admitted create's outcome. Error text never enters the report.
func (m *Module) RecordStart(ctx context.Context, platform string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.healthEnabled(ctx, platform) {
		return
	}
	counts := &m.healthRow(platform).Starts
	switch {
	case err == nil:
		counts.Ready++
	case errors.Is(err, context.DeadlineExceeded):
		counts.Timeout++
	case errors.Is(err, context.Canceled):
		counts.Canceled++
	default:
		counts.Failed++
	}
}

func (m *Module) healthEnabled(ctx context.Context, platform string) bool {
	if m.deps.Store == nil || !usagewire.HealthPlatform(platform) {
		return false
	}
	enabled, err := m.enabled(ctx)
	return enabled && err == nil && endpoint(m.deps.RelayURL()) != ""
}

func (m *Module) healthRow(platform string) *usagewire.Health {
	now := m.now().UTC()
	day := now.Format(time.DateOnly)
	m.pruneHealth(now)
	if m.health == nil {
		m.health = make(map[string]*healthDay)
	}
	bucket := m.health[day]
	if bucket == nil {
		bucket = &healthDay{id: uuid.NewString(), rows: make(map[string]*usagewire.Health)}
		m.health[day] = bucket
	}
	if bucket.rows[platform] == nil {
		bucket.rows[platform] = &usagewire.Health{Platform: platform}
	}
	return bucket.rows[platform]
}

func (m *Module) pruneHealth(now time.Time) {
	yesterday, today := now.AddDate(0, 0, -1).Format(time.DateOnly), now.Format(time.DateOnly)
	for day := range m.health {
		if day != yesterday && day != today {
			delete(m.health, day)
		}
	}
}

func (m *Module) sendHealth(ctx context.Context) {
	m.mu.Lock()
	now := m.now().UTC()
	m.pruneHealth(now)
	day := now.AddDate(0, 0, -1).Format(time.DateOnly)
	bucket := m.health[day]
	url := endpoint(m.deps.RelayURL())
	enabled, err := m.enabled(ctx)
	if !enabled || err != nil || bucket == nil || url == "" || now.Before(m.nextHealth) {
		m.mu.Unlock()
		return
	}
	report := &usagewire.Report{InstallationID: bucket.id, Date: day, Version: usagewire.NormalizeVersion(m.deps.Version())}
	for _, row := range bucket.rows {
		report.Health = append(report.Health, *row)
	}
	sort.Slice(report.Health, func(i, j int) bool { return report.Health[i].Platform < report.Health[j].Platform })
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	m.cancel = cancel
	m.nextHealth = now.Add(15 * time.Minute)
	m.mu.Unlock()
	ok := report.Valid(now) && m.post(requestCtx, url+"/health", report)
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancel = nil
	if ok && m.health[day] == bucket {
		delete(m.health, day)
	}
}
