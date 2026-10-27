package platforms

import (
	"errors"
	"fmt"
	"testing"
)

func TestBusyError(t *testing.T) {
	cause := errors.New("writer lease rejected")
	for _, message := range []string{"close the other session and retry", ""} {
		err := &BusyError{Message: message, Cause: cause}
		want := message
		if want == "" {
			want = ErrBusy.Error()
		}
		if err.Error() != want {
			t.Fatalf("Error = %q, want %q", err.Error(), want)
		}
		wrapped := fmt.Errorf("send failed: %w", err)
		if !errors.Is(wrapped, ErrBusy) || !errors.Is(wrapped, cause) || errors.Is(wrapped, ErrNotFound) {
			t.Fatalf("lost conflict classification or cause: %v", wrapped)
		}
	}
}
