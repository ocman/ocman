package hostsvc

import "context"

// ManagedRootReader is the owner-local read-only view of managed project
// identity. Replacement callbacks execute on the owner and use Router.Local(),
// including replacements requested over RPC; remote projections need no reader.
type ManagedRootReader interface {
	ManagedOpencodeRoot(context.Context, string) (string, error)
}
