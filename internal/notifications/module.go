// Package notifications delivers OS notifications without an open browser page.
package notifications

import (
	"context"
	"net/http"
	"time"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

type Notification struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Tag   string `json:"tag"`
	Kind  string `json:"kind"`
}

// Key identifies a prompt request or the final assistant message, not a poll.
type Candidate struct {
	Key          string
	Notification Notification
}

type Deps struct {
	Store    *state.DB
	Snapshot func(context.Context) ([]Candidate, error)
	Guards   module.Guards
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
	r.Handle("GET /api/notifications/push-key", m.d.Guards.Auth(m.publicKey))
	r.Handle("POST /api/notifications/subscribe", m.d.Guards.Auth(m.subscribe))
	r.Handle("POST /api/notifications/unsubscribe", m.d.Guards.Auth(m.unsubscribe))
}

func (m *Module) Run(ctx context.Context) {
	if m.store.db == nil {
		return
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if err := m.tick(ctx); err != nil && ctx.Err() == nil {
			log.WithError(err).Warn("checking Web Push notifications")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (m *Module) tick(ctx context.Context) error {
	// Delivery retries do not depend on a healthy session snapshot.
	if err := m.deliver(ctx); err != nil {
		return err
	}
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
	return m.deliver(ctx)
}
