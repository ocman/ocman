package db

import (
	"context"
	"database/sql"
	"reflect"
)

// reconcileMirrorRows executes writes only for changed groups. The first
// column identifies a group (one session/message, or all timings for a message).
// Both queries must order duplicate keys identically.
// ponytail: compare the refresh window in memory; batch it if windows outgrow RAM.
// Full rebuilds still use the streaming copyRows path.
func reconcileMirrorRows(ctx context.Context, src *sql.DB, tx *sql.Tx, query string, args []any,
	existing, insert, remove string, n int,
) (bool, error) {
	rows, err := tx.QueryContext(ctx, existing, args...)
	old, err := mirrorRowGroups(rows, err, n)
	if err != nil {
		return false, err
	}
	rows, err = src.QueryContext(ctx, query, args...)
	next, err := mirrorRowGroups(rows, err, n)
	if err != nil {
		return false, err
	}
	ins, err := tx.PrepareContext(ctx, insert)
	if err != nil {
		return false, err
	}
	defer ins.Close()
	del, err := tx.PrepareContext(ctx, remove)
	if err != nil {
		return false, err
	}
	defer del.Close()
	for key, rows := range next {
		previous, exists := old[key]
		delete(old, key)
		if reflect.DeepEqual(previous, rows) {
			continue
		}
		if exists {
			if _, err := del.ExecContext(ctx, key); err != nil {
				return false, err
			}
		}
		for _, row := range rows {
			if _, err := ins.ExecContext(ctx, row...); err != nil {
				return false, err
			}
		}
	}
	for key := range old {
		if _, err := del.ExecContext(ctx, key); err != nil {
			return false, err
		}
	}
	return len(old) != 0, nil
}

func mirrorRowGroups(rows *sql.Rows, queryErr error, n int) (map[string][][]any, error) {
	if queryErr != nil {
		return nil, queryErr
	}
	defer rows.Close()
	groups := make(map[string][][]any)
	for rows.Next() {
		values := make([]any, n)
		pointers := make([]any, n)
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		key := values[0].(string)
		groups[key] = append(groups[key], values)
	}
	return groups, rows.Err()
}
