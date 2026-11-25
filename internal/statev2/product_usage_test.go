package statev2

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/usagewire"
)

func TestProductUsageDayScopesAndDeduplicates(t *testing.T) {
	d := openTestDB(t)
	day := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	for _, instance := range []string{"first", "second", "excluded"} {
		register(t, d, instance)
		b := fixture(instance)
		commit(t, d, b)
		for _, m := range []struct {
			model, role string
			hour        int
			valid       int
		}{
			{"gpt-5.4", "assistant", 0, 1}, {"gpt-5.4", "assistant", 23, 1},
			{"private-alias", "assistant", 3, 1}, {"other-private-alias", "assistant", 3, 1},
			{"claude-opus-4-7", "assistant", 24, 1}, {"claude-opus-4-7", "assistant", -1, 1},
			{"claude-opus-4-7", "assistant", 1, 0}, {"claude-opus-4-7", "user", 1, 1},
		} {
			bucket := day.Add(time.Duration(m.hour) * time.Hour).UnixMilli()
			_, err := d.db.Exec(`INSERT INTO analytics_hour VALUES(?,?,?,?,?,?,?,'','',?,?,1,2,3,0,0,0,1,0,1,0,0,0,0,0,0,0,0,0,2,3,0,0)`, instance, b.Session.SessionID, bucket, m.role, "provider", m.model, m.valid, bucket, bucket)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := d.ProductUsageDay(t.Context(), []string{"first", "second"}, day)
	if err != nil || got.Sessions != 2 || len(got.Models) != 2 || !got.Valid() {
		t.Fatalf("summary %+v: %v", got, err)
	}
	want := []usagewire.ModelUsage{{Model: "gpt-5.4", Sessions: 2, InputTokens: 8, OutputTokens: 12}, {Model: "other", Sessions: 2, InputTokens: 8, OutputTokens: 12}}
	for i, model := range want {
		if got.Models[i] != model {
			t.Fatalf("model %d: %+v, want %+v", i, got.Models[i], model)
		}
	}
	if got, err := d.ProductUsageDay(t.Context(), nil, day); err != nil || got.Sessions != 0 {
		t.Fatal(got, err)
	}
}
