package platforms

import "context"

// SessionTitleNamer generates a title in an isolated helper, not the user's turn.
// Implemented on the owning machine; remote proxies leave naming to that owner.
type SessionTitleNamer interface {
	GenerateSessionTitle(context.Context, string, string) (string, error)
}
