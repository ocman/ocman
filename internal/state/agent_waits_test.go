package state

import (
	"reflect"
	"sort"
	"testing"
)

func TestAgentPermissionWaits(t *testing.T) {
	d := openTestStateDB(t)
	for _, row := range []PermissionLifecycle{
		{Platform: "opencode", SessionID: "manual", PermissionID: "one", RequestedAt: 1000, ResolvedAt: 4000, Resolution: PermissionResolutionUserOnce},
		{Platform: "opencode", SessionID: "judged", PermissionID: "two", RequestedAt: 1000, JudgeStartedAt: 1100, JudgeCompletedAt: 2000, ResolvedAt: 5000, EvaluationResult: PermissionEvaluationUnsafe, Resolution: PermissionResolutionUserAlways},
		{Platform: "opencode", SessionID: "safe", PermissionID: "three", RequestedAt: 1000, ResolvedAt: 3000, Resolution: PermissionResolutionAutoApproved},
		{Platform: "remote", SessionID: "foreign", PermissionID: "four", RequestedAt: 1000, ResolvedAt: 4000, Resolution: PermissionResolutionUserOnce},
		{Platform: "opencode", SessionID: "pending", PermissionID: "five", RequestedAt: 2000},
		{Platform: "opencode", SessionID: "preempted", PermissionID: "six", RequestedAt: 1000, JudgeStartedAt: 1100, JudgeCompletedAt: 4000, ResolvedAt: 3000, Resolution: PermissionResolutionUserRejected, ManuallyPreempted: true},
	} {
		if err := d.UpsertPermissionLifecycle(t.Context(), row); err != nil {
			t.Fatal(err)
		}
	}
	got, err := d.AgentUserWaits(t.Context(), "opencode", 1500, 6000)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].SessionID < got[j].SessionID })
	want := []AgentWait{{"judged", 2000, 5000}, {"manual", 1000, 4000}, {"pending", 2000, 6000}, {"preempted", 1000, 3000}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("waits %v, want %v", got, want)
	}
}

func TestAgentUserWaitLifecycle(t *testing.T) {
	d := openTestStateDB(t)
	for range 2 {
		if err := d.StartAgentUserWait(t.Context(), "opencode", "s", "question", "q", 1000); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.ResolveAgentUserWait(t.Context(), "opencode", "s", "question", "q", 2000); err != nil {
		t.Fatal(err)
	}
	if err := d.ResolveAgentUserWait(t.Context(), "opencode", "s", "question", "q", 3000); err != nil {
		t.Fatal(err)
	}
	if err := d.StartAgentUserWait(t.Context(), "opencode", "s", "question", "q", 4000); err != nil {
		t.Fatal(err)
	}
	// Resolving before observing an ask fences a replay, rather than reopening it.
	if err := d.ResolveAgentUserWait(t.Context(), "opencode", "s", "permission", "late", 2000); err != nil {
		t.Fatal(err)
	}
	if err := d.StartAgentUserWait(t.Context(), "opencode", "s", "permission", "late", 3000); err != nil {
		t.Fatal(err)
	}
	if err := d.StartAgentUserWait(t.Context(), "opencode", "s", "permission", "pending", 3000); err != nil {
		t.Fatal(err)
	}
	if err := d.ResolveSessionUserWaits(t.Context(), "opencode", "s", 4000); err != nil {
		t.Fatal(err)
	}
	got, err := d.AgentUserWaits(t.Context(), "opencode", 0, 5000)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Start < got[j].Start })
	if !reflect.DeepEqual(got, []AgentWait{{"s", 1000, 2000}, {"s", 3000, 4000}}) {
		t.Fatal(got)
	}
}
