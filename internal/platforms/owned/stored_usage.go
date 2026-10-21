package owned

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
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

func (a *Adapter) storedUnread(ctx context.Context, cutoffs map[string]int64) (map[string]int, error) {
	groups := map[string]map[string]int64{}
	for id, cutoff := range cutoffs {
		identity, err := a.storedIdentity(ctx, id)
		if errors.Is(err, platforms.ErrNotFound) || errors.Is(err, sessioncatalog.ErrCreating) {
			continue
		} else if err != nil {
			return nil, err
		}
		if groups[identity.SourceKey] == nil {
			groups[identity.SourceKey] = map[string]int64{}
		}
		groups[identity.SourceKey][id] = cutoff
	}
	out := map[string]int{}
	for source, validated := range groups {
		instance, err := a.history.FindInstance(ctx, a.owner, a.source.Platform, source)
		if err != nil {
			return nil, err
		}
		counts, err := a.history.UnreadCounts(ctx, instance.ID, validated)
		if err != nil {
			return nil, err
		}
		for id, count := range counts {
			out[id] = count
		}
	}
	return out, nil
}
