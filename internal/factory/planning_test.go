package factory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type fakePlanningLauncher struct {
	calls   []PlanningSessionRequest
	stops   []PlanningSession
	probes  []PlanningSession
	dead    map[string]bool
	result  PlanningSession
	err     error
	stopErr error
	alive   map[string]bool
}

func (f *fakePlanningLauncher) LaunchPlanningSession(_ context.Context, req PlanningSessionRequest) (PlanningSession, error) {
	f.calls = append(f.calls, req)
	if f.alive != nil && f.result.ID != "" {
		f.alive[f.result.ID] = true
	}
	return f.result, f.err
}

func (f *fakePlanningLauncher) StopPlanningSession(_ context.Context, session PlanningSession) error {
	f.stops = append(f.stops, session)
	if f.stopErr != nil {
		return f.stopErr
	}
	delete(f.alive, session.ID)
	return nil
}

func (f *fakePlanningLauncher) ProbePlanningSession(_ context.Context, session PlanningSession) (bool, error) {
	f.probes = append(f.probes, session)
	return !f.dead[session.ID], f.err
}

type fakeFactoryStore struct {
	fakeAckStore
	sessions      map[string]PlanningSession
	cleanups      map[string]PlanningSession
	audits        []FactoryAuditRecord
	putErr        error
	cleanupPutErr error
	auditErr      error
	auditFailAt   int
	auditCalls    int
}

func (s *fakeFactoryStore) AppendFactoryAuditOnce(ctx context.Context, record FactoryAuditRecord) error {
	for _, existing := range s.audits {
		if existing.EpicID == record.EpicID && existing.Action == record.Action && reflect.DeepEqual(existing.Details, record.Details) {
			return nil
		}
	}
	return s.AppendFactoryAudit(ctx, record)
}

func (s *fakeFactoryStore) GetFactoryPlanningSession(_ context.Context, workID string) (PlanningSession, bool, error) {
	session, ok := s.sessions[workID]
	return session, ok, nil
}

func (s *fakeFactoryStore) PutFactoryPlanningSession(_ context.Context, epicID, workID string, session PlanningSession) error {
	if s.putErr != nil {
		return s.putErr
	}
	if s.sessions == nil {
		s.sessions = map[string]PlanningSession{}
	}
	s.sessions[workID] = session
	return nil
}

func (s *fakeFactoryStore) DeleteFactoryPlanningSession(_ context.Context, workID string) error {
	delete(s.sessions, workID)
	return nil
}

func (s *fakeFactoryStore) PutFactoryPlanningSessionCleanup(ctx context.Context, _, workID string, session PlanningSession) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.cleanupPutErr != nil {
		return s.cleanupPutErr
	}
	if s.cleanups == nil {
		s.cleanups = map[string]PlanningSession{}
	}
	s.cleanups[workID] = session
	return nil
}

func (s *fakeFactoryStore) ListFactoryPlanningSessionCleanups(context.Context) (map[string]PlanningSession, error) {
	return s.cleanups, nil
}

func (s *fakeFactoryStore) DeleteFactoryPlanningSessionCleanup(_ context.Context, workID string) error {
	delete(s.cleanups, workID)
	return nil
}

func TestCreateWorkEpicRecordsCleanupAndDegradesWhenMappingAndDisposalFail(t *testing.T) {
	project, _ := filepath.EvalSymlinks(t.TempDir())
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {out: listEnvelope(`[]`)},
		{out: `{"schema_version":1,"data":{"ids":{"epic":"fac-1","planning":"fac-1.1","approval":"fac-1.2"}}}`},
		{out: `{"schema_version":1,"data":{}}`},
	}}
	store := &fakeFactoryStore{putErr: errors.New("mapping failed")}
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "session-1"}, stopErr: errors.New("dispose failed"), alive: map[string]bool{}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning, svc.owned = launcher, true

	_, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{InstantiationID: "request-1", Goal: "Ship", InitialProject: project, AcknowledgeLocalExecution: true})
	status := svc.Status(context.Background())
	if err == nil || !launcher.alive["session-1"] || store.cleanups["fac-1.1"] != launcher.result || status.Health != HealthDegraded || status.Reason != ReasonRecoveryFailed || !status.ReadOnly {
		t.Fatalf("CreateWorkEpic error = %v, session alive = %v, cleanups = %#v, status = %#v", err, launcher.alive["session-1"], store.cleanups, status)
	}
}

func TestCreateWorkEpicRetainsCleanupWhenCleanupIntentWriteFails(t *testing.T) {
	project, _ := filepath.EvalSymlinks(t.TempDir())
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {out: listEnvelope(`[]`)},
		{out: `{"schema_version":1,"data":{"ids":{"epic":"fac-1","planning":"fac-1.1","approval":"fac-1.2"}}}`},
		{out: `{"schema_version":1,"data":{}}`},
	}}
	store := &fakeFactoryStore{putErr: errors.New("mapping failed"), cleanupPutErr: errors.New("cleanup intent unavailable")}
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "session-1"}, stopErr: errors.New("dispose failed"), alive: map[string]bool{}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning, svc.projects, svc.owned = launcher, fakeProjectResolver{root: project}, true

	_, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{InstantiationID: "request-1", Goal: "Ship", InitialProject: project, AcknowledgeLocalExecution: true})
	status := svc.Status(context.Background())
	if err == nil || !launcher.alive["session-1"] || status.Health != HealthDegraded || status.Reason != ReasonRecoveryFailed || !status.ReadOnly || !strings.Contains(status.Message, "cleanup intent unavailable") {
		t.Fatalf("CreateWorkEpic error = %v, session alive = %v, status = %#v", err, launcher.alive["session-1"], status)
	}

	err = svc.AcknowledgeLocalExecution(context.Background(), project)
	if !errors.Is(err, ErrFactoryUnavailable) || len(launcher.stops) != 2 || launcher.stops[1] != launcher.result {
		t.Fatalf("AcknowledgeLocalExecution error = %v, disposal attempts = %#v", err, launcher.stops)
	}
}

