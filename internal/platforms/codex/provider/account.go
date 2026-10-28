package codexprovider

import (
	"fmt"

	"github.com/ocman/ocman/internal/platforms"
	codexappserver "github.com/ocman/ocman/internal/platforms/codex/appserver"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// Account maps Codex's account and rate-limit reads to the provider
// account Settings shows. A signed-out Codex is platforms.ErrNotFound.
func Account(account *codexappserver.AccountResponse, limits *codexappserver.RateLimitSnapshot) (*providerprotocol.Account, error) {
	if account == nil || account.Account == nil {
		return nil, platforms.ErrNotFound
	}
	out := &providerprotocol.Account{RateLimits: []providerprotocol.RateLimit{}}
	switch account.Account.Type {
	case "chatgpt":
		out.SubscriptionType = account.Account.PlanType
	case "apiKey":
		out.SubscriptionType = "API key"
	case "amazonBedrock":
		out.SubscriptionType = "Amazon Bedrock"
	default:
		out.SubscriptionType = account.Account.Type
	}
	if limits == nil {
		return out, nil
	}
	for _, w := range []*codexappserver.RateLimitWindow{limits.Primary, limits.Secondary} {
		if w == nil {
			continue
		}
		limit := providerprotocol.RateLimit{Name: windowName(w.WindowDurationMins), Utilization: w.UsedPercent / 100}
		if w.ResetsAt != nil {
			limit.ResetsAt = *w.ResetsAt * 1000
		}
		out.RateLimits = append(out.RateLimits, limit)
	}
	return out, nil
}

// windowName uses the names Settings labels, five_hour and weekly; other
// windows are named by their length.
func windowName(mins *int64) string {
	switch {
	case mins == nil:
		return "usage"
	case *mins == 300:
		return "five_hour"
	case *mins == 7*24*60:
		return "weekly"
	}
	return fmt.Sprintf("%d_minutes", *mins)
}
