package factory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type nativeTransferStore interface {
	ReserveFactoryIssueTransfer(context.Context, string, string, time.Time) (string, string, error)
	RecordFactoryIssueTransfer(context.Context, string, string, string, string) error
	ReleaseFactoryIssueTransfer(context.Context, string, string, string) error
	CompleteFactoryIssueTransfer(context.Context, string, string, string, time.Time) error
}

// ErrTrackerUncertain marks a create call that may have reached the tracker.
// Its reservation stays pending until the user checks and cancels it.
var ErrTrackerUncertain = errors.New("the tracker may have created the issue")

// errTransferLive refuses to cancel a transfer whose create call still runs.
var errTransferLive = fmt.Errorf("%w: the transfer is still running", ErrInvalidRequest)

// TransferIssue moves an open ticket out of the Epic into an external
// tracker. The ticket is reserved (held from dispatch, removal dry-run) before
// create is called, and the created URL is recorded before the ticket is
// removed, so a retry after a local failure never creates a second issue.
func (s *NativeService) TransferIssue(ctx context.Context, epicID, issueID string, create func(context.Context, Issue) (string, error)) (string, error) {
	store, ok := s.store.(nativeTransferStore)
	if !ok {
		return "", ErrFactoryUnavailable
	}
	// Reserving and registering the live call are one step under the lock, as
	// are Cancel's live check and release, so Cancel can never release a
	// reservation whose tracker call is about to start.
	s.transfersMu.Lock()
	url, claim, err := store.ReserveFactoryIssueTransfer(ctx, epicID, issueID, time.Now())
	if err == nil && url == "" {
		if s.liveTransfers == nil {
			s.liveTransfers = map[string]bool{}
		}
		s.liveTransfers[issueID] = true
	}
	s.transfersMu.Unlock()
	if errors.Is(err, model.ErrInvalidGraphMutation) {
		return "", fmt.Errorf("%w: %w", ErrInvalidRequest, err)
	}
	if err != nil {
		return "", err
	}
	local := context.WithoutCancel(ctx)
	if url == "" {
		defer func() {
			s.transfersMu.Lock()
			delete(s.liveTransfers, issueID)
			s.transfersMu.Unlock()
		}()
		issues, err := s.ListIssues(ctx, epicID)
		if err != nil {
			_ = store.ReleaseFactoryIssueTransfer(local, epicID, issueID, claim)
			return "", err
		}
		var issue Issue
		for _, candidate := range issues {
			if candidate.ID == issueID {
				issue = candidate
			}
		}
		if url, err = create(ctx, issue); err != nil {
			if !errors.Is(err, ErrTrackerUncertain) {
				_ = store.ReleaseFactoryIssueTransfer(local, epicID, issueID, claim)
			}
			return "", err
		}
		if err := store.RecordFactoryIssueTransfer(local, epicID, issueID, claim, url); err != nil {
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
	s.transfersMu.Lock()
	defer s.transfersMu.Unlock()
	if s.liveTransfers[issueID] {
		return errTransferLive
	}
	if err := store.ReleaseFactoryIssueTransfer(ctx, epicID, issueID, ""); err != nil {
		return err
	}
	s.wakeDispatch()
	return nil
}
