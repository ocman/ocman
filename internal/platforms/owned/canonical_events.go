package owned

import (
	"context"
	"errors"
	"io"

	"github.com/NoUseFreak/ocman/internal/sessionview"
)

// CanonicalEventValidator validates declared session references in an
// authenticated owner's stream without allocating or translating identities.
// Message/part/request IDs and opaque provider payloads are unchanged.
func CanonicalEventValidator(ctx context.Context, out io.Writer) io.WriteCloser {
	projector := sessionview.Projector{Shape: eventProperties, RequireCanonical: true}
	return &eventWriter{ctx: ctx, out: out, projector: projector}
}

// Close validates the final boundary; it never closes the caller's destination.
func (w *eventWriter) Close() error {
	if len(w.buffer) != 0 {
		return errors.New("session event stream ended with an incomplete frame")
	}
	return nil
}
