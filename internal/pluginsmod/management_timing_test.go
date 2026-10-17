package pluginsmod

import (
	"testing"
	"time"
)

func TestManagementReadinessBudget(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ready, want time.Duration
	}{
		{"default", 0, 20 * time.Second},
		{"scaled", 1500 * time.Millisecond, 2 * time.Second},
		{"negative uses default", -time.Second, 20 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModule(Deps{ReadyTimeout: tc.ready})
			if got := m.managementReadyTimeout(); got != tc.want {
				t.Fatalf("management budget = %v, want %v", got, tc.want)
			}
		})
	}
}
