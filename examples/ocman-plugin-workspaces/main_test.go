package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/NoUseFreak/ocman/sdk/plugin"
)

func TestWorkspaceLifecycle(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := description().Validate(); err != nil {
		t.Fatal(err)
	}
	w := &workspace{}
	req := plugin.PageRequest{PageID: "services", ProjectID: "project-a"}
	doc, err := w.handle(t.Context(), "read", req)
	if err != nil || *doc.Configured || doc.Collection == nil {
		t.Fatalf("initial: %+v %v", doc, err)
	}
	req.Revision = doc.Revision
	req.Values = json.RawMessage(`{"services":[{"name":"api","replicas":2,"environment":"staging"}]}`)
	if doc, err = w.handle(t.Context(), "validate", req); err != nil || len(doc.Issues) > 0 {
		t.Fatalf("validate: %+v %v", doc, err)
	}
	if doc, err = w.handle(t.Context(), "save", req); err != nil || !*doc.Configured || len(doc.Collection.Rows) != 1 {
		t.Fatalf("save: %+v %v", doc, err)
	}
	if _, err = w.handle(t.Context(), "save", req); err == nil {
		t.Fatal("stale save accepted")
	}
	read := plugin.PageRequest{PageID: req.PageID, ProjectID: req.ProjectID, ItemID: doc.Collection.Rows[0].ID}
	detail, err := w.handle(t.Context(), "read", read)
	if err != nil || detail.Detail == nil || detail.Detail.Title != "api" || len(detail.Fields) > 0 {
		t.Fatalf("detail: %+v %v", detail, err)
	}
	read.ItemID = "missing"
	if _, err = w.handle(t.Context(), "read", read); err == nil {
		t.Fatal("missing detail accepted")
	}
	read.ItemID = ""
	read.Cursor = "bad"
	if _, err = w.handle(t.Context(), "read", read); err == nil {
		t.Fatal("unsupported cursor accepted")
	}
	read.Cursor = ""
	read.ProjectID = "other-project"
	other, err := w.handle(t.Context(), "read", read)
	if err != nil || *other.Configured {
		t.Fatal("project isolation")
	}
	_, path, err := load(req.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("private storage permissions")
	}
	for _, raw := range []string{`null`, `[]`, `{"services":[{}]}`, `{"services":[{"name":"reserved","replicas":1,"environment":"staging"}]}`, `{"services":[{"name":"a","replicas":1,"environment":"staging"},{"name":"a","replicas":1,"environment":"staging"}]}`} {
		if len(issues(json.RawMessage(raw))) == 0 {
			t.Fatalf("invalid input: %s", raw)
		}
	}
}
