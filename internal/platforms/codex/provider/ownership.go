package codexprovider

import (
	"errors"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/ocman/ocman/internal/platforms"
)

func threadLoadError(err error) error {
	var rpc *acp.RequestError
	if errors.As(err, &rpc) && rpc.Code == -32600 && strings.Contains(rpc.Message, "already has an active writer") {
		return &platforms.BusyError{
			Message: "Another Codex process owns this thread. Close the session in that process and retry.",
			Cause:   errors.Join(platforms.ErrSessionOwnedElsewhere, err),
		}
	}
	return err
}
