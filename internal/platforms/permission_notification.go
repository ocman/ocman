package platforms

import (
	"context"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms/identity"
)

// PermissionNotificationKey keeps legacy deduplication inside the integration.
// A missing canonical compatibility reader must not mint a replacement key.
func PermissionNotificationKey(ctx context.Context, adapter Platform, scope, session, permission string) (string, error) {
	if reader, ok := adapter.(PermissionIdentityReader); ok {
		return reader.PermissionInboxKey(ctx, scope, session, permission)
	}
	if identity.IsSessionID(session) {
		return "", ErrHistoryUnavailable
	}
	return "permission-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope+"\x00"+session+"\x00"+permission)).String(), nil
}
