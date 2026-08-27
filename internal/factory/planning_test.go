package factory

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

type fakePlanningLauncher struct {
	calls  []PlanningSessionRequest
	stops  []PlanningSession
	result PlanningSession
	err    error
}

func (f *fakePlanningLauncher) LaunchPlanningSession(_ context.Context, req PlanningSessionRequest) (PlanningSession, error) {
	f.calls = append(f.calls, req)
	return f.result, f.err
}

func (f *fakePlanningLauncher) StopPlanningSession(_ context.Context, session PlanningSession) error {
	f.stops = append(f.stops, session)
	return f.err
}

type fakeFactoryStore struct {
	fakeAckStore
	sessions map[string]PlanningSession
	audits   []FactoryAuditRecord
}

func (s *fakeFactoryStore) GetFactoryPlanningSession(_ context.Context, workID string) (PlanningSession, bool, error) {
	session, ok := s.sessions[workID]
	return session, ok, nil
}

func (s *fakeFactoryStore) PutFactoryPlanningSession(_ context.Context, epicID, workID string, session PlanningSession) error {
	if s.sessions == nil {
		s.sessions = map[string]PlanningSession{}
	}
	s.sessions[workID] = session
	return nil
}

func (s *fakeFactoryStore) AppendFactoryAudit(_ context.Context, record FactoryAuditRecord) error {
	s.audits = append(s.audits, record)
	return nil
}

func TestCreateWorkEpicLaunchesRepositoryScopedPlanningSessionOnce(t *testing.T) {
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope},
		{out: listEnvelope(`[]`)},
		{out: `{"schema_version":1,"data":{"ids":{"epic":"fac-1","planning":"fac-1.1","approval":"fac-1.2"}}}`},
		{out: `{"schema_version":1,"data":{}}`},
	}}
	store := &fakeFactoryStore{}
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "local-agent", ID: "session-1"}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning = launcher
	svc.owned = true

	epic, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{
		InstantiationID: "intake-1", Goal: "Ship it", InitialProject: project, AcknowledgeLocalExecution: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.calls) != 1 {
		t.Fatalf("planning launches = %d, want 1", len(launcher.calls))
	}
	want := PlanningSessionRequest{EpicID: "fac-1", WorkID: "fac-1.1", Repository: project, Title: "Plan: Ship it"}
	if !reflect.DeepEqual(launcher.calls[0], want) {
		t.Fatalf("planning launch = %#v, want %#v", launcher.calls[0], want)
	}
	if epic.Plan.Planning[0].Session != (PlanningSession{Platform: "local-agent", ID: "session-1"}) {
		t.Fatalf("planning session = %#v", epic.Plan.Planning[0].Session)
	}

	// A retry/restart adopts the durable mapping instead of launching duplicate work.
	if _, err := svc.ensurePlanningSession(context.Background(), epic, epic.Plan.Planning[0]); err != nil {
		t.Fatal(err)
	}
	if len(launcher.calls) != 1 {
		t.Fatalf("planning launches after adoption = %d, want 1", len(launcher.calls))
	}
}

func TestServiceRestartRecoversUnmappedPlanningSession(t *testing.T) {
	plan := Plan{Revision: 2, State: PlanDraft, Draft: PlanGraph{Intent: "Ship", Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: "/repo"}}}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: "/repo", Status: "in_progress"}}}
	plan.Hash = hashPlanGraph(plan.Draft)
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {},
		{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))},
	}}
	store := &fakeFactoryStore{}
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "recovered-session"}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning = launcher
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if len(launcher.calls) != 1 || store.sessions["fac-1.1"].ID != "recovered-session" {
		t.Fatalf("restart launches = %#v, sessions = %#v", launcher.calls, store.sessions)
	}
}

