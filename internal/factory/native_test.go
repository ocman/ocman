package factory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

type nativeStoreFake struct {
	epic          model.NativeEpic
	issues        []model.NativeIssue
	poured        bool
	pouredFormula model.NativeFormula
	policy        model.FactoryCapacityPolicy
	formulas      []model.NativeFormulaRevision
	mutation      model.GraphMutation
	mutationErr   error
	acknowledged  string
	comments      []model.NativeIssueComment
	commentErr    error
}

func (s *nativeStoreFake) UpsertFactoryLocalExecutionAck(_ context.Context, host, project, profile, version, _ string, _ time.Time) error {
	s.acknowledged = strings.Join([]string{host, project, profile, version}, "/")
	return nil
}

type nativeClosureStoreFake struct {
	nativeStoreFake
	closedMol, closedEpic string
	removed               []model.NativeIssue
}

func (s *nativeClosureStoreFake) CloseFactoryMol(_ context.Context, epicID, molID string) error {
	s.closedMol = epicID + "/" + molID
	return nil
}
func (s *nativeClosureStoreFake) CloseFactoryEpic(_ context.Context, epicID string) error {
	s.closedEpic = epicID
	return nil
}
func (s *nativeClosureStoreFake) ListRemovedFactoryIssues(context.Context, string) ([]model.NativeIssue, error) {
	return s.removed, nil
}

func (s *nativeStoreFake) MutateFactoryGraph(_ context.Context, mutation model.GraphMutation) error {
	s.mutation = mutation
	return s.mutationErr
}

func (s *nativeStoreFake) ListNativeFactoryFormulaRevisions(context.Context) ([]model.NativeFormulaRevision, error) {
	return s.formulas, nil
}
func (s *nativeStoreFake) GetNativeFactoryFormulaRevision(_ context.Context, id string, revision int) (model.NativeFormulaRevision, error) {
	for _, formula := range s.formulas {
		if formula.FormulaID == id && formula.Revision == revision {
			return formula, nil
		}
	}
	return model.NativeFormulaRevision{}, sql.ErrNoRows
}
func (s *nativeStoreFake) SaveNativeFactoryFormulaRevision(_ context.Context, formula model.NativeFormulaRevision, _ time.Time) (model.NativeFormulaRevision, error) {
	for _, existing := range s.formulas {
		if existing.FormulaID == formula.FormulaID && existing.ContentHash == formula.ContentHash {
			return existing, nil
		}
	}
	for _, existing := range s.formulas {
		if existing.FormulaID == formula.FormulaID && existing.Revision >= formula.Revision {
			formula.Revision = existing.Revision + 1
		}
	}
	if formula.Revision == 0 {
		formula.Revision = 1
	}
	s.formulas = append(s.formulas, formula)
	return formula, nil
}

func (s *nativeStoreFake) GetFactoryCapacityPolicy(context.Context) (model.FactoryCapacityPolicy, error) {
	if s.policy.GlobalCapacity == 0 {
		return model.FactoryCapacityPolicy{GlobalCapacity: 10, ProjectCapacity: 4, ProjectOverrides: map[string]int{}}, nil
	}
	return s.policy, nil
}
func (s *nativeStoreFake) SetFactoryCapacityPolicy(_ context.Context, policy model.FactoryCapacityPolicy) error {
	s.policy = policy
	return nil
}

type testProjectResolver struct {
	root string
	err  error
}

func (r testProjectResolver) ResolveLocalProject(context.Context, string) (string, error) {
	return r.root, r.err
}

func (s *nativeStoreFake) CreateFactoryEpic(_ context.Context, goal, brief, project, _ string, formula model.NativeFormula) (model.NativeEpic, error) {
	s.epic = model.NativeEpic{ID: "epic-1", Status: "open", Goal: goal, Brief: brief, InitialProject: project, FormulaID: formula.ID, FormulaVersion: formula.Version, FormulaHash: formula.Hash}
	return s.epic, nil
}

