package factory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
	_ "modernc.org/sqlite"
)

func formulaTestService(t *testing.T, runner *fakeRunner) (*Service, *state.DB) {
	t.Helper()
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	db, err := state.OpenFromSQL(raw)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	svc := newWithRunner(t.TempDir(), runner, db)
	svc.owned = true
	return svc, db
}

func TestFormulaDraftValidationPreviewAndImmutableRevisions(t *testing.T) {
	svc, _ := formulaTestService(t, &fakeRunner{})
	draft, err := svc.CopyFormula(context.Background(), DefaultFormulaID, DefaultFormulaVersion)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(draft.DefinitionYAML, "schema: 1") || draft.Origin != FormulaOriginBuiltIn {
		t.Fatalf("draft = %#v", draft)
	}

	invalid := strings.Replace(draft.DefinitionYAML, "exact_revision: true", "exact_revision: false", 1)
	validation := svc.ValidateFormula(invalid)
	if validation.Valid || !strings.Contains(strings.Join(validation.Errors, "\n"), "exact provider check") {
		t.Fatalf("validation = %#v", validation)
	}
	if _, err := svc.PreviewFormula(invalid, map[string]string{"goal": "Ship it", "initial_project": "/repo"}); !errors.Is(err, ErrInvalidFormula) {
		t.Fatalf("PreviewFormula error = %v, want invalid formula", err)
	}
	if _, err := svc.SaveFormula(context.Background(), SaveFormulaRequest{ID: "custom/unsafe", Name: "Unsafe", DefinitionYAML: invalid}); !errors.Is(err, ErrInvalidFormula) {
		t.Fatalf("SaveFormula error = %v, want invalid formula", err)
	}

	customYAML := strings.Replace(draft.DefinitionYAML, "name: Shipped delivery", "name: Team delivery", 1)
	preview, err := svc.PreviewFormula(customYAML, map[string]string{"goal": "Ship it", "initial_project": "/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if preview.FormulaHash == "" || len(preview.Nodes) != 6 || preview.Nodes[0].Title != "Ship it" {
		t.Fatalf("preview = %#v", preview)
	}
	rev1, err := svc.SaveFormula(context.Background(), SaveFormulaRequest{ID: "custom/team", Name: "Team delivery", DefinitionYAML: customYAML})
	if err != nil {
		t.Fatal(err)
	}
	rev2YAML := strings.Replace(customYAML, "Plan: ${goal}", "Design: ${goal}", 1)
	rev2, err := svc.SaveFormula(context.Background(), SaveFormulaRequest{ID: "custom/team", Name: "Team delivery", DefinitionYAML: rev2YAML})
	if err != nil {
		t.Fatal(err)
	}
	if rev1.Revision != 1 || rev2.Revision != 2 || rev1.ContentHash == rev2.ContentHash {
		t.Fatalf("revisions = %#v, %#v", rev1, rev2)
	}
	duplicate, err := svc.SaveFormula(context.Background(), SaveFormulaRequest{ID: "custom/team", Name: "Team delivery", DefinitionYAML: customYAML})
	if err != nil || duplicate.Revision != 1 || duplicate.CurrentRevision != 2 {
		t.Fatalf("duplicate revision = %#v, %v", duplicate, err)
	}
	got1, err := svc.GetFormulaRevision(context.Background(), "custom/team", 1)
	if err != nil || got1.DefinitionYAML != customYAML {
		t.Fatalf("revision 1 = %#v, %v", got1, err)
	}
	library, err := svc.ListFormulas(context.Background())
	if err != nil || len(library) != 2 || len(library[1].Revisions) != 2 || library[1].Revisions[0].ContentHash != rev1.ContentHash {
		t.Fatalf("Formula library = %#v, %v", library, err)
	}
	if _, err := svc.SaveFormula(context.Background(), SaveFormulaRequest{ID: DefaultFormulaID, Name: "Changed", DefinitionYAML: customYAML}); !errors.Is(err, ErrBuiltInFormulaImmutable) {
		t.Fatalf("built-in save error = %v", err)
	}
}

func TestFormulaPolicyRejectsRemovedSafetyConstraintsAndUnknownParameters(t *testing.T) {
	svc, _ := formulaTestService(t, &fakeRunner{})
	draft, err := svc.CopyFormula(context.Background(), DefaultFormulaID, DefaultFormulaVersion)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"unknown field", draft.DefinitionYAML + "future: true\n", "field future not found"},
		{"unknown parameter", strings.Replace(draft.DefinitionYAML, "initial_project:\n    type: local-project", "initial_project:\n    type: local-project\n  shell:\n    type: string", 1), "parameter shell"},
		{"reserved epic key", strings.NewReplacer("key: planning", "key: epic", "to: planning", "to: epic").Replace(draft.DefinitionYAML), "reserved"},
		{"plan approval", strings.Replace(draft.DefinitionYAML, "kind: plan-approval", "kind: question", 1), "Plan approval"},
		{"delivery", strings.Replace(draft.DefinitionYAML, "kind: delivery", "kind: agent-work", 1), "Delivery"},
		{"provider check", strings.Replace(draft.DefinitionYAML, "kind: provider-check", "kind: question", 1), "exact provider check"},
		{"human merge", strings.Replace(draft.DefinitionYAML, "kind: human-merge", "kind: question", 1), "human merge"},
		{"profile", strings.Replace(draft.DefinitionYAML, "factory-plan/v1", "factory-admin/v1", 1), "profile"},
		{"missing project parameter", strings.Replace(draft.DefinitionYAML, "project_parameter: initial_project", "project_parameter: missing", 1), "local-project parameter"},
		{"non-project parameter", strings.Replace(draft.DefinitionYAML, "project_parameter: initial_project", "project_parameter: goal", 1), "local-project parameter"},
		{"duplicate delivery", strings.Replace(draft.DefinitionYAML, "  - key: provider-check", "  - key: delivery-copy\n    kind: delivery\n    title: Copy\n    profile: factory-deliver/v1\n    project_parameter: initial_project\n  - key: provider-check", 1), "one Delivery per project"},
		{"duplicate provider check", strings.Replace(draft.DefinitionYAML, "  - key: human-merge", "  - key: provider-check-copy\n    kind: provider-check\n    title: Copy\n    project_parameter: initial_project\n    exact_revision: true\n  - key: human-merge", 1), "one exact provider check per project"},
		{"duplicate human merge", strings.Replace(draft.DefinitionYAML, "edges:", "  - key: human-merge-copy\n    kind: human-merge\n    title: Copy\n    project_parameter: initial_project\nedges:", 1), "one human merge per project"},
		{"gating edge", strings.Replace(draft.DefinitionYAML, "  - from: human-merge\n    to: provider-check\n    type: blocks\n", "", 1), "gating chain"},
		{"cycle", draft.DefinitionYAML + "  - from: planning\n    to: approval\n", "acyclic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.ValidateFormula(tt.yaml)
			if got.Valid || !strings.Contains(strings.Join(got.Errors, "\n"), tt.want) {
				t.Fatalf("ValidateFormula = %#v, want %q", got, tt.want)
			}
		})
	}
}

