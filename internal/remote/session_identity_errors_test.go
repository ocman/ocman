package remote

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func TestIdentityDomainErrorsRoundTrip(t *testing.T) {
	for _, want := range []error{sessioncatalog.ErrCreating, sessioncatalog.ErrCreationFailed, sessioncatalog.ErrBindingConflict, platforms.ErrNotFound} {
		got := identityClientError(identityRPCError(want))
		if !errors.Is(got, want) {
			t.Fatalf("identity error lost: %v => %v", want, got)
		}
	}
	if identityClientError(identityRPCError(nil)) != nil {
		t.Fatal("nil error became a failure")
	}
}

func TestCreationCertaintyRoundTripsWithoutGuessingFromStatus(t *testing.T) {
	for _, cause := range []error{platforms.ErrUnsupported, platforms.ErrNotFound, errors.New("validation failed before allocation")} {
		rejected := errors.Join(platforms.ErrCreationRejected, cause)
		got := remotePlatformError(svcErr(rejected))
		if !errors.Is(got, platforms.ErrCreationRejected) {
			t.Fatalf("definite rejection became uncertain: %v", got)
		}
		if errors.Is(cause, platforms.ErrUnsupported) && !errors.Is(got, platforms.ErrUnsupported) {
			t.Fatalf("unsupported cause lost: %v", got)
		}
		if got := remotePlatformError(svcErr(cause)); errors.Is(got, platforms.ErrCreationRejected) {
			t.Fatalf("unmarked error assumed safe: %v", got)
		}
	}
	if remotePlatformError(nil) != nil {
		t.Fatal("nil error became a failure")
	}
}
