package forge

import "testing"

func TestRollUp(t *testing.T) {
	tests := []struct {
		name   string
		checks []Check
		want   CIState
	}{
		{
			name:   "no checks is unknown",
			checks: nil,
			want:   CIStateUnknown,
		},
		{
			name:   "all success",
			checks: []Check{{State: CIStateSuccess}, {State: CIStateSuccess}},
			want:   CIStateSuccess,
		},
		{
			name:   "any failure wins over success",
			checks: []Check{{State: CIStateSuccess}, {State: CIStateFailure}},
			want:   CIStateFailure,
		},
		{
			name:   "failure wins over pending",
			checks: []Check{{State: CIStatePending}, {State: CIStateFailure}},
			want:   CIStateFailure,
		},
		{
			name:   "pending when no failures and some pending",
			checks: []Check{{State: CIStateSuccess}, {State: CIStatePending}},
			want:   CIStatePending,
		},
		{
			name:   "unknown check is treated as pending",
			checks: []Check{{State: CIStateSuccess}, {State: CIStateUnknown}},
			want:   CIStatePending,
		},
		{
			name:   "single success",
			checks: []Check{{State: CIStateSuccess}},
			want:   CIStateSuccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RollUp(tt.checks); got != tt.want {
				t.Errorf("RollUp() = %q, want %q", got, tt.want)
			}
		})
	}
}
