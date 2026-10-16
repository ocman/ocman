package routines

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// A cron expression that parses but never matches a real date (February 31)
// used to be saved with a zero-time next due, so it silently never fired.
func TestImpossibleCronIsRejected(t *testing.T) {
	h := newHarness(t)
	existing, err := h.svc.Create(t.Context(), validInput())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		cron string
		ok   bool
	}{
		{"february 31", "0 0 31 2 *", false},
		{"april 31", "0 0 31 4 *", false},
		{"february 30", "0 0 30 2 *", false},
		{"february 29 recurs within the search bound", "0 0 29 2 *", true},
		{"daily", "0 9 * * *", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := validInput()
			input.Name = test.name
			input.Schedule = Schedule{Kind: ScheduleCron, Cron: test.cron, Timezone: "UTC"}
			created, err := h.svc.Create(t.Context(), input)
			update := input
			update.Name = existing.Name
			_, updateErr := h.svc.Update(t.Context(), existing.ID, update)
			if test.ok {
				if err != nil || updateErr != nil || created.NextDueAt <= h.now.Load() {
					t.Fatalf("create = %+v, %v; update err = %v", created, err, updateErr)
				}
				return
			}
			for _, err := range []error{err, updateErr} {
				if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "never") {
					t.Fatalf("err = %v, want a never-fires validation error", err)
				}
			}
		})
	}
}

func TestEncodeScheduleRejectsNoNextOccurrence(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, due, err := encodeSchedule(Schedule{Kind: ScheduleCron, Cron: "0 0 31 2 *"}, now); !errors.Is(err, ErrValidation) || due != 0 {
		t.Fatalf("due = %d, err = %v", due, err)
	}
}
