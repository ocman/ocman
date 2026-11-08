package local

import (
	"context"
	"os"
)

// CreateOneOffDirectory leaves workspace contents intact for later session visits.
// The operating system owns temporary-directory cleanup, not session archival.
func (h *Host) CreateOneOffDirectory(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return os.MkdirTemp("", "ocman-one-off-")
}