func TestNativeServiceCanonicalizesProjectAndRejectsResolverFailure(t *testing.T) {
	store := &nativeStoreFake{}
	svc := NewNative(store, testProjectResolver{root: "/repo"})
	if _, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{Goal: "Ship", InitialProject: "/repo/subdir"}); !errors.Is(err, ErrAcknowledgementRequired) {
		t.Fatalf("missing acknowledgement error = %v", err)
	}
	created, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{Goal: "Ship", InitialProject: "/repo/subdir", AcknowledgeLocalExecution: true})
	if err != nil || created.InitialProject != "/repo" || store.acknowledged != "local//repo/factory-implement/v1" {
		t.Fatalf("CreateWorkEpic = %#v, %v", created, err)
	}
	svc = NewNative(store, testProjectResolver{err: errors.New("not a repo")})
	if _, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{Goal: "Ship", InitialProject: "/repo", AcknowledgeLocalExecution: true}); !errors.Is(err, ErrProjectNotLocalGit) {
		t.Fatalf("CreateWorkEpic error = %v, want ErrProjectNotLocalGit", err)
	}
}

func TestNativeServiceMutatesGraphThroughTypedStore(t *testing.T) {
	store := &nativeStoreFake{}
	mutation := GraphMutation{Action: "edit", EpicID: "epic-1", IssueID: "epic-1.1", Title: "Rename"}
	if err := NewNative(store).MutateGraph(context.Background(), mutation); err != nil || store.mutation != mutation {
		t.Fatalf("MutateGraph = %#v, %v", store.mutation, err)
	}
	if err := NewNative(struct{ nativeStore }{&nativeStoreFake{}}).MutateGraph(context.Background(), mutation); !errors.Is(err, ErrFactoryUnavailable) {
		t.Fatalf("MutateGraph unavailable error = %v", err)
	}
}

func TestNativeServiceRejectsUnavailableOptionalStores(t *testing.T) {
	ctx := context.Background()
	svc := NewNative(struct{ nativeStore }{&nativeStoreFake{}})

	tests := []struct {
		name string
		call func() error
	}{
		{"capacity policy", func() error { _, err := svc.GetCapacityPolicy(ctx); return err }},
		{"set capacity policy", func() error {
			_, err := svc.SetCapacityPolicy(ctx, CapacityPolicy{GlobalCapacity: 1, ProjectCapacity: 1})
			return err
		}},
		{"close Mol", func() error { return svc.CloseMol(ctx, "epic", "mol") }},
		{"close Epic", func() error { return svc.CloseEpic(ctx, "epic") }},
		{"defer Issue", func() error { return svc.DeferIssue(ctx, "epic", "issue", "later") }},
		{"resume Issue", func() error { return svc.ResumeIssue(ctx, "epic", "issue") }},
		{"retry Issue", func() error { return svc.RetryIssueAt(ctx, "epic", "issue", time.Now().Add(time.Minute)) }},
		{"materialize", func() error { _, err := svc.Materialize(ctx, "epic", "issue"); return err }},
		{"create recovery gate", func() error {
			_, err := svc.CreateRecoveryGate(ctx, "attempt", "token", "question", "reason", nil)
			return err
		}},
		{"resolve recovery gate", func() error { _, err := svc.ResolveRecoveryGate(ctx, "gate", "resume", ""); return err }},
		{"escalate permission", func() error {
			_, _, err := svc.EscalatePermission(ctx, "session", "request", "bash", "git status")
			return err
		}},
		{"resolve authority gate", func() error { _, err := svc.ResolveAuthorityEscalationGate(ctx, "gate", "approve"); return err }},
		{"queue", func() error { _, err := svc.Queue(ctx); return err }},
		{"complete attempt", func() error { return svc.CompleteAttempt(ctx, "attempt", "token", "done", "https://example.com/pr/1") }},
		{"claim plan", func() error { _, err := svc.ClaimPlan(ctx, "epic", "plan"); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.call(); !errors.Is(err, ErrFactoryUnavailable) {
				t.Fatalf("error = %v, want ErrFactoryUnavailable", err)
			}
		})
	}

	implementation, err := svc.IsImplementationSession(ctx, "session")
	if err != nil || implementation {
		t.Fatalf("IsImplementationSession = %v, %v", implementation, err)
	}
}

