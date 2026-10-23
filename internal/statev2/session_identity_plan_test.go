package statev2

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestPlannedSessionRekeysResumeBoundedBatches(t *testing.T) {
	d := openTestDB(t)
	for _, id := range []string{"a-parent", "z-child"} {
		b := fixture("first")
		b.Session.SessionID, b.Checkpoint = id, nil
		if id == "z-child" {
			b.Session.ParentID = "a-parent"
		}
		commit(t, d, b)
	}
	mapping := map[string]string{"a-parent": "ocm_parent", "z-child": "ocm_child"}
	for i := range 499 {
		mapping[fmt.Sprintf("m%03d", i)] = fmt.Sprintf("ocm_missing%03d", i)
	}
	if err := d.PlanSessionRekeys(t.Context(), "first", mapping); err != nil {
		t.Fatal(err)
	}
	if err := d.SetSessionRekeyInventoryReady(t.Context(), "owner", true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	err := d.ApplyPlannedSessionRekeys(ctx, "owner", func(completed int) {
		if completed != 500 {
			t.Fatalf("unbounded migration batch: %d", completed)
		}
		cancel()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected interrupted migration: %v", err)
	}
	child := read(t, d, Key{"first", "z-child"})
	if child.Session.ParentID != "ocm_parent" {
		t.Fatalf("expected partially migrated relationship: %+v", child.Session)
	}
	path := d.FilePath()
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if ready, err := d.SessionRekeyInventoryReady(t.Context(), "owner"); err != nil || !ready {
		t.Fatalf("inventory completion was not durable: %v %v", ready, err)
	}
	if err := d.ApplyPlannedSessionRekeys(t.Context(), "owner", nil); err != nil {
		t.Fatal(err)
	}
	child = read(t, d, Key{"first", "ocm_child"})
	if child.Session.ParentID != "ocm_parent" || child.Parts[0].Text != "retained content" {
		t.Fatalf("resume lost relationship or conversation: %+v", child)
	}
	backups, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".statev2-before-session-ids-*.db"))
	if len(backups) != 2 {
		t.Fatalf("want one backup per interrupted/resumed pass, got %d", len(backups))
	}
	if err := d.ApplyPlannedSessionRekeys(t.Context(), "owner", nil); err != nil {
		t.Fatal(err)
	}
	again, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".statev2-before-session-ids-*.db"))
	if len(again) != len(backups) {
		t.Fatal("completed plan was replayed")
	}
}

func TestSessionIdentityPlansRejectConflictsAndStayOwnerScoped(t *testing.T) {
	d := openTestDB(t)
	if err := d.PlanSessionRekeys(t.Context(), "first", map[string]string{"session": "ocm_one"}); err != nil {
		t.Fatal(err)
	}
	if err := d.PlanSessionRekeys(t.Context(), "first", map[string]string{"session": "ocm_other"}); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("rebound staged identity: %v", err)
	}
	for _, mapping := range []map[string]string{{"": "ocm_one"}, {"native": ""}, {"native": "native"}} {
		if err := d.PlanSessionRekeys(t.Context(), "first", mapping); !errors.Is(err, ErrIdentityConflict) {
			t.Fatalf("invalid plan accepted: %+v %v", mapping, err)
		}
	}
	if err := d.ApplyPlannedSessionRekeys(t.Context(), "other-owner", nil); err != nil {
		t.Fatal(err)
	}
	_, plan, err := d.sessionRekeyPage(t.Context(), "owner")
	if err != nil || plan["session"] != "ocm_one" {
		t.Fatalf("foreign owner consumed plan: %+v %v", plan, err)
	}
}

func TestLegacyHistoryInvalidatesPreparationReceipt(t *testing.T) {
	d := openTestDB(t)
	if err := d.CompletePreparation(t.Context(), "owner", "receipt"); err != nil {
		t.Fatal(err)
	}
	if got, err := d.PreparationReceipt(t.Context(), "owner"); err != nil || got != "receipt" {
		t.Fatalf("missing receipt: %q %v", got, err)
	}
	commit(t, d, fixture("first"))
	if got, err := d.PreparationReceipt(t.Context(), "owner"); err != nil || got != "" {
		t.Fatalf("legacy history bypassed preparation: %q %v", got, err)
	}
}
