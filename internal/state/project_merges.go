package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const projectMergesSetting = "projects.merges"

var ErrSameProject = errors.New("projects already belong to the same group")

type ProjectMergeTarget struct {
	Key       string `json:"key"`
	Directory string `json:"directory"`
	RemoteID  string `json:"remoteId"`
}

type ProjectMerges map[string]ProjectMergeTarget

func ProjectMergeKey(remoteID, directory string) string {
	return "dir:" + NormalizeRemoteID(remoteID) + ":" + ProjectRootForDirectory(directory)
}

func (m ProjectMerges) Resolve(target ProjectMergeTarget) ProjectMergeTarget {
	// Bound traversal so a corrupt setting cannot hang a project listing.
	for range len(m) {
		next, ok := m[target.Key]
		if !ok {
			break
		}
		target = next
	}
	return target
}

func readProjectMerges(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}) (ProjectMerges, error) {
	var value string
	err := q.QueryRowContext(ctx, `SELECT value FROM setting WHERE key = ?`, projectMergesSetting).Scan(&value)
	merges := ProjectMerges{}
	if errors.Is(err, sql.ErrNoRows) {
		return merges, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(value), &merges); err != nil {
		return nil, err
	}
	if merges == nil {
		merges = ProjectMerges{}
	}
	return merges, nil
}

func (d *DB) ProjectMerges(ctx context.Context) (ProjectMerges, error) {
	return readProjectMerges(ctx, d.db)
}

func (d *DB) MergeProjects(ctx context.Context, sources []string, target ProjectMergeTarget) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	merges, err := readProjectMerges(ctx, tx)
	if err != nil {
		return err
	}
	target = merges.Resolve(target)
	for i, source := range sources {
		sources[i] = merges.Resolve(ProjectMergeTarget{Key: source}).Key
		if sources[i] == target.Key {
			return ErrSameProject
		}
	}
	for _, source := range sources {
		merges[source] = target
	}
	value, err := json.Marshal(merges)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO setting (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		projectMergesSetting, string(value), time.Now().UnixMilli())
	if err != nil {
		return err
	}
	return tx.Commit()
}
