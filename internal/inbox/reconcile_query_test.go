package inbox

import (
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/testutil"
)

func TestReconcileDoesNotLoadUnrelatedInboxBodies(t *testing.T) {
	exporter := testutil.RecordSQL(t)
	d := statetest.Open(t)
	if _, err := d.CreateSessionInboxItem(t.Context(), "general", "not needed by reconciliation", state.InboxGeneral, nil); err != nil {
		t.Fatal(err)
	}
	exporter.Reset()
	NewModule(Deps{Store: d}).Reconcile(t.Context())
	found := false
	for _, span := range exporter.GetSpans() {
		for _, attr := range span.Attributes {
			if attr.Key == "db.query.summary" && strings.Contains(attr.Value.AsString(), "from inbox_item") {
				found = true
				if strings.Contains(attr.Value.AsString(), "body") {
					t.Errorf("reconciliation loaded display fields: %s", attr.Value.AsString())
				}
			}
		}
	}
	if !found {
		t.Fatal("permission read not observed")
	}
}
