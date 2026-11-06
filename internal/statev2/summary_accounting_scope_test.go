package statev2

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSummarySelectionsPreserveModelsCostsAndDeletedHistory(t *testing.T) {
	d := openTestDB(t)
	register(t, d, "second")
	for _, tc := range []struct {
		instance, id, directory string
		deleted                 bool
	}{
		{"first", "root", "/repo", false}, {"first", "deleted", "/repo", true},
		{"first", "other", "/elsewhere", false}, {"second", "root", "/repo", false},
	} {
		b := summaryBatch(tc.instance, tc.id, "")
		b.Session.Directory, b.Session.SourceDeleted = tc.directory, tc.deleted
		b.Messages = append(b.Messages,
			Message{ID: "unpaid", Role: "assistant", Position: 2, Extensions: json.RawMessage(`{"role":"assistant","providerID":"p","modelID":"model","cost":0,"tokens":{"input":11,"output":5,"cache":{"read":3,"write":4}}}`)},
			Message{ID: "refund", Role: "assistant", Position: 3, Extensions: json.RawMessage(`{"role":"assistant","providerID":"p","modelID":"model","cost":-0.25,"tokens":{"input":13,"output":6}}`)},
			Message{ID: "other-model", Role: "assistant", Position: 4, Extensions: json.RawMessage(`{"role":"assistant","providerID":"q","modelID":"model","cost":1,"tokens":{"input":17,"output":7}}`)})
		commit(t, d, b)
	}
	root, err := d.SessionSummaries(t.Context(), "first", "", "root")
	if err != nil || len(root) != 1 {
		t.Fatalf("root: %+v %v", root, err)
	}
	want := []ConversationModelTotal{
		{Provider: "p", Model: "model", Input: 24, Output: 11, CacheRead: 3, CacheWrite: 4, Unpaid: -0.25},
		{Provider: "p", Model: "model", Input: 7, Output: 3, CacheRead: 2, CacheWrite: 1, Paid: 0.5},
		{Provider: "q", Model: "model", Input: 17, Output: 7, Paid: 1},
	}
	if !reflect.DeepEqual(root[0].Totals.Models, want) || root[0].Totals.Cost != 1.25 {
		t.Fatalf("accounting changed: %+v", root[0].Totals)
	}
	for _, tc := range []struct {
		directory, id string
		count         int
	}{
		{"", "", 2}, {"/repo", "", 1}, {"/elsewhere", "", 1},
		{"", "deleted", 1}, {"/repo", "deleted", 1}, {"/elsewhere", "deleted", 0}, {"", "missing", 0},
	} {
		rows, err := d.SessionSummaries(t.Context(), "first", tc.directory, tc.id)
		if err != nil || len(rows) != tc.count {
			t.Fatalf("%+v: %+v %v", tc, rows, err)
		}
		for _, row := range rows {
			if !reflect.DeepEqual(row.Totals.Models, want) {
				t.Fatalf("scope %+v lost models: %+v", tc, row.Totals.Models)
			}
		}
	}
}
