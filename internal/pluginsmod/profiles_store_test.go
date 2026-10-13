package pluginsmod

import (
	"errors"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

const readOnlyRulesJSON = `[{"permission":"bash","pattern":"*","action":"deny"},{"permission":"edit","pattern":"*","action":"deny"},{"permission":"write","pattern":"*","action":"deny"}]`

func readOnlyRules() []platforms.PermissionRule {
	return []platforms.PermissionRule{
		{Permission: "bash", Pattern: "*", Action: "deny"},
		{Permission: "edit", Pattern: "*", Action: "deny"},
		{Permission: "write", Pattern: "*", Action: "deny"},
	}
}

func TestPermissionProfileCRUDRoundTripAndUniqueness(t *testing.T) {
	db := openPluginStore(t)
	ctx := t.Context()

	profile := PermissionProfile{ID: "profile-1", Name: "read-only", RulesJSON: readOnlyRulesJSON}
	if err := db.CreatePermissionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	if err := db.CreatePermissionProfile(ctx, PermissionProfile{ID: "profile-2", Name: "Bravo", RulesJSON: `[{"permission":"bash","pattern":"*","action":"deny"}]`}); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetPermissionProfile(ctx, "profile-1")
	if err != nil || got != profile {
		t.Fatalf("get = %+v, %v", got, err)
	}

	// Unique names, case-insensitively.
	err = db.CreatePermissionProfile(ctx, PermissionProfile{ID: "profile-3", Name: "Read-Only", RulesJSON: `[]`})
	if !errors.Is(err, ErrPermissionProfileNameTaken) {
		t.Fatalf("duplicate create err = %v", err)
	}

	list, err := db.ListPermissionProfiles(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "Bravo" || list[1].Name != "read-only" {
		t.Fatalf("list = %+v, %v", list, err)
	}

	profile.RulesJSON = `[{"permission":"bash","pattern":"git *","action":"allow"}]`
	if err := db.UpdatePermissionProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetPermissionProfile(ctx, "profile-1")
	if err != nil || got.RulesJSON != profile.RulesJSON {
		t.Fatalf("after update = %+v, %v", got, err)
	}
	// Renaming onto a taken name conflicts.
	other := list[0]
	other.Name = "read-only"
	if err := db.UpdatePermissionProfile(ctx, other); !errors.Is(err, ErrPermissionProfileNameTaken) {
		t.Fatalf("rename conflict err = %v", err)
	}

	if err := db.DeletePermissionProfile(ctx, "profile-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetPermissionProfile(ctx, "profile-1"); !errors.Is(err, ErrPermissionProfileNotFound) {
		t.Fatalf("get after delete err = %v", err)
	}
	if err := db.DeletePermissionProfile(ctx, "profile-1"); !errors.Is(err, ErrPermissionProfileNotFound) {
		t.Fatalf("double delete err = %v", err)
	}
	if err := db.UpdatePermissionProfile(ctx, profile); !errors.Is(err, ErrPermissionProfileNotFound) {
		t.Fatalf("update after delete err = %v", err)
	}
	// The name is free again after the delete.
	if err := db.CreatePermissionProfile(ctx, PermissionProfile{ID: "profile-4", Name: "read-only", RulesJSON: readOnlyRulesJSON}); err != nil {
		t.Fatalf("recreate after delete: %v", err)
	}
}

func TestResolvePermissionProfile(t *testing.T) {
	db := openPluginStore(t)
	ctx := t.Context()

	if _, err := db.ResolvePermissionProfile(ctx, "read-only"); !errors.Is(err, ErrPermissionProfileNotFound) {
		t.Fatalf("missing resolve err = %v", err)
	}
	if err := db.CreatePermissionProfile(ctx, PermissionProfile{ID: "profile-1", Name: "read-only", RulesJSON: readOnlyRulesJSON}); err != nil {
		t.Fatal(err)
	}

	rules, err := db.ResolvePermissionProfile(ctx, "read-only")
	if err != nil || len(rules) == 0 || !reflect.DeepEqual(rules, readOnlyRules()) {
		t.Fatalf("resolve = %+v, %v", rules, err)
	}
	// Name lookup matches the unique index's case-insensitivity.
	if rules, err := db.ResolvePermissionProfile(ctx, "READ-ONLY"); err != nil || !reflect.DeepEqual(rules, readOnlyRules()) {
		t.Fatalf("case-insensitive resolve = %+v, %v", rules, err)
	}

	if err := db.DeletePermissionProfile(ctx, "profile-1"); err != nil {
		t.Fatal(err)
	}
	if rules, err := db.ResolvePermissionProfile(ctx, "read-only"); !errors.Is(err, ErrPermissionProfileNotFound) || rules != nil {
		t.Fatalf("resolve after delete = %+v, %v", rules, err)
	}
}

func TestResolvePermissionProfileRejectsEmptyStore(t *testing.T) {
	db := openPluginStore(t)
	ctx := t.Context()
	if err := db.CreatePermissionProfile(ctx, PermissionProfile{ID: "profile-1", Name: "empty", RulesJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ResolvePermissionProfile(ctx, "empty"); err == nil {
		t.Fatal("empty stored rule set resolved without error")
	}
}
