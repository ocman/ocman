package statev2

import "testing"

func TestBackgroundCompletionRequiresAuthoritativeFiniteTaskEdge(t *testing.T) {
	for _, tc := range []struct {
		name       string
		before     SidebarObservation
		after      map[string]SidebarObservation
		lease      int64
		generation string
		want       int
	}{
		{"task finishes", SidebarObservation{BackgroundTasks: 1}, map[string]SidebarObservation{"native": {}}, 100, "g", 1},
		{"task omitted", SidebarObservation{BackgroundTasks: 1}, nil, 100, "g", 1},
		{"remaining task", SidebarObservation{BackgroundTasks: 2}, map[string]SidebarObservation{"native": {BackgroundTasks: 1}}, 100, "g", 0},
		{"service finishes", SidebarObservation{BackgroundTasks: 1, BackgroundServices: 1}, nil, 100, "g", 0},
		{"only service remains", SidebarObservation{BackgroundTasks: 2, BackgroundServices: 1}, map[string]SidebarObservation{"native": {BackgroundTasks: 1, BackgroundServices: 1}}, 100, "g", 1},
		{"disconnect", SidebarObservation{BackgroundTasks: 1}, nil, 0, "g", 0},
		{"new generation", SidebarObservation{BackgroundTasks: 1}, nil, 100, "next", 0},
		{"never busy", SidebarObservation{}, nil, 100, "g", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := openTestDB(t)
			scope := Instance{OwnerID: "owner", Platform: "test", SourceKey: "source"}
			if err := d.BeginSidebarGeneration(t.Context(), scope, "g"); err != nil {
				t.Fatal(err)
			}
			if err := d.ReplaceSidebarObservations(t.Context(), scope, "g", 100, map[string]SidebarObservation{"native": tc.before}); err != nil {
				t.Fatal(err)
			}
			if tc.generation != "g" {
				if err := d.BeginSidebarGeneration(t.Context(), scope, tc.generation); err != nil {
					t.Fatal(err)
				}
			}
			if err := d.ReplaceSidebarObservations(t.Context(), scope, tc.generation, tc.lease, tc.after); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := d.db.QueryRow(`SELECT count(*) FROM sidebar_background_completion WHERE owner_id='owner' AND platform='test' AND source_key='source' AND native_id='native' AND completed_at>0`).Scan(&count); err != nil || count != tc.want {
				t.Fatalf("completion count=%d want=%d err=%v", count, tc.want, err)
			}
		})
	}
}
