package statev2

import (
	"context"
	"strings"
)

// Checkpoints reads every cursor of an instance whose stream starts with
// prefix, keyed by the remainder. Catch-up compares many sessions at once
// without one query per session.
func (d *DB) Checkpoints(ctx context.Context, instanceID, prefix string) (map[string]string, error) {
	// substr() avoids LIKE wildcards in caller-supplied prefixes.
	rows, err := d.reader().QueryContext(ctx, `SELECT stream,cursor FROM checkpoint WHERE instance_id=? AND substr(stream,1,?)=?`,
		instanceID, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var stream, cursor string
		if err := rows.Scan(&stream, &cursor); err != nil {
			return nil, err
		}
		out[strings.TrimPrefix(stream, prefix)] = cursor
	}
	return out, rows.Err()
}