func TestNativeServiceSetsValidatedCanonicalCapacityPolicy(t *testing.T) {
	store := &nativeStoreFake{}
	svc := NewNative(store, testProjectResolver{root: "/repo"})
	policy, err := svc.SetCapacityPolicy(context.Background(), CapacityPolicy{GlobalCapacity: 10, ProjectCapacity: 4, ProjectOverrides: map[string]int{"/repo/worktree": 2}})
	if err != nil || policy.ProjectOverrides["/repo"] != 2 {
		t.Fatalf("SetCapacityPolicy = %#v, %v", policy, err)
	}
	if _, err := svc.SetCapacityPolicy(context.Background(), CapacityPolicy{GlobalCapacity: 0, ProjectCapacity: 4}); err == nil {
		t.Fatal("accepted invalid capacity")
	}
	if store.policy.GlobalCapacity != 10 || store.policy.ProjectCapacity != 4 || store.policy.ProjectOverrides["/repo"] != 2 {
		t.Fatalf("invalid policy changed stored policy: %#v", store.policy)
	}
	if _, err := svc.SetCapacityPolicy(context.Background(), CapacityPolicy{GlobalCapacity: 10, ProjectCapacity: 4, ProjectOverrides: map[string]int{"/repo/a": 2, "/repo/b": 3}}); err == nil {
		t.Fatal("accepted conflicting aliases")
	}
}

func TestNativeServiceInspectsBuiltInTracerFormula(t *testing.T) {
	formula, err := NewNative(&nativeStoreFake{}).GetFormula(context.Background(), "ocman/tracer", 1)
	if err != nil {
		t.Fatalf("GetFormula: %v", err)
	}
	if formula.Source != tracerFormulaSource || formula.Hash == "" || !formula.Valid {
		t.Fatalf("Formula = %#v", formula)
	}
	if formula.SourceHash != sourceHash(tracerFormulaSource) {
		t.Fatalf("built-in source hash = %q", formula.SourceHash)
	}
	if sum := sha256.Sum256(formula.Compiled); formula.Hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("built-in compiled hash = %q", formula.Hash)
	}
	if len(formula.Inputs) != 2 || len(formula.Nodes) != 3 || len(formula.Edges) != 2 {
		t.Fatalf("Formula graph = %#v", formula)
	}
	if _, err := NewNative(&nativeStoreFake{}).GetFormula(context.Background(), "missing", 1); !errors.Is(err, ErrFormulaNotFound) {
		t.Fatalf("missing Formula error = %v", err)
	}
	if _, err := NewNative(&nativeStoreFake{}).GetFormula(context.Background(), "ocman/tracer", 2); !errors.Is(err, ErrFormulaNotFound) {
		t.Fatalf("unknown Formula version error = %v", err)
	}
}

func TestNativeFormulaRevisionsAreTOMLOnlyAndContentAddressed(t *testing.T) {
	store := &nativeStoreFake{}
	svc := NewNative(store)
	source := `version = 1
name = "Team"

[[input]]
key = "goal"

[[input]]
key = "initial_project"

[[issue]]
key = "plan"
kind = "plan"
`
	first, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/team", Source: source})
	if err != nil || first.Version != 1 || first.Hash == "" || string(first.Compiled) == "" {
		t.Fatalf("SaveFormula = %#v, %v", first, err)
	}
	if sum := sha256.Sum256(first.Compiled); first.Hash != hex.EncodeToString(sum[:]) || first.SourceHash == first.Hash {
		t.Fatalf("Formula hashes = %#v", first)
	}
	repeated, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/team", Source: source})
	if err != nil || repeated.Version != 1 || repeated.Hash != first.Hash || len(store.formulas) != 1 {
		t.Fatalf("repeated SaveFormula = %#v, %v; revisions = %#v", repeated, err, store.formulas)
	}
	changed := strings.Replace(source, `name = "Team"`, `name = "Team v2"`, 1)
	second, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/team", Source: changed})
	if err != nil || second.Version != 2 || second.Hash == first.Hash || store.formulas[0].SourceTOML != source {
		t.Fatalf("changed SaveFormula = %#v, %v; revisions = %#v", second, err, store.formulas)
	}
	for _, invalid := range []string{"name: Team", "version = 1\nname = Team"} {
		if _, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/team", Source: invalid}); err == nil {
			t.Fatalf("SaveFormula accepted invalid source %q", invalid)
		}
	}
	builtIn := BuiltInTracerFormula()
	if got := sha256.Sum256(formulaCompiled(t, tracerFormulaSource)); builtIn.Hash != hex.EncodeToString(got[:]) {
		t.Fatalf("built-in hash changed: %q", builtIn.Hash)
	}
}

