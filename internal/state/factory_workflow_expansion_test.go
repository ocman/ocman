package state

import "testing"

func TestFactoryExpandedWorkflowPersistsEveryPrerequisite(t *testing.T) {
	d := openTestStateDB(t)
	t.Cleanup(func() { _ = d.Close() })
	ep, err := d.CreateFactoryEpicWithProjects(t.Context(), "", "Expand workflow", "", "/repo", "", nativeTracerFormula(t), []string{"/other"})
	if err != nil {
		t.Fatal(err)
	}
	parent := factoryIssueID(t, d, ep.ID, "mol")
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := d.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	work := parent + ".90"
	verify := parent + ".91"
	delivery := parent + ".92"
	for i, row := range []struct{ id, kind, step string }{
		{work, "task", `{"key":"implement","kind":"implementation"}`},
		{verify, "workflow_template", `{"key":"verify","kind":"verification","needs":["implement"]}`},
		{delivery, "workflow_template", `{"key":"deliver","kind":"delivery","needs":["verify"]}`},
	} {
		exec(`INSERT INTO factory_issue(id, epic_id, project_path, kind, title, status, created_at) VALUES (?, ?, '/repo', ?, ?, 'open', 1)`, row.id, ep.ID, row.kind, row.id)
		exec(`INSERT INTO factory_issue_hierarchy(parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, ?, 'required')`, parent, row.id, 90+i)
		exec(`INSERT INTO factory_workflow_step(issue_id, definition_json) VALUES (?, ?)`, row.id, row.step)
	}
	if _, err := d.reconcileFactoryWorkflow(t.Context(), ep.ID); err != nil {
		t.Fatal(err)
	}
	// The first project's instances now have persisted dependencies. A later
	// project must get new edges rather than inheriting those IDs only in memory.
	other := parent + ".93"
	exec(`INSERT INTO factory_issue(id, epic_id, project_path, kind, title, status, created_at) VALUES (?, ?, '/other', 'task', 'Other implementation', 'open', 2)`, other, ep.ID)
	exec(`INSERT INTO factory_issue_hierarchy(parent_issue_id, child_issue_id, child_index, requirement) VALUES (?, ?, 93, 'required')`, parent, other)
	exec(`INSERT INTO factory_workflow_step(issue_id, definition_json) VALUES (?, '{"key":"implement","kind":"implementation"}')`, other)
	if _, err := d.reconcileFactoryWorkflow(t.Context(), ep.ID); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE factory_issue SET status = 'closed', outcome = 'succeeded' WHERE id = ?`, other)
	issues, err := d.ListFactoryIssues(t.Context(), ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	checks, deliveries := 0, 0
	for _, issue := range issues {
		if issue.Project != "/other" || issue.Workflow == nil {
			continue
		}
		switch issue.Workflow.Kind {
		case "verification":
			checks++
		case "delivery":
			deliveries++
		default:
			continue
		}
		if len(issue.DependsOn) != 2 || issue.DispatchState == "ready" {
			t.Errorf("new %s has dependencies %#v and state %q; prerequisite in /repo is unfinished", issue.Workflow.Kind, issue.DependsOn, issue.DispatchState)
		}
	}
	if checks != 1 || deliveries != 1 {
		t.Fatalf("expanded instances = %d checks, %d deliveries", checks, deliveries)
	}
}
