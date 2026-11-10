package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
)

func TestConnectionErrorsRetainTransportSemantics(t *testing.T) {
	for _, tc := range []struct{ input, want error }{
		{platformconnections.ErrNotFound, platforms.ErrNotFound},
		{platformconnections.ErrUnavailable, platforms.ErrPlatformUnreachable},
		{platformconnections.ErrSessionConflict, platforms.ErrBusy},
		{platformconnections.ErrCreationConflict, platforms.ErrBusy},
		{context.Canceled, context.Canceled},
	} {
		got := connectionDispatchError(tc.input)
		if !errors.Is(got, tc.want) || !errors.Is(got, tc.input) {
			t.Fatalf("lost error meaning: %v, want %v and %v", got, tc.want, tc.input)
		}
	}
	var invalid *ValidationError
	if err := connectionDispatchError(platformconnections.ErrInvalid); !errors.As(err, &invalid) || !errors.Is(err, platformconnections.ErrInvalid) {
		t.Fatalf("invalid connection input is not a validation error: %v", err)
	}
	if err := connectionDispatchError(nil); err != nil {
		t.Fatal(err)
	}
}
