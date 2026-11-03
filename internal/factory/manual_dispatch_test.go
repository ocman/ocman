package factory

import "context"

// Dispatch drives domain tests synchronously without starting a worker.
// Production mutations only wake the lifecycle-controlled dispatch loop.
func (s *NativeService) Dispatch(ctx context.Context) error {
	return s.dispatch(ctx, nil)
}
