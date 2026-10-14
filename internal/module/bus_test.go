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

func TestBusFirstPromptDeliveryAndPanicRecovery(t *testing.T) {
	var b Bus
	var got []FirstPromptAdded
	b.OnFirstPromptAdded(func(context.Context, FirstPromptAdded) { panic("broken listener") })
	b.OnFirstPromptAdded(func(_ context.Context, e FirstPromptAdded) { got = append(got, e) })
	e := FirstPromptAdded{Platform: "claudecode", SessionID: "s", Prompt: "task"}
	b.PublishFirstPromptAdded(t.Context(), e)
	if len(got) != 1 || got[0] != e {
		t.Fatal(got)
	}
}
