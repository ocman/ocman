package platforms

import "context"

// PermissionIdentityReader supplies an opaque Inbox deduplication token. The
// execution integration preserves legacy tokens without exposing native session
// IDs to notification services. Scope is the caller's owner-qualified platform.
type PermissionIdentityReader interface {
	PermissionInboxKey(ctx context.Context, scope, sessionID, permissionID string) (string, error)
}
