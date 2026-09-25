package db

import (
	"database/sql"
	"errors"
	"testing"
)

func TestGetSessionMessageStatus(t *testing.T) {
	d := openTestDB(t)
	defer d.Close()
	for _, tc := range []struct {
		name string
		data map[string]any
		want SessionStatus
	}{
		{"empty", nil, StatusDone},
		{"user", map[string]any{"role": "user"}, StatusDone},
		{"unfinished", map[string]any{"role": "assistant"}, StatusBusy},
		{"finished", map[string]any{"role": "assistant", "finish": "stop"}, StatusWaiting},
		{"error", map[string]any{"role": "assistant", "error": map[string]any{"name": "Failure"}}, StatusError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			insertSession(t, d, tc.name, "test", "/repo", 1, 2)
			if tc.data != nil {
				insertMessage(t, d, tc.name+"-a", tc.name, 1, map[string]any{"role": "user"})
				// Same timestamp: the ID is the deterministic tie-breaker.
				insertMessage(t, d, tc.name+"-z", tc.name, 1, tc.data)
			}
			got, err := d.GetSessionMessageStatus(t.Context(), tc.name)
			if err != nil || got != tc.want {
				t.Fatalf("status=%q err=%v, want %q", got, err, tc.want)
			}
		})
	}
	if _, err := d.GetSessionMessageStatus(t.Context(), "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing session error=%v", err)
	}
}