func TestMutatePlanRejectsStaleRevisionWithoutOverwrite(t *testing.T) {
	current := Plan{
		Revision: 3,
		State:    PlanDraft,
		Draft: PlanGraph{
			Intent:  "Current intent",
			Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: "/repo"}},
		},
	}
	current.Hash = hashPlanGraph(current.Draft)
	issues := issuesWithPlan(t, current)
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issues)}}}
	svc := newWithRunner(t.TempDir(), runner, &fakeFactoryStore{})
	svc.owned = true

	result, err := svc.MutatePlan(context.Background(), "fac-1", MutatePlanRequest{
		ExpectedRevision: 2,
		Graph:            PlanGraph{Intent: "Stale overwrite"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Stale || result.Plan.Revision != 3 || result.Plan.Draft.Intent != "Current intent" {
		t.Fatalf("mutation result = %#v", result)
	}
	if len(runner.seen) != 2 {
		t.Fatalf("stale mutation wrote Beads: %v", runner.seen)
	}
}

func TestGetPlanReadsTheCurrentRevision(t *testing.T) {
	current := Plan{Revision: 4, State: PlanDraft, Draft: PlanGraph{Intent: "Current"}}
	current.Hash = hashPlanGraph(current.Draft)
	svc := newWithRunner(t.TempDir(), &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, current))},
	}}, &fakeFactoryStore{})

	plan, err := svc.GetPlan(context.Background(), "fac-1")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Revision != current.Revision || plan.Hash != current.Hash || plan.Draft.Intent != current.Draft.Intent || len(plan.Validation) != 2 {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestMutatePlanReplacesWholeDraftAndInvalidatesApproval(t *testing.T) {
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	current := Plan{Revision: 3, State: PlanDraft, Draft: PlanGraph{Intent: "Old"}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: repository, Status: "closed", Outcome: "succeeded"}}, Approval: &PlanApproval{Revision: 3, Hash: "old"}}
	current.Hash = hashPlanGraph(current.Draft)
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, current))}, {out: `{"schema_version":1,"data":{}}`},
	}}
	store := &fakeFactoryStore{}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.owned = true

	result, err := svc.MutatePlan(context.Background(), "fac-1", MutatePlanRequest{
		ExpectedRevision: 3,
		Graph: PlanGraph{
			Intent:  "New",
			Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: repository}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stale || result.Plan.Revision != 4 || result.Plan.State != PlanDraft || result.Plan.Approval != nil {
		t.Fatalf("mutation result = %#v", result)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "plan.mutated" {
		t.Fatalf("audit = %#v", store.audits)
	}
	if len(runner.seen) != 3 || runner.seen[2][0] != "update" || runner.seen[2][1] != "fac-1" {
		t.Fatalf("commands = %v", runner.seen)
	}
}

func TestMutatePlanRequiresExplicitRevisionAfterApproval(t *testing.T) {
	current := Plan{Revision: 3, State: PlanApproved, Draft: PlanGraph{Intent: "Approved"}, Approval: &PlanApproval{Revision: 3}}
	current.Hash = hashPlanGraph(current.Draft)
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, current))}}}
	svc := newWithRunner(t.TempDir(), runner, &fakeFactoryStore{})
	svc.owned = true
	_, err := svc.MutatePlan(context.Background(), "fac-1", MutatePlanRequest{ExpectedRevision: 3, Graph: PlanGraph{Intent: "Overwrite"}})
	if !errors.Is(err, ErrPlanNotApprovable) || len(runner.seen) != 2 {
		t.Fatalf("MutatePlan error = %v, commands = %v", err, runner.seen)
	}
}

func issuesWithPlan(t *testing.T, plan Plan) string {
	t.Helper()
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]string{
		"ocman.contract": "1", "ocman.kind": "work-epic", "ocman.formula_id": DefaultFormulaID,
		"ocman.formula_version": "1", "ocman.formula_origin": "built-in", "ocman.instantiation_id": "intake-1",
		"ocman.goal": "Ship it", "ocman.initial_project": "/repo", "ocman.planning_work_id": "fac-1.1",
		"ocman.plan_approval_gate_id": "fac-1.2", planMetadataKey: string(encoded),
	}
	epicMetadata, _ := json.Marshal(metadata)
	planningStatus := "closed"
	planningOutcome := "succeeded"
	if len(plan.Planning) != 0 && plan.Planning[0].Status != "" {
		planningStatus = plan.Planning[0].Status
		planningOutcome = plan.Planning[0].Outcome
	}
	outcomeMetadata := ""
	if planningOutcome != "" {
		outcomeMetadata = `,"ocman.terminal_outcome":` + strconv.Quote(planningOutcome)
	}
	return `[{"id":"fac-1","status":"open","issue_type":"epic","metadata":` + string(epicMetadata) + `},` +
		`{"id":"fac-1.1","status":` + strconv.Quote(planningStatus) + `,"issue_type":"task","metadata":{"ocman.contract":"1","ocman.kind":"agent-work","ocman.formula_id":"ocman/default","ocman.formula_version":"1","ocman.formula_origin":"built-in","ocman.instantiation_id":"intake-1","ocman.work_epic_id":"fac-1","ocman.permission_profile":"factory-plan/v1"` + outcomeMetadata + `}},` +
		`{"id":"fac-1.2","status":"open","issue_type":"gate","metadata":{"ocman.contract":"1","ocman.kind":"gate","ocman.formula_id":"ocman/default","ocman.formula_version":"1","ocman.formula_origin":"built-in","ocman.instantiation_id":"intake-1","ocman.work_epic_id":"fac-1","ocman.gate_type":"plan-approval"}}]`
}

