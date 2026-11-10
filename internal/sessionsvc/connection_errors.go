package sessionsvc

import (
	"errors"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
)

// Keep connection selection errors recognizable by both the connection module
// and existing HTTP/MCP/remote session transports.
func connectionDispatchError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, platformconnections.ErrNotFound):
		return errors.Join(platforms.ErrNotFound, err)
	case errors.Is(err, platformconnections.ErrUnavailable):
		return errors.Join(platforms.ErrPlatformUnreachable, err)
	case errors.Is(err, platformconnections.ErrInvalid):
		return errors.Join(validation(err.Error()), err)
	case errors.Is(err, platformconnections.ErrSessionConflict), errors.Is(err, platformconnections.ErrCreationConflict):
		return &platforms.BusyError{Message: err.Error(), Cause: err}
	default:
		return err
	}
}
