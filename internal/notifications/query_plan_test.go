package notifications

import (
	"strings"
	"testing"
)

func TestDeliveryPollingUsesReceiptIndexes(t *testing.T) {
	m := fixture(t)
	for _, tt := range []struct{ name, query, index string }{
		{"due", dueDeliveriesSQL, "push_delivery_due"},
		{"retention", deliveryRetentionSQL, "push_delivery_retention"},
		{"expiry", deliveryExpirySQL, "push_delivery_"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := m.store.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+tt.query, 100)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var plan []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				plan = append(plan, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if details := strings.Join(plan, "\n"); !strings.Contains(details, tt.index) {
				t.Fatalf("polling must not scan all receipts:\n%s", details)
			}
		})
	}
}
