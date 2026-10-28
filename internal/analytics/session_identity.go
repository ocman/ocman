package analytics

import (
	"context"
	"github.com/ocman/ocman/internal/db"
	"net/http"
)

func (m *Module) publicMetricsOptions(r *http.Request) db.MetricsDashboardOptions {
	opts := metricsOptions(r)
	if m.deps.SessionIdentity == nil {
		return opts
	}
	// Many requests belong to the same session. One metadata identity lookup
	// per source/session is enough for the complete dashboard computation.
	resolved := map[[2]string]string{}
	opts.SessionIdentity = func(ctx context.Context, instance, nativeID string) (string, error) {
		key := [2]string{instance, nativeID}
		if id := resolved[key]; id != "" {
			return id, nil
		}
		id, err := m.deps.SessionIdentity(ctx, instance, nativeID)
		if err == nil {
			resolved[key] = id
		}
		return id, err
	}
	return opts
}
