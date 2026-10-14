package state_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/NoUseFreak/ocman/internal/factory"
	"github.com/NoUseFreak/ocman/internal/factory/model"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

// These tests drive Factory's service against state, so they live in the
// external test package; state's own tests must not import factory.

func init() {
	state.BuiltInTracerFormula = func() (string, int, string, string) {
		f := factory.BuiltInTracerFormula()
		return f.ID, f.Version, f.Source, f.Hash
	}
}

func openFactoryTestDB(t *testing.T) *state.DB {
	t.Helper()
	db, err := state.Open(statetest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	return db
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
	db := openFactoryTestDB(t)
	defer db.Close()
	formula := nativeTracerFormulaExternal()
	epic, err := db.CreateFactoryEpic(context.Background(), "", "Ship search", "Keep it small", "/repo", "", formula)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := factory.NewNative(db).Pour(context.Background(), epic.ID); err != nil {
		t.Fatalf("service Pour created built-in Epic: %v", err)
	}
}

func TestFactoryPourNormalizesLegacyBuiltInSourceHash(t *testing.T) {
	db := openFactoryTestDB(t)
	defer db.Close()
	ctx := context.Background()
	formula := nativeTracerFormulaExternal()
	legacy, err := factory.NewNative(db).GetFormula(ctx, "ocman/tracer", 1)
	if err != nil {
		t.Fatal(err)
	}
	formula.Version, formula.Source, formula.Hash = legacy.Version, legacy.Source, legacy.Hash
	epic, err := db.CreateFactoryEpic(ctx, "", "Ship search", "Keep it small", "/repo", "", formula)
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