func formulaCompiled(t *testing.T, source string) []byte {
	t.Helper()
	compiled, err := compileNativeFormula(source)
	if err != nil {
		t.Fatal(err)
	}
	return []byte(compiled.JSON)
}

func TestNativeFormulaCompositionValidationAndSave(t *testing.T) {
	store := &nativeStoreFake{}
	svc := NewNative(store)
	child := `version = 1
name = "Child"

[[input]]
key = "goal"

[[input]]
key = "initial_project"

[[issue]]
key = "child-plan"
kind = "plan"
`
	_, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/child", Source: child})
	if err != nil {
		t.Fatal(err)
	}
	parent := `version = 1
name = "Parent"

[[input]]
key = "goal"

[[input]]
key = "initial_project"

[[issue]]
key = "plan"
kind = "plan"

[[composition]]
key = "child"
formula = "custom/child"
revision = 1
requirement = "optional"
bind_goal = "goal"
bind_initial_project = "initial_project"
`
	preview, err := svc.PreviewFormula(context.Background(), parent, "")
	if err != nil || !preview.Valid || len(preview.Composition) != 1 || preview.Composition[0].Requirement != "optional" || preview.Composition[0].Bindings["goal"] != "goal" || !strings.Contains(string(preview.Compiled), `"composition"`) {
		t.Fatalf("PreviewFormula = %#v, %v", preview, err)
	}
	if _, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/parent", Source: parent}); err != nil {
		t.Fatal(err)
	}

	invalid := []struct{ name, source, want, saveWant string }{
		{"missing revision", strings.Replace(parent, "custom/child", "custom/missing", 1), "missing Formula revision", "missing Formula revision"},
		{"incomplete binding", strings.Replace(parent, "bind_initial_project = \"initial_project\"\n", "", 1), "missing binding", "missing binding"},
		{"unresolved binding", strings.Replace(parent, `bind_goal = "goal"`, `bind_goal = "missing"`, 1), "unresolved", "unresolved"},
		{"duplicate stable key", strings.Replace(parent, "key = \"child\"\nformula", "key = \"plan\"\nformula", 1), "stable keys", "stable keys"},
		{"invalid requirement", strings.Replace(parent, `requirement = "optional"`, `requirement = "later"`, 1), "requirement", "requirement"},
		{"direct cycle", strings.Replace(parent, "custom/child", "custom/self", 1), "missing Formula revision", "cycle"},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			before := len(store.formulas)
			view, err := svc.ValidateFormula(context.Background(), tt.source, "")
			if err != nil || view.Valid || !strings.Contains(strings.Join(view.Errors, "\n"), tt.want) {
				t.Fatalf("ValidateFormula = %#v, %v; want %q", view, err, tt.want)
			}
			if _, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/self", Source: tt.source}); !errors.Is(err, ErrInvalidFormula) || !strings.Contains(err.Error(), tt.saveWant) || len(store.formulas) != before {
				t.Fatalf("SaveFormula = %v; formulas = %#v", err, store.formulas)
			}
		})
	}
	direct := strings.Replace(parent, "custom/child", "custom/self", 1)
	if view, err := svc.PreviewFormula(context.Background(), direct, "custom/self"); err != nil || view.Valid || !strings.Contains(strings.Join(view.Errors, "\n"), "cycle") {
		t.Fatalf("PreviewFormula direct cycle = %#v, %v", view, err)
	}

	base := strings.Replace(parent, `[[composition]]
key = "child"
formula = "custom/child"
revision = 1
bind_goal = "goal"
bind_initial_project = "initial_project"
`, "", 1)
	if _, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/a", Source: base}); err != nil {
		t.Fatal(err)
	}
	childToA := strings.Replace(parent, "custom/child", "custom/a", 1)
	if _, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/b", Source: childToA}); err != nil {
		t.Fatal(err)
	}
	indirect := strings.Replace(parent, "custom/child", "custom/b", 1)
	view, err := svc.PreviewFormula(context.Background(), indirect, "")
	if err != nil || !view.Valid { // A is not the prospective root for preview.
		t.Fatalf("PreviewFormula = %#v, %v", view, err)
	}
	if saved, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/a", Source: indirect}); err != nil || saved.Version != 2 {
		t.Fatalf("SaveFormula revision-pinned composition = %#v, %v", saved, err)
	}
}

