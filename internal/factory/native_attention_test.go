package factory

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestFactoryAttention(t *testing.T) {
	for _, tc := range []struct {
		name  string
		issue Issue
		want  bool
	}{
		{"planning", Issue{Kind: "plan", DispatchState: "ready"}, true},
		{"materialization", Issue{Kind: "materialization", DispatchState: "ready"}, true},
		{"approval", Issue{Kind: "approval", DispatchState: "ready"}, true},
		{"auto work", Issue{Kind: "task", DispatchState: "ready"}, false},
		{"running", Issue{Kind: "plan", DispatchState: "running"}, false},
		{"retry", Issue{Kind: "task", DispatchState: "retry_wait"}, false},
		{"failed", Issue{Kind: "task", Status: "closed", Outcome: "failed"}, true},
		{"cancelled", Issue{Kind: "delivery", Status: "closed", Outcome: "cancelled"}, true},
		{"blocked", Issue{DispatchState: "terminally_blocked"}, true},
		{"recovery", Issue{Recovery: &RecoveryGate{Resolution: "open"}}, true},
		{"resolved recovery", Issue{Recovery: &RecoveryGate{Resolution: "resume"}}, false},
		{"authority", Issue{Authority: &AuthorityEscalationGate{Resolution: "approve_pending"}}, true},
		{"resolved authority", Issue{Authority: &AuthorityEscalationGate{Resolution: "approve"}}, false},
		{"project", Issue{ProjectRequest: &ProjectRequestGate{Resolution: "open"}}, true},
		{"resolved project", Issue{ProjectRequest: &ProjectRequestGate{Resolution: "approved"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.issue.ID, tc.issue.Title = "ship.1", "Do work"
			got := factoryAttention(WorkEpic{ID: "ship", Status: "open"}, []Issue{tc.issue})
			if (len(got) > 0) != tc.want {
				t.Fatalf("actions = %#v", got)
			}
			for _, body := range got {
				if !strings.Contains(body, "/factory/epics/ship") {
					t.Fatal(body)
				}
			}
			if len(factoryAttention(WorkEpic{Status: "closed"}, []Issue{tc.issue})) != 0 {
				t.Fatal("closed epic notified")
			}
		})
	}
	ep := WorkEpic{ID: "ship", Status: "open", PlanGate: &PlanGate{IssueID: "gate", ProposalRevision: 1, ProposalHash: "hash", Resolution: "open"}}
	first := factoryAttention(ep, nil)
	ep.PlanGate.ProposalRevision++
	for key := range factoryAttention(ep, nil) {
		if _, exists := first[key]; exists {
			t.Fatal("revision identity reused")
		}
	}
	ep.PlanGate.Resolution = "approved"
	if len(factoryAttention(ep, nil)) != 0 {
		t.Fatal("approved plan notified")
	}
	ep.Progress.Stuck = true
	if len(factoryAttention(ep, nil)) != 1 {
		t.Fatal("stuck epic missing")
	}
}

type attentionErrorStore struct {
	nativeStore
	listErr, issueErr, syncErr error
	issueReads, syncs          int
}

func (s *attentionErrorStore) ListFactoryEpics(context.Context) ([]model.NativeEpic, error) {
	return []model.NativeEpic{{ID: "ship", Status: "open"}}, s.listErr
}
func (s *attentionErrorStore) ListFactoryIssues(context.Context, string) ([]model.NativeIssue, error) {
	s.issueReads++
	// ListWorkEpics reads progress first, then the attention scan reads details.
	if s.issueReads > 1 {
		return nil, s.issueErr
	}
	return nil, nil
}
func (s *attentionErrorStore) SyncFactoryActionInbox(context.Context, string, string, map[string]string) error {
	s.syncs++
	return s.syncErr
}
func TestFactoryAttentionReadErrorsPreserveInbox(t *testing.T) {
	NewNative(&nativeStoreFake{}).reconcileAttention(t.Context())
	for _, tc := range []struct {
		name                       string
		listErr, issueErr, syncErr error
		syncs                      int
	}{
		{"epics unavailable", errors.New("offline"), nil, nil, 0},
		{"issues unavailable", nil, errors.New("offline"), nil, 0},
		{"inbox unavailable", nil, nil, errors.New("offline"), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &attentionErrorStore{listErr: tc.listErr, issueErr: tc.issueErr, syncErr: tc.syncErr}
			NewNative(store).reconcileAttention(t.Context())
			if store.syncs != tc.syncs {
				t.Fatalf("syncs = %d, want %d", store.syncs, tc.syncs)
			}
		})
	}
}

func TestReconcileFactoryAttention(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNative(db, testProjectResolver{root: "/repo"})
	ep, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{EpicID: "ship", Goal: "Ship", InitialProject: "/repo", InstantiationID: "once", AcknowledgeLocalExecution: true})
	if err != nil {
		t.Fatal(err)
	}
	svc.reconcileAttention(t.Context())
	items, err := db.ListInboxItems(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v, %v", items, err)
	}
	if err := db.CloseFactoryEpic(t.Context(), ep.ID, true); err != nil {
		t.Fatal(err)
	}
	svc.reconcileAttention(t.Context())
	items, err = db.ListInboxItems(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("closed items = %#v, %v", items, err)
	}
}

func TestFactoryAttentionSurvivesPauseResume(t *testing.T) {
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNative(db, testProjectResolver{root: "/repo"})
	ep, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{EpicID: "ship", Goal: "Ship", InitialProject: "/repo", InstantiationID: "pause-resume", AcknowledgeLocalExecution: true})
	if err != nil {
		t.Fatal(err)
	}
	svc.reconcileAttention(t.Context())
	items, err := db.ListInboxItems(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("initial items = %#v, %v", items, err)
	}
	id := items[0].ID
	for _, paused := range []bool{true, false} {
		if err := svc.SetEpicPaused(t.Context(), ep.ID, paused); err != nil {
			t.Fatal(err)
		}
		svc.reconcileAttention(t.Context())
		items, err = db.ListInboxItems(t.Context())
		if err != nil || len(items) != 1 || items[0].ID != id {
			t.Fatalf("paused=%v: items = %#v, %v; want original pending action", paused, items, err)
		}
	}
	if err := svc.CloseEpic(t.Context(), ep.ID, true); err != nil {
		t.Fatal(err)
	}
	svc.reconcileAttention(t.Context())
	items, err = db.ListInboxItems(t.Context())
	if err != nil || len(items) != 0 {
		t.Fatalf("closed items = %#v, %v", items, err)
	}
}
