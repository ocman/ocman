package server

import (
	"testing"
)

// main.go wires the remote gRPC server's webhook dispatcher before Start.
// A dispatcher that only exists after Start reached the remote as a typed
// nil, which passes the poller's nil check and panics on the first delivery.
func TestRoutineServiceAvailableBeforeStart(t *testing.T) {
	srv := New(nil, openTestStateDB(t), "127.0.0.1:0", nil, nil)
	if srv.RoutineService() == nil {
		t.Fatal("RoutineService() is nil before Start")
	}
}

func TestRoutineServiceNilWithoutStateDB(t *testing.T) {
	if d := New(nil, nil, "127.0.0.1:0", nil, nil).RoutineService(); d != nil {
		t.Fatalf("RoutineService() = %#v, want untyped nil", d)
	}
}
