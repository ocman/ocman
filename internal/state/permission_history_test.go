package state

import "testing"

func TestPermissionHistoryPreservesPromptWhenReplyArrivesFirst(t *testing.T) {
	d := openTestDB(t)
	ctx := t.Context()
	request := InboxPermission{Platform: "p", SessionID: "s", PermissionID: "fast", Permission: "bash", Patterns: []string{"tea do something"}, Always: []string{"tea do *"}, Metadata: map[string]any{"command": "tea do something"}}
	if err := d.ResolvePermissionInboxItem(ctx, "p", "s", "fast"); err != nil {
		t.Fatal(err)
	}
	if err := d.EnsurePermissionInboxItem(ctx, request); err != nil {
		t.Fatal(err)
	}
	history, err := d.SessionPermissionHistory(ctx, "p", []string{"s"})
	if err != nil || len(history) != 1 {
		t.Fatalf("fast reply lost history: %+v, %v", history, err)
	}
	if len(history[0].Always) != 1 || history[0].Always[0] != "tea do *" || history[0].ResolvedAt == 0 {
		t.Fatalf("original request scope lost: %+v", history[0])
	}
	active, err := d.ListInboxItems(ctx)
	if err != nil || len(active) != 0 {
		t.Fatalf("resolved notification reopened: %+v, %v", active, err)
	}
}

func TestSessionPermissionHistory(t *testing.T) {
	d := openTestDB(t)
	ctx := t.Context()
	if got, err := d.SessionPermissionHistory(ctx, "p", nil); err != nil || len(got) != 0 {
		t.Fatalf("empty: %+v %v", got, err)
	}
	for _, request := range []InboxPermission{
		{Platform: "p", SessionID: "s", PermissionID: "pending", Permission: "bash", Patterns: []string{"tea do something"}},
		{Platform: "p", SessionID: "child", PermissionID: "rejected", Permission: "bash", Patterns: []string{"tea do delete"}},
		{Platform: "other", SessionID: "s", PermissionID: "foreign", Permission: "edit"},
		{Platform: "p", SessionID: "other", PermissionID: "foreign", Permission: "edit"},
	} {
		if err := d.EnsurePermissionInboxItem(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.RecordApprovedPermission(ctx, "p", "s", ApprovedPermission{PermissionID: "old-approval", PermissionText: "edit", Patterns: []string{"file.go"}, ApprovedBy: ApprovalActorUser, Reply: ApprovalReplyOnce, ApprovedAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := d.RecordApprovedPermission(ctx, "p", "s", ApprovedPermission{PermissionID: "pending", PermissionText: "bash", Patterns: []string{"tea do something"}, ApprovedAt: 2}); err != nil {
		t.Fatal(err)
	}
	if err := d.UpsertPermissionLifecycle(ctx, PermissionLifecycle{Platform: "p", SessionID: "child", PermissionID: "rejected", ResolvedAt: 3, Resolution: PermissionResolutionUserRejected}); err != nil {
		t.Fatal(err)
	}
	history, err := d.SessionPermissionHistory(ctx, "p", []string{"s", "child"})
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 {
		t.Fatalf("history = %+v", history)
	}
	resolutions := map[string]string{}
	for _, entry := range history {
		resolutions[entry.PermissionID] = entry.Resolution
	}
	if resolutions["old-approval"] != "user-once" || resolutions["pending"] != "ai-once" || resolutions["rejected"] != "user-rejected" {
		t.Fatalf("resolutions = %+v", resolutions)
	}
}
