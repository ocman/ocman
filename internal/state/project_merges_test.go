package state

import (
	"errors"
	"testing"
)

func TestProjectMergesPersistChainAndRejectCycles(t *testing.T) {
	path := templateDBPath(t)
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	b := ProjectMergeTarget{Key: "b", Directory: "/b", RemoteID: "remote"}
	c := ProjectMergeTarget{Key: "c", Directory: "/c"}
	for _, merge := range []struct {
		source string
		target ProjectMergeTarget
	}{{"a", b}, {"b", c}} {
		if err := d.MergeProjects(ctx, merge.source, merge.target); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.MergeProjects(ctx, "c", ProjectMergeTarget{Key: "a"}); !errors.Is(err, ErrSameProject) {
		t.Fatalf("cycle: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	merges, err := d.ProjectMerges(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := merges.Resolve(ProjectMergeTarget{Key: "a"}); got != c {
		t.Fatalf("got %+v, want %+v", got, c)
	}
	if len(merges) != 2 {
		t.Fatalf("unexpected merges: %v", merges)
	}
}

func TestProjectMergesRejectBrokenStorage(t *testing.T) {
	d, err := Open(templateDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for _, value := range []string{"null", "invalid"} {
		if err := d.SetSetting(t.Context(), projectMergesSetting, value); err != nil {
			t.Fatal(err)
		}
		_, err = d.ProjectMerges(t.Context())
		if (err != nil) != (value == "invalid") {
			t.Fatalf("value %q: %v", value, err)
		}
	}
	if err := d.MergeProjects(t.Context(), "a", ProjectMergeTarget{Key: "b"}); err == nil {
		t.Fatal("accepted malformed stored merges")
	}
}

func TestProjectMergeWriteFailurePreservesExistingMerge(t *testing.T) {
	d, err := Open(templateDBPath(t))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	b := ProjectMergeTarget{Key: "b", Directory: "/b"}
	if err := d.MergeProjects(t.Context(), "a", b); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`CREATE TRIGGER reject_merge_update BEFORE UPDATE ON setting
		WHEN NEW.key = 'projects.merges' BEGIN SELECT RAISE(ABORT, 'write failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := d.MergeProjects(t.Context(), "b", ProjectMergeTarget{Key: "c", Directory: "/c"}); err == nil {
		t.Fatal("accepted failed write")
	}
	merges, err := d.ProjectMerges(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := merges.Resolve(ProjectMergeTarget{Key: "a"}); got != b {
		t.Fatalf("failed merge changed target: %+v", got)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ProjectMerges(t.Context()); err == nil {
		t.Fatal("accepted closed database read")
	}
	if err := d.MergeProjects(t.Context(), "a", b); err == nil {
		t.Fatal("accepted closed database write")
	}
}