func TestBuiltInFormulaRetainsEveryRevision(t *testing.T) {
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	runner := &fakeRunner{}
	svc, _ := formulaTestService(t, runner)
	library, err := svc.ListFormulas(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := library[0].Revisions; len(got) != DefaultFormulaVersion || got[0].Revision != 1 || got[0].Instantiable || got[1].Revision != 2 || !got[1].Instantiable || got[0].ContentHash == got[1].ContentHash {
		t.Fatalf("built-in revisions = %#v", got)
	}
	revision, err := svc.GetFormulaRevision(context.Background(), DefaultFormulaID, 1)
	if err != nil || revision.Revision != 1 || revision.ContentHash != formulaHash(revision.DefinitionYAML) || strings.Contains(revision.DefinitionYAML, "kind: delivery") {
		t.Fatalf("built-in revision 1 = %#v, %v", revision, err)
	}
	draft, err := svc.CopyFormula(context.Background(), DefaultFormulaID, 1)
	if err != nil || draft.SourceRevision != 1 || draft.DefinitionYAML != revision.DefinitionYAML {
		t.Fatalf("built-in revision 1 draft = %#v, %v", draft, err)
	}
	_, err = svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{
		InstantiationID: "legacy", Goal: "Repeat legacy", InitialProject: project,
		FormulaID: DefaultFormulaID, FormulaRevision: 1, AcknowledgeLocalExecution: true,
	})
	if !errors.Is(err, ErrInvalidFormula) {
		t.Fatalf("CreateWorkEpic revision 1 error = %v, want invalid Formula", err)
	}
	if len(runner.seen) != 0 {
		t.Fatalf("unsafe built-in reached Beads: %#v", runner.seen)
	}
}

