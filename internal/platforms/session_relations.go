package platforms

import "context"

// SessionDescendantReader returns the root and its descendants without reading
// transcript content. Identifiers follow the adapter's session-ID contract.
type SessionDescendantReader interface {
	SessionDescendants(context.Context, string) ([]string, error)
}
