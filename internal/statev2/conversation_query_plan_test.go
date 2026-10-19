package statev2

import (
	"strings"
	"testing"
)

func TestConversationDefaultsBoundMessageReadsToCandidateSessions(t *testing.T) {
	d := openTestDB(t)
	for _, filter := range []string{"", " AND directory=?"} {
		args := []any{"first", "current"}
		if filter != "" {
			args = append(args, "/repo")
		}
		args = append(args, "first")
		rows, err := d.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+conversationDefaultsQuery(filter), args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		bounded := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
			if strings.Contains(detail, "SEARCH m ") && strings.Contains(detail, "session_id=?") {
				bounded = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bounded {
			t.Fatalf("composer defaults scan messages outside candidate sessions:\n%s", strings.Join(plan, "\n"))
		}
	}
}
