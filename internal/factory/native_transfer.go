package factory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type nativeTransferStore interface {
	ReserveFactoryIssueTransfer(context.Context, string, string, time.Time) (string, error)
	RecordFactoryIssueTransfer(context.Context, string, string, string) error
	ReleaseFactoryIssueTransfer(context.Context, string, string) error
	CompleteFactoryIssueTransfer(context.Context, string, string, string, time.Time) error
}

// TransferIssue moves an open ticket out of the Epic into an external
// tracker. The ticket is reserved (held from dispatch, removal dry-run) before
// create is called, and the created URL is recorded before the ticket is
// removed, so a retry after a local failure never creates a second issue.
func (s *NativeService) TransferIssue(ctx context.Context, epicID, issueID string, create func(context.Context, Issue) (string, error)) (string, error) {
	store, ok := s.store.(nativeTransferStore)
	if !ok {
		return "", ErrFactoryUnavailable
	}
	url, err := store.ReserveFactoryIssueTransfer(ctx, epicID, issueID, time.Now())
	if errors.Is(err, model.ErrInvalidGraphMutation) {
		return "", fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	if err != nil {
		return "", err
	}
	local := context.WithoutCancel(ctx)
	if url == "" {
		issues, err := s.ListIssues(ctx, epicID)
		if err != nil {
			_ = store.ReleaseFactoryIssueTransfer(local, epicID, issueID)
			return "", err
		}
		var issue Issue
		for _, candidate := range issues {
			if candidate.ID == issueID {
				issue = candidate
			}
		}
		if url, err = create(ctx, issue); err != nil {
			_ = store.ReleaseFactoryIssueTransfer(local, epicID, issueID)
			return "", err
		}
		if err := store.RecordFactoryIssueTransfer(local, epicID, issueID, url); err != nil {
			return url, err
		}
	}
	if err := store.CompleteFactoryIssueTransfer(local, epicID, issueID, url, time.Now()); err != nil {
		return url, err
	}
	s.wakeDispatch()
	return url, nil
}

// CancelIssueTransfer releases a transfer that is in flight or whose result is
// unknown, putting the ticket back in the Epic. Recorded transfers resume
// instead; they cannot be cancelled.
func (s *NativeService) CancelIssueTransfer(ctx context.Context, epicID, issueID string) error {
	store, ok := s.store.(nativeTransferStore)
	if !ok {
		return ErrFactoryUnavailable
	}
	if err := store.ReleaseFactoryIssueTransfer(ctx, epicID, issueID); err != nil {
		return err
	}
	s.wakeDispatch()
	return nil
}
