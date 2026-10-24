package opencode

import "context"

func (a *Adapter) ModelOptions(ctx context.Context) []string {
	for _, port := range a.rt.DiscoverOpenCodePortsContext(ctx) {
		_, models, err := ProjectCatalog(ctx, "http://127.0.0.1:"+port, "")
		if err == nil && len(models) > 0 {
			return models
		}
	}
	return nil
}
