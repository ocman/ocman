package statev2

import (
	"context"
	"fmt"
)

// ProjectSummary contains owner-local display statistics, never native runtime
// authority. Totals include helpers; the visible session count excludes them.
type ProjectSummary struct {
	Directory                               string
	SessionCount, MessageCount              int
	LastUsed, TotalTokensIn, TotalTokensOut int64
	TotalCost                               float64
}

// ProjectVersion is a durable invalidation watermark. It is sampled before a
// project refresh so a concurrent commit can only cause an extra refresh.
func (d *DB) ProjectVersion(ctx context.Context, owner string) (int64, error) {
	if owner == "" {
		return 0, fmt.Errorf("statev2: project owner is required")
	}
	var version int64
	err := d.reader().QueryRowContext(ctx, `SELECT COALESCE((SELECT c.sequence FROM change_outbox c
		JOIN instance i ON i.id=c.instance_id WHERE i.owner_id=? ORDER BY c.sequence DESC LIMIT 1),0)`, owner).Scan(&version)
	return version, err
}

func (d *DB) Projects(ctx context.Context, owner string) ([]ProjectSummary, error) {
	if owner == "" {
		return nil, fmt.Errorf("statev2: project owner is required")
	}
	rows, err := d.reader().QueryContext(ctx, `SELECT a.directory,SUM(CASE WHEN s.is_subagent=0 THEN 1 ELSE 0 END),
		SUM((SELECT count(*) FROM analytics_fact f WHERE f.instance_id=a.instance_id AND f.session_id=a.id)),
		max(a.time_updated),sum(a.input_tokens),sum(a.output_tokens),sum(a.cost)
		FROM analytics_session a JOIN session s ON s.instance_id=a.instance_id AND s.id=a.id
		JOIN instance i ON i.id=a.instance_id
		WHERE i.owner_id=? AND a.source_deleted=0 AND a.directory!=''
		GROUP BY a.directory ORDER BY max(a.time_updated) DESC,a.directory`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProjectSummary{}
	for rows.Next() {
		var p ProjectSummary
		if err := rows.Scan(&p.Directory, &p.SessionCount, &p.MessageCount, &p.LastUsed, &p.TotalTokensIn, &p.TotalTokensOut, &p.TotalCost); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (d *DB) ProjectDirectory(ctx context.Context, owner, project string) (string, error) {
	if owner == "" {
		return "", fmt.Errorf("statev2: project owner is required")
	}
	if project == "" {
		return "", nil
	}
	rows, err := d.reader().QueryContext(ctx, `SELECT DISTINCT json_extract(s.extensions,'$.ProjectDirectory') FROM session s
		JOIN instance i ON i.id=s.instance_id WHERE i.owner_id=? AND s.project_id=? AND s.source_deleted=0
		AND json_type(s.extensions,'$.ProjectDirectory')='text'
		AND COALESCE(json_extract(s.extensions,'$.ProjectDirectory'),'') NOT IN ('','/') LIMIT 2`, owner, project)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	directory := ""
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			return "", err
		}
		if directory != "" {
			return "", ErrConflict
		}
		directory = candidate
	}
	return directory, rows.Err()
}

func (d *DB) Directories(ctx context.Context, owner string) ([]string, error) {
	if owner == "" {
		return nil, fmt.Errorf("statev2: directory owner is required")
	}
	rows, err := d.reader().QueryContext(ctx, `SELECT DISTINCT s.directory FROM session s JOIN instance i ON i.id=s.instance_id
		WHERE i.owner_id=? AND s.source_deleted=0 AND s.directory!='' ORDER BY s.directory`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var dir string
		if err := rows.Scan(&dir); err != nil {
			return nil, err
		}
		out = append(out, dir)
	}
	return out, rows.Err()
}
