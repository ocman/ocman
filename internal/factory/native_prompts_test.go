package factory

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
)

type promptIssueStore struct {
	*state.DB
	issues []model.NativeIssue
	err    error
}

func (s promptIssueStore) ListFactoryIssues(context.Context, string) ([]model.NativeIssue, error) {
	return s.issues, s.err
}

func TestFormulaPromptAncestry(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{})
	child, err := svc.SaveFormula(t.Context(), FormulaSaveRequest{ID: "custom/child", Source: "prompt_implementation = \"Child instructions\"\n" + tracerFormulaSource})
	if err != nil {
		t.Fatal(err)
	}
	builtin := BuiltInTracerFormula()
	epic := model.NativeEpic{ID: "epic", FormulaID: builtin.ID, FormulaVersion: builtin.Version, FormulaHash: builtin.Hash}
	for _, tc := range []struct {
		name     string
		issues   []model.NativeIssue
		storeErr error
		want     string
		wantErr  bool
	}{
		{name: "child revision", issues: []model.NativeIssue{{ID: "work", ParentID: "mol"}, {ID: "mol", FormulaID: child.ID, FormulaVersion: child.Version, FormulaHash: child.Hash}}, want: "Child instructions"},
		{name: "epic fallback", issues: []model.NativeIssue{{ID: "work"}}, want: DefaultFormulaPrompts()["implementation"]},
		{name: "missing parent", issues: []model.NativeIssue{{ID: "work", ParentID: "missing"}}, wantErr: true},
		{name: "cycle", issues: []model.NativeIssue{{ID: "work", ParentID: "work"}}, wantErr: true},
		{name: "hash mismatch", issues: []model.NativeIssue{{ID: "work", FormulaID: child.ID, FormulaVersion: child.Version, FormulaHash: "wrong"}}, wantErr: true},
		{name: "missing revision", issues: []model.NativeIssue{{ID: "work", FormulaID: child.ID, FormulaVersion: 999}}, wantErr: true},
		{name: "storage error", storeErr: errors.New("offline"), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc.store = promptIssueStore{DB: db, issues: tc.issues, err: tc.storeErr}
			got, err := svc.issuePrompt(t.Context(), epic, "work", "implementation")
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
	svc.store = promptIssueStore{DB: db, issues: []model.NativeIssue{{ID: "work"}}}
	legacy, err := svc.GetFormula(t.Context(), builtin.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	epic.FormulaVersion, epic.FormulaHash = 1, legacy.SourceHash
	if got, err := svc.issuePrompt(t.Context(), epic, "work", "planning"); err != nil || got != legacy.Prompts["planning"] {
		t.Fatalf("legacy source pin = %q, %v", got, err)
	}
}

func TestFormulaPromptsCompilation(t *testing.T) {
	legacy, err := compileNativeFormula(tracerFormulaSource)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(legacy.JSON, `"prompts"`) {
		t.Fatal("legacy hash input changed")
	}
	for _, prefix := range []string{
		`prompt_planning = ""`,
		`prompt_unknown = "instructions"`,
		"prompt_planning = \"one\"\nprompt_planning = \"two\"",
		`prompt_delivery = "` + strings.Repeat("a", 32769) + `"`,
		`prompt_implementation = invalid`,
	} {
		if _, err := compileNativeFormula(prefix + "\n" + tracerFormulaSource); err == nil {
			t.Fatalf("accepted %q", prefix[:min(len(prefix), 80)])
		}
	}
	custom, err := compileNativeFormula("prompt_planning = \"Research first.\\nThen propose.\"\n" + tracerFormulaSource)
	if err != nil {
		t.Fatal(err)
	}
	if custom.Hash == legacy.Hash || custom.Prompts["planning"] != "Research first.\nThen propose." {
		t.Fatalf("compiled = %#v", custom)
	}
	prompts := effectiveFormulaPrompts(custom.Prompts)
	if prompts["planning"] != custom.Prompts["planning"] || prompts["delivery"] != DefaultFormulaPrompts()["delivery"] {
		t.Fatalf("prompts = %#v", prompts)
	}
}

func TestBuiltInFormulaContainsItsPrompts(t *testing.T) {
	svc := NewNative(&nativeStoreFake{})
	current := BuiltInTracerFormula()
	view, err := svc.GetFormula(t.Context(), current.ID, current.Version)
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compileNativeFormula(view.Source)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 2 || len(compiled.Prompts) != 4 {
		t.Fatalf("built-in = %#v", view)
	}
	for stage, prompt := range view.Prompts {
		if compiled.Prompts[stage] != prompt || !strings.Contains(view.Source, "prompt_"+stage+" = ") {
			t.Fatalf("%s prompt missing from source", stage)
		}
	}
	legacy, err := svc.GetFormula(t.Context(), current.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	old, err := compileNativeFormula(tracerFormulaSource)
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Hash != old.Hash || legacy.Source != tracerFormulaSource || current.Hash == legacy.Hash {
		t.Fatal("legacy revision was rewritten")
	}
	listed, err := svc.ListFormulas(t.Context())
	if err != nil || len(listed) == 0 || listed[0].Version != 2 {
		t.Fatalf("listed = %#v, %v", listed, err)
	}
	if source, err := svc.compositionSource(t.Context(), current.ID, current.Version); err != nil || source != view.Source {
		t.Fatalf("composition source = %q, %v", source, err)
	}
}

func TestPlanningUsesPinnedFormulaPrompt(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakePlanningLauncher{result: PlanningSession{Platform: "agent", ID: "plan-1"}}
	svc := NewNativeWithPlanning(db, testProjectResolver{root: "/repo"}, launcher)
	first, err := svc.SaveFormula(t.Context(), FormulaSaveRequest{ID: "custom/prompts", Source: "prompt_planning = \"First revision instructions\"\n" + tracerFormulaSource})
	if err != nil {
		t.Fatal(err)
	}
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Ship", InitialProject: "/repo", AcknowledgeLocalExecution: true, FormulaID: first.ID, FormulaRevision: first.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pour(t.Context(), epic.ID); err != nil {
		t.Fatal(err)
	}
	second, err := svc.SaveFormula(t.Context(), FormulaSaveRequest{ID: first.ID, Source: "prompt_planning = \"Second revision instructions\"\n" + tracerFormulaSource})
	if err != nil {
		t.Fatal(err)
	}
	if second.Version == first.Version || second.Hash == first.Hash {
		t.Fatal("prompt edit did not create a revision")
	}
	if _, err := svc.ClaimPlan(t.Context(), epic.ID, pouredIssueID(t, svc, epic.ID, "plan")); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != 1 || launcher.prompts[0].Prompt != "First revision instructions" {
		t.Fatalf("requests = %#v", launcher.prompts)
	}
}

func TestDispatchUsesFormulaImplementationAndDeliveryPrompts(t *testing.T) {
	db, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	launcher := &fakeImplementationLauncher{store: db}
	svc := NewNativeWithExecution(db, testProjectResolver{root: "/repo"}, &fakePlanningLauncher{}, launcher)
	formula, err := svc.SaveFormula(t.Context(), FormulaSaveRequest{ID: "custom/execution", Source: "prompt_implementation = \"Implement with tests\"\nprompt_delivery = \"Check integration\"\n" + tracerFormulaSource})
	if err != nil {
		t.Fatal(err)
	}
	epic, err := svc.CreateWorkEpic(t.Context(), CreateWorkEpicRequest{Goal: "Ship", InitialProject: "/repo", AcknowledgeLocalExecution: true, FormulaID: formula.ID, FormulaRevision: formula.Version})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pour(t.Context(), epic.ID); err != nil {
		t.Fatal(err)
	}
	proposal, err := svc.SubmitProposal(t.Context(), SubmitProposalRequest{EpicID: epic.ID, Manifest: ProposalManifest{EpicID: epic.ID, MolID: pouredIssueID(t, svc, epic.ID, "mol"), Project: "/repo", Nodes: []ManifestNode{{Key: "implement", Type: "implementation", Requirement: "required"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecidePlanGate(t.Context(), epic.ID, "approve", PlanGateDecisionRequest{ExpectedRevision: proposal.Revision, ExpectedHash: proposal.ContentHash}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Materialize(t.Context(), epic.ID, pouredIssueID(t, svc, epic.ID, "materialization")); err != nil {
		t.Fatal(err)
	}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != 1 || launcher.prompts[0].Prompt != "Implement with tests" {
		t.Fatalf("prompts = %#v", launcher.prompts)
	}
	request := launcher.prompts[0]
	if err := svc.CompleteAttempt(t.Context(), request.AttemptID, request.AgentToken, "done", ""); err != nil {
		t.Fatal(err)
	}
	launcher.result = PlanningSession{Platform: "opencode", ID: "delivery-1"}
	if err := svc.Dispatch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(launcher.prompts) != 2 || !launcher.prompts[1].Delivery || launcher.prompts[1].Prompt != "Check integration" {
		t.Fatalf("prompts = %#v", launcher.prompts)
	}
}
