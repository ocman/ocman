package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

var sessionJSONReferences = []struct{ table, column, scope string }{
	{"inbox_item", "permission_json", "1"}, {"inbox_item", "session_json", "1"},
	{"factory_external_mapping", "metadata_json", "system='factory' AND external_kind='control_session' AND entity_kind='epic'"},
}

func rekeyReplacementBaselines(ctx context.Context, tx *sql.Tx, platform string, identities map[string]string) error {
	for _, column := range []string{"baseline_json", "stop_baseline_json"} {
		rows, err := tx.QueryContext(ctx, "SELECT rowid,"+column+" FROM session_replacement WHERE platform=?", platform)
		if err != nil {
			return err
		}
		type update struct {
			row  int64
			data string
		}
		var updates []update
		for rows.Next() {
			var row int64
			var raw string
			if err := rows.Scan(&row, &raw); err != nil {
				rows.Close()
				return err
			}
			var baseline map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &baseline); err != nil {
				rows.Close()
				return err
			}
			changed := false
			for old, id := range identities {
				value, ok := baseline[old]
				if !ok || old == id {
					continue
				}
				if _, exists := baseline[id]; exists {
					rows.Close()
					return fmt.Errorf("replacement baseline contains conflicting session identities")
				}
				baseline[id] = value
				delete(baseline, old)
				changed = true
			}
			if changed {
				data, err := json.Marshal(baseline)
				if err != nil {
					rows.Close()
					return err
				}
				updates = append(updates, update{row: row, data: string(data)})
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, u := range updates {
			if _, err := tx.ExecContext(ctx, "UPDATE session_replacement SET "+column+"=? WHERE rowid=?", u.data, u.row); err != nil {
				return err
			}
		}
	}
	return nil
}
