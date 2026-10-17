package plugins

import (
	"testing"
	"time"
)

func TestStartProcessReadinessBudget(t *testing.T) {
	for _, tc := range []struct {
		name           string
		override, want time.Duration
	}{
		{"default", 0, 15 * time.Second},
		{"scaled", 1500 * time.Millisecond, 1500 * time.Millisecond},
		{"negative uses default", -time.Second, 15 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := processFixture(t, "normal")
			config.ReadyTimeout = tc.override
			p, err := StartProcess(t.Context(), config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(p.Stop)
			if p.policy.ready != tc.want {
				t.Fatalf("handshake budget = %v, want %v", p.policy.ready, tc.want)
			}
			if defaultProcessPolicy.ready != 15*time.Second {
				t.Fatal("per-process override changed the default")
			}
		})
	}
}
