package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/state"
)

type historyApprovalStore struct {
	approvals []state.ApprovedPermission
}

func (s historyApprovalStore) ListApprovedPermissions(context.Context, string, string) ([]state.ApprovedPermission, error) {
	return s.approvals, nil
}

func TestInjectApprovalNotices_HistoryWindow(t *testing.T) {
	store := historyApprovalStore{approvals: []state.ApprovedPermission{
		{PermissionID: "older", PermissionText: "bash", ApprovedAt: 100},
		{PermissionID: "boundary", PermissionText: "bash", ApprovedAt: 200},
		{PermissionID: "newer", PermissionText: "bash", ApprovedAt: 300},
	}}

	for _, tc := range []struct {
		name     string
		messages []db.Message
		want     int
	}{
		{"recent page", []db.Message{{ID: "recent", TimeCreated: 200, Data: json.RawMessage(`{"role":"assistant"}`)}}, 3},
		{"older history loaded", []db.Message{
			{ID: "recent", TimeCreated: 200, Data: json.RawMessage(`{"role":"assistant"}`)},
			{ID: "old", TimeCreated: 50, Data: json.RawMessage(`{"role":"user"}`)},
		}, 5},
		{"empty history", nil, 3},
		{"existing notice does not extend window", []db.Message{
			{ID: "notice", TimeCreated: 50, Data: json.RawMessage(`{"role":"notice"}`)},
			{ID: "recent", TimeCreated: 200, Data: json.RawMessage(`{"role":"assistant"}`)},
		}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			messages := append([]db.Message(nil), tc.messages...)
			var parts []db.Part
			injectApprovalNotices(t.Context(), "test", "session", store, &messages, &parts)
			if len(messages) != tc.want {
				t.Fatalf("messages = %d, want %d; old approvals must wait for their history page", len(messages), tc.want)
			}
		})
	}
}
