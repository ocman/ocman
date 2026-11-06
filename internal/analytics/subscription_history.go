package analytics

import (
	"context"
	"net/http"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/platforms"
)

func (m *Module) runSubscriptionUsageLoop(ctx context.Context) {
	if m.deps.Store == nil {
		return
	}
	interval := m.subscriptionSampleEvery
	if interval <= 0 {
		interval = subscriptionSampleInterval
	}
	module.Every(ctx, "subscription-usage", interval, func(ctx context.Context) {
		if err := m.collectSubscriptionUsage(ctx, time.Now()); err != nil && ctx.Err() == nil {
			module.ReportError(ctx, err)
			log.WithError(err).Warn("collecting subscription usage")
		}
	})
}

func (m *Module) collectSubscriptionUsage(ctx context.Context, now time.Time) error {
	if m.deps.Store == nil {
		return nil
	}
	providers := []subscriptionProviderUsage{}
	if m.deps.Platforms != nil {
		for _, p := range m.deps.Platforms() {
			if remote, _ := platforms.SplitPlatformID(string(p.ID())); remote != "" {
				continue
			}
			reader, ok := p.(subscriptionAccountReader)
			if !ok {
				continue
			}
			// Read each platform afresh. One unavailable platform cannot use the
			// entire sampling interval or prevent other platforms from recording.
			probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			provider, supported := fetchPlatformSubscription(probeCtx, p, reader)
			cancel()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if supported {
				providers = append(providers, provider)
			}
		}
	}
	return recordSubscriptionUsage(ctx, m.deps.Store.SQL(), now, providers)
}

func (m *Module) handleSubscriptionUsageHistory(w http.ResponseWriter, r *http.Request) {
	days := 7
	if raw := r.URL.Query().Get("days"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 365 {
			http.Error(w, "days must be between 1 and 365, or 0 for all history", http.StatusBadRequest)
			return
		}
		days = value
	}
	if m.deps.Store == nil {
		http.Error(w, "subscription history unavailable", http.StatusServiceUnavailable)
		return
	}
	history, err := readSubscriptionUsageHistory(r.Context(), m.deps.Store.SQL(), time.Now(), days)
	if err != nil {
		httpx.ServerError(w, "failed to read subscription history", err)
		return
	}
	httpx.WriteJSON(w, history)
}
