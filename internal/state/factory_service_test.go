package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/ocman/ocman/internal/factory"
	"github.com/ocman/ocman/internal/factory/model"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

// These tests drive Factory's service against state, so they live in the
// external test package; state's own tests must not import factory.

func init() {
	state.BuiltInTracerFormula = func() (string, int, string, string) {
		f := factory.BuiltInTracerFormula()
		return f.ID, f.Version, f.Source, f.Hash
	}
}

func nativeTracerFormulaExternal() model.NativeFormula {
	formula := factory.BuiltInTracerFormula()
	return model.NativeFormula{
		ID: formula.ID, Version: formula.Version, Source: formula.Source, Hash: formula.Hash,
		Nodes: []model.NativeFormulaNode{{Key: "plan", Kind: "plan"}, {Key: "approval", Kind: "gate"}, {Key: "materialization", Kind: "materialization"}},
		Edges: []model.NativeFormulaEdge{{From: "approval", To: "plan", Type: "blocks"}, {From: "materialization", To: "approval", Type: "blocks"}},
	}
}

func TestFactoryServicePoursCreatedBuiltInEpic(t *testing.T) {
	db := statetest.Open(t)
	formula := nativeTracerFormulaExternal()
	epic, err := db.CreateFactoryEpicWithProjectsAndPermissionRules(context.Background(), "", "Ship search", "Keep it small", "/repo", "", formula, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := factory.NewNativeWithExecution(db, nil, nil, nil).Pour(context.Background(), epic.ID); err != nil {
		t.Fatalf("service Pour created built-in Epic: %v", err)
	}
}

func TestFactoryPourNormalizesLegacyBuiltInSourceHash(t *testing.T) {
	db := statetest.Open(t)
	ctx := context.Background()
	formula := nativeTracerFormulaExternal()
	legacy, err := factory.NewNativeWithExecution(db, nil, nil, nil).GetFormula(ctx, "ocman/tracer", 1)
	if err != nil {
		t.Fatal(err)
	}
	formula.Version, formula.Source, formula.Hash = legacy.Version, legacy.Source, legacy.Hash
	epic, err := db.CreateFactoryEpicWithProjectsAndPermissionRules(ctx, "", "Ship search", "Keep it small", "/repo", "", formula, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(formula.Source))
	legacyHash := hex.EncodeToString(sum[:])
	if _, err := db.SQL().Exec(`UPDATE factory_epic SET formula_hash = ? WHERE id = ?`, legacyHash, epic.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.PourFactoryEpic(ctx, epic.ID, formula); err != nil {
		t.Fatalf("PourFactoryEpic legacy built-in pin: %v", err)
	}
	got, err := db.GetFactoryEpic(ctx, epic.ID)
	if err != nil || got.FormulaHash != legacyHash {
		t.Fatalf("legacy pin changed = %#v, %v", got, err)
	}
}
