package inbox

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	log "github.com/sirupsen/logrus"
)

var errPermissionAdapterUnavailable = errors.New("permission adapter not registered for scope and session")

func permissionIdentityWarning(err error, operation, scope, session, permission string) {
	reason := "identity_lookup_failed"
	switch {
	case errors.Is(err, errPermissionAdapterUnavailable):
		reason = "adapter_not_found"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		reason = "identity_lookup_interrupted"
	case errors.Is(err, sessioncatalog.ErrInvalid):
		reason = "invalid_identity"
	case errors.Is(err, platforms.ErrNotFound), errors.Is(err, sessioncatalog.ErrNotFound):
		reason = "identity_not_found"
	case errors.Is(err, platforms.ErrPlatformUnreachable):
		reason = "owner_unavailable"
	case errors.Is(err, platforms.ErrHistoryUnavailable):
		reason = "identity_source_unavailable"
	case errors.Is(err, sessioncatalog.ErrCreating):
		reason = "identity_not_ready"
	}
	log.WithError(err).WithFields(log.Fields{
		"operation": operation, "platform": scope, "sessionID": session,
		"permissionID": permission, "reason": reason,
	}).Warn("resolving permission Inbox identity")
}
