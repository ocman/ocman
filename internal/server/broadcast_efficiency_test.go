package server

import "testing"

func TestBroadcastIdenticalBacklogsShareMergeWork(t *testing.T) {
	h := newBroadcastHub()
	for range 50 {
		_, unsubscribe := h.subscribe()
		t.Cleanup(unsubscribe)
	}
	data := []byte(`{"sessionID":"s1","platform":"opencode","patch":{"status":"busy"}}`)
	for range 20 {
		h.broadcast("ocman.session.changed", data)
	}
	allocs := testing.AllocsPerRun(100, func() {
		h.broadcast("ocman.session.changed", data)
	})
	if allocs > 50 {
		t.Fatalf("50 identical backlogs allocate %.0f objects per publication, want <=50", allocs)
	}
}
