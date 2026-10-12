package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/state"
)

func (f *fakeFactoryService) StopEpic(ctx context.Context, epicID string) error {
	return f.CloseEpic(ctx, epicID, true)
}

func TestFactoryControlUpgradesExistingInstructionsOnce(t *testing.T) {
	f := newControlFixture(t)
	control, _, err := f.srv.ensureFactoryControlSession(t.Context(), "epic-1")
	if err != nil {
		t.Fatal(err)
	}
	control.ProtocolVersion = 0
	if _, err := f.srv.stateDB.SetFactoryControlSession(t.Context(), control, time.Now()); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		current, created, err := f.srv.ensureFactoryControlSession(t.Context(), "epic-1")
		if err != nil || created || current.ProtocolVersion != factoryControlProtocolVersion {
			t.Fatalf("upgrade = %#v, %v, %v", current, created, err)
		}
	}
	queued, err := f.srv.stateDB.ListQueuedMessages(t.Context(), control.Platform, control.SessionID)
	if err != nil || len(queued) != 1 || !strings.Contains(queued[0].Text, "replace the previous control protocol") || !strings.Contains(queued[0].Text, "stop immediately") || f.creates != 1 {
		t.Fatalf("upgrade messages = %#v, %v", queued, err)
	}
	stored, _, err := f.srv.stateDB.GetFactoryControlSession(t.Context(), "epic-1")
	if err != nil || stored != (state.FactoryControlSession{EpicID: "epic-1", Platform: control.Platform, SessionID: control.SessionID, ProtocolVersion: factoryControlProtocolVersion}) {
		t.Fatalf("stored = %#v, %v", stored, err)
	}
}