func TestPlanningLaunchFailurePersistsCleanupIntent(t *testing.T) {
	store := &fakeFactoryStore{}
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "restricted-session"}, err: errors.New("permission setup and disposal failed"), stopErr: errors.New("still unavailable")}
	svc := newWithRunner(t.TempDir(), &fakeRunner{}, store)
	svc.planning, svc.owned = launcher, true
	project := t.TempDir()
	svc.projects = fakeProjectResolver{root: project}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := svc.ensurePlanningSession(ctx, WorkEpic{ID: "fac-1", Goal: "Ship"}, PlanningWork{ID: "fac-1.1", Repository: "/repo"})
	status := svc.Status(context.Background())
	if err == nil || store.cleanups["fac-1.1"] != launcher.result || status.Health != HealthDegraded || status.Reason != ReasonRecoveryFailed || !status.ReadOnly {
		t.Fatalf("ensurePlanningSession error = %v, cleanups = %#v, status = %#v", err, store.cleanups, status)
	}

	mutations := []struct {
		name string
		call func() error
	}{
		{"create", func() error {
			_, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{InstantiationID: "request-2", Goal: "Ship", InitialProject: project, AcknowledgeLocalExecution: true})
			return err
		}},
		{"formula", func() error {
			_, err := svc.SaveFormula(context.Background(), SaveFormulaRequest{ID: "custom/team", Name: "Team", DefinitionYAML: "invalid"})
			return err
		}},
		{"plan", func() error {
			_, err := svc.MutatePlan(context.Background(), "fac-1", MutatePlanRequest{})
			return err
		}},
		{"intake", func() error { return svc.AcknowledgeLocalExecution(context.Background(), project) }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if err := mutation.call(); !errors.Is(err, ErrFactoryUnavailable) {
				t.Fatalf("mutation error = %v, want ErrFactoryUnavailable", err)
			}
		})
	}
}

func (s *fakeFactoryStore) AppendFactoryAudit(_ context.Context, record FactoryAuditRecord) error {
	s.auditCalls++
	if s.auditErr != nil {
		return s.auditErr
	}
	if s.auditFailAt == s.auditCalls {
		return errors.New("audit unavailable")
	}
	s.audits = append(s.audits, record)
	return nil
}

func capturePersistedPlan(t *testing.T, plan *Plan) func(context.Context, []string) {
	t.Helper()
	return func(_ context.Context, args []string) {
		if len(args) < 4 || args[0] != "update" || !strings.HasPrefix(args[3], "@") {
			return
		}
		encoded, err := os.ReadFile(strings.TrimPrefix(args[3], "@"))
		if err != nil {
			t.Fatal(err)
		}
		var metadata map[string]string
		if err := json.Unmarshal(encoded, &metadata); err != nil {
			t.Fatal(err)
		}
		if value := metadata[planMetadataKey]; value != "" {
			*plan = Plan{}
			if err := json.Unmarshal([]byte(value), plan); err != nil {
				t.Fatal(err)
			}
		}
	}
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

func TestServiceRecoveryFailureDegradesHealthAndDisablesMutations(t *testing.T) {
	plan := Plan{Revision: 1, State: PlanDraft, Draft: PlanGraph{Intent: "Ship", Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: "/repo"}}}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: "/repo", Status: "open"}}}
	plan.Hash = hashPlanGraph(plan.Draft)
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {},
		{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))},
		{out: versionEnvelope}, {out: `{"schema_version":1,"data":{"summary":{"total_issues":1}}}`},
		{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))},
	}}
	svc := newWithRunner(t.TempDir(), runner, &fakeFactoryStore{})
	svc.planning = &fakePlanningLauncher{err: errors.New("launch failed")}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)

	status := svc.Status(context.Background())
	if status.Health != HealthDegraded || status.Reason != Reason("recovery_failed") || !status.ReadOnly {
		t.Fatalf("status = %#v", status)
	}
	_, err := svc.MutatePlan(context.Background(), "fac-1", MutatePlanRequest{ExpectedRevision: 1, Graph: plan.Draft})
	if !errors.Is(err, ErrFactoryUnavailable) {
		t.Fatalf("MutatePlan error = %v, want ErrFactoryUnavailable", err)
	}
}

