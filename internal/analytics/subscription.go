package analytics

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

type subscriptionUsageResponse struct {
	Providers []subscriptionProviderUsage `json:"providers"`
}

type subscriptionProviderUsage struct {
	ID           string                     `json:"id"`
	Name         string                     `json:"name"`
	PlatformName string                     `json:"platformName,omitempty"`
	MachineName  string                     `json:"machineName,omitempty"`
	Plan         string                     `json:"plan,omitempty"`
	Status       string                     `json:"status"`
	Windows      []subscriptionUsageWindow  `json:"windows"`
	Budgets      []platforms.ProviderBudget `json:"budgets,omitempty"`
	Spend        *providerSpend             `json:"spend,omitempty"`
}

type subscriptionUsageWindow struct {
	Name        string  `json:"name"`
	UsedPercent float64 `json:"usedPercent"`
	ResetsAt    string  `json:"resetsAt,omitempty"`
}

type subscriptionAccountReader interface {
	Account(context.Context) (*providerprotocol.Account, error)
}

const subscriptionUsageTTL = time.Minute

// Cache each platform independently, but always enumerate the current registry
// so disabling a platform immediately removes its account from the response.
type subscriptionUsageCache struct {
	mu      sync.Mutex
	entries map[platforms.ID]subscriptionUsageEntry
}

type subscriptionUsageEntry struct {
	at    time.Time
	value subscriptionProviderUsage
}

func (c *subscriptionUsageCache) get(ctx context.Context, sources []SubscriptionSource) subscriptionUsageResponse {
	c.mu.Lock()
	defer c.mu.Unlock()
	usage := subscriptionUsageResponse{Providers: []subscriptionProviderUsage{}}
	if c.entries == nil {
		c.entries = map[platforms.ID]subscriptionUsageEntry{}
	}
	active := map[platforms.ID]bool{}
	for _, source := range sources {
		p := source.Platform
		reader, ok := p.(subscriptionAccountReader)
		if !ok {
			continue
		}
		active[source.Key] = true
		entry, cached := c.entries[source.Key]
		if !cached || time.Since(entry.at) >= subscriptionUsageTTL {
			value, supported := fetchPlatformSubscription(ctx, p, reader)
			if !supported {
				delete(c.entries, source.Key)
				continue
			}
			entry = subscriptionUsageEntry{at: time.Now(), value: value}
			if ctx.Err() == nil {
				c.entries[source.Key] = entry
			}
		}
		// Names are live metadata, not part of the cached account snapshot.
		usage.Providers = append(usage.Providers, source.label(entry.value))
	}
	for id := range c.entries {
		if !active[id] {
			delete(c.entries, id)
		}
	}
	return usage
}

func (m *Module) handleSubscriptionUsage(w http.ResponseWriter, r *http.Request) {
	sources, err := m.subscriptionSources(r.Context())
	if err != nil {
		httpx.ServerError(w, "failed to read platform connections", err)
		return
	}
	usage := m.subscriptionCache.get(r.Context(), sources)
	// Local TypeSafe estimates are separate from platform-reported account usage.
	if err := m.appendTypeSafeUsage(r.Context(), &usage); err != nil {
		httpx.ServerError(w, "failed to read provider usage", err)
		return
	}
	httpx.WriteJSON(w, usage)
}

func fetchPlatformSubscription(ctx context.Context, p platforms.Platform, reader subscriptionAccountReader) (subscriptionProviderUsage, bool) {
	out := subscriptionProviderUsage{ID: string(p.ID()), Name: p.DisplayName(), Status: "upstream_error", Windows: []subscriptionUsageWindow{}}
	account, err := reader.Account(ctx)
	switch {
	case errors.Is(err, platforms.ErrUnsupported):
		return out, false
	case errors.Is(err, platforms.ErrNotFound):
		out.Status = "signed_out"
	case errors.Is(err, platforms.ErrPlatformUnauthenticated):
		out.Status = "unauthorized"
	case err == nil && account != nil:
		out.Status, out.Plan, out.Budgets = "ok", account.SubscriptionType, account.Budgets
		for _, limit := range account.RateLimits {
			out.Windows = append(out.Windows, subscriptionUsageWindow{Name: subscriptionWindowName(limit.Name), UsedPercent: max(0, min(100, limit.Utilization*100)), ResetsAt: subscriptionReset(limit.ResetsAt)})
		}
	}
	return out, true
}

func subscriptionWindowName(name string) string {
	switch name {
	case "five_hour":
		return "5 hours"
	case "weekly":
		return "7 days"
	default:
		return name
	}
}

func subscriptionReset(millis int64) string {
	if millis <= 0 {
		return ""
	}
	return time.UnixMilli(millis).UTC().Format(time.RFC3339)
}