func TestPlanningLaunchFailureDoesNotCreateSessionMapping(t *testing.T) {
	store := &fakeFactoryStore{}
	svc := newWithRunner(t.TempDir(), &fakeRunner{}, store)
	svc.planning = &fakePlanningLauncher{err: errors.New("launch failed")}
	_, err := svc.ensurePlanningSession(context.Background(), WorkEpic{ID: "fac-1", Goal: "Ship"}, PlanningWork{ID: "work-1", Repository: "/repo"})
	if err == nil || len(store.sessions) != 0 {
		t.Fatalf("ensurePlanningSession error = %v, sessions = %#v", err, store.sessions)
	}
}

func TestAddPlanningWorkAddsTargetAndLaunchesOnlyInThatRepository(t *testing.T) {
	repository, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	current := Plan{Revision: 2, State: PlanDraft, Draft: PlanGraph{Intent: "Ship", Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: "/repo"}}}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: "/repo", Status: "closed"}}}
	current.Hash = hashPlanGraph(current.Draft)
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, current))},
		{out: `{"schema_version":1,"data":{"id":"fac-1.3"}}`},
		{out: `{"schema_version":1,"data":{}}`},
	}}
	store := &fakeFactoryStore{sessions: map[string]PlanningSession{"fac-1.1": {Platform: "agent", ID: "session-1"}}}
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "session-2"}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning, svc.owned = launcher, true

	result, err := svc.AddPlanningWork(context.Background(), "fac-1", AddPlanningWorkRequest{
		ExpectedRevision:          2,
		Target:                    PlanTarget{ID: "api", HostID: localHostID, Repository: repository},
		AcknowledgeLocalExecution: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Plan.Revision != 3 || len(result.Plan.Draft.Targets) != 2 || len(result.Plan.Planning) != 2 {
		t.Fatalf("result = %#v", result)
	}
	if len(launcher.calls) != 1 || launcher.calls[0].Repository != repository || launcher.calls[0].WorkID != "fac-1.3" {
		t.Fatalf("planning launches = %#v", launcher.calls)
	}
	if len(store.calls) != 1 || store.calls[0][1] != repository {
		t.Fatalf("local acknowledgements = %#v", store.calls)
	}
}

func TestApprovePlanRequiresSuccessfulPlanningAndExactValidGraph(t *testing.T) {
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	valid := Plan{
		Revision: 4, State: PlanDraft,
		Draft: PlanGraph{
			Intent:  "Ship",
			Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: repository, DeliveryBase: DeliveryBase{Remote: "origin", BaseBranch: "main", BaseSHA: "abc123"}}},
			Items: []PlanItem{
				{ID: "build", Kind: "agent-work", Title: "Build", TargetID: "app", Profile: "factory-implement/v1"},
				{ID: "deliver", Kind: "delivery", Title: "Deliver", TargetID: "app", Profile: "factory-deliver/v1"},
				{ID: "checks", Kind: "gate", Title: "Provider checks", TargetID: "app", GateType: "provider-check"},
				{ID: "merge", Kind: "gate", Title: "Human merge", TargetID: "app", GateType: "human-merge"},
			},
			Dependencies: []PlanDependency{{From: "deliver", To: "build"}, {From: "checks", To: "deliver"}, {From: "merge", To: "checks"}},
		},
		Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: repository, Status: "closed", Outcome: "succeeded"}},
	}
	valid.Hash = hashPlanGraph(valid.Draft)

	for _, tt := range []struct {
		name   string
		plan   Plan
		req    PlanDecisionRequest
		wantOK bool
	}{
		{name: "exact valid revision", plan: valid, req: PlanDecisionRequest{ExpectedRevision: 4, ExpectedHash: valid.Hash, Actor: "dries", AcknowledgeLocalExecution: true}, wantOK: true},
		{name: "stale hash", plan: valid, req: PlanDecisionRequest{ExpectedRevision: 4, ExpectedHash: "stale", Actor: "dries"}},
		{name: "unfinished planning", plan: func() Plan {
			p := valid
			p.Planning = []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: repository, Status: "in_progress"}}
			return p
		}(), req: PlanDecisionRequest{ExpectedRevision: 4, ExpectedHash: valid.Hash, Actor: "dries"}},
		{name: "manual close without success outcome", plan: func() Plan {
			p := valid
			p.Planning = []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: repository, Status: "closed"}}
			return p
		}(), req: PlanDecisionRequest{ExpectedRevision: 4, ExpectedHash: valid.Hash, Actor: "dries"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runs := []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, tt.plan))}}
			if tt.wantOK {
				runs = append(runs, fakeRun{out: `{"schema_version":1,"data":{}}`}, fakeRun{out: `{"schema_version":1,"data":{}}`})
			}
			runner := &fakeRunner{runs: runs}
			store := &fakeFactoryStore{}
			svc := newWithRunner(t.TempDir(), runner, store)
			svc.owned = true
			approved, err := svc.ApprovePlan(context.Background(), "fac-1", tt.req)
			if !tt.wantOK {
				if !errors.Is(err, ErrPlanNotApprovable) || len(runner.seen) != 2 {
					t.Fatalf("ApprovePlan = %#v, %v; commands=%v", approved, err, runner.seen)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if approved.State != PlanApproved || approved.Approval == nil || approved.Approval.Hash != valid.Hash || approved.Approval.Graph.Intent != "Ship" {
				t.Fatalf("approved = %#v", approved)
			}
			if len(store.audits) != 1 || store.audits[0].Action != "plan.approved" {
				t.Fatalf("audit = %#v", store.audits)
			}
		})
	}
}

