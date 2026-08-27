package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"

	"github.com/NoUseFreak/ocman/internal/factory"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
)

type fakeFactoryService struct {
	prepared   factory.PreparedWork
	epic       factory.WorkEpic
	prepareErr error
	ackErr     error
	createErr  error
	ackedPath  string
	created    factory.PreparedWork
}

func (f *fakeFactoryService) PrepareWork(context.Context, factory.PrepareWorkRequest) (factory.PreparedWork, error) {
	return f.prepared, f.prepareErr
}

func (f *fakeFactoryService) AcknowledgeLocalExecution(_ context.Context, projectPath string) error {
	f.ackedPath = projectPath
	return f.ackErr
}

func (f *fakeFactoryService) CreatePreparedWorkEpic(_ context.Context, prepared factory.PreparedWork) (factory.WorkEpic, error) {
	f.created = prepared
	return f.epic, f.createErr
}

func buildFactoryMCPServer(t *testing.T, svc *fakeFactoryService) *mcptest.Server {
	t.Helper()
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{FactoryService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv
}

func TestFactoryToolsPrepareAcknowledgeAndCreateConfirmedWork(t *testing.T) {
	brief := "## Constraints\n\n- Keep the API stable."
	svc := &fakeFactoryService{
		prepared: factory.PreparedWork{
			PreparationKey: "factory-intake-42", Goal: "Ship search", Brief: brief, ProjectPath: "/repo",
			Formula: factory.DefaultFormula(), AcknowledgementRequired: true,
		},
		epic: factory.WorkEpic{
			ID: "fac-1", Status: "open", Goal: "Ship search", Brief: brief, InitialProject: "/repo",
			Planning: factory.PlanningState{WorkID: "fac-1.1", WorkStatus: "open", ApprovalGateID: "fac-1.2", ApprovalStatus: "open"},
		},
	}
	srv := buildFactoryMCPServer(t, svc)

	prepared := resultObject(t, srv, "prepare_factory_work", map[string]interface{}{
		"goal": "Ship search", "brief": brief, "project_path": "/repo/nested",
	})
	if prepared["preparation_key"] != "factory-intake-42" || prepared["project_path"] != "/repo" || prepared["acknowledgement_required"] != true {
		t.Fatalf("prepare result = %#v", prepared)
	}
	formula, _ := prepared["formula"].(map[string]interface{})
	if formula["id"] != factory.DefaultFormulaID || formula["version"] != float64(factory.DefaultFormulaVersion) {
		t.Fatalf("formula = %#v", formula)
	}

	acknowledged := resultObject(t, srv, "acknowledge_factory_execution", map[string]interface{}{"project_path": "/repo"})
	if acknowledged["acknowledged"] != true || svc.ackedPath != "/repo" {
		t.Fatalf("acknowledgement = %#v, path %q", acknowledged, svc.ackedPath)
	}

	created := resultObject(t, srv, "create_factory_work_epic", map[string]interface{}{
		"preparation_key": "factory-intake-42", "goal": "Ship search", "brief": brief, "project_path": "/repo",
	})
	if created["work_epic_id"] != "fac-1" || created["mission_control_path"] != "/factory" || created["handoff_complete"] != true {
		t.Fatalf("create result = %#v", created)
	}
	if svc.created.Formula.ID != factory.DefaultFormulaID || svc.created.PreparationKey != "factory-intake-42" {
		t.Fatalf("created input = %#v", svc.created)
	}
}

func TestFactoryToolsHideImplementationFailures(t *testing.T) {
	svc := &fakeFactoryService{prepareErr: errors.New("Beads and SQLite failed at /secret/path")}
	result := callTool(t, buildFactoryMCPServer(t, svc), "prepare_factory_work", map[string]interface{}{
		"goal": "Ship search", "brief": "Confirmed brief", "project_path": "/repo",
	})
	text := resultText(result)
	if !result.IsError || text != "factory_unavailable: Open Mission Control for diagnostics." {
		t.Fatalf("result = error %v, %q", result.IsError, text)
	}
	for _, hidden := range []string{"Beads", "SQLite", "/secret"} {
		if strings.Contains(text, hidden) {
			t.Fatalf("implementation detail %q leaked in %q", hidden, text)
		}
	}
}

func TestFactoryToolsReturnNeutralDomainErrors(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want string
	}{
		{factory.ErrProjectNotLocalGit, "project_not_local_git: The project must be an existing local Git repository."},
		{factory.ErrPreparationStale, "factory_preparation_stale: Prepare and confirm the handoff again."},
		{factory.ErrAcknowledgementRequired, "factory_acknowledgement_required: Show the local execution warning and obtain explicit acknowledgement."},
		{factory.ErrInstantiationConflict, "factory_intake_conflict: This preparation belongs to different confirmed inputs."},
	} {
		svc := &fakeFactoryService{prepareErr: tt.err}
		result := callTool(t, buildFactoryMCPServer(t, svc), "prepare_factory_work", map[string]interface{}{
			"goal": "Ship search", "brief": "Confirmed brief", "project_path": "/repo",
		})
		if !result.IsError || resultText(result) != tt.want {
			t.Errorf("error %v = %q, want %q", tt.err, resultText(result), tt.want)
		}
	}
}

func TestFactoryToolSchemasHideImplementationDetails(t *testing.T) {
	tools := internalmcp.ServerTools(internalmcp.Deps{FactoryService: &fakeFactoryService{}})
	definitions := make([]interface{}, 0, len(tools))
	for _, tool := range tools {
		definitions = append(definitions, tool.Tool)
	}
	encoded, err := json.Marshal(definitions)
	if err != nil {
		t.Fatal(err)
	}
	schema := strings.ToLower(string(encoded))
	for _, hidden := range []string{"beads", "sqlite"} {
		if strings.Contains(schema, hidden) {
			t.Errorf("Factory tool schema exposes implementation term %q", hidden)
		}
	}
}
