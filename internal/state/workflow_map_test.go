package state

import "testing"

func TestWorkflowMapItemCreationIsAtomicAndIdempotent(t *testing.T) {
	db := seedResourceRun(t)
	item := WorkflowMapItem{RunID: "run-1", MapNode: "one", ItemKey: "a", ItemIndex: 0, ChildRunID: "child-a", State: "active", CreatedAt: 10}
	child := WorkflowRun{ID: "child-a", WorkflowID: "workflow-1", VersionID: "version-1", State: "active", CreatedAt: 10, UpdatedAt: 10, ParentRunID: "run-1", ParentNodeID: "one", ItemKey: "a"}
	created, err := db.CreateWorkflowMapItem(t.Context(), item, child)
	if err != nil || !created {
		t.Fatalf("create map item: %v, %v", created, err)
	}

	duplicate := child
	duplicate.ID = "duplicate-child"
	created, err = db.CreateWorkflowMapItem(t.Context(), item, duplicate)
	if err != nil || created {
		t.Fatalf("duplicate map item: %v, %v", created, err)
	}
	if _, err := db.GetWorkflowRun(t.Context(), duplicate.ID); err == nil {
		t.Fatal("duplicate stable key created an orphan child run")
	}
	if err := db.SetWorkflowMapItemState(t.Context(), item.RunID, item.MapNode, item.ItemKey, "successful"); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListWorkflowMapItems(t.Context(), item.RunID, item.MapNode)
	if err != nil || len(items) != 1 || items[0].State != "successful" || items[0].ChildRunID != child.ID {
		t.Fatalf("persisted map items: %+v, %v", items, err)
	}

	orphan := child
	orphan.ID = "rolled-back-child"
	badItem := item
	badItem.ItemKey, badItem.ChildRunID = "b", orphan.ID
	if _, err := db.db.Exec(`CREATE TRIGGER fail_map_item BEFORE INSERT ON workflow_map_item WHEN NEW.item_key = 'b' BEGIN SELECT RAISE(ABORT, 'forced map item failure'); END`); err != nil {
		t.Fatal(err)
	}
	if created, err := db.CreateWorkflowMapItem(t.Context(), badItem, orphan); err == nil || created {
		t.Fatalf("invalid map item creation: %v, %v", created, err)
	}
	if _, err := db.GetWorkflowRun(t.Context(), orphan.ID); err == nil {
		t.Fatal("failed map item transaction retained its child run")
	}
}