func TestServiceRestartReconcilesApprovedGateAndAudit(t *testing.T) {
	approval := &PlanApproval{Revision: 2, Hash: "hash-2", Actor: "dries", Reason: "reviewed"}
	plan := Plan{SchemaVersion: planSchemaVersion, Revision: 2, Hash: "hash-2", State: PlanApproved, Approval: approval}
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {}, {out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}}}
	store := &fakeFactoryStore{}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning = &fakePlanningLauncher{}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if len(store.audits) != 1 || store.audits[0].Action != "plan.approved" || !strings.Contains(fmt.Sprint(runner.seen[4]), "reviewed") {
		t.Fatalf("commands = %#v, audits = %#v", runner.seen, store.audits)
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

func TestMutatePlanReturnsCurrentPlanWithoutPlanningSessionEffectsWhenStale(t *testing.T) {
	current := Plan{SchemaVersion: planSchemaVersion, Revision: 3, State: PlanDraft, Draft: PlanGraph{Intent: "Current"}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: "/repo", Status: "open"}}}
	current.Hash = hashPlanGraph(current.Draft)
	store := &fakeFactoryStore{}
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "unexpected-session"}}
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, current))}}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning, svc.owned = launcher, true

	result, err := svc.MutatePlan(context.Background(), "fac-1", MutatePlanRequest{ExpectedRevision: 2, Graph: PlanGraph{Intent: "Stale"}})
	if err != nil || !result.Stale || result.Plan.Revision != 3 {
		t.Fatalf("MutatePlan result = %#v, error = %v", result, err)
	}
	if len(store.sessions) != 0 || len(launcher.calls) != 0 || len(launcher.probes) != 0 || len(runner.seen) != 2 {
		t.Fatalf("stale request had effects: sessions = %#v, launches = %#v, probes = %#v, commands = %#v", store.sessions, launcher.calls, launcher.probes, runner.seen)
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
	if len(store.audits) != 2 || store.audits[0].Action != "plan.mutation.requested" || store.audits[1].Action != "plan.mutated" {
		t.Fatalf("audit = %#v", store.audits)
	}
	if len(runner.seen) != 4 || runner.seen[2][0] != "update" || runner.seen[2][1] != "fac-1" || runner.seen[3][0] != "update" || runner.seen[3][1] != "fac-1" {
		t.Fatalf("commands = %v", runner.seen)
	}
}

func TestMutatePlanRecoversFinalAuditAfterPersistence(t *testing.T) {
	current := Plan{SchemaVersion: planSchemaVersion, Revision: 2, State: PlanDraft, Draft: PlanGraph{Intent: "Old"}}
	current.Hash = hashPlanGraph(current.Draft)
	var persisted Plan
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, current))}, {}, {}}}
	runner.onRun = capturePersistedPlan(t, &persisted)
	store := &fakeFactoryStore{auditFailAt: 2}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.owned = true

	_, err := svc.MutatePlan(context.Background(), "fac-1", MutatePlanRequest{ExpectedRevision: 2, Graph: PlanGraph{Intent: "New"}})
	if err == nil || len(store.audits) != 1 || store.audits[0].Action != "plan.mutation.requested" {
		t.Fatalf("MutatePlan error = %v, audits = %#v", err, store.audits)
	}
	store.auditFailAt = 0
	svc.runner = &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, persisted))}}}
	if err := svc.recoverPlanningSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.audits) != 2 || store.audits[1].Action != "plan.mutated" {
		t.Fatalf("recovered audits = %#v", store.audits)
	}
}

func TestRecoveryAppliesPendingPlanMutation(t *testing.T) {
	graph := PlanGraph{Intent: "New"}
	plan := Plan{SchemaVersion: planSchemaVersion, Revision: 2, State: PlanDraft, Draft: PlanGraph{Intent: "Old"}}
	plan.Hash = hashPlanGraph(plan.Draft)
	operation := &PlanOperation{Action: "plan.mutated", FromRevision: 2, FromHash: plan.Hash, Actor: "planner", Graph: &graph}
	plan.PendingOperation = operation
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}}}
	store := &fakeFactoryStore{audits: []FactoryAuditRecord{{EpicID: "fac-1", Actor: "planner", Action: "plan.mutation.requested", Details: operation}}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning = &fakePlanningLauncher{}

	if err := svc.recoverPlanningSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.audits) != 2 || store.audits[1].Action != "plan.mutated" || len(runner.seen) != 3 || runner.seen[2][0] != "update" {
		t.Fatalf("audits = %#v, commands = %#v", store.audits, runner.seen)
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
		if len(plan.Planning) != 0 && plan.Planning[0].CompletedRevision != 0 {
			outcomeMetadata += `,"ocman.plan_revision":` + strconv.Quote(strconv.Itoa(plan.Planning[0].CompletedRevision)) + `,"ocman.plan_hash":` + strconv.Quote(plan.Planning[0].CompletedHash)
		}
	}
	return `[{"id":"fac-1","status":"open","issue_type":"epic","metadata":` + string(epicMetadata) + `},` +
		`{"id":"fac-1.1","status":` + strconv.Quote(planningStatus) + `,"issue_type":"task","metadata":{"ocman.contract":"1","ocman.kind":"agent-work","ocman.formula_id":"ocman/default","ocman.formula_version":"1","ocman.formula_origin":"built-in","ocman.instantiation_id":"intake-1","ocman.work_epic_id":"fac-1","ocman.permission_profile":"factory-plan/v1"` + outcomeMetadata + `}},` +
		`{"id":"fac-1.2","status":"open","issue_type":"gate","metadata":{"ocman.contract":"1","ocman.kind":"gate","ocman.formula_id":"ocman/default","ocman.formula_version":"1","ocman.formula_origin":"built-in","ocman.instantiation_id":"intake-1","ocman.work_epic_id":"fac-1","ocman.gate_type":"plan-approval"}}]`
}

