package state

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func stateQueryPlan(t *testing.T, db *sql.DB, query string, args ...any) string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(details, "\n")
}

func TestPermissionHistoryUsesScopedIdentityIndex(t *testing.T) {
	d := openTestStateDB(t)
	plan := stateQueryPlan(t, d.db, fmt.Sprintf(sessionPermissionHistorySQL, "?", "?"), "p", "s", "p", "s")
	for _, want := range []string{
		"SEARCH inbox_item USING INDEX inbox_permission_identity",
		"SEARCH i USING INDEX inbox_permission_identity (<expr>=? AND <expr>=? AND <expr>=?)",
	} {
		if !strings.Contains(plan, want) {
			t.Errorf("missing %s:\n%s", want, plan)
		}
	}
}

func TestPermissionApprovalWindowsUseRangeIndexes(t *testing.T) {
	d := openTestStateDB(t)
	for _, tt := range []struct{ directory, index string }{
		{"/repo", "permission_lifecycle_dashboard_idx (project_root=? AND requested_at>?)"},
		{"", "permission_lifecycle_requested (requested_at>?)"},
	} {
		t.Run(tt.directory, func(t *testing.T) {
			query, args := permissionApprovalQuery(100, tt.directory)
			plan := stateQueryPlan(t, d.db, query, args...)
			if !strings.Contains(plan, tt.index) || strings.Contains(plan, "TEMP B-TREE") {
				t.Fatalf("window must use an ordered range, got:\n%s", plan)
			}
		})
	}
}

func TestHistoryMaintenanceUsesPredicateIndexes(t *testing.T) {
	d := openTestStateDB(t)
	for _, tt := range []struct{ name, query, index string }{
		{"size-window", `SELECT database,sampled_at,size_bytes FROM database_size_sample WHERE sampled_at>=? ORDER BY sampled_at,database`, "database_size_sample_window"},
		{"delivery-retention", `DELETE FROM webhook_delivery WHERE accepted_at>0 AND accepted_at<?`, "webhook_delivery_retention"},
		{"routine-cancel", `UPDATE webhook_dispatch SET state='cancelled',finished_at=1 WHERE routine_id=? AND state='queued'`, "webhook_dispatch_queued_routine"},
		{"scope-plan", `SELECT issue_id FROM factory_project_request_gate WHERE plan_issue_id=? AND resolution='approved'`, "factory_project_request_plan"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := stateQueryPlan(t, d.db, tt.query, 100)
			if !strings.Contains(plan, tt.index) || strings.Contains(plan, "TEMP B-TREE") {
				t.Fatal(plan)
			}
		})
	}
}

func TestListPluginsReadsRegistrationsInOneQuery(t *testing.T) {
	exporter := recordStateSQL(t)
	d, desc, instances := pluginFixture(t)
	for i := range 3 {
		desc.ID = fmt.Sprintf("io.ocman.test%d", i)
		requirePluginOK(t, d.DiscoverPlugin(t.Context(), desc, "/bin/plugin", strings.Repeat("a", 64), instances))
	}
	requirePluginOK(t, d.SetPluginConfiguration(t.Context(), desc.ID, nil, map[string]string{"token": "do-not-leak"}))
	requirePluginOK(t, d.MarkPluginConfigurationWorking(t.Context(), desc.ID))
	requirePluginOK(t, d.SetPluginHealth(t.Context(), desc.ID, PluginHealth{Status: "unhealthy", LastError: "failed do-not-leak"}))
	requirePluginOK(t, d.RemovePlugin(t.Context(), desc.ID))
	want := map[string]PluginRegistration{}
	for _, id := range []string{"io.ocman.test", "io.ocman.test0", "io.ocman.test1", "io.ocman.test2"} {
		p, err := d.GetPlugin(t.Context(), id)
		requirePluginOK(t, err)
		want[id] = p
	}
	exporter.Reset()
	plugins, err := d.ListPlugins(t.Context())
	if err != nil || len(plugins) != 4 {
		t.Fatalf("plugins = %v, %v", plugins, err)
	}
	queries := 0
	for _, span := range exporter.GetSpans() {
		if span.Name == "sql.conn.query" {
			queries++
		}
	}
	if queries != 1 {
		t.Fatalf("listing four registrations made %d queries, want one", queries)
	}
	for _, p := range plugins {
		if !reflect.DeepEqual(p, want[p.Description.ID]) {
			t.Errorf("batch decoding changed registration: %+v", p)
		}
		if strings.Contains(p.Health.LastError, "do-not-leak") {
			t.Fatal("batch listing leaked a secret")
		}
	}
}
