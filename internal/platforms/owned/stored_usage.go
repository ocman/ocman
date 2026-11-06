package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

func (a *Adapter) storedUsage(ctx context.Context, id string) (map[string]platforms.Usage, error) {
	key, err := a.storedKey(ctx, id)
	if err != nil {
		return nil, err
	}
	models, err := a.history.DescendantUsage(ctx, key)
	if err != nil {
		return nil, err
	}
	out := map[string]platforms.Usage{}
	config := a.storedConfig("")
	for id, rows := range models {
		if _, err := a.storedIdentity(ctx, id); err != nil {
			return nil, err
		}
		var total platforms.Usage
		for _, row := range rows {
			total.Tokens.Input += row.Input
			total.Tokens.Output += row.Output
			total.Tokens.CacheRead += row.CacheRead
			total.Tokens.CacheWrite += row.CacheWrite
			total.Cost += row.Paid + row.Unpaid
			if config.Pricing != nil && row.Model != "" {
				model := row.Model
				if row.Provider != "" {
					model = row.Provider + "/" + model
				}
				total.EstCost += config.Pricing.CalcCost(model, row.Input, row.Output, row.CacheRead, row.CacheWrite)
			}
		}
		out[id] = total
	}
	return out, nil
}
