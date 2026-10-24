// Package notifications delivers OS notifications without an open browser page.
package notifications

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

type Notification struct {
	Title      string `json:"title"`
	Body       string `json:"body"`
	URL        string `json:"url"`
	Tag        string `json:"tag"`
	Kind       string `json:"kind"`
	BadgeCount int    `json:"badgeCount"`
}

// Key identifies a prompt request or the final assistant message, not a poll.
type Candidate struct {
	Key          string
	Notification Notification
}

type Deps struct {
	Store    *state.DB
	Snapshot func(context.Context) ([]Candidate, error)
}

type Module struct {
	d        Deps
	store    store
	client   *http.Client
	baseline map[string]bool
}

func New(d Deps) *Module {
	m := &Module{d: d, client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	if d.Store != nil {
		m.store.db = d.Store.SQL()
	}
	return m
}

func (*Module) Name() string { return "notifications" }

func (m *Module) Routes(r module.Router) {
	r.Setting("GET /api/settings/notification-badge", m.badgeSettings)
	r.Setting("POST /api/settings/notification-badge", m.badgeSettings)
	r.Handle("GET /api/notifications/push-key", m.publicKey)
	r.Handle("POST /api/notifications/subscribe", m.subscribe)
	r.Handle("POST /api/notifications/subscription", m.subscriptionStatus)
	r.Handle("POST /api/notifications/unsubscribe", m.unsubscribe)
}

func (m *Module) Run(ctx context.Context) {
	m.run(ctx, 10*time.Second)
}

func (m *Module) run(ctx context.Context, interval time.Duration) {
	if m.store.db == nil {
		return
	}
	wake := make(chan struct{}, 1)
	var workers sync.WaitGroup
	workers.Go(func() { m.runDelivery(ctx, interval, wake) })
	defer workers.Wait()
	module.Every(ctx, "web-push", interval, func(ctx context.Context) {
		if err := m.tick(ctx); err != nil && ctx.Err() == nil {
			log.WithError(err).Warn("checking Web Push notifications")
		}
		select {
		case wake <- struct{}{}:
		default:
		}
	})
}

func (m *Module) runDelivery(ctx context.Context, interval time.Duration, wake <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
		}
		if err := m.deliver(ctx); err != nil && ctx.Err() == nil {
			log.WithError(err).Warn("delivering Web Push notifications")
		}
	}
}

func (m *Module) tick(ctx context.Context) error {
	n, err := m.store.count(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		m.baseline = nil
		return nil
	}
	candidates, err := m.d.Snapshot(ctx)
	if err != nil {
		return err
	}
	next := make(map[string]bool, len(candidates))
	for _, c := range candidates {
		next[c.Key] = true
		// First snapshot is a baseline, never a flood of historical sessions.
		if m.baseline != nil && !m.baseline[c.Key] {
			if err := m.store.enqueue(ctx, c); err != nil {
				return err
			}
		}
	}
	m.baseline = next
	return nil
}