func TestNativeFormulaCompositionAllowsEarlierRevisionOfParent(t *testing.T) {
	store := &nativeStoreFake{}
	svc := NewNative(store)
	base := `version = 1
name = "Formula"

[[input]]
key = "goal"

[[input]]
key = "initial_project"

[[issue]]
key = "plan"
kind = "plan"
`
	if _, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/a", Source: base}); err != nil {
		t.Fatal(err)
	}
	b := base + `
[[composition]]
key = "a"
formula = "custom/a"
revision = 1
bind_goal = "goal"
bind_initial_project = "initial_project"
`
	if _, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/b", Source: b}); err != nil {
		t.Fatal(err)
	}
	a2 := strings.Replace(b, `formula = "custom/a"`, `formula = "custom/b"`, 1)
	if got, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/a", Source: a2}); err != nil || got.Version != 2 {
		t.Fatalf("SaveFormula revision-sensitive composition = %#v, %v", got, err)
	}
}

func TestNativeFormulaCompositionValidation(t *testing.T) {
	store := &nativeStoreFake{}
	svc := NewNative(store)
	child := `version = 1
name = "Child"

[[input]]
key = "goal"

[[input]]
key = "initial_project"

[[issue]]
key = "plan"
kind = "plan"
`
	_, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/child", Source: child})
	if err != nil {
		t.Fatal(err)
	}
	parent := child + `
[[composition]]
key = "child"
formula = "custom/child"
revision = 1
bind_goal = "goal"
bind_initial_project = "initial_project"
`
	if got, err := svc.ValidateFormula(context.Background(), parent, ""); err != nil || !got.Valid || len(got.Errors) != 0 || got.Composition[0].Requirement != "required" {
		t.Fatalf("valid composition = %#v, %v", got, err)
	}
	for name, source := range map[string]string{
		"missing revision":     strings.Replace(parent, `custom/child`, `custom/missing`, 1),
		"missing binding":      strings.Replace(parent, "bind_initial_project = \"initial_project\"\n", "", 1),
		"unresolved binding":   strings.Replace(parent, `bind_goal = "goal"`, `bind_goal = "missing"`, 1),
		"duplicate stable key": strings.Replace(parent, `key = "child"`, `key = "plan"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := svc.ValidateFormula(context.Background(), source, "")
			if err != nil || got.Valid || len(got.Errors) == 0 {
				t.Fatalf("ValidateFormula = %#v, %v; want validation error", got, err)
			}
		})
	}

	a := strings.Replace(parent, `custom/child`, `custom/b`, 1)
	b := strings.Replace(parent, `custom/child`, `custom/a`, 1)
	for id, source := range map[string]string{"custom/a": a, "custom/b": b} {
		compiled, err := compileNativeFormula(source)
		if err != nil {
			t.Fatal(err)
		}
		store.formulas = append(store.formulas, model.NativeFormulaRevision{FormulaID: id, Name: id, Revision: 1, SourceTOML: source, CompiledJSON: compiled.JSON, ContentHash: compiled.Hash})
	}
	if got, err := svc.ValidateFormula(context.Background(), strings.Replace(parent, `custom/child`, `custom/a`, 1), ""); err != nil || got.Valid || !strings.Contains(strings.Join(got.Errors, "\n"), "cycle") {
		t.Fatalf("cyclic composition = %#v, %v", got, err)
	}
}

func TestNativeFormulaValidationReportsSyntaxAndInvalidComposition(t *testing.T) {
	store := &nativeStoreFake{formulas: []model.NativeFormulaRevision{{FormulaID: "custom/broken", Revision: 1, SourceTOML: "version = 1", CompiledJSON: "{}", ContentHash: "broken"}}}
	svc := NewNative(store)

	if view, err := svc.ValidateFormula(context.Background(), "version = 1\nname = \"Bad\"\n[[unknown]]", "custom/bad"); err != nil || view.Valid || !strings.Contains(strings.Join(view.Errors, "\n"), "unsupported TOML table") {
		t.Fatalf("ValidateFormula malformed source = %#v, %v", view, err)
	}

	parent := `version = 1
name = "Parent"
[[input]]
key = "goal"
[[input]]
key = "initial_project"
[[issue]]
key = "plan"
kind = "plan"
[[composition]]
key = "broken"
formula = "custom/broken"
revision = 1
bind_goal = "goal"
bind_initial_project = "initial_project"
`
	if view, err := svc.PreviewFormula(context.Background(), parent, "custom/parent"); err != nil || view.Valid || !strings.Contains(strings.Join(view.Errors, "\n"), "references invalid Formula revision") {
		t.Fatalf("PreviewFormula invalid composition = %#v, %v", view, err)
	}
	if _, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/parent", Source: parent}); !errors.Is(err, ErrInvalidFormula) {
		t.Fatalf("SaveFormula invalid composition error = %v", err)
	}
}

func TestNativeFormulaRejectsCorruptStoredCompiledJSON(t *testing.T) {
	store := &nativeStoreFake{formulas: []model.NativeFormulaRevision{{FormulaID: "custom/team", Revision: 1, SourceTOML: "version = 1", ContentHash: "hash", CompiledJSON: "{"}}}
	if _, err := NewNative(store).GetFormula(context.Background(), "custom/team", 1); !errors.Is(err, ErrFormulaCorrupt) {
		t.Fatalf("GetFormula corrupt revision error = %v", err)
	}
}

func TestNativeFormulaRejectsStoredCompiledJSONThatDiffersFromSource(t *testing.T) {
	source := `version = 1
name = "Team"

[[input]]
key = "goal"

[[input]]
key = "initial_project"

[[issue]]
key = "plan"
kind = "plan"
`
	compiled, err := compileNativeFormula(source)
	if err != nil {
		t.Fatal(err)
	}
	stored := strings.Replace(compiled.JSON, `"name":"Team"`, `"name":"Other"`, 1)
	sum := sha256.Sum256([]byte(stored))
	store := &nativeStoreFake{formulas: []model.NativeFormulaRevision{{FormulaID: "custom/team", Revision: 1, SourceTOML: source, ContentHash: hex.EncodeToString(sum[:]), CompiledJSON: stored}}}
	if _, err := NewNative(store).GetFormula(context.Background(), "custom/team", 1); !errors.Is(err, ErrFormulaCorrupt) {
		t.Fatalf("GetFormula mismatched revision error = %v", err)
	}
}
func (s *nativeStoreFake) ListFactoryEpics(context.Context) ([]model.NativeEpic, error) {
	return []model.NativeEpic{s.epic}, nil
}
func (s *nativeStoreFake) GetFactoryEpic(context.Context, string) (model.NativeEpic, error) {
	return s.epic, nil
}
func (s *nativeStoreFake) PourFactoryEpic(_ context.Context, id string, formula model.NativeFormula) (model.NativeEpic, []model.NativeIssue, error) {
	s.poured = true
	s.pouredFormula = formula
	s.epic.FormulaID, s.epic.FormulaVersion, s.epic.FormulaHash = formula.ID, formula.Version, formula.Hash
	s.issues = []model.NativeIssue{{ID: id + ".plan", Kind: "plan"}, {ID: id + ".approval", Kind: "gate"}, {ID: id + ".materialization", Kind: "materialization"}}
	return s.epic, s.issues, nil
}

func TestNativeServiceResolvesPinnedNestedFormulaBeforePour(t *testing.T) {
	store := &nativeStoreFake{}
	svc := NewNative(store, testProjectResolver{root: "/repo"})
	child := `version = 1
name = "Child"
[[input]]
key = "goal"
[[input]]
key = "initial_project"
[[issue]]
key = "plan"
kind = "plan"
`
	if _, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/child", Source: child}); err != nil {
		t.Fatal(err)
	}
	childV1, err := svc.GetFormula(context.Background(), "custom/child", 1)
	if err != nil {
		t.Fatal(err)
	}
	parent := `version = 1
name = "Parent"
[[input]]
key = "goal"
[[input]]
key = "initial_project"
[[issue]]
key = "plan"
kind = "plan"
[[composition]]
key = "child"
formula = "custom/child"
revision = 1
bind_goal = "goal"
bind_initial_project = "initial_project"
`
	saved, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/parent", Source: parent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/child", Source: strings.Replace(child, `name = "Child"`, `name = "Child v2"`, 1)}); err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{Goal: "Ship", InitialProject: "/repo", FormulaID: "custom/parent", FormulaRevision: 1, AcknowledgeLocalExecution: true})
	if err != nil || created.FormulaID != "custom/parent" || created.FormulaHash != saved.Hash || created.FormulaOrigin != FormulaOriginCustom {
		t.Fatalf("CreateWorkEpic = %#v, %v", created, err)
	}
	if _, err := svc.Pour(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	if len(store.pouredFormula.Composition) != 1 || store.pouredFormula.Composition[0].Formula.ID != "custom/child" || store.pouredFormula.Composition[0].Formula.Hash != childV1.Hash {
		t.Fatalf("resolved Formula = %#v", store.pouredFormula)
	}
}

func TestNativeServiceCreatesEpicFromPinnedCustomFormula(t *testing.T) {
	store := &nativeStoreFake{}
	svc := NewNative(store, testProjectResolver{root: "/repo"})
	source := `version = 1
name = "Custom"
[[input]]
key = "goal"
[[input]]
key = "initial_project"
[[issue]]
key = "plan"
kind = "plan"
`
	saved, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/team", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	created, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{Goal: "Ship", InitialProject: "/repo", FormulaID: saved.ID, FormulaRevision: saved.Version, AcknowledgeLocalExecution: true})
	if err != nil || created.FormulaID != saved.ID || created.FormulaHash != saved.Hash {
		t.Fatalf("CreateWorkEpic = %#v, %v", created, err)
	}
}
func (s *nativeStoreFake) ListFactoryIssues(context.Context, string) ([]model.NativeIssue, error) {
	return s.issues, nil
}
func (s *nativeStoreFake) ListFactoryIssueComments(context.Context, string, string) ([]model.NativeIssueComment, error) {
	return s.comments, s.commentErr
}
func (s *nativeStoreFake) AppendFactoryIssueComment(_ context.Context, _, issueID, actor, body string, at time.Time) (model.NativeIssueComment, error) {
	comment := model.NativeIssueComment{ID: 1, IssueID: issueID, Actor: actor, Body: body, CreatedAt: at.UnixMilli()}
	s.comments = append(s.comments, comment)
	return comment, s.commentErr
}

func TestNativeServiceIssueComments(t *testing.T) {
	store := &nativeStoreFake{}
	svc := NewNative(store)
	comment, err := svc.AddIssueComment(t.Context(), "epic-1", "issue-1", "mcp", "Reviewed")
	if err != nil || comment.Actor != "mcp" || comment.Body != "Reviewed" {
		t.Fatalf("AddIssueComment = %#v, %v", comment, err)
	}
	comments, err := svc.ListIssueComments(t.Context(), "epic-1", "issue-1")
	if err != nil || len(comments) != 1 {
		t.Fatalf("ListIssueComments = %#v, %v", comments, err)
	}
	store.commentErr = model.ErrInvalidGraphMutation
	if _, err := svc.ListIssueComments(t.Context(), "epic-1", "missing"); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("missing issue error = %v", err)
	}
}

func TestNativeServiceCreatesReadsAndPoursTracer(t *testing.T) {
	store := &nativeStoreFake{}
	svc := NewNative(store, testProjectResolver{root: "/repo"})
	created, err := svc.CreateWorkEpic(context.Background(), CreateWorkEpicRequest{Goal: "Ship it", InitialProject: "/repo", AcknowledgeLocalExecution: true})
	if err != nil || created.ID != "epic-1" {
		t.Fatalf("CreateWorkEpic = %#v, %v", created, err)
	}
	if _, err := svc.GetWorkEpic(context.Background(), created.ID); err != nil {
		t.Fatalf("GetWorkEpic: %v", err)
	}
	if _, err := svc.ListWorkEpics(context.Background()); err != nil {
		t.Fatalf("ListWorkEpics: %v", err)
	}
	poured, err := svc.Pour(context.Background(), created.ID)
	if err != nil || !store.poured || len(poured) != 3 || poured[0].Kind != "plan" || poured[1].Kind != "gate" || poured[2].Kind != "materialization" {
		t.Fatalf("Pour = %#v, %v", poured, err)
	}
}

func TestNativeQueueRequiresAttemptStore(t *testing.T) {
	_, err := NewNative(&nativeStoreFake{}).Queue(context.Background())
	if !errors.Is(err, ErrFactoryUnavailable) {
		t.Fatalf("Queue error = %v", err)
	}
}

func TestNativeFormulaListAndProgressReflectBehavior(t *testing.T) {
	store := &nativeStoreFake{}
	svc := NewNative(store)
	if _, err := svc.SaveFormula(context.Background(), FormulaSaveRequest{ID: "custom/team", Source: `version = 1
name = "Team"
[[input]]
key = "goal"
[[input]]
key = "initial_project"
[[issue]]
key = "plan"
kind = "plan"`}); err != nil {
		t.Fatal(err)
	}
	formulas, err := svc.ListFormulas(context.Background())
	if err != nil || len(formulas) != 2 || formulas[1].ID != "custom/team" {
		t.Fatalf("ListFormulas = %#v, %v", formulas, err)
	}
	progress := factoryProgress([]model.NativeIssue{
		{ID: "required", Kind: "implementation", Title: "Required", Status: "closed", Outcome: "succeeded"},
		{ID: "gate", Kind: "gate", Title: "Gate", Status: "closed", Outcome: "succeeded", GateResolution: "rejected"},
		{ID: "optional", Kind: "implementation", Requirement: "optional", Status: "open", DispatchState: "ready"},
		{ID: "reference", Kind: "implementation", Requirement: "reference", Status: "open"},
	})
	if progress.RequiredTotal != 2 || progress.RequiredSucceeded != 1 || progress.OptionalOpen != 1 || !reflect.DeepEqual(progress.ClosureBlockers, []string{"Gate"}) || progress.Stuck {
		t.Fatalf("factoryProgress = %#v", progress)
	}
	failed := model.NativeIssue{ID: "failed", Kind: "task", Title: "Failed", Status: "closed", Outcome: "failed", DispatchState: "completed"}
	for name, tt := range map[string]struct {
		extra model.NativeIssue
		stuck bool
	}{
		"nothing movable":       {extra: model.NativeIssue{ID: "mol", Kind: "mol", Status: "open", DispatchState: "ready"}, stuck: true},
		"ready work":            {extra: model.NativeIssue{ID: "ready", Kind: "task", Status: "open", DispatchState: "ready"}},
		"ready materialization": {extra: model.NativeIssue{ID: "mat", Kind: "materialization", Status: "open", DispatchState: "ready"}},
		"running work":          {extra: model.NativeIssue{ID: "running", Kind: "task", Status: "in_progress", DispatchState: "waiting"}},
		"retry wait":            {extra: model.NativeIssue{ID: "retry", Kind: "task", Status: "retry_wait", DispatchState: "retry_wait"}},
		"deferred work":         {extra: model.NativeIssue{ID: "deferred", Kind: "task", Status: "deferred", DispatchState: "deferred"}, stuck: true},
		"open gate":             {extra: model.NativeIssue{ID: "gate", Kind: "gate", Requirement: "reference", Status: "open"}},
		"blocked dependent":     {extra: model.NativeIssue{ID: "dep", Kind: "task", Status: "open", DispatchState: "terminally_blocked"}, stuck: true},
	} {
		if got := factoryProgress([]model.NativeIssue{failed, tt.extra}); got.Stuck != tt.stuck {
			t.Errorf("%s: Stuck = %v, want %v", name, got.Stuck, tt.stuck)
		}
	}
	if factoryProgress([]model.NativeIssue{{ID: "done", Kind: "task", Status: "closed", Outcome: "succeeded"}}).Stuck {
		t.Error("closable epic reported stuck")
	}
}

func TestNativeClosureAndRemovedIssueAccessors(t *testing.T) {
	store := &nativeClosureStoreFake{removed: []model.NativeIssue{{ID: "removed", Kind: "implementation", Status: "closed"}}}
	svc := NewNative(store)
	if err := svc.CloseMol(context.Background(), "epic", "mol"); err != nil || store.closedMol != "epic/mol" {
		t.Fatalf("CloseMol = %v, %q", err, store.closedMol)
	}
	if err := svc.CloseEpic(context.Background(), "epic"); err != nil || store.closedEpic != "epic" {
		t.Fatalf("CloseEpic = %v, %q", err, store.closedEpic)
	}
	issues, err := svc.ListRemovedIssues(context.Background(), "epic")
	if err != nil || len(issues) != 1 || issues[0].ID != "removed" {
		t.Fatalf("ListRemovedIssues = %#v, %v", issues, err)
	}
}
