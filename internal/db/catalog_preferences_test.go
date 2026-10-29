package db

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/statev2"
)

func TestCatalogPreferencesKeepCollidingIdentitiesScoped(t *testing.T) {
	path, store := historyPerformanceStore(t)
	if err := store.RegisterInstance(t.Context(), statev2.Instance{ID: "two", OwnerID: "owner", Platform: "p", SourceKey: "other"}); err != nil {
		t.Fatal(err)
	}
	for i, instance := range []string{"one", "two"} {
		metadata, err := json.Marshal(map[string]string{"providerID": "provider", "modelID": instance})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Commit(t.Context(), statev2.Batch{
			Session:  statev2.Session{Key: statev2.Key{InstanceID: instance, SessionID: "shared"}, Directory: "/" + instance, UpdatedAt: int64(i + 10)},
			Messages: []statev2.Message{{ID: "shared-message", Role: "assistant", CreatedAt: int64(i + 10), Extensions: metadata}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, instances := range [][]string{{"one"}, {"one", "two"}} {
		base, err := OpenHistoryAnalytics(path, instances)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = base.Close() })
		recents, defaults, err := base.CatalogPreferences(t.Context(), "", "/one")
		if err != nil || len(recents) != len(instances) || defaults.Model != "provider/one" {
			t.Fatalf("catalog for %v: %+v %+v %v", instances, recents, defaults, err)
		}
		if len(instances) == 2 {
			if _, release, err := base.ScopedHistoryReader(t.Context()); err == nil {
				release()
				t.Fatal("legacy analytics must still reject colliding IDs")
			}
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, _, err := base.CatalogPreferences(ctx, "", ""); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled catalog: %v", err)
		}
	}
	if _, _, err := (&DB{}).CatalogPreferences(t.Context(), "", ""); err == nil {
		t.Fatal("non-history reader accepted")
	}
}

func TestCatalogPreferencesPropagateQueryErrors(t *testing.T) {
	for _, query := range []string{
		`DROP VIEW history_fact`,
		`DROP VIEW session; CREATE TEMP VIEW session AS SELECT instance_id,id,time_updated FROM main.analytics_session`,
	} {
		t.Run(query, func(t *testing.T) {
			path, _ := historyPerformanceStore(t)
			base, err := OpenHistoryAnalytics(path, []string{"one"})
			if err != nil {
				t.Fatal(err)
			}
			defer base.Close()
			base.db.SetMaxOpenConns(1)
			if _, err := base.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if _, _, err := base.CatalogPreferences(t.Context(), "", "/project"); err == nil {
				t.Fatal("catalog query failure ignored")
			}
		})
	}
}