func TestFactoryControlOwnsAllEpicDecisions(t *testing.T) {
	for _, tc := range []struct {
		action string
		args   map[string]string
	}{
		{"claim_plan", map[string]string{"issue_id": "plan-1"}},
		{"materialize", map[string]string{"issue_id": "materialize-1"}},
		{"decide_plan", map[string]string{"decision": "approve", "expected_revision": "3", "expected_hash": "hash", "implementation_model": "provider/model", "response": "Ship it"}},
		{"resolve_authority", map[string]string{"authority_gate_id": "authority-1", "decision": "approve"}},
		{"resolve_project", map[string]string{"project_gate_id": "project-1", "decision": "approve", "acknowledge_local_execution": "true", "response": "Use this project"}},
	} {
		t.Run(tc.action, func(t *testing.T) {
			f := newControlFixture(t)
			f.svc.issues = append(f.svc.issues, factory.Issue{ID: "authority-1", Authority: &factory.AuthorityEscalationGate{IssueID: "authority-1"}}, factory.Issue{ID: "project-1", ProjectRequest: &factory.ProjectRequestGate{IssueID: "project-1"}})
			id, err := f.srv.proposeFactoryControl(t.Context(), "epic-1", tc.action, tc.args, "Allow "+tc.action)
			if err != nil {
				t.Fatal(err)
			}
			if f.svc.gateAction != "" || f.svc.authorityGateID != "" || f.svc.projectGateID != "" || f.svc.claimIssueID != "" || f.svc.materializeIssueID != "" {
				t.Fatal("decision ran before confirmation")
			}
			got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control-actions/"+id+"/confirm")
			if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"status":"done"`) {
				t.Fatalf("confirm = %d %s", got.Code, got.Body.String())
			}
			switch tc.action {
			case "claim_plan":
				if f.svc.claimIssueID != "plan-1" {
					t.Fatal("plan not started")
				}
			case "materialize":
				if f.svc.materializeIssueID != "materialize-1" {
					t.Fatal("plan not materialized")
				}
			case "decide_plan":
				if f.svc.gateRequest.Actor != "user" || f.svc.gateRequest.ExpectedRevision != 3 || f.svc.gateRequest.ExpectedHash != "hash" || f.svc.gateRequest.ImplementationModel != "provider/model" || f.svc.gateRequest.Feedback != "Ship it" {
					t.Fatalf("plan request = %#v", f.svc.gateRequest)
				}
			case "resolve_authority":
				if f.svc.authorityGateID != "authority-1" || f.svc.authorityAction != "approve" {
					t.Fatal("authority not resolved")
				}
			case "resolve_project":
				if f.svc.projectGateID != "project-1" || !f.svc.projectGateAck || f.svc.projectGateResponse != "Use this project" {
					t.Fatal("project not resolved")
				}
			}
		})
	}
}

func TestFactoryControlDecisionValidation(t *testing.T) {
	f := newControlFixture(t)
	for _, tc := range []struct {
		action string
		args   map[string]string
	}{
		{"decide_plan", map[string]string{"decision": "approve", "expected_revision": "0", "expected_hash": "hash"}},
		{"decide_plan", map[string]string{"decision": "maybe", "expected_revision": "1", "expected_hash": "hash"}},
		{"resolve_authority", map[string]string{"authority_gate_id": "gate", "decision": "maybe"}},
		{"resolve_project", map[string]string{"project_gate_id": "gate", "decision": "approve", "acknowledge_local_execution": "maybe"}},
	} {
		if _, err := f.srv.proposeFactoryControl(t.Context(), "epic-1", tc.action, tc.args, "Do it"); !errors.Is(err, factory.ErrInvalidRequest) {
			t.Fatalf("%s accepted invalid args: %v", tc.action, err)
		}
	}
	for _, action := range []string{"resolve_authority", "resolve_project"} {
		err := f.srv.runFactoryControlDecision(t.Context(), "epic-1", action, map[string]string{"authority_gate_id": "foreign", "project_gate_id": "foreign", "decision": "approve"})
		if !errors.Is(err, factory.ErrInvalidRequest) || f.svc.authorityGateID != "" || f.svc.projectGateID != "" {
			t.Fatalf("foreign gate accepted: %v", err)
		}
	}
}

func TestFactoryControlRetiresAlternativeProposals(t *testing.T) {
	f := newControlFixture(t)
	old, err := f.srv.proposeFactoryControl(t.Context(), "epic-1", "resume", nil, "Retry the loop")
	if err != nil {
		t.Fatal(err)
	}
	stop, err := f.srv.proposeFactoryControl(t.Context(), "epic-1", "stop", nil, "End the loop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.decideFactoryControlAction(t.Context(), f.srv.stateDB, "epic-1", stop, "confirm"); err != nil {
		t.Fatal(err)
	}
	previous, _, err := f.srv.stateDB.GetFactoryControlAction(t.Context(), "epic-1", old)
	if err != nil || previous.Status != "dismissed" || !strings.Contains(previous.Result, "Superseded") {
		t.Fatalf("alternative = %#v, %v", previous, err)
	}
	if _, err := f.srv.decideFactoryControlAction(t.Context(), f.srv.stateDB, "epic-1", old, "confirm"); err == nil || f.svc.pausedEpicID != "" {
		t.Fatal("superseded proposal ran")
	}
}

type closingControlFactory struct{ *fakeFactoryService }

func (f closingControlFactory) StopEpic(ctx context.Context, id string) error {
	f.epics[0].Status = "closed"
	return f.fakeFactoryService.StopEpic(ctx, id)
}

func TestFactoryControlGetsFinalOutcomeAfterStop(t *testing.T) {
	f := newControlFixture(t)
	if _, _, err := f.srv.ensureFactoryControlSession(t.Context(), "epic-1"); err != nil {
		t.Fatal(err)
	}
	f.srv.factory = closingControlFactory{f.svc}
	id, err := f.srv.proposeFactoryControl(t.Context(), "epic-1", "stop", nil, "End the loop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.srv.decideFactoryControlAction(t.Context(), f.srv.stateDB, "epic-1", id, "confirm"); err != nil {
		t.Fatal(err)
	}
	if f.creates != 1 || !f.delivered(t, "The Epic is closed") || !f.delivered(t, "unfinished work cancelled") {
		t.Fatal("final outcome lost or control relaunched")
	}
}

func TestFactoryControlCanStopWithoutDelivery(t *testing.T) {
	f := newControlFixture(t)
	id, err := f.srv.proposeFactoryControl(t.Context(), "epic-1", "stop", nil, "Stop and leave unfinished work cancelled")
	if err != nil {
		t.Fatal(err)
	}
	if f.svc.closedEpicID != "" {
		t.Fatal("stop ran before confirmation")
	}
	got := f.call(http.MethodPost, "/api/factory/epics/epic-1/control-actions/"+id+"/confirm")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"status":"done"`) || f.svc.closedEpicID != "epic-1" || !f.svc.closedEpicForce {
		t.Fatalf("stop = %d %s", got.Code, got.Body.String())
	}
}
