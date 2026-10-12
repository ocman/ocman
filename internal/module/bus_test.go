package module

import (
	"context"
	"testing"
)

func TestBusDeliversInOrderAndSurvivesPanics(t *testing.T) {
	var b Bus
	var got []string
	b.OnSessionIdle(func(_ context.Context, e SessionIdle) { got = append(got, "first:"+e.SessionID) })
	b.OnSessionIdle(func(context.Context, SessionIdle) { panic("boom") })
	b.OnSessionIdle(func(_ context.Context, e SessionIdle) { got = append(got, "last:"+e.Platform) })
	b.PublishSessionIdle(t.Context(), SessionIdle{Platform: "opencode", SessionID: "s1"})
	if len(got) != 2 || got[0] != "first:s1" || got[1] != "last:opencode" {
		t.Fatalf("delivered %v", got)
	}
}
