package analytics

import (
	"context"
	"errors"
	"net/http"
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
	RefreshedAt  string                     `json:"refreshedAt,omitempty"`
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

func (m *Module) handleSubscriptionUsage(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), subscriptionResponseBudget)
	defer cancel()
	sources, err := m.subscriptionSources(ctx)
	if err != nil {
		httpx.ServerError(w, "failed to read platform connections", err)
		return
	}
	var typeSafe subscriptionRefresh
	if m.deps.Store != nil && m.deps.TypeSafeConfigured != nil {
		typeSafe = m.fetchTypeSafeUsage
	}
	usage := m.subscriptionCache.get(ctx, sources, typeSafe)
	if httpx.WriteCancellation(w, "provider usage", r.Context().Err()) {
		return
	}
	httpx.WriteJSON(w, usage)
}

func fetchPlatformSubscription(ctx context.Context, p platforms.Platform, reader subscriptionAccountReader) (subscriptionProviderUsage, bool) {
	out := unavailableUsage(string(p.ID()), p.DisplayName())
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
