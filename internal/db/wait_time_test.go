package db

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestWaitTimeByAgent(t *testing.T) {
	for _, mirror := range []bool{false, true} {
		t.Run(map[bool]string{false: "source", true: "mirror"}[mirror], func(t *testing.T) {
			d := openTestDB(t)
			defer d.Close()
			insertSession(t, d, "s", "test", "/repo", 1000, 11000)
			insertSession(t, d, "outside", "test", "/other", 1000, 11000)
			for _, tc := range []struct {
				id, session, agent, model, finish string
				tools                             []toolTiming
			}{
				{"overlap", "s", "build", "one", "tool-calls", []toolTiming{{3000, 11000}, {5000, 11000}}},
				{"plain", "s", "plan", "two", "stop", nil},
				{"unknown", "s", "build", "one", "tool-calls", []toolTiming{{2000, 0}}},
				{"missing", "s", "build", "one", "tool-calls", nil},
				{"all-tools", "s", "build", "one", "stop", []toolTiming{{1000, 11000}}},
				{"clipped", "s", "build", "one", "stop", []toolTiming{{500, 2000}, {10000, 15000}}},
				{"unnamed", "s", "", "one", "stop", nil},
				{"outside", "outside", "build", "one", "stop", nil},
			} {
				insertMessage(t, d, tc.id, tc.session, 1000, map[string]any{
					"role": "assistant", "agent": tc.agent, "providerID": "p", "modelID": tc.model, "finish": tc.finish,
					"time": map[string]any{"created": 1000, "completed": 11000},
				})
				for i, timing := range tc.tools {
					insertPart(t, d, tc.id+string(rune('a'+i)), tc.id, tc.session, 2000, map[string]any{
						"type": "tool", "state": map[string]any{"time": timing},
					})
				}
			}
			insertMessage(t, d, "unfinished", "s", 1000, map[string]any{
				"role": "assistant", "agent": "build", "providerID": "p", "modelID": "one",
				"time": map[string]any{"created": 1000},
			})
			if mirror {
				if err := d.EnableAnalyticsMirror(filepath.Join(t.TempDir(), "analytics.db"), "test"); err != nil {
					t.Fatal(err)
				}
				if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
					t.Fatal(err)
				}
				if _, err := d.db.Exec(`DROP TABLE part`); err != nil {
					t.Fatal(err)
				}
			}
			check := func(opts MetricsDashboardOptions, want map[string][3]int64) {
				t.Helper()
				metrics, err := d.GetMetricsPerformance(t.Context(), opts)
				if err != nil {
					t.Fatal(err)
				}
				// Check the public wire fields, including their zero values.
				raw, err := json.Marshal(metrics.Agents)
				if err != nil {
					t.Fatal(err)
				}
				var agents []map[string]any
				if err := json.Unmarshal(raw, &agents); err != nil {
					t.Fatal(err)
				}
				if len(agents) != len(want) {
					t.Fatalf("agents = %s; want %v", raw, want)
				}
				for _, agent := range agents {
					expected, ok := want[agent["agent"].(string)]
					if !ok {
						t.Fatalf("unexpected agent: %v", agent)
					}
					for i, field := range []string{"agentDurationMs", "toolDurationMs", "unknownDurationMs"} {
						if agent[field] != float64(expected[i]) {
							t.Fatalf("%s = %v, want %d; agent = %v", field, agent[field], expected[i], agent)
						}
					}
				}
			}
			check(MetricsDashboardOptions{Dir: "/repo"}, map[string][3]int64{"build": {10000, 20000, 20000}, "plan": {10000, 0, 0}, "": {10000, 0, 0}})
			check(MetricsDashboardOptions{Dir: "/repo", AgentFilter: "build", ModelFilter: "p/one"}, map[string][3]int64{"build": {10000, 20000, 20000}})
			check(MetricsDashboardOptions{Dir: "/repo", ModelFilter: "p/two"}, map[string][3]int64{"plan": {10000, 0, 0}})
			check(MetricsDashboardOptions{Since: 11000}, map[string][3]int64{})
		})
	}
}