func TestCompletePlanningWorkRecordsFactorySuccessBeforeClosing(t *testing.T) {
	plan := Plan{Revision: 2, State: PlanDraft, Draft: PlanGraph{Intent: "Ship", Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: "/repo"}}}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: "/repo", Status: "in_progress"}}}
	plan.Hash = hashPlanGraph(plan.Draft)
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {out: `{"schema_version":1,"data":{}}`}, {out: `{"schema_version":1,"data":{}}`}}}
	store := &fakeFactoryStore{}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.owned = true

	got, err := svc.CompletePlanningWork(context.Background(), "fac-1", "fac-1.1", CompletePlanningWorkRequest{ExpectedRevision: 2, ExpectedHash: plan.Hash, Actor: "planner"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Planning[0].Status != "closed" || got.Planning[0].Outcome != "succeeded" || len(store.audits) != 1 {
		t.Fatalf("completed Plan = %#v, audits=%#v", got, store.audits)
	}
	if len(runner.seen) != 4 || runner.seen[2][0] != "update" || runner.seen[3][0] != "close" {
		t.Fatalf("commands = %v", runner.seen)
	}
}

func TestPlanDecisionsPreserveAuditAndCancellationStopsPlanning(t *testing.T) {
	for _, action := range []string{"revise", "reject", "cancel"} {
		t.Run(action, func(t *testing.T) {
			state := PlanApproved
			if action == "reject" {
				state = PlanDraft
			}
			plan := Plan{Revision: 2, State: state, Draft: PlanGraph{Intent: "Ship", Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: "/repo"}}}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: "/repo", Status: "in_progress", Session: PlanningSession{Platform: "agent", ID: "session-1"}}}, Approval: &PlanApproval{Revision: 2, Hash: "approved"}}
			plan.Hash = hashPlanGraph(plan.Draft)
			runs := []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {out: `{"schema_version":1,"data":{}}`}, {out: `{"schema_version":1,"data":{}}`}}
			if action == "cancel" {
				runs = append(runs, fakeRun{out: `{"schema_version":1,"data":{}}`})
			}
			runner := &fakeRunner{runs: runs}
			store := &fakeFactoryStore{sessions: map[string]PlanningSession{"fac-1.1": {Platform: "agent", ID: "session-1"}}}
			launcher := &fakePlanningLauncher{}
			svc := newWithRunner(t.TempDir(), runner, store)
			svc.planning, svc.owned = launcher, true
			req := PlanDecisionRequest{ExpectedRevision: 2, ExpectedHash: plan.Hash, Actor: "dries", Reason: "change requested"}
			var got Plan
			var err error
			switch action {
			case "revise":
				got, err = svc.RevisePlan(context.Background(), "fac-1", req)
			case "reject":
				got, err = svc.RejectPlan(context.Background(), "fac-1", req)
			case "cancel":
				got, err = svc.CancelPlan(context.Background(), "fac-1", req)
			}
			if err != nil {
				t.Fatal(err)
			}
			wantAction := map[string]string{"revise": "plan.revised", "reject": "plan.rejected", "cancel": "plan.cancelled"}[action]
			if len(store.audits) != 1 || store.audits[0].Action != wantAction {
				t.Fatalf("audit = %#v", store.audits)
			}
			if action == "revise" && (got.State != PlanDraft || got.Revision != 3 || got.Approval != nil) {
				t.Fatalf("revised plan = %#v", got)
			}
			if action == "cancel" && (got.State != PlanCancelled || len(launcher.stops) != 1) {
				t.Fatalf("cancelled plan = %#v, stops=%#v", got, launcher.stops)
			}
		})
	}
}