func issuesWithPlanMetadata(t *testing.T, metadata string) string {
	t.Helper()
	var issues []map[string]any
	if err := json.Unmarshal([]byte(issuesWithPlan(t, Plan{Revision: 1, State: PlanDraft})), &issues); err != nil {
		t.Fatal(err)
	}
	issues[0]["metadata"].(map[string]any)[planMetadataKey] = metadata
	encoded, err := json.Marshal(issues)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func issuesWithAdditionalPlanningWork(t *testing.T, plan Plan, workID string, target PlanTarget) string {
	t.Helper()
	var issues []map[string]any
	if err := json.Unmarshal([]byte(issuesWithPlan(t, plan)), &issues); err != nil {
		t.Fatal(err)
	}
	issues = append(issues, map[string]any{
		"id": workID, "status": "open", "issue_type": "task",
		"metadata": map[string]string{"ocman.contract": "1", "ocman.kind": "agent-work", "ocman.work_epic_id": "fac-1", "ocman.planning_target": target.ID, "ocman.target_repository": target.Repository},
	})
	encoded, err := json.Marshal(issues)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestListWorkEpicsQuarantinesIncompatiblePlanMetadata(t *testing.T) {
	for _, tt := range []struct{ name, metadata string }{
		{name: "malformed", metadata: "{"},
		{name: "invalid", metadata: `{}`},
		{name: "future schema", metadata: `{"schemaVersion":2,"revision":1}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlanMetadata(t, tt.metadata))}}}
			svc := newWithRunner(t.TempDir(), runner, &fakeFactoryStore{})

			epics, err := svc.ListWorkEpics(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(epics) != 1 || epics[0].PlanError == "" {
				t.Fatalf("epics = %#v", epics)
			}
		})
	}
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

func TestPlanningRecoveryReplacesDeadPersistedSession(t *testing.T) {
	store := &fakeFactoryStore{sessions: map[string]PlanningSession{"work-1": {Platform: "agent", ID: "dead-session"}}}
	launcher := &fakePlanningLauncher{dead: map[string]bool{"dead-session": true}, result: PlanningSession{Platform: "agent", ID: "replacement"}}
	svc := newWithRunner(t.TempDir(), &fakeRunner{}, store)
	svc.planning = launcher

	got, err := svc.ensurePlanningSession(context.Background(), WorkEpic{ID: "fac-1", Goal: "Ship"}, PlanningWork{ID: "work-1", Repository: "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "replacement" || len(launcher.probes) != 1 || len(launcher.calls) != 1 || store.sessions["work-1"].ID != "replacement" {
		t.Fatalf("session = %#v, probes = %#v, launches = %#v, mappings = %#v", got, launcher.probes, launcher.calls, store.sessions)
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
		{out: `{"schema_version":1,"data":{}}`},
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

func TestAddPlanningWorkReturnsCurrentPlanBeforeRepositoryValidationWhenStale(t *testing.T) {
	current := Plan{SchemaVersion: planSchemaVersion, Revision: 3, State: PlanDraft, Draft: PlanGraph{Intent: "Current"}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: "/repo", Status: "open"}}}
	current.Hash = hashPlanGraph(current.Draft)
	store := &fakeFactoryStore{}
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "unexpected-session"}}
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, current))}}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning, svc.owned = launcher, true

	result, err := svc.AddPlanningWork(context.Background(), "fac-1", AddPlanningWorkRequest{
		ExpectedRevision:          2,
		Target:                    PlanTarget{ID: "api", HostID: localHostID, Repository: filepath.Join(t.TempDir(), "missing")},
		AcknowledgeLocalExecution: true,
	})
	if err != nil || !result.Stale || result.Plan.Revision != 3 || result.Plan.Draft.Intent != "Current" {
		t.Fatalf("AddPlanningWork result = %#v, error = %v", result, err)
	}
	if len(store.calls) != 0 || len(store.audits) != 0 || len(store.sessions) != 0 || len(launcher.calls) != 0 || len(launcher.probes) != 0 || len(runner.seen) != 2 {
		t.Fatalf("stale request mutated state: acknowledgements = %#v, audits = %#v, sessions = %#v, launches = %#v, probes = %#v, commands = %#v", store.calls, store.audits, store.sessions, launcher.calls, launcher.probes, runner.seen)
	}
}

func TestAddPlanningWorkRetryConvergesAfterRepositoryDisappears(t *testing.T) {
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	target := PlanTarget{ID: "api", HostID: localHostID, Repository: repository}
	operation := &PlanOperation{Action: "planning.added", FromRevision: 2, FromHash: "hash-2", Actor: operatorActor, Target: &target, WorkID: "fac-1.3"}
	current := Plan{
		SchemaVersion: planSchemaVersion, Revision: 3, State: PlanDraft,
		Draft:         PlanGraph{Intent: "Current", Targets: []PlanTarget{target}},
		Planning:      []PlanningWork{{ID: "fac-1.3", TargetID: target.ID, Repository: repository, Status: "open"}},
		LastOperation: operation,
	}
	current.Hash = hashPlanGraph(current.Draft)
	if err := os.Remove(repository); err != nil {
		t.Fatal(err)
	}
	store := &fakeFactoryStore{}
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "unexpected-session"}}
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, current))}}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning, svc.owned = launcher, true

	result, err := svc.AddPlanningWork(context.Background(), "fac-1", AddPlanningWorkRequest{ExpectedRevision: 2, Target: target, AcknowledgeLocalExecution: true})
	if err != nil || result.Stale || result.Plan.Revision != 3 {
		t.Fatalf("AddPlanningWork retry result = %#v, error = %v", result, err)
	}
	if len(store.calls) != 0 || len(store.audits) != 1 || store.audits[0].Action != "planning.added" || len(store.sessions) != 0 || len(launcher.calls) != 0 || len(launcher.probes) != 0 || len(runner.seen) != 2 {
		t.Fatalf("retry did not converge cleanly: acknowledgements = %#v, audits = %#v, sessions = %#v, launches = %#v, probes = %#v, commands = %#v", store.calls, store.audits, store.sessions, launcher.calls, launcher.probes, runner.seen)
	}
}

func TestAddPlanningWorkRecoversFinalAuditAfterSideEffects(t *testing.T) {
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	current := Plan{SchemaVersion: planSchemaVersion, Revision: 2, State: PlanDraft, Draft: PlanGraph{Intent: "Ship", Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: "/repo"}}}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: "/repo", Status: "closed"}}}
	current.Hash = hashPlanGraph(current.Draft)
	var persisted Plan
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, current))}, {}, {out: `{"schema_version":1,"data":{"id":"fac-1.3"}}`}, {}}}
	runner.onRun = capturePersistedPlan(t, &persisted)
	store := &fakeFactoryStore{sessions: map[string]PlanningSession{"fac-1.1": {Platform: "agent", ID: "session-1"}}, auditFailAt: 2}
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "session-2"}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning, svc.owned = launcher, true

	_, err := svc.AddPlanningWork(context.Background(), "fac-1", AddPlanningWorkRequest{ExpectedRevision: 2, Target: PlanTarget{ID: "api", HostID: localHostID, Repository: repository}, AcknowledgeLocalExecution: true})
	if err == nil || len(store.audits) != 1 || store.audits[0].Action != "planning.addition.requested" {
		t.Fatalf("AddPlanningWork error = %v, audits = %#v", err, store.audits)
	}
	store.auditFailAt = 0
	svc.runner = &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, persisted))}}}
	if err := svc.recoverPlanningSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.audits) != 2 || store.audits[1].Action != "planning.added" || len(launcher.calls) != 1 {
		t.Fatalf("recovered audits = %#v, launches = %#v", store.audits, launcher.calls)
	}
}

func TestRecoveryAdoptsPlanningWorkCreatedBeforePlanPersistence(t *testing.T) {
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	target := PlanTarget{ID: "api", HostID: localHostID, Repository: repository}
	plan := Plan{SchemaVersion: planSchemaVersion, Revision: 2, State: PlanDraft, Draft: PlanGraph{Intent: "Ship", Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: "/repo"}}}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: "/repo", Status: "closed"}}}
	plan.Hash = hashPlanGraph(plan.Draft)
	operation := &PlanOperation{Action: "planning.added", FromRevision: 2, FromHash: plan.Hash, Actor: operatorActor, Target: &target}
	plan.PendingOperation = operation
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))},
		{out: versionEnvelope}, {out: listEnvelope(issuesWithAdditionalPlanningWork(t, plan, "fac-1.3", target))}, {},
	}}
	store := &fakeFactoryStore{sessions: map[string]PlanningSession{"fac-1.1": {Platform: "agent", ID: "session-1"}}, audits: []FactoryAuditRecord{{EpicID: "fac-1", Actor: operatorActor, Action: "planning.addition.requested", Details: operation}}}
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "session-2"}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning = launcher

	if err := svc.recoverPlanningSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.calls) != 1 || launcher.calls[0].WorkID != "fac-1.3" || len(store.audits) != 2 || store.audits[1].Action != "planning.added" {
		t.Fatalf("launches = %#v, audits = %#v", launcher.calls, store.audits)
	}
	creates := 0
	for _, args := range runner.seen {
		if len(args) != 0 && args[0] == "create" {
			creates++
		}
	}
	if creates != 0 {
		t.Fatalf("recovery duplicated Planning Work: %#v", runner.seen)
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
		Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: repository, Status: "closed", Outcome: "succeeded", CompletedRevision: 4}},
	}
	valid.Hash = hashPlanGraph(valid.Draft)
	valid.Planning[0].CompletedHash = valid.Hash

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
			if len(store.audits) != 2 || store.audits[0].Action != "plan.approval.requested" || store.audits[1].Action != "plan.approved" {
				t.Fatalf("audit = %#v", store.audits)
			}
		})
	}
}

func TestApprovePlanJournalsDecisionBeforeMutatingBeads(t *testing.T) {
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	plan := Plan{
		Revision: 2, State: PlanDraft,
		Draft: PlanGraph{
			Intent: "Ship", Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: repository, DeliveryBase: DeliveryBase{Remote: "origin", BaseBranch: "main", BaseSHA: "abc"}}},
			Items:        []PlanItem{{ID: "deliver", Kind: "delivery", Title: "Deliver", TargetID: "app", Profile: "factory-deliver/v1"}, {ID: "checks", Kind: "gate", Title: "Checks", TargetID: "app", GateType: "provider-check"}, {ID: "merge", Kind: "gate", Title: "Merge", TargetID: "app", GateType: "human-merge"}},
			Dependencies: []PlanDependency{{From: "checks", To: "deliver"}, {From: "merge", To: "checks"}},
		},
		Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: repository, Status: "closed", Outcome: "succeeded", CompletedRevision: 2}},
	}
	plan.Hash = hashPlanGraph(plan.Draft)
	plan.Planning[0].CompletedHash = plan.Hash
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}, {}}}
	store := &fakeFactoryStore{auditErr: errors.New("audit unavailable")}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.owned = true

	_, err := svc.ApprovePlan(context.Background(), "fac-1", PlanDecisionRequest{ExpectedRevision: 2, ExpectedHash: plan.Hash, Reason: "ready", AcknowledgeLocalExecution: true})
	if err == nil || len(runner.seen) != 2 {
		t.Fatalf("ApprovePlan error = %v, Beads commands = %#v", err, runner.seen)
	}
}

func TestApprovePlanRetryReconcilesGateAndAudit(t *testing.T) {
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	plan := Plan{
		Revision: 2, State: PlanDraft,
		Draft: PlanGraph{
			Intent: "Ship", Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: repository, DeliveryBase: DeliveryBase{Remote: "origin", BaseBranch: "main", BaseSHA: "abc"}}},
			Items:        []PlanItem{{ID: "deliver", Kind: "delivery", Title: "Deliver", TargetID: "app", Profile: "factory-deliver/v1"}, {ID: "checks", Kind: "gate", Title: "Checks", TargetID: "app", GateType: "provider-check"}, {ID: "merge", Kind: "gate", Title: "Merge", TargetID: "app", GateType: "human-merge"}},
			Dependencies: []PlanDependency{{From: "checks", To: "deliver"}, {From: "merge", To: "checks"}},
		},
		Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: repository, Status: "closed", Outcome: "succeeded", CompletedRevision: 2}},
	}
	plan.Hash = hashPlanGraph(plan.Draft)
	plan.Planning[0].CompletedHash = plan.Hash
	var persisted Plan
	first := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}, {err: errors.New("Gate close failed")}}}
	first.onRun = func(_ context.Context, args []string) {
		if len(args) < 4 || args[0] != "update" || !strings.HasPrefix(args[3], "@") {
			return
		}
		encoded, err := os.ReadFile(strings.TrimPrefix(args[3], "@"))
		if err != nil {
			t.Fatal(err)
		}
		var metadata map[string]string
		if err := json.Unmarshal(encoded, &metadata); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(metadata[planMetadataKey]), &persisted); err != nil {
			t.Fatal(err)
		}
	}
	store := &fakeFactoryStore{}
	svc := newWithRunner(t.TempDir(), first, store)
	svc.owned = true
	req := PlanDecisionRequest{ExpectedRevision: 2, ExpectedHash: plan.Hash, Actor: "dries", Reason: "reviewed", AcknowledgeLocalExecution: true}
	if _, err := svc.ApprovePlan(context.Background(), "fac-1", req); err == nil {
		t.Fatal("ApprovePlan succeeded despite Gate close failure")
	}

	second := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, persisted))}, {}}}
	svc.runner = second
	got, err := svc.ApprovePlan(context.Background(), "fac-1", req)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != PlanApproved || len(store.audits) != 2 || store.audits[0].Action != "plan.approval.requested" || store.audits[1].Action != "plan.approved" {
		t.Fatalf("plan = %#v, audits = %#v", got, store.audits)
	}
	if !strings.Contains(strings.Join(second.seen[2], " "), "reviewed") {
		t.Fatalf("Gate close command omitted reason: %#v", second.seen[2])
	}
}

func TestApprovePlanRetryAfterFinalAuditFailure(t *testing.T) {
	approval := &PlanApproval{Revision: 2, Hash: "hash-2", Actor: "dries", Reason: "reviewed"}
	plan := Plan{SchemaVersion: planSchemaVersion, Revision: 2, Hash: "hash-2", State: PlanApproved, Approval: approval}
	store := &fakeFactoryStore{auditFailAt: 1}
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.owned = true
	req := PlanDecisionRequest{ExpectedRevision: 2, ExpectedHash: "hash-2"}
	if _, err := svc.ApprovePlan(context.Background(), "fac-1", req); err == nil {
		t.Fatal("ApprovePlan succeeded despite final audit failure")
	}
	svc.runner = &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}}}
	if _, err := svc.ApprovePlan(context.Background(), "fac-1", req); err != nil {
		t.Fatal(err)
	}
	if len(store.audits) != 1 || store.audits[0].Action != "plan.approved" {
		t.Fatalf("audits = %#v", store.audits)
	}
}

func TestApprovePlanIdempotentRetryDoesNotDuplicateAudit(t *testing.T) {
	// The recovery test above leaves the Plan in the exact persisted approved
	// shape a client retry observes after a successful response is lost.
	approval := &PlanApproval{Revision: 2, Hash: "hash-2", Actor: "dries", Reason: "reviewed"}
	plan := Plan{SchemaVersion: planSchemaVersion, Revision: 2, Hash: "hash-2", State: PlanApproved, Approval: approval}
	store := &fakeFactoryStore{audits: []FactoryAuditRecord{{EpicID: "fac-1", Actor: "dries", Action: "plan.approved", Details: approval}}}
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.owned = true

	if _, err := svc.ApprovePlan(context.Background(), "fac-1", PlanDecisionRequest{ExpectedRevision: 2, ExpectedHash: "hash-2"}); err != nil {
		t.Fatal(err)
	}
	if len(store.audits) != 1 {
		t.Fatalf("audits = %#v", store.audits)
	}
}

func TestApprovePlanRejectsPlanningSuccessFromAnotherRevision(t *testing.T) {
	repository, _ := filepath.EvalSymlinks(t.TempDir())
	plan := Plan{
		Revision: 4, State: PlanDraft,
		Draft: PlanGraph{
			Intent:  "Ship",
			Targets: []PlanTarget{{ID: "app", HostID: localHostID, Repository: repository, DeliveryBase: DeliveryBase{Remote: "origin", BaseBranch: "main", BaseSHA: "abc123"}}},
			Items: []PlanItem{
				{ID: "deliver", Kind: "delivery", Title: "Deliver", TargetID: "app", Profile: "factory-deliver/v1"},
				{ID: "checks", Kind: "gate", Title: "Checks", TargetID: "app", GateType: "provider-check"},
				{ID: "merge", Kind: "gate", Title: "Merge", TargetID: "app", GateType: "human-merge"},
			},
			Dependencies: []PlanDependency{{From: "checks", To: "deliver"}, {From: "merge", To: "checks"}},
		},
		Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: repository, Status: "closed", Outcome: "succeeded", CompletedRevision: 3, CompletedHash: "old-hash"}},
	}
	plan.Hash = hashPlanGraph(plan.Draft)
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}}}
	svc := newWithRunner(t.TempDir(), runner, &fakeFactoryStore{})
	svc.owned = true

	_, err := svc.ApprovePlan(context.Background(), "fac-1", PlanDecisionRequest{ExpectedRevision: 4, ExpectedHash: plan.Hash, AcknowledgeLocalExecution: true})
	if !errors.Is(err, ErrPlanNotApprovable) {
		t.Fatalf("ApprovePlan error = %v, want ErrPlanNotApprovable", err)
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
	if got.Planning[0].Status != "closed" || got.Planning[0].Outcome != "succeeded" || len(store.audits) != 2 {
		t.Fatalf("completed Plan = %#v, audits=%#v", got, store.audits)
	}
	if len(runner.seen) != 6 || runner.seen[2][0] != "update" || runner.seen[3][0] != "update" || runner.seen[4][0] != "close" || runner.seen[5][0] != "update" {
		t.Fatalf("commands = %v", runner.seen)
	}
}

func TestCompletePlanningWorkRecoversFinalAuditAfterClose(t *testing.T) {
	plan := Plan{SchemaVersion: planSchemaVersion, Revision: 2, State: PlanDraft, Draft: PlanGraph{Intent: "Ship"}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: "/repo", Status: "in_progress"}}}
	plan.Hash = hashPlanGraph(plan.Draft)
	var persisted Plan
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}, {}, {}, {}}}
	runner.onRun = capturePersistedPlan(t, &persisted)
	store := &fakeFactoryStore{sessions: map[string]PlanningSession{"fac-1.1": {Platform: "agent", ID: "session-1"}}, auditFailAt: 2}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning, svc.owned = &fakePlanningLauncher{}, true

	_, err := svc.CompletePlanningWork(context.Background(), "fac-1", "fac-1.1", CompletePlanningWorkRequest{ExpectedRevision: 2, ExpectedHash: plan.Hash, Actor: "planner"})
	if err == nil || len(store.audits) != 1 || store.audits[0].Action != "planning.completion.requested" {
		t.Fatalf("CompletePlanningWork error = %v, audits = %#v", err, store.audits)
	}
	store.auditFailAt = 0
	svc.runner = &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, persisted))}, {}}}
	if err := svc.recoverPlanningSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.audits) != 2 || store.audits[1].Action != "planning.succeeded" {
		t.Fatalf("recovered audits = %#v", store.audits)
	}
}

func TestRecoveryCompletesPendingPlanningWork(t *testing.T) {
	plan := Plan{SchemaVersion: planSchemaVersion, Revision: 2, State: PlanDraft, Draft: PlanGraph{Intent: "Ship"}, Planning: []PlanningWork{{ID: "fac-1.1", TargetID: "app", Repository: "/repo", Status: "in_progress"}}}
	plan.Hash = hashPlanGraph(plan.Draft)
	operation := &PlanOperation{Action: "planning.succeeded", FromRevision: 2, FromHash: plan.Hash, Actor: "planner", WorkID: "fac-1.1"}
	plan.PendingOperation = operation
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}, {}, {}}}
	store := &fakeFactoryStore{sessions: map[string]PlanningSession{"fac-1.1": {Platform: "agent", ID: "session-1"}}, audits: []FactoryAuditRecord{{EpicID: "fac-1", WorkID: "fac-1.1", Actor: "planner", Action: "planning.completion.requested", Details: operation}}}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning = &fakePlanningLauncher{}

	if err := svc.recoverPlanningSessions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.audits) != 2 || store.audits[1].Action != "planning.succeeded" || len(runner.seen) != 5 || runner.seen[2][0] != "update" || runner.seen[3][0] != "close" || runner.seen[4][0] != "update" {
		t.Fatalf("audits = %#v, commands = %#v", store.audits, runner.seen)
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
			if len(store.audits) != 2 || store.audits[1].Action != wantAction {
				t.Fatalf("audit = %#v", store.audits)
			}
			decision, ok := store.audits[1].Details.(*PlanDecision)
			if !ok || decision.Reason != "change requested" {
				t.Fatalf("decision audit = %#v", store.audits[1])
			}
			if !strings.Contains(fmt.Sprint(runner.seen[2:]), "change requested") {
				t.Fatalf("decision transition omitted reason: %#v", runner.seen)
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

func TestCancelPlanDisposesEveryMappedPlanningSession(t *testing.T) {
	plan := Plan{Revision: 2, State: PlanDraft, Draft: PlanGraph{Intent: "Ship"}, Planning: []PlanningWork{{ID: "fac-1.1", Status: "closed"}, {ID: "fac-1.2", Status: "in_progress"}}}
	plan.Hash = hashPlanGraph(plan.Draft)
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}, {}, {}}}
	store := &fakeFactoryStore{sessions: map[string]PlanningSession{
		"fac-1.1": {Platform: "agent", ID: "closed-session"},
		"fac-1.2": {Platform: "agent", ID: "active-session"},
	}}
	launcher := &fakePlanningLauncher{}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning, svc.owned = launcher, true

	if _, err := svc.CancelPlan(context.Background(), "fac-1", PlanDecisionRequest{ExpectedRevision: 2, ExpectedHash: plan.Hash}); err != nil {
		t.Fatal(err)
	}
	if len(launcher.stops) != 2 || len(store.sessions) != 0 {
		t.Fatalf("disposed = %#v, mappings = %#v", launcher.stops, store.sessions)
	}
}

func TestStartupRecoveryRetriesCleanupBeforeMutations(t *testing.T) {
	plan := Plan{Revision: 1, State: PlanDraft, Draft: PlanGraph{Intent: "Ship"}}
	plan.Hash = hashPlanGraph(plan.Draft)
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {},
		{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))},
		{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))},
	}}
	store := &fakeFactoryStore{cleanups: map[string]PlanningSession{"fac-1.1": {Platform: "agent", ID: "restricted-session"}}}
	launcher := &fakePlanningLauncher{stopErr: errors.New("provider unavailable")}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning = launcher
	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if svc.Status(context.Background()).Health != HealthDegraded {
		t.Fatal("Factory admitted work after cleanup failed")
	}

	launcher.stopErr = nil
	if err := svc.requireMutationStore(context.Background()); err != nil {
		t.Fatalf("recovery retry: %v", err)
	}
	if len(store.cleanups) != 0 || len(launcher.stops) != 2 {
		t.Fatalf("cleanups = %#v, disposal attempts = %#v", store.cleanups, launcher.stops)
	}
}

func TestStartupRecoveryDisposesCancelledPlanningMapping(t *testing.T) {
	decision := &PlanDecision{Action: "plan.cancelled", FromRevision: 2, Revision: 2, Hash: "hash-2", Actor: "operator"}
	plan := Plan{Revision: 2, State: PlanCancelled, Draft: PlanGraph{Intent: "Ship"}, Planning: []PlanningWork{{ID: "fac-1.1", Status: "closed"}}, LastDecision: decision}
	plan.Hash = hashPlanGraph(plan.Draft)
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {},
		{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}, {},
	}}
	store := &fakeFactoryStore{sessions: map[string]PlanningSession{"fac-1.1": {Platform: "agent", ID: "completed-session"}}}
	launcher := &fakePlanningLauncher{}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning = launcher

	if err := svc.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Close)
	if len(launcher.stops) != 1 || len(store.sessions) != 0 {
		t.Fatalf("startup disposals = %#v, mappings = %#v", launcher.stops, store.sessions)
	}
}

func TestPlanDecisionJournalsBeforeBeadsMutation(t *testing.T) {
	for _, action := range []string{"revise", "reject", "cancel"} {
		t.Run(action, func(t *testing.T) {
			state := PlanDraft
			if action == "revise" {
				state = PlanApproved
			}
			plan := Plan{Revision: 2, State: state, Draft: PlanGraph{Intent: "Ship"}}
			plan.Hash = hashPlanGraph(plan.Draft)
			runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}, {}, {}}}
			store := &fakeFactoryStore{auditErr: errors.New("audit unavailable")}
			svc := newWithRunner(t.TempDir(), runner, store)
			svc.owned = true
			req := PlanDecisionRequest{ExpectedRevision: 2, ExpectedHash: plan.Hash, Reason: "not ready"}
			var err error
			switch action {
			case "revise":
				_, err = svc.RevisePlan(context.Background(), "fac-1", req)
			case "reject":
				_, err = svc.RejectPlan(context.Background(), "fac-1", req)
			case "cancel":
				_, err = svc.CancelPlan(context.Background(), "fac-1", req)
			}
			if err == nil || len(runner.seen) != 2 {
				t.Fatalf("%s error = %v, Beads commands = %#v", action, err, runner.seen)
			}
		})
	}
}

func TestCancelPlanRetryReconcilesEpicClose(t *testing.T) {
	plan := Plan{SchemaVersion: planSchemaVersion, Revision: 2, State: PlanDraft, Draft: PlanGraph{Intent: "Ship"}, Planning: []PlanningWork{{ID: "fac-1.1", Status: "in_progress"}}}
	plan.Hash = hashPlanGraph(plan.Draft)
	var persisted Plan
	runner := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, plan))}, {}, {}, {err: errors.New("epic close failed")}}}
	runner.onRun = capturePersistedPlan(t, &persisted)
	store := &fakeFactoryStore{sessions: map[string]PlanningSession{"fac-1.1": {Platform: "agent", ID: "session-1"}}}
	launcher := &fakePlanningLauncher{}
	svc := newWithRunner(t.TempDir(), runner, store)
	svc.planning, svc.owned = launcher, true
	req := PlanDecisionRequest{ExpectedRevision: 2, ExpectedHash: plan.Hash, Actor: "dries", Reason: "stop"}
	if _, err := svc.CancelPlan(context.Background(), "fac-1", req); err == nil {
		t.Fatal("CancelPlan succeeded despite epic close failure")
	}

	retry := &fakeRunner{runs: []fakeRun{{out: versionEnvelope}, {out: listEnvelope(issuesWithPlan(t, persisted))}, {}, {}}}
	svc.runner = retry
	if _, err := svc.CancelPlan(context.Background(), "fac-1", req); err != nil {
		t.Fatal(err)
	}
	if len(retry.seen) != 4 || retry.seen[2][0] != "close" || retry.seen[2][1] != "fac-1.2" || retry.seen[3][0] != "close" || retry.seen[3][1] != "fac-1" || len(launcher.stops) != 1 || len(store.sessions) != 0 {
		t.Fatalf("retry commands = %#v, stops = %#v", retry.seen, launcher.stops)
	}
}
