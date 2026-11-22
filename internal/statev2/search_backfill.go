package statev2

import "context"

const searchBackfillBatch = 64

// BackfillSessionSearch advances a durable cursor by one small transaction.
// Live writes are indexed separately, including inserts behind the cursor.
func (d *DB) BackfillSessionSearch(ctx context.Context) (done bool, err error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var from, to [4]string
	if err := tx.QueryRowContext(ctx, `SELECT cursor_instance,cursor_session,cursor_message,cursor_part,done FROM session_search_progress WHERE id=1`).Scan(&from[0], &from[1], &from[2], &from[3], &done); err != nil || done {
		return done, err
	}
	// Advance over all parts, not only eligible ones, to bound work on tool-heavy histories.
	rows, err := tx.QueryContext(ctx, `SELECT instance_id,session_id,message_id,id FROM part
		WHERE (instance_id,session_id,message_id,id)>(?,?,?,?)
		ORDER BY instance_id,session_id,message_id,id LIMIT ?`, from[0], from[1], from[2], from[3], searchBackfillBatch)
	if err != nil {
		return false, err
	}
	n := 0
	for rows.Next() {
		if err := rows.Scan(&to[0], &to[1], &to[2], &to[3]); err != nil {
			_ = rows.Close()
			return false, err
		}
		n++
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return false, err
	}
	_ = rows.Close()
	if n > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO session_search_doc(instance_id,session_id,message_id,part_id)
			SELECT instance_id,session_id,message_id,id FROM session_search_eligible
			WHERE (instance_id,session_id,message_id,id)>(?,?,?,?) AND (instance_id,session_id,message_id,id)<=(?,?,?,?)`,
			from[0], from[1], from[2], from[3], to[0], to[1], to[2], to[3]); err != nil {
			return false, err
		}
	} else {
		to = from
	}
	done = n < searchBackfillBatch
	if _, err := tx.ExecContext(ctx, `UPDATE session_search_progress SET cursor_instance=?,cursor_session=?,cursor_message=?,cursor_part=?,done=? WHERE id=1`, to[0], to[1], to[2], to[3], done); err != nil {
		return false, err
	}
	return done, tx.Commit()
}
