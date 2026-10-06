package platforms

import "context"

// Usage keeps recorded billing separate from the token-price estimate.
type Usage struct {
	Tokens  TokenTotals `json:"tokens"`
	Cost    float64     `json:"cost"`
	EstCost float64     `json:"estCost"`
}

func (u *Usage) Add(other Usage) {
	u.Tokens.Input += other.Tokens.Input
	u.Tokens.Output += other.Tokens.Output
	u.Tokens.CacheRead += other.Tokens.CacheRead
	u.Tokens.CacheWrite += other.Tokens.CacheWrite
	u.Cost += other.Cost
	u.EstCost += other.EstCost
}

// UsageReader reads stored usage for a session and its descendants, keyed by
// session ID. It does not fetch parts, live catalogs, or ancestor sessions.
type UsageReader interface {
	SessionUsage(context.Context, string) (map[string]Usage, error)
}
