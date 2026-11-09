package owned

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/statev2"
)

func TestIdentityImportPreservesNativeCreationTimestamp(t *testing.T) {
	for _, path := range []string{"inventory", "identity probe", "ingestion"} {
		t.Run(path, func(t *testing.T) {
			a, p, catalogue := fixture(t)
			const created int64 = 1790841600456
			const activity int64 = created + 3600000
			p.rows = []db.Session{{ID: "historical", Directory: "/repo", TimeCreated: created, TimeUpdated: activity}}
			before := time.Now().UnixMilli()
			switch path {
			case "inventory":
				if err := a.importRows(t.Context(), p.rows); err != nil {
					t.Fatal(err)
				}
			case "identity probe":
				if _, err := a.canonical(t.Context(), "historical"); err != nil {
					t.Fatal(err)
				}
			case "ingestion":
				publish(t, a, p.rows...)
			}
			row, err := catalogue.FindBinding(t.Context(), a.binding("historical"))
			if err != nil || row.CreatedAt != created || row.UpdatedAt < before || row.UpdatedAt > time.Now().UnixMilli() {
				t.Fatalf("import lost native creation or catalogue modification time: %+v %v", row, err)
			}
			if path == "ingestion" {
				stored, err := a.history.ReadConversation(t.Context(), statev2.Key{InstanceID: "source", SessionID: row.ID}, 10, 0)
				if err != nil || stored.Session.CreatedAt != created || stored.Session.UpdatedAt != activity {
					t.Fatalf("history activity timestamps changed: %+v %v", stored, err)
				}
			}
		})
	}
}
