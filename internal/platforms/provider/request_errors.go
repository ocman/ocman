package provider

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func providerResponseError(code int, wire providerprotocol.Error) error {
	var err error
	switch {
	case code == http.StatusNotFound:
		err = platforms.ErrNotFound
	case code == http.StatusConflict:
		err = platforms.ErrBusy
		if wire.Message != "" {
			err = &platforms.BusyError{Message: wire.Message}
		}
		if wire.Code == providerprotocol.CodeSessionOwnedElsewhere {
			err = &platforms.BusyError{Message: wire.Message, Cause: platforms.ErrSessionOwnedElsewhere}
		}
	case code == http.StatusNotImplemented:
		err = platforms.ErrUnsupported
	case code == http.StatusServiceUnavailable && wire.Code == providerprotocol.CodeReplyUnrecorded:
		err = fmt.Errorf("%w: %s", platforms.ErrReplyUnrecorded, wire.Message)
	case code >= 400 && code < 500:
		err = &platforms.UpstreamError{Status: code, Message: wire.Message}
	default:
		err = fmt.Errorf("provider HTTP %d: %s", code, wire.Message)
	}
	if wire.Code == providerprotocol.CodeCreationRejected {
		return errors.Join(platforms.ErrCreationRejected, err)
	}
	return err
}
