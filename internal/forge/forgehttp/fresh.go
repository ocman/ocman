package forgehttp

import "context"

type freshReadsKey struct{}

// WithFreshReads rejects stale snapshots where successful reads advance durable
// watch state. Display-only reads may still use stale data under quota pressure.
func WithFreshReads(ctx context.Context) context.Context {
	return context.WithValue(ctx, freshReadsKey{}, true)
}

func FreshReads(ctx context.Context) bool {
	return ctx.Value(freshReadsKey{}) == true
}
