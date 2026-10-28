package sessionsvc

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

func commitCreation(ctx context.Context, p platforms.Platform, id string) error {
	committer, ok := p.(platforms.SessionCreationCommitter)
	if !ok {
		return nil
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := committer.CommitCreation(writeCtx, id); err != nil {
		return &platforms.CreationPendingError{SessionID: id, Err: err}
	}
	return nil
}
