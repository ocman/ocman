package remote

import (
	"errors"

	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These codes belong to the identity RPCs, not arbitrary platform operations.
func identityRPCError(err error) error {
	switch {
	case errors.Is(err, sessioncatalog.ErrCreating):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, sessioncatalog.ErrCreationFailed):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, sessioncatalog.ErrBindingConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		return svcErr(err)
	}
}

func identityClientError(err error) error {
	switch status.Code(err) {
	case codes.Aborted:
		return errors.Join(sessioncatalog.ErrCreating, err)
	case codes.FailedPrecondition:
		return errors.Join(sessioncatalog.ErrCreationFailed, err)
	case codes.AlreadyExists:
		return errors.Join(sessioncatalog.ErrBindingConflict, err)
	default:
		return remotePlatformError(err)
	}
}
