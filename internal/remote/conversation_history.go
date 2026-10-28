package remote

import (
	"errors"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Owner readiness is an application error, not a broken connection. The stable
// sentinel prefix survives the existing RPC error envelope without a protocol
// version change, and restores the HTTP 503/history-error mapping on the hub.
func conversationReadError(conn *RemoteConn, err error) error {
	if status.Code(err) == codes.NotFound {
		conn.markSeen()
		return errors.Join(platforms.ErrNotFound, err)
	}
	message := status.Convert(err).Message()
	sentinel := platforms.ErrHistoryUnavailable.Error()
	if status.Code(err) == codes.Unavailable && (message == sentinel || strings.HasPrefix(message, sentinel+":")) {
		conn.markSeen()
		return errors.Join(platforms.ErrHistoryUnavailable, err)
	}
	conn.markOffline()
	return err
}
