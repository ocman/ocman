package state

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

func TestBulkSessionRekeyPreservesOwnerAndOpaquePayloads(t *testing.T) {
	d := openTestStateDB(t)
	mapping := map[string]string{"old-a": "ocm_a", "old-b": "ocm_b", "old-c": "ocm_c"}
	for id := range mapping {
		for _, platform := range []string{"test", "remote:test"} {
			if err := d.EnqueueMessage(t.Context(), QueuedMessage{ID: platform + id, Platform: platform, SessionID: id, Text: id}); err != nil {
				t.Fatal(err)
			}
		}
		if err := d.EnsurePermissionInboxItem(t.Context(), InboxPermission{Platform: "test", SessionID: id,
			PermissionID: id + "-permission", Permission: "read", Metadata: map[string]any{"sessionId": id}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.RekeySessionReferences(t.Context(), "test", mapping); err != nil {
		t.Fatal(err)
	}
	for old, id := range mapping {
		for platform, session := range map[string]string{"test": id, "remote:test": old} {
			rows, err := d.ListQueuedMessages(t.Context(), platform, session)
			if err != nil || len(rows) != 1 || rows[0].Text != old || rows[0].ID != platform+old {
				t.Fatalf("queue data changed: %+v %v", rows, err)
			}
		}
	}
	items, err := d.ListInboxItems(t.Context())
	if err != nil || len(items) != len(mapping) {
		t.Fatalf("inbox data lost: %+v %v", items, err)
	}
	for _, item := range items {
		old := item.Permission.Metadata["sessionId"].(string)
		if item.Permission.SessionID != mapping[old] || item.Permission.PermissionID != old+"-permission" {
			t.Fatalf("opaque fields or correlation changed: %+v", item)
		}
	}
	got, err := d.SessionReferences(t.Context(), "test")
	want := []string{"ocm_a", "ocm_b", "ocm_c"}
	sort.Strings(got)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("references = %v %v", got, err)
	}
}

func BenchmarkSessionRekey(b *testing.B) {
	for _, count := range []int{100, 500} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				d := openTestStateDB(b)
				mapping := make(map[string]string, count)
				for i := range count {
					old := fmt.Sprintf("old-%06d", i)
					mapping[old] = fmt.Sprintf("ocm_%06d", i)
					if err := d.PinSession(b.Context(), "test", old); err != nil {
						b.Fatal(err)
					}
					if _, err := d.CreateSessionInboxItem(b.Context(), old, "body", "general", &InboxSession{Platform: "test", SessionID: old}); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
				if err := d.RekeySessionReferences(b.Context(), "test", mapping); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				_ = d.Close()
				b.StartTimer()
			}
		})
	}
}
