package sessionsvc

import (
	"context"
	"errors"
	"testing"
)

func TestRoutinePublicationWaitsForAllCreatesAndHonorsCancellation(t *testing.T) {
	s := &Service{}
	first, second := s.routinePublication.begin(), s.routinePublication.begin()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.WaitForRoutineCreation(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("wait=%v", err)
	}
	first()
	if err := s.WaitForRoutineCreation(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("second create did not hold admission: %v", err)
	}
	second()
	if err := s.WaitForRoutineCreation(t.Context()); err != nil {
		t.Fatal(err)
	}
	var absent *Service
	if err := absent.WaitForRoutineCreation(t.Context()); err != nil {
		t.Fatal(err)
	}
}
