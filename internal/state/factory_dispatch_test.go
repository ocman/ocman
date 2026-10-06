package state

import (
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/factory/model"
)

func TestFactoryDispatchEpicsExcludeHistory(t *testing.T) {
	d := openTestStateDB(t)
	t.Cleanup(func() { _ = d.Close() })
	for _, status := range []string{"open", "paused", "closed"} {
		ep, err := d.CreateFactoryEpicWithProjects(t.Context(), "", status, "", "/repo", "", nativeTracerFormula(t), []string{"/secondary"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.db.Exec(`UPDATE factory_epic SET status = ? WHERE id = ?`, status, ep.ID); err != nil {
			t.Fatal(err)
		}
	}
	epics, err := d.ListFactoryDispatchEpics(t.Context())
	if err != nil || len(epics) != 1 || epics[0].Status != "open" || len(epics[0].Projects) != 2 || !epics[0].Projects[1].Removable {
		t.Fatalf("dispatch epics = %#v, %v", epics, err)
	}
}

func TestFactoryDispatchDeadlines(t *testing.T) {
	d := openTestStateDB(t)
	t.Cleanup(func() { _ = d.Close() })
	ep, err := d.CreateFactoryEpic(t.Context(), "", "Deadlines", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := d.db.Exec(query, args...); err != nil {
			t.Fatal(err)
		}
	}
	assertDeadline := func(want time.Time) {
		t.Helper()
		got, err := d.NextFactoryDispatchAt(t.Context(), 30*time.Second)
		if err != nil || !got.Equal(want) {
			t.Fatalf("deadline = %v, %v, want %v", got, err, want)
		}
	}
	assertDeadline(time.Time{})
	exec(`INSERT INTO factory_issue(id, epic_id, project_path, kind, title, status, retry_at, created_at) VALUES ('retry', ?, '/repo', 'task', 'Retry', 'retry_wait', 60000, 1)`, ep.ID)
	assertDeadline(time.UnixMilli(60000))
	exec(`UPDATE factory_epic SET status = 'paused' WHERE id = ?`, ep.ID)
	assertDeadline(time.Time{})
	exec(`UPDATE factory_epic SET status = 'open' WHERE id = ?`, ep.ID)
	exec(`INSERT INTO factory_removed_issue(issue_id, plan_id, plan_revision, removed_at) VALUES ('retry', ?, 0, 1)`, ep.ID)
	assertDeadline(time.Time{})
	exec(`INSERT INTO factory_issue(id, epic_id, project_path, kind, title, status, outcome, created_at) VALUES ('delivery', ?, '/repo', 'delivery', 'Delivery', 'closed', 'succeeded', 1)`, ep.ID)
	exec(`INSERT INTO factory_issue(id, epic_id, project_path, kind, title, status, created_at) VALUES ('dependent', ?, '/repo', 'task', 'Dependent', 'open', 2)`, ep.ID)
	exec(`INSERT INTO factory_issue_dependency(issue_id, depends_on_issue_id, type) VALUES ('dependent', 'delivery', 'merge_gated')`)
	exec(`INSERT INTO factory_attempt(id, epic_id, work_item_id, sequence, phase, terminal_outcome, frozen_policy_json, result_json, created_at, updated_at, finished_at) VALUES ('attempt', ?, 'delivery', 1, 'terminal', 'succeeded', '{}', '{"prUrl":"https://forge.example/pulls/1","commitSha":"one"}', 1, 1, 1)`, ep.ID)
	assertDeadline(time.UnixMilli(0))
	if err := d.RecordFactoryDeliveryObservation(t.Context(), model.FactoryDeliveryObservation{DeliveryIssueID: "delivery", AttemptID: "attempt", Status: "open", ObservedAt: 1000}); err != nil {
		t.Fatal(err)
	}
	assertDeadline(time.UnixMilli(31000))
	exec(`UPDATE factory_merge_gate_observation SET status = 'merged'`)
	assertDeadline(time.Time{})
	exec(`UPDATE factory_merge_gate_observation SET status = 'open'`)
	exec(`INSERT INTO factory_removed_issue(issue_id, plan_id, plan_revision, removed_at) VALUES ('dependent', ?, 0, 1)`, ep.ID)
	assertDeadline(time.Time{})
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.NextFactoryDispatchAt(t.Context(), time.Second); err == nil {
		t.Fatal("closed DB returned a deadline")
	}
}

func TestFactoryWorkflowSkipsExistingDependencies(t *testing.T) {
	exporter := recordStateSQL(t)
	d := openTestStateDB(t)
	t.Cleanup(func() { _ = d.Close() })
	ep, err := d.CreateFactoryEpic(t.Context(), "", "Dependencies", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		if _, err := d.db.Exec(`INSERT INTO factory_issue(id, epic_id, project_path, kind, title, status, created_at) VALUES (?, ?, '/repo', 'task', ?, 'open', 1)`, id, ep.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.db.Exec(`INSERT INTO factory_workflow_step(issue_id, definition_json) VALUES ('first', '{"key":"first","kind":"implementation"}'), ('second', '{"key":"second","kind":"implementation","needs":["first"]}')`); err != nil {
		t.Fatal(err)
	}
	for pass := range 2 {
		exporter.Reset()
		if _, err := d.reconcileFactoryWorkflow(t.Context(), ep.ID); err != nil {
			t.Fatal(err)
		}
		inserts := 0
		for _, span := range exporter.GetSpans() {
			for _, attr := range span.Attributes {
				if attr.Key == "db.statement" && strings.Contains(attr.Value.AsString(), "INSERT OR IGNORE INTO factory_issue_dependency") {
					inserts++
				}
			}
		}
		if want := 1 - pass; inserts != want {
			t.Fatalf("pass %d attempted %d dependency inserts, want %d", pass, inserts, want)
		}
	}
}