func TestCreateWorkEpicSupportsRenamedPlanningNodes(t *testing.T) {
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	runner := &fakeRunner{runs: []fakeRun{
		{out: versionEnvelope}, {out: listEnvelope(`[]`)},
		{out: `{"schema_version":1,"data":{"ids":{"epic":"fac-3","design":"fac-3.1","signoff":"fac-3.2","delivery":"fac-3.3","provider-check":"fac-3.4","human-merge":"fac-3.5"}}}`},
		{out: listEnvelope(`[]`)},
	}}
	svc, _ := formulaTestService(t, runner)
	draft, _ := svc.CopyFormula(context.Background(), DefaultFormulaID, DefaultFormulaVersion)
	definition := strings.NewReplacer(
		"name: Shipped delivery", "name: Renamed delivery",
		"key: planning", "key: design",
		"key: approval", "key: signoff",
		"from: approval", "from: signoff",
		"to: planning", "to: design",
	).Replace(draft.DefinitionYAML)
	if _, err := svc.SaveFormula(context.Background(), SaveFormulaRequest{ID: "custom/renamed", Name: "Renamed delivery", DefinitionYAML: definition}); err != nil {
		t.Fatal(err)
	}
	epic, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{
		InstantiationID: "renamed", Goal: "Ship renamed", InitialProject: project,
		FormulaID: "custom/renamed", FormulaRevision: 1, AcknowledgeLocalExecution: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if epic.Planning.WorkID != "fac-3.1" || epic.Planning.ApprovalGateID != "fac-3.2" {
		t.Fatalf("planning state = %#v", epic.Planning)
	}
	var plan graphPlan
	if err := json.Unmarshal(runner.plans[0], &plan); err != nil {
		t.Fatal(err)
	}
	if refs := plan.Nodes[0].MetadataRefs; refs["ocman.planning_work_id"] != "design" || refs["ocman.plan_approval_gate_id"] != "signoff" {
		t.Fatalf("epic refs = %#v", refs)
	}
}

type blockingFormulaStore struct {
	formulaStore
	selected chan struct{}
	release  chan struct{}
	once     sync.Once
}

func (s *blockingFormulaStore) GetFactoryFormulaRevision(ctx context.Context, id string, revision int) (FactoryFormula, FactoryFormulaRevision, error) {
	formula, saved, err := s.formulaStore.GetFactoryFormulaRevision(ctx, id, revision)
	if id == "custom/team" {
		s.once.Do(func() {
			close(s.selected)
			<-s.release
		})
	}
	return formula, saved, err
}

type serializedFormulaRunner struct {
	mu      sync.Mutex
	created bool
	issues  string
}

func (r *serializedFormulaRunner) LookPath(string) (string, error) { return "/usr/bin/bd", nil }

func (r *serializedFormulaRunner) Run(_ context.Context, _ string, _ string, args, _ []string) ([]byte, []byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(args) == 2 && args[0] == "version" {
		return []byte(versionEnvelope), nil, nil
	}
	if len(args) >= 2 && args[0] == "--readonly" && args[1] == "list" {
		if r.created {
			return []byte(listEnvelope(r.issues)), nil, nil
		}
		return []byte(listEnvelope(`[]`)), nil, nil
	}
	if len(args) >= 2 && args[0] == "create" && args[1] == "--graph" {
		r.created = true
		return []byte(`{"schema_version":1,"data":{"ids":{"epic":"fac-2","planning":"fac-2.1","approval":"fac-2.2","delivery":"fac-2.3","provider-check":"fac-2.4","human-merge":"fac-2.5"}}}`), nil, nil
	}
	return nil, nil, errors.New("unexpected Beads command")
}

func TestDeleteWaitsForExactRevisionPour(t *testing.T) {
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	runner := &serializedFormulaRunner{}
	svc, _ := formulaTestService(t, nil)
	svc.runner = runner
	draft, _ := svc.CopyFormula(context.Background(), DefaultFormulaID, DefaultFormulaVersion)
	customYAML := strings.Replace(draft.DefinitionYAML, "name: Shipped delivery", "name: Team delivery", 1)
	revision, err := svc.SaveFormula(context.Background(), SaveFormulaRequest{ID: "custom/team", Name: "Team delivery", DefinitionYAML: customYAML})
	if err != nil {
		t.Fatal(err)
	}
	runner.issues = customPouredIssues(project, revision.ContentHash)
	store := &blockingFormulaStore{formulaStore: svc.formulas, selected: make(chan struct{}), release: make(chan struct{})}
	svc.formulas = store
	t.Cleanup(func() { store.once.Do(func() { close(store.release) }) })

	created := make(chan error, 1)
	go func() {
		_, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{
			InstantiationID: "custom-intake", Goal: "Ship custom", InitialProject: project,
			FormulaID: "custom/team", FormulaRevision: 1, AcknowledgeLocalExecution: true,
		})
		created <- err
	}()
	<-store.selected
	deleted := make(chan error, 1)
	go func() { deleted <- svc.DeleteFormula(context.Background(), "custom/team") }()
	select {
	case err := <-deleted:
		close(store.release)
		if createErr := <-created; createErr != nil {
			t.Fatal(createErr)
		}
		if !errors.Is(err, ErrFormulaReferenced) {
			t.Fatalf("DeleteFormula completed during pour with %v", err)
		}
	case <-time.After(50 * time.Millisecond):
		close(store.release)
		if createErr := <-created; createErr != nil {
			t.Fatal(createErr)
		}
		if err := <-deleted; !errors.Is(err, ErrFormulaReferenced) {
			t.Fatalf("DeleteFormula error = %v, want referenced", err)
		}
	}
}

