package state

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPermissionReadUpgradeRetainsMalformedLegacyPayloads(t *testing.T) {
	path := templateDBPath(t)
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, index := range []string{"inbox_permission_identity", "inbox_permission_invalid", "inbox_permission_pending", "permission_lifecycle_requested", "database_size_sample_window", "webhook_delivery_retention", "webhook_dispatch_queued_routine", "factory_project_request_plan"} {
		if _, err := d.db.Exec("DROP INDEX " + index); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.db.Exec(`DELETE FROM schema_version WHERE version>131;
		INSERT INTO inbox_item(id,title,body,created_at,category,permission_json,session_json)
		VALUES('bad','old','retained',1,'permission','{','')`); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatalf("index migration rejected an existing malformed payload: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.SessionPermissionHistory(t.Context(), "p", []string{"s"}); err == nil {
		t.Fatal("history silently hid malformed audit data")
	}
	if _, err := d.PendingInboxPermissions(t.Context()); err == nil {
		t.Fatal("reconciliation silently hid malformed permission data")
	}
	var raw string
	if err := d.db.QueryRow(`SELECT permission_json FROM inbox_item WHERE id='bad'`).Scan(&raw); err != nil || raw != "{" {
		t.Fatalf("migration changed retained data: %q, %v", raw, err)
	}
	if plan := stateQueryPlan(t, d.db, invalidPermissionHistorySQL); !strings.Contains(plan, "inbox_permission_invalid") {
		t.Fatal(plan)
	}
}

func TestPermissionHistoryPreservesStringIdentitySemantics(t *testing.T) {
	d := openTestStateDB(t)
	for i, raw := range []string{
		`{"platform":"1","sessionId":"2","permissionId":"text"}`,
		`{"platform":1,"sessionId":"2","permissionId":"numeric-platform"}`,
		`{"platform":"1","sessionId":2,"permissionId":"numeric-session"}`,
		`{"platform":null,"sessionId":"2","permissionId":"null"}`,
		`{"platform":true,"sessionId":"2","permissionId":"boolean"}`,
		`{"sessionId":"2","permissionId":"missing"}`,
	} {
		if _, err := d.db.Exec(`INSERT INTO inbox_item(id,title,body,created_at,category,permission_json,session_json) VALUES(?, 'permission', 'body', ?, 'permission', ?, '')`, i, i, raw); err != nil {
			t.Fatal(err)
		}
	}
	history, err := d.SessionPermissionHistory(t.Context(), "1", []string{"2"})
	if err != nil || len(history) != 1 || history[0].PermissionID != "text" {
		t.Fatalf("history = %+v, %v", history, err)
	}
	if _, err := d.db.Exec(`UPDATE inbox_item SET permission_json='{"platform":"1","sessionId":"2","permissionId":99}' WHERE id='0'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SessionPermissionHistory(t.Context(), "1", []string{"2"}); err == nil {
		t.Fatal("typed payload corruption was hidden")
	}
}

func TestPendingInboxPermissionsProjection(t *testing.T) {
	d := openTestStateDB(t)
	ctx := t.Context()
	want := InboxPermission{Platform: "p", SessionID: "s", PermissionID: "pending", Permission: "read", Patterns: []string{"a"}, Metadata: map[string]any{"command": "cat a"}}
	for _, permission := range []InboxPermission{want, {Platform: "p", SessionID: "s", PermissionID: "archived", Permission: "read"}} {
		if err := d.EnsurePermissionInboxItem(ctx, permission); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.ArchiveInboxItems(ctx, []string{permissionInboxID("p", "s", "archived")}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO inbox_item(id,title,body,created_at,category,permission_json,session_json)
		VALUES('unrelated','general','body',1,'general','','{'),('null','null','body',2,'permission','null','')`); err != nil {
		t.Fatal(err)
	}
	got, err := d.PendingInboxPermissions(ctx)
	if err != nil || !reflect.DeepEqual(got, []InboxPermission{want}) {
		t.Fatalf("permissions = %#v, %v", got, err)
	}
	plan := stateQueryPlan(t, d.db, pendingInboxPermissionsSQL)
	if !strings.Contains(plan, "inbox_permission_pending") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal(plan)
	}
}

func TestPermissionReadsReportStorageErrors(t *testing.T) {
	for _, mode := range []string{"cancelled", "closed", "missing-table"} {
		t.Run(mode, func(t *testing.T) {
			d := openTestStateDB(t)
			ctx := t.Context()
			switch mode {
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "closed":
				if err := d.Close(); err != nil {
					t.Fatal(err)
				}
			case "missing-table":
				if _, err := d.db.Exec(`DROP TABLE inbox_item`); err != nil {
					t.Fatal(err)
				}
			}
			_, historyErr := d.SessionPermissionHistory(ctx, "p", []string{"s"})
			_, pendingErr := d.PendingInboxPermissions(ctx)
			if historyErr == nil || pendingErr == nil {
				t.Fatalf("storage failures hidden: %v, %v", historyErr, pendingErr)
			}
			if mode == "cancelled" && (!errors.Is(historyErr, context.Canceled) || !errors.Is(pendingErr, context.Canceled)) {
				t.Fatalf("cancellation lost: %v, %v", historyErr, pendingErr)
			}
		})
	}
}

func TestArchiveMembershipIsPlatformScoped(t *testing.T) {
	d := openTestStateDB(t)
	if err := d.ArchiveSession(t.Context(), "p", "s", 42); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		platform, id string
		want         bool
	}{{"p", "s", true}, {"other", "s", false}, {"p", "other", false}, {"", "s", false}} {
		got, err := d.IsSessionArchived(t.Context(), tt.platform, tt.id)
		if err != nil || got != tt.want {
			t.Fatalf("archive %q/%q = %v, %v", tt.platform, tt.id, got, err)
		}
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.IsSessionArchived(t.Context(), "p", "s"); err == nil {
		t.Fatal("archive read error hidden")
	}
}
