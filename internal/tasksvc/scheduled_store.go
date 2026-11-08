package tasksvc

import (
	"context"
	"encoding/json"
)

func (s store) beginScheduled(ctx context.Context, task Task) error {
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	// ObserveSchedule holds the manager's admission lock and has checked that
	// this job is not executing. A failed final write can leave a running
	// receipt behind; observation state must not block the next iteration.
	_, err = s.db.ExecContext(ctx, `INSERT INTO background_task(id,kind,task_key,status,updated_at,data) VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET status=excluded.status,updated_at=excluded.updated_at,data=excluded.data`, task.ID, task.Kind, task.Key, task.Status, task.UpdatedAt, string(raw))
	return err
}