func TestReadOnlyServiceCannotPersistFormula(t *testing.T) {
	svc, _ := formulaTestService(t, &fakeRunner{})
	draft, err := svc.CopyFormula(context.Background(), DefaultFormulaID, DefaultFormulaVersion)
	if err != nil {
		t.Fatal(err)
	}
	svc.owned = false
	_, err = svc.SaveFormula(context.Background(), SaveFormulaRequest{ID: "custom/team", Name: "Shipped delivery", DefinitionYAML: draft.DefinitionYAML})
	if !errors.Is(err, ErrFactoryUnavailable) {
		t.Fatalf("SaveFormula error = %v, want unavailable", err)
	}
	formulas, err := svc.ListFormulas(context.Background())
	if err != nil || len(formulas) != 1 {
		t.Fatalf("Formulas after rejected save = %#v, %v", formulas, err)
	}
}

func TestArchivedAndDeletedFormulaPreservesReferencedRevision(t *testing.T) {
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	runner := &fakeRunner{}
	svc, _ := formulaTestService(t, runner)
	draft, _ := svc.CopyFormula(context.Background(), DefaultFormulaID, DefaultFormulaVersion)
	customYAML := strings.Replace(draft.DefinitionYAML, "name: Shipped delivery", "name: Team delivery", 1)
	revision, err := svc.SaveFormula(context.Background(), SaveFormulaRequest{ID: "custom/team", Name: "Team delivery", DefinitionYAML: customYAML})
	if err != nil {
		t.Fatal(err)
	}
	issues := customPouredIssues(project, revision.ContentHash)
	runner.runs = []fakeRun{
		{out: versionEnvelope}, {out: listEnvelope(`[]`)},
		{out: `{"schema_version":1,"data":{"ids":{"epic":"fac-2","planning":"fac-2.1","approval":"fac-2.2","delivery":"fac-2.3","provider-check":"fac-2.4","human-merge":"fac-2.5"}}}`},
		{out: versionEnvelope}, {out: listEnvelope(issues)},
		{out: versionEnvelope}, {out: listEnvelope(issues)},
	}
	epic, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{
		InstantiationID: "custom-intake", Goal: "Ship custom", InitialProject: project,
		FormulaID: "custom/team", FormulaRevision: 1, AcknowledgeLocalExecution: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if epic.FormulaID != "custom/team" || epic.FormulaRevision != 1 || epic.FormulaHash != revision.ContentHash || epic.FormulaOrigin != FormulaOriginCustom {
		t.Fatalf("epic provenance = %#v", epic)
	}
	if err := svc.ArchiveFormula(context.Background(), "custom/team"); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteFormula(context.Background(), "custom/team"); !errors.Is(err, ErrFormulaReferenced) {
		t.Fatalf("DeleteFormula error = %v, want referenced", err)
	}
	if _, err := svc.GetFormulaRevision(context.Background(), "custom/team", 1); err != nil {
		t.Fatalf("referenced revision was lost: %v", err)
	}
	if got, err := svc.GetWorkEpic(context.Background(), "fac-2"); err != nil || got.FormulaHash != revision.ContentHash {
		t.Fatalf("pinned epic = %#v, %v", got, err)
	}
}

func TestDeleteFormulaPreservesRevisionWhenReferencedEpicGraphIsDamaged(t *testing.T) {
	runner := &fakeRunner{}
	svc, _ := formulaTestService(t, runner)
	draft, _ := svc.CopyFormula(context.Background(), DefaultFormulaID, DefaultFormulaVersion)
	customYAML := strings.Replace(draft.DefinitionYAML, "name: Shipped delivery", "name: Team delivery", 1)
	if _, err := svc.SaveFormula(context.Background(), SaveFormulaRequest{ID: "custom/team", Name: "Team delivery", DefinitionYAML: customYAML}); err != nil {
		t.Fatal(err)
	}
	runner.runs = []fakeRun{
		{out: versionEnvelope},
		{out: listEnvelope(`[{"id":"fac-damaged","status":"open","issue_type":"epic","metadata":{"ocman.contract":"1","ocman.kind":"work-epic","ocman.formula_id":"custom/team","ocman.formula_revision":"1","ocman.formula_origin":"custom","ocman.planning_work_id":"missing","ocman.plan_approval_gate_id":"missing"}}]`)},
	}

	if err := svc.DeleteFormula(context.Background(), "custom/team"); !errors.Is(err, ErrFormulaReferenced) {
		t.Fatalf("DeleteFormula error = %v, want referenced", err)
	}
	if _, err := svc.GetFormulaRevision(context.Background(), "custom/team", 1); err != nil {
		t.Fatalf("referenced revision was deleted: %v", err)
	}
}

func customPouredIssues(project, hash string) string {
	issues := strings.ReplaceAll(`[
 {"id":"fac-2","status":"open","issue_type":"epic","metadata":{"ocman.contract":"1","ocman.kind":"work-epic","ocman.formula_id":"custom/team","ocman.formula_revision":"1","ocman.formula_hash":"HASH","ocman.formula_origin":"custom","ocman.instantiation_id":"custom-intake","ocman.goal":"Ship custom","ocman.initial_project":"PROJECT","ocman.planning_work_id":"fac-2.1","ocman.plan_approval_gate_id":"fac-2.2"}},
 {"id":"fac-2.1","status":"open","issue_type":"task","metadata":{"ocman.contract":"1","ocman.kind":"agent-work","ocman.formula_id":"custom/team","ocman.formula_revision":"1","ocman.formula_hash":"HASH","ocman.formula_origin":"custom","ocman.instantiation_id":"custom-intake","ocman.work_epic_id":"fac-2","ocman.permission_profile":"factory-plan/v1"}},
 {"id":"fac-2.2","status":"open","issue_type":"gate","metadata":{"ocman.contract":"1","ocman.kind":"gate","ocman.formula_id":"custom/team","ocman.formula_revision":"1","ocman.formula_hash":"HASH","ocman.formula_origin":"custom","ocman.instantiation_id":"custom-intake","ocman.work_epic_id":"fac-2","ocman.gate_type":"plan-approval"}}
]`, "PROJECT", project)
	return strings.ReplaceAll(issues, "HASH", hash)
}
