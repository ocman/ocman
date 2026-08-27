package factory

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

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
		{"plan approval", strings.Replace(draft.DefinitionYAML, "kind: plan-approval", "kind: question", 1), "Plan approval"},
		{"delivery", strings.Replace(draft.DefinitionYAML, "kind: delivery", "kind: agent-work", 1), "Delivery"},
		{"provider check", strings.Replace(draft.DefinitionYAML, "kind: provider-check", "kind: question", 1), "exact provider check"},
		{"human merge", strings.Replace(draft.DefinitionYAML, "kind: human-merge", "kind: question", 1), "human merge"},
		{"profile", strings.Replace(draft.DefinitionYAML, "factory-plan/v1", "factory-admin/v1", 1), "profile"},
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

func customPouredIssues(project, hash string) string {
	issues := strings.ReplaceAll(`[
 {"id":"fac-2","status":"open","issue_type":"epic","metadata":{"ocman.contract":"1","ocman.kind":"work-epic","ocman.formula_id":"custom/team","ocman.formula_revision":"1","ocman.formula_hash":"HASH","ocman.formula_origin":"custom","ocman.instantiation_id":"custom-intake","ocman.goal":"Ship custom","ocman.initial_project":"PROJECT","ocman.planning_work_id":"fac-2.1","ocman.plan_approval_gate_id":"fac-2.2"}},
 {"id":"fac-2.1","status":"open","issue_type":"task","metadata":{"ocman.contract":"1","ocman.kind":"agent-work","ocman.formula_id":"custom/team","ocman.formula_revision":"1","ocman.formula_hash":"HASH","ocman.formula_origin":"custom","ocman.instantiation_id":"custom-intake","ocman.work_epic_id":"fac-2","ocman.permission_profile":"factory-plan/v1"}},
 {"id":"fac-2.2","status":"open","issue_type":"gate","metadata":{"ocman.contract":"1","ocman.kind":"gate","ocman.formula_id":"custom/team","ocman.formula_revision":"1","ocman.formula_hash":"HASH","ocman.formula_origin":"custom","ocman.instantiation_id":"custom-intake","ocman.work_epic_id":"fac-2","ocman.gate_type":"plan-approval"}}
]`, "PROJECT", project)
	return strings.ReplaceAll(issues, "HASH", hash)
}
