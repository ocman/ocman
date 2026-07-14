package loops

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestWorkflowCompatibilityControlsTriggerAndStep(t *testing.T) {
	store := newMemStore()
	store.loops["loop_1"] = state.Loop{ID: "loop_1", State: StateActive, TriggerType: TriggerSchedule}
	var calls int
	svc := NewService(Deps{
		Store: store,
		TriggerWorkflow: func(_ context.Context, id string) error {
			if id != "loop_1" {
				t.Fatalf("workflow trigger loop = %q", id)
			}
			calls++
			return nil
		},
	})
	if err := svc.TriggerNow(context.Background(), "loop_1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Step(context.Background(), "loop_1"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("workflow trigger calls = %d, want 2", calls)
	}
	loop, err := store.GetLoop("loop_1")
	if err != nil {
		t.Fatal(err)
	}
	if loop.State != StatePaused {
		t.Fatalf("step state = %q, want %q", loop.State, StatePaused)
	}
}
