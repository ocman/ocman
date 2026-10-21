package platforms

import "context"

// PluginSessionCompatibility projects only session.v1 wire correlations and
// opaque completion/notice deduplication prefixes. Never use its result to route
// an application session operation or to build a public link.
type PluginSessionCompatibility interface {
	PluginSessionReference(context.Context, string) (string, error)
}
